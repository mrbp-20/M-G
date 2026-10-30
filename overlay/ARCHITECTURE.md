# MAX Gateway Adapter — Architecture and Algorithms

This document describes the internal structure of the MAX messenger gateway adapter and how the source files implement it.

## 1. Overview and data flow

The adapter turns the MAX Bot API update stream into Coddy session turns and posts answers back into the chat.

```
GET /updates (long poll)
        ↓
client.go  parses the page, one Update at a time
        ↓
poll.go    hands each Update to Bot.Handle
        ↓
dispatch.go  routes callbacks, de-duplicates messages, checks ACL,
             builds a session key, enqueues the turn
        ↓
session worker (one per session key) runs the turn
        ↓
runner.HandleSessionPromptWithSender(ctx, params, sender, opts)
        ↓
sender.go  (acp.UpdateSender) posts/edits the live message,
           splits long answers, renders keyboards
        ↓
MAX chat
```

Entry point: `external/gateway/serve_max.go` builds the `Bot` and starts `gateway.Hub`. The hub restarts the bot on failure; `Bot.Start` runs until the context is cancelled.

## 2. Transport — `external/gateway/max/client.go`

`Client` is a hand-written HTTP client for the MAX Bot API.

### Authentication and request shape
- The token is sent raw in the `Authorization` header, with no `Bearer` prefix (`NewClient`, `once`).
- The base URL defaults to `https://platform-api2.max.ru`; it can be overridden with `CODDY_MAX_API_BASE` (`apiBaseURL` in `bot.go`, `WithBaseURL`).

### HTTP client and TLS
- `bot.go` builds the HTTP client through `proxyutil.BuildHTTPClient(cfg.Proxy)` and, when `cfg.CAFile` is set, augments the system cert pool with the PEM bundle (`withRootCA`). The transport is cloned so the gateway setting does not mutate `http.DefaultTransport`.
- Default request timeout is `poll timeout + 15 s` so a held long poll is not cut off early.

### Endpoints
- `Me(ctx) (User, error)` — `GET /me`. Used at startup to validate the token and read `selfID`/`name`.
- `fetchUpdates(ctx) ([]Update, int64, error)` and `Updates(ctx) ([]Update, error)` — `GET /updates`. The low-level call returns the parsed page and the new marker without advancing the cursor; the convenience wrapper advances it.
- `Send(ctx, Outgoing) (string, error)` — `POST /messages`. Returns the message id (`mid`) MAX assigned.
- `EditMessage(ctx, messageID, text, buttons)` — `PUT /messages`. A `nil` keyboard leaves attachments untouched; an empty non-nil slice removes them.
- `EditText(ctx, messageID, text)` — `PUT /messages` with `buttons == nil`.
- `AnswerCallback(ctx, callbackID, notification)` — `POST /answers`.

### Retries and rate limits
- `do` loops on a 429, reading `Retry-After` (capped at `maxRetryAfter` = 60 s) and backing off exponentially when the header is absent.
- Only 429 is retried for writes; other HTTP errors and network failures are returned immediately because the server may already have processed the request.
- `IsTransient(err)` reports network/server/rate-limit errors; `IsStatus(err, status)` checks for a specific HTTP status.

### Pacer
- `pacer` spaces every POST/PUT/DELETE at `writeGap` = 500 ms (~2 writes/s), shared across all senders of the same bot because the limit is per token. GETs are not paced.

### Errors
- `APIError` carries HTTP status, `code`, `message`, and `Retry-After`.
- `ErrBadUpdate` marks a page where some elements could not be parsed; the elements that parsed are still returned.

### Cursor persistence
- `loadCursor` / `saveCursor` keep the poll marker in `gateway_max_cursor` under Coddy's state directory. `saveCursor` writes to a temp file and renames it so readers never see a partial value.

## 3. Long polling — `external/gateway/max/poll.go`

`PollLoop` (and the testable `pollLoop`) read the update stream until the context is cancelled.

- Each iteration calls `Client.fetchUpdates` once.
- Updates are handed to `Handler.Handle` one at a time and in order; a slow turn does not block the rest of the page.
- The cursor is saved only after the whole page is handled. This gives **at-least-once** semantics: if the process stops mid-page, the next startup replays the page; deduplication by `message_id` prevents double turns.
- A page containing malformed updates is delivered and logged (`ErrBadUpdate`); the cursor still advances past the whole page so the loop does not spin forever on one bad element.
- If `ctx` is cancelled during the poll or while handling the page, the loop exits cleanly.
- A fixed pause (`pollInterval`, default 1 s, min 1 s, max 60 s) is taken **after** each answer, never while the server is holding the long poll open. This avoids the spin-at-2000-rps behaviour observed in the reference client.

## 4. Dispatcher — `external/gateway/max/dispatch.go`

`Bot.Handle` is the `Handler` the poll loop calls.

### Routing
- `message_callback` → `Bot.HandleCallback` (answers a button press and resolves it to a waiting prompt).
- `message_created` → `Bot.handleMessage`.
- Other update types are ignored.

### De-duplication and self-messages
- `message_created` updates are deduplicated by `MessageID` using a bounded in-memory set (`seenIDs`, limit 4096).
- Updates whose sender has `IsBot == true` are dropped, so the bot never answers itself or other bots.

### Access control
- `allowed` consults `access.EffectiveAccess` and `access.CanAccess` to enforce the configured access level (`all`, `admins`, or `group:<name>`).
- In groups, `access.EffectiveIsolation` selects the isolation mode; `IsolationAdmin` additionally requires the user to be in `cfg.Admins`.

### Session key
- `sessionstore.SessionKey(adapterName, chatID, userID, isolation, isGroup)` produces the stable chat-to-session key. Private chats always use individual isolation; groups use the configured mode.

### Worker queue
- `enqueue` maintains one goroutine (`sessionWorker`) per session key, fed by a channel of capacity `workerQueueCap` (32). This guarantees **one turn per session at a time**.
- If the queue is full, the update is rejected with a service message.
- If the bot is stopping, the update is not accepted; it stays in the stream for the next process.

### Turn execution (`processMessage`)
- Empty text → replies with `noTextNote`.
- `/start`, `/help`, `/clear`, `/model`, `/context` are handled locally or delegated to helpers (see Commands below).
- For ordinary messages, `ensureSession` gets or creates the Coddy session, `applyInitialModel` stamps the last chosen or first configured model on a fresh session, and the turn runs under `turnContext` (the start context without cancellation) with a 10-minute timeout.
- A non-admin user gets `access.NonAdminTurn()` as a `session.TurnRestriction` and `sender.refuseApprovals = true`, restricting tools and auto-refusing permission requests.
- The sender is wrapped with `session.Mirror` so turn mirrors see the chat.

## 5. Sender — `external/gateway/max/sender.go`

`Sender` implements `acp.UpdateSender` for one turn in one chat.

### Live message
- The turn produces a single "live" message. It is posted on the first meaningful update and edited in place while the answer grows.
- The live preview shows the last `previewRunes` = 1500 runes of the buffer, plus a tool indicator (`⚙️ <tool>…`) when a tool is running.
- Live edits are paced to at most one per `editInterval` = 1 s and bounded by `previewTimeout` = 5 s.

### Final delivery (`Flush`)
- `Flush` replaces the live message with the final answer.
- Answers longer than `maxTextRunes` = 4000 runes are split into pieces. The first piece edits the live message; subsequent pieces are posted as new messages.
- `splitText` prefers paragraph, line, then word boundaries and never splits a rune.
- If the turn context expired, delivery still runs under `context.WithoutCancel(s.ctx)` with `flushTimeout` = 2 min.
- If only tools ran and the buffer is empty, the live message is edited to "Done.".

### Documents
- A document leaves as an artefact of the chat's own session, never as a path: `session.ToolFile` names it by id, `toolFilesForSurfaces` (`internal/agent`) publishes it in the meta of the completed `share_file` call, and the sender reads the copy the session captured.
- Three calls: `UploadFile` asks `POST /uploads?type=file` for an address, posts the bytes there as multipart in a field `data` with no authorization, and reads the token from that answer; `Send` then carries `{type:file,payload:{token}}`. Both upload calls are paced like writes and never retried.
- The body streams: `uploadEnvelope` builds the two ends of the multipart envelope by hand, `io.MultiReader` puts the file between them, and the request keeps a declared `Content-Length`.
- Guards run before the upload: the size limit of the kind, the kind itself, name hygiene, the copy's
  sha256 against the manifest, and `PathInWorkspace` for a confined turn. A refusal is said in the chat
  in Russian.
- Four limits meet on the way out, and the smallest decides: 25 MiB is what Coddy lets a tool call
  capture as an artefact (100 MiB for a whole session, `internal/session/artifacts.go`); 50 MB is our
  own limit for a document, 250 MB for video and 256 MB for audio
  (`external/gateway/max/documents.go`); the platform takes a document up to 4 GB; and the turn itself
  lasts ten minutes with the upload inside it limited to five. The document path, therefore, is capped
  at 25 MiB by the capture step, not by the gateway.
- `attachment.not.ready` is waited out by repeating the message only (3/6/9 s, four attempts); a message that never lands logs `max: upload token unused`, because MAX cannot delete the copy.
- One message carries one document; the caption is the name plus what the client wrote, cut at `maxTextRunes`.
- A picture the agent **shared** travels as a document too: the photo route carries only what the model *read* (`callImages`), so filtering images out of `toolFilesForSurfaces` lost them silently - the web showed a card the chat never got.

### Keyboards and prompts
- Permission requests and questions with options are rendered as inline keyboards (`attachments` in `client.go`).
- `Button.Payload` has the form `ask:<token>:<index>`; a press comes back as a `message_callback` update.
- `askOptions` posts the prompt, waits on a channel, and settles the message (removes the keyboard, adds a note) when answered or when the turn ends.
- `Bot.HandleCallback` / `resolvePress` confirm the press via `AnswerCallback` and route the choice to the waiting prompt. Stale or foreign presses are confirmed but ignored.

### Non-admin handling
- When `refuseApprovals` is set, every permission request returns `cancelled`/`reject` without showing the keyboard.

## 6. Commands — `external/gateway/max/commands.go`

`parseCommand` splits `/word rest` into command and arguments. `knownCommand` recognises the adapter commands and settings commands; unknown slash commands are ignored.

- `/start` — greeting.
- `/help` — command list from `helpText()`.
- `/clear` — resets the session key (`store.Reset`), forgets the old live session, replies «Начата новая беседа.»
- `/model` without args — reports the current effective model and the configured model list.
- `/model <id>` — when typed exactly, `session.ParseSettingsCommands` parses it and the gateway remembers the model in `store.SetLastModel` (admins only) so fresh sessions start on it.
- `/context` — reports the session's context breakdown via `formatContextBreakdown`.
- Settings commands (`/agent`, `/plan`, `/ask`, etc.) pass through to the session; `changesSettings` identifies them. In groups, settings commands are allowed only for admins (`adminOnlyNote`).

## 7. Wire models — `external/gateway/max/model.go`

The types mirror the MAX Bot API payloads:

- `Update` — top-level event with `UpdateType`, `Timestamp`, `ChatID`, `UserID`, `MessageID`, `User`, and the raw `Message` / `Callback` bodies (`json.RawMessage`).
- `Message` — `Sender`, `Recipient`, `Timestamp`, `Body`.
- `Text` — `Mid`, `Seq`, `Text`.
- `Recipient` — `UserID`, `ChatID`, `ChatType`.
- `Callback` — `CallbackID`, `Payload`, `Timestamp`, `User`.
- `User` — `UserID`, `FirstName`, `LastName`, `Username`, `Name`, `IsBot`; `DisplayName()` builds a readable name.
- `Target` — `UserID` or `ChatID`; one is set per outgoing message.
- `Button` — `Type`, `Text`, `Payload`.
- `Outgoing` — `Target`, `Text`, `Format`, `Buttons`.

Bodies are kept as `json.RawMessage` so a malformed inner object cannot fail the whole page decode; the adapter parses only the fields it needs.

## 8. Wiring and configuration

### Gateway wiring
- `external/gateway/serve_max.go` (build tag `gateway || gateway.max`) creates the `Bot` and runs it through `gateway.Hub`. It stores `gateway_max_sessions.json` and `gateway_max_cursor` in `cfg.ResolvedSessionsRoot()` — Coddy's state directory, never the process working directory.
- `external/gateway/serve_max_stub.go` (build tag `!(gateway || gateway.max)`) sets `MaxAvailable = false` and returns an error if called.
- `cmd/coddy/serve.go` registers the MAX subsystem (`KindGatewayMax`, build tag `gateway`, fingerprint `gateway.MaxFingerprint`). The `--gateway` flag controls Telegram; MAX follows `gateways.max.enable`.

### Configuration
- `internal/config/gateway.go` defines `MaxGatewayConfig` with fields: `Enabled`, `Token`, `Proxy`, `CAFile`, `PollTimeoutSeconds`, `PollIntervalSeconds`, `Admins`, `DefaultAccess`, `DefaultIsolation`, `UserGroups`, `Chats`.
- Defaults: poll timeout 30 s, poll interval 1 s, access `all`, isolation `individual`.
- Token resolution: `EffectiveToken()` returns the configured token or `MAX_BOT_TOKEN` from the environment.
- Validation checks proxy format, CA file existence, and poll range.
- `internal/config/config.schema.json` documents the same keys under `gateways.max`.

### Build tags
- Adapter files: `//go:build gateway || gateway.max`.
- Stub file: `//go:build !(gateway || gateway.max)`.
- Building with `-tags gateway` or `-tags gateway.max` includes the adapter.

## File map

Sizes are deliberately left out: they change with every retry branch added, and a number that lies
misleads worse than no number at all. What each file is for does not change as fast.

| File | Role |
|------|------|
| `external/gateway/max/bot.go` | Adapter lifecycle, HTTP client setup with a finite timeout, session runner interface, drain on stop |
| `external/gateway/max/burst.go` | Messages of one chat held for a moment, so a text and the picture sent with it run one turn |
| `external/gateway/max/client.go` | HTTP transport, endpoints, pacer, retries, errors, cursor persistence |
| `external/gateway/max/poll.go` | Long-poll loop and at-least-once cursor handling |
| `external/gateway/max/dispatch.go` | Update routing, ACL, session key, worker queue, turn execution |
| `external/gateway/max/sender.go` | `acp.UpdateSender`, the answer, splitting, keyboards, callback presses, the model menu, chat actions |
| `external/gateway/max/commands.go` | Slash commands, `/context`, the `/model` menu, settings passthrough |
| `external/gateway/max/commands_menu.go` | The hint list published through `PATCH /me/commands` at start |
| `external/gateway/max/stop.go` | `/stop`: cancelling the turn of the chat the way the web UI and the console do |
| `external/gateway/max/resume.go` | `/resume`: the session list, its paging and pictograms, the switch |
| `external/gateway/max/history.go` | The history card posted after a switch, kept inside one message |
| `external/gateway/max/mirror.go` | The way back: a turn started in the browser is published into the chat its session is bound to, and a scheduled report is stamped and delivered |
| `external/gateway/max/tooldetails.go` | Tool calls: the details store, paging and the view a press opens |
| `external/gateway/max/incoming.go` | Incoming files: the `Max-in` folder, safe names, the platform limits, the length of a recording read rather than trusted |
| `external/gateway/max/media.go` | Incoming pictures: download them and hand them to a multimodal model |
| `external/gateway/max/pictures.go` | Outgoing pictures: the copies a turn's calls left, uploaded and posted |
| `external/gateway/max/documents.go` | Outgoing files: the artefacts a turn's calls left, guarded, uploaded and posted; documents, video and audio all go as a file |
| `external/gateway/max/model.go` | API wire types |
| `external/gateway/serve_max.go` | Build-gated wiring that constructs the bot |
| `external/gateway/serve_max_stub.go` | Build-gated no-op stub |
| `external/gateway/sessionstore/sessionstore.go` | The chat-to-session map on disk, and the failed save it now reports |
| `internal/session/session_origin.go` | `Manager.SessionOrigin`: where a session started, from the bundle metadata |
| `internal/session/tool_files.go` | `ToolFile`: the artefact a surface may send - no path in it |
| `internal/session/turn_answer.go` | `ErrAnsweredElsewhere` and the two predicates: the same rule on every surface |
| `internal/agent/tool_files.go` | `toolFilesForSurfaces`: a finished `share_file` call publishes its document in the meta |
| `internal/agent/react.go` | The agent loop; our two lines inside the `share_file` block publish the same artefact as a document |
| `internal/config/gateway.go` | `MaxGatewayConfig` and shared gateway config helpers |
| `cmd/coddy/serve.go` | Subsystem registration and `coddy serve` orchestration |
| `internal/config/config.schema.json` | JSON schema including `gateways.max` |

## Assumptions and notes

- The document treats `gateway_max_sessions.json` and `gateway_max_cursor` as stable file names; they are literal strings in `serve_max.go`.
- The exact set of non-admin tools is defined in `external/gateway/access/turn.go`; the adapter consumes it via `access.NonAdminTurn()` without enumerating the list.
- The split of responsibilities between `poll.go` (cursor after the page) and `client.go` (`fetchUpdates` does not move the cursor) is the at-least-once guarantee; `Updates()` is documented but the loop uses `fetchUpdates`.
- A `message_callback` update carries the id of the message the button belongs to in `message.body.mid` and no `message_id` of its own. `Update.MessageKey` reads whichever id the update carries; the model menu is edited by that id, and reading the update's own field would edit an empty id and be refused.
