# The MAX gateway adapter: a reference by file

Generated from the code itself by `scripts/gen-max-api-reference.py`: every type and function of the adapter with the comment that stands above it, grouped by the file it lives in. The comments are the source of truth and stay as written there; this file only collects them. Regenerate after changing the adapter rather than editing it by hand.

Reading order for a newcomer: `bot.go` (the adapter and its settings), `dispatch.go` (what happens to an incoming message), `sender.go` (what the chat sees), then `documents.go` and `pictures.go` (files and pictures), `incoming.go` (files the person sends), `client.go` (the API), and `model.go` (the wire types).

19 files, 43 types, 256 functions; 286 of 299 carry a description in the code.

## Contents

- [external/gateway/max/bot.go](#externalgatewaymaxbotgo) — 3 types, 14 functions
- [external/gateway/max/burst.go](#externalgatewaymaxburstgo) — 2 types, 6 functions
- [external/gateway/max/client.go](#externalgatewaymaxclientgo) — 5 types, 35 functions
- [external/gateway/max/commands.go](#externalgatewaymaxcommandsgo) — 1 types, 19 functions
- [external/gateway/max/commands_menu.go](#externalgatewaymaxcommands_menugo) — 1 types, 2 functions
- [external/gateway/max/dispatch.go](#externalgatewaymaxdispatchgo) — 2 types, 18 functions
- [external/gateway/max/documents.go](#externalgatewaymaxdocumentsgo) — 1 types, 14 functions
- [external/gateway/max/history.go](#externalgatewaymaxhistorygo) — 1 types, 13 functions
- [external/gateway/max/incoming.go](#externalgatewaymaxincominggo) — 1 types, 19 functions
- [external/gateway/max/media.go](#externalgatewaymaxmediago) — 1 types, 6 functions
- [external/gateway/max/mirror.go](#externalgatewaymaxmirrorgo) — 1 types, 12 functions
- [external/gateway/max/model.go](#externalgatewaymaxmodelgo) — 12 types, 7 functions
- [external/gateway/max/nopreview.go](#externalgatewaymaxnopreviewgo) — 0 types, 2 functions
- [external/gateway/max/pictures.go](#externalgatewaymaxpicturesgo) — 1 types, 2 functions
- [external/gateway/max/poll.go](#externalgatewaymaxpollgo) — 1 types, 3 functions
- [external/gateway/max/resume.go](#externalgatewaymaxresumego) — 4 types, 22 functions
- [external/gateway/max/sender.go](#externalgatewaymaxsendergo) — 4 types, 38 functions
- [external/gateway/max/stop.go](#externalgatewaymaxstopgo) — 0 types, 9 functions
- [external/gateway/max/tooldetails.go](#externalgatewaymaxtooldetailsgo) — 2 types, 15 functions

## external/gateway/max/bot.go

### Types

- `type SessionRunner interface` — SessionRunner is what the bot needs from the session manager; session.Manager satisfies it.
- `type Bot struct` — Bot is the MAX gateway adapter: the bot's settings, the connection it makes at Start, and the dispatch of incoming updates into Coddy sessions. Name and Start are the whole of gateway.Adapter, satisfied structurally - importing external/gateway here would be a cycle, since that package builds this one in serve_max.go.
- `type workerJob struct` — _no description in the code_

### Functions

- `func (b *Bot) trackSender(s *Sender)` — trackSender remembers a turn in flight.
- `func (b *Bot) untrackSender(s *Sender)` — untrackSender forgets a turn that ended.
- `func (b *Bot) detachChat(chat Target, keep string) int` — detachChat stops the turns of a chat that no longer owns them: after a switch to another conversation they keep running in their own sessions, where the web UI still shows them, but they stop writing into the chat. Only a document still goes out, because it was asked for here (documents.go).
- `func New(cfg *config.MaxGatewayConfig, runner SessionRunner, cwd string, log *slog.Logger, storePath, cursorPath string, mirror session.TurnMirror) *Bot` — New returns a MAX adapter reading cfg, keeping its poll marker in cursorPath and the chat-to-session map in storePath; "" for either keeps it in memory only. The logger is expected to be tagged with the gateway.max component, which serve_max.go does.
- `func (b *Bot) Name() string { return "max" }` — Name satisfies gateway.Adapter.
- `func (b *Bot) apiBaseURL() string` — apiBaseURL is the API root this bot talks to: CODDY_MAX_API_BASE when set, the v2 host otherwise.
- `func (b *Bot) Start(ctx context.Context) error` — Start connects to the MAX Bot API and runs the update loop until ctx is cancelled. The loop hands every update to the bot itself, which turns a message into a session turn and a callback into the answer of a waiting prompt.
- `func (b *Bot) setClient(c *Client)` — setClient records the client of a running bot; nil means the bot stopped.
- `func (b *Bot) connectedClient() *Client` — connectedClient is the client of a running bot, nil when stopped.
- `func (b *Bot) bursts() *burstBuffer` — bursts is the buffer of open message bursts, built on first use (burst.go).
- `func (b *Bot) turnContext(ctx context.Context) context.Context` — turnContext is the context turns run under: the start's context without its cancellation, so a stop ends intake before it ends generation.
- `func (b *Bot) drain()` — _no description in the code_
- `func withRootCA(hc *http.Client, path string) (*http.Client, error)` — withRootCA returns a client that trusts the certificates of the PEM bundle at path in addition to the system store - a national root is an addition to it, not a replacement. The client given is copied, never modified in place: BuildHTTPClient answers "inherit" with http.DefaultClient itself, and a gateway setting must not reach every other request of the process.
- `func withFiniteTimeout(hc *http.Client, atLeast time.Duration) *http.Client` — withFiniteTimeout returns a client that gives up on a dead connection of its own accord. The client is copied, never changed in place: proxyutil may hand back http.DefaultClient, and a deadline written into that one would reach every other part of the program. A deadline the caller already set is kept when it is shorter than the one asked for here.

## external/gateway/max/burst.go

### Types

- `type burstBuffer struct` — burstBuffer holds the messages of each chat until its burst is complete.
- `type burstEntry struct` — burstEntry is one open burst: what arrived so far and the clock that ends it.

### Functions

- `func newBurstBuffer(window time.Duration, flush func(context.Context, inbound, string)) *burstBuffer` — _no description in the code_
- `func (bb *burstBuffer) add(ctx context.Context, in inbound, key string)` — add takes one message of a chat into its burst and restarts the clock that ends it, so a person writing several messages is answered once.
- `func (bb *burstBuffer) fire(key string)` — fire delivers the completed burst of one chat.
- `func (bb *burstBuffer) flushKey(key string)` — flushKey delivers what a chat still holds, for a command that must not wait behind the burst it interrupts.
- `func (bb *burstBuffer) flushAll()` — flushAll delivers every held burst: what is held here is already past the cursor, so a stop that dropped it would lose the message for good.
- `func mergeInbound(a, b inbound) inbound` — mergeInbound folds the next message of a burst into the one before it: the texts follow each other and the pictures add up.

## external/gateway/max/client.go

**What this file is for.** Package max is the MAX messenger gateway: the bot's own HTTP client and the adapter that turns incoming messages into Coddy sessions. The client is written by hand rather than on the official library, and every rule below exists for a measured reason: an audit of max-bot-api-client-go/v2@v2.4.4 against a fake server reproduced each of the failures these rules avoid. - The HTTP status stays on the error. The official client drops it, so a 429 and a 500 with the same body are indistinguishable and a caller cannot tell a rate limit from a refusal. - A 429 is waited out, up to maxRetryAfter, and only a 429: MAX refuses the request before doing the work, so a repeated POST cannot post a second message. Nothing else is retried for a write, because a 5xx or a broken connection may well have been processed. - Response bodies are read through a limit. Without one, a hostile or misconfigured server makes the process allocate without bound: the audit read a 100 MiB body into a JSON string and the heap grew by ~484 MiB. - The update page is decoded in two steps. One malformed element must not drop the page: the official client fails the whole response on a single bad update, the cursor does not move, and the same page is re-fetched forever. - The cursor belongs to us and only a successful poll moves it. The official client answers a failed poll with marker 0, so the loop its own documentation shows loses the position and replays everything. - Polls are not paced here. The pause between them belongs to the caller: the official WithPollingPause is dead code, and against instant empty answers the client spins at roughly 2000 requests a second. - Writes are paced here, and only writes. The API refuses a bot that sends faster than two requests a second (the 429 of the schema), and the refusal costs a whole round trip to discover; a pacer in the transport holds every POST, PUT and DELETE at that rate whatever calls it, while a GET - the long poll above all - stays as fast as its caller asks.

### Types

- `type Client struct` — Client talks to MAX with one bot token.
- `type Option func(*Client)` — Option configures a Client.
- `type videoDetails struct` — videoDetails is the answer of GET /videos/{token}: the direct links of the file by resolution, and its duration.
- `type APIError struct` — APIError is a refusal from MAX, carrying the status the API answered.
- `type pacer struct` — pacer spaces a client's writes at writeGap apart - two a second, the rate MAX tolerates. It is a plain reservation rather than a token bucket: every waiter takes the next free instant and the call after it is pushed one gap further, so a burst of callers leaves in the order they arrived, whatever they do between the reservation and the request. Waiting for a slot honors the context, so a cancelled turn does not sit out the gap.

### Functions

- `func WithHTTPClient(hc *http.Client) Option { return func(c *Client) { c.http = hc } }` — WithHTTPClient sets the HTTP client. It is the one that carries the MinCifry root certificate, the proxy and the timeout, and its timeout must exceed the poll timeout: the official client ships 30 s for both, and a held poll then reads as a timeout after 93 s of retries.
- `func WithBaseURL(base string) Option` — WithBaseURL points the client at another API root, for tests.
- `func WithCursor(path string) Option { return func(c *Client) { c.cursorPath = path } }` — WithCursor keeps the poll marker in path, so a restart resumes where the bot stopped.
- `func WithPollTimeout(d time.Duration) Option { return func(c *Client) { c.poll = d } }` — WithPollTimeout sets how long the server may hold one poll open.
- `func NewClient(token string, opts ...Option) *Client` — NewClient returns a client for token. The token travels in the Authorization header as MAX wants it: the raw value, with no "Bearer " prefix.
- `func (c *Client) Cursor() int64` — Cursor is the marker of the last successful poll.
- `func (c *Client) loadCursor()` — _no description in the code_
- `func (c *Client) saveCursor(v int64) error` — saveCursor records a marker, in memory and on disk. A write to a temporary file plus a rename keeps a reader from ever seeing half a number.
- `func (c *Client) Updates(ctx context.Context) ([]Update, error)` — Updates makes one long poll: GET /updates. It returns the page and advances the cursor when the poll succeeded and the server sent a new non-zero marker. A page with a malformed element returns the updates that parsed together with ErrBadUpdate. It is the convenience of a caller with no handler to run between the page and the next poll; the poll loop uses fetchUpdates instead, so the cursor advances only after the page is handled.
- `func (c *Client) fetchUpdates(ctx context.Context) ([]Update, int64, error)` — fetchUpdates makes one long poll and returns the page beside the marker the server answered. It does not advance the cursor: the caller decides when the page is handled, so a stop in the middle of it replays the page on the next process instead of losing the unhandled rest.
- `func (c *Client) Me(ctx context.Context) (User, error)` — Me reads the bot's own card: GET /me. A revoked or mistyped token is refused here, which is what makes this the cheapest call to probe a token with.
- `func (c *Client) Send(ctx context.Context, out Outgoing) (string, error)` — Send posts an outgoing message and returns the id MAX gave it.
- `func (c *Client) SetCommands(ctx context.Context, cmds []BotCommand) error` — SetCommands replaces the hint list the messenger shows when a person types "/" in a dialog with the bot or in a group: PATCH /me/commands. The list travels in the order given, and an empty one takes the hints away instead of sending a null the API does not read as a list. It is a write, so it waits its turn in the client's pacer (two a second, the rate MAX tolerates) like every other non-GET request.
- `func (c *Client) SendAction(ctx context.Context, chatID int64, action string) error` — SendAction posts one chat action. Unlike the message routes the chat travels in the path, and the action is the whole body: the schema's ActionRequestBody has one required field and carries no message ids, so mark_seen reads the chat rather than a list of messages. It is a write, so it waits its turn in the client's pacer like every other non-GET request.
- `func imageAttachment(token string) map[string]any` — imageAttachment renders a stored photo in the shape the API takes it: an image attachment carrying the token UploadImage returned.
- `func fileAttachment(token string) map[string]any` — fileAttachment renders a stored document in the shape the API takes it: a file attachment carrying the token UploadFile returned.
- `func (c *Client) UploadImage(ctx context.Context, name string, data []byte) (string, error)` — UploadImage stores an image in MAX and returns the token an outgoing message carries as an image attachment.
- `func (c *Client) UploadFile(ctx context.Context, name, mime string, size int64, r io.Reader) (string, error)` — UploadFile stores a document in MAX and returns the token an outgoing message carries as a file attachment. size is how many bytes r must yield: the upload is a stream, so the request declares its length up front. A reader that yields less fails at the transport; one that yields more is cut at size, so a caller whose content may grow takes its size from the same read that verified the content.
- `func (c *Client) VideoFile(ctx context.Context, token string) (string, int, error)` — VideoFile asks the platform where the file of a video attachment is. The payload of a video carries a player page, not the file, so the bytes come from here: the best resolution the platform offers, or "" when it offers none (a video still being processed answers that way).
- `func (c *Client) uploadMedia(ctx context.Context, kind, name, mime string, size int64, r io.Reader) (string, error)` — uploadMedia stores media in MAX and returns the token an outgoing message carries as an attachment of that kind. The API takes media in two calls: POST /uploads?type=<kind> answers an address of its own, and the bytes go there as multipart/form-data in a field named "data", with no authorization at all; that answer names the stored media by a token - the first answer carries no token at all. Both calls are writes, so both are paced like writes. Neither is retried: a repeated upload leaves a second copy in the messenger's storage, and a copy nobody sends is nobody's to take away. The body is streamed, never held: a document may be tens of megabytes, and a buffer that size would be the whole process's problem instead of one file's. The multipart envelope around the stream is therefore built by hand, which keeps the request's length known - the API is given no chunked body.
- `func uploadEnvelope(name, mime string) (head, tail []byte, contentType string, err error)` — uploadEnvelope is the multipart form-data around the file itself: the part header before it and the closing boundary after it. Built by hand rather than by multipart.Writer so that the three pieces can be counted before the first byte leaves, and the request still carries a length.
- `func escapeFilename(name string) string` — escapeFilename keeps a name inside the part header it is written into: a quote ends the value early and a newline starts a header of its own.
- `func (c *Client) EditMessage(ctx context.Context, messageID, text string, buttons [][]Button) error` — EditMessage replaces the text of a message the bot sent and, when buttons is not nil, its inline keyboard: nil leaves the attachments as they are, an empty non-nil slice takes them away. PUT /messages reads an empty attachments list as "delete them" and a missing one as "leave them alone", which is exactly the distinction the two forms of buttons carry.
- `func (c *Client) EditText(ctx context.Context, messageID, text string) error` — EditText replaces the text of a message the bot sent, leaving its attachments as they are: PUT /messages.
- `func (c *Client) EditMarkdown(ctx context.Context, messageID, text string) error` — EditMarkdown replaces the text of a message and asks MAX to render it as markdown. The agent writes markdown, and the client draws bold, code and headings from it - the same shape the web shows, instead of the markers.
- `func attachments(buttons [][]Button) []any` — attachments renders an inline keyboard in the shape the API takes it (schema.yaml, InlineKeyboardAttachmentRequest): [{"type":"inline_keyboard","payload":{"buttons":[[{"type":"callback", "text":"Approve","payload":"ask:9f3c:0"}]]}}] An empty non-nil slice renders as an empty list, which is how an edit takes a keyboard away. Buttons without an explicit type are callback buttons, the only kind this bot posts.
- `func (c *Client) AnswerCallback(ctx context.Context, callbackID, notification string) error` — AnswerCallback answers a button press: POST /answers. The notification is what the presser sees.
- `func (e *APIError) Error() string` — _no description in the code_
- `func (e *APIError) TooManyRequests() bool { return e.Status == http.StatusTooManyRequests }` — TooManyRequests reports whether the refusal is a rate limit.
- `func IsStatus(err error, status int) bool` — IsStatus reports whether err is a MAX refusal with this HTTP status.
- `func IsTransient(err error) bool` — IsTransient reports whether err may well not happen again: the network, a server error or a rate limit, as opposed to a refusal that will stand (a bad token, a message that does not exist).
- `func (p *pacer) wait(ctx context.Context) error` — wait blocks until this client's next write may go out.
- `func (c *Client) do(ctx context.Context, method, path string, q url.Values, in, out any) error` — do sends one request and waits out a 429.
- `func (c *Client) once(ctx context.Context, method, path string, q url.Values, body []byte, out any) error` — once performs a single request.
- `func parseAPIError(resp *http.Response) error` — parseAPIError reads the error body ({"code","error","message"} per the API schema) and the pause a 429 asked for.

## external/gateway/max/commands.go

### Types

- `type contextHeader struct` — contextHeader names what a breakdown belongs to. The sizes alone say nothing about which model they are measured against or which conversation they describe, so /context carries the conversation's human name, the model id and the window that model runs with (0 when no window is known).

### Functions

- `func parseCommand(text string) (string, string)` — parseCommand splits "/word rest" into its command word and the rest.
- `func knownCommand(cmd string) bool` — knownCommand reports whether cmd is a built-in command or a settings command the bot hands to the session. Anything else that starts with a slash is somebody else's command and is ignored.
- `func changesSettings(cmd string) bool` — changesSettings reports whether cmd changes the session's settings or replaces the conversation.
- `func adminEverywhere(cmd string) bool` — adminEverywhere reports the settings that belong to the admins in every chat, not only where the conversation is shared. Moving a chat to another model and throwing a conversation away are not the same as picking a mode for a turn: the first decides what every later turn costs, the second destroys history. A group has always been guarded; a personal chat is where the plain user lives, and the model menu there was the one door left open (29.10.2026).
- `func isSettingsCommand(cmd string) bool` — _no description in the code_
- `func (b *Bot) helpText() string` — _no description in the code_
- `func (b *Bot) handleModelCommand(ctx context.Context, in inbound, key string)` — handleModelCommand opens the model menu when no id is given. A direct "/model <id>" is handled by the session turn and changes the model in text.
- `func modelMenuText(current string) string` — modelMenuText is the text above the model keyboard.
- `func buildModelKeyboard(models []config.ModelEntry, current string) [][]Button` — buildModelKeyboard returns one button per configured model, with a tick on the current one. Every payload carries the model action prefix so the tap is recognised in HandleCallback.
- `func callbackValue(prefixLen int, value string) string` — callbackValue returns the payload a button carries for value, next to a prefix of prefixLen bytes. A value that fits travels as itself; a longer one travels as a digest, because truncating it would send back a name nothing is stored under - the tap would be rejected and the keyboard would look broken.
- `func modelCallbackValue(model string) string` — modelCallbackValue returns the payload carried by one model button.
- `func resolveModelCallback(models []config.ModelEntry, payload string) (string, bool)` — resolveModelCallback maps a payload back to a configured model id. A model dropped from the configuration since the keyboard was sent resolves to nothing, which is better than applying a name that is gone.
- `func (b *Bot) handleContextCommand(ctx context.Context, in inbound, key string)` — handleContextCommand reports the session's context breakdown.
- `func (b *Bot) contextHeader(st *session.State) contextHeader` — contextHeader builds the header for st. The model and the window come from the same resolution every other reader uses, so /context cannot disagree with the composer or with usage_update.
- `func (b *Bot) handleModelCallback(ctx context.Context, c *Client, u Update, cb Callback, st *session.State) (string, bool, error)` — handleModelCallback applies a model chosen from the inline keyboard and edits the menu message so the tick moves to the new model. It returns the notification to show the presser and whether the menu was edited.
- `func sameModelKeyboard(u Update, models []config.ModelEntry, newModel string) bool` — sameModelKeyboard reports whether the message the callback came from already shows the keyboard for newModel. It reads the inline keyboard from the message body carried by the callback update, if any.
- `func sameButtonKeyboard(attachments []json.RawMessage, want [][]Button) bool` — sameButtonKeyboard reports whether the attachments already show the wanted keyboard, row by row and button by button.
- `func formatContextBreakdown(bd *session.ContextBreakdown, head contextHeader) string` — formatContextBreakdown renders a session's context breakdown for /context: what the numbers belong to (conversation, model, its window), the sizes themselves, and how much of the window the estimate takes. It is plain text on purpose: MAX does not render markdown, so a "**bold**" or a fenced block would reach the chat as literal asterisks and backticks. The sizes are one per line without column padding for the same reason - a proportional font in the chat would not line them up anyway.
- `func groupDigits(n int) string` — groupDigits writes an integer with a space every three digits, so a count of 183289 reads as 183 289 in a chat.

## external/gateway/max/commands_menu.go

### Types

- `type BotCommand struct` — BotCommand is one row of the hint list: the word typed after "/" and the one-line description shown beside it.

### Functions

- `func botCommands() []BotCommand` — botCommands is the menu published at Start: the short list a person meets first, the same shape the Telegram side publishes. It holds the commands processMessage answers itself (start, help, model, context, clear) and the three modes (agent, plan, ask), with goal closing it. The rest of the settings catalogue (reasoning, think, nothink, toolset, plus the aliases effort and no_think) works and is handed to the session, but it stays out of the hints: it is what somebody reaches for after the short list, not what a first "/" should offer. Adding one is a one-line change here, and commands_menu_test.go holds the platform bounds when that happens. Names carry no slash, since the client adds it.
- `func (b *Bot) publishCommands(ctx context.Context)` — publishCommands replaces the messenger's hint list with botCommands. It is best effort on purpose: a bot that cannot publish its hints still answers people, so the failure is logged and never returned. A stopped bot has no client to publish through, and nothing to publish for.

## external/gateway/max/dispatch.go

### Types

- `type inbound struct` — inbound is a message the bot will answer.
- `type seenIDs struct` — seenIDs remembers recently handled message ids, so a message MAX delivered twice runs one turn.

### Functions

- `func (b *Bot) Handle(ctx context.Context, u Update) error` — Handle routes one update. It satisfies the Handler the poll loop hands updates to: a callback is answered, a created message becomes a session turn, everything else - an edit, a reaction - is ignored. The bot never answers itself: its own messages arrive back as updates, and are told apart by the is_bot flag of their sender.
- `func (b *Bot) handleMessage(ctx context.Context, u Update) error` — handleMessage turns one created message into an enqueued turn, or ignores it.
- `func (b *Bot) deliver(ctx context.Context, in inbound, key string)` — deliver hands one message, or the burst that completed it, to the worker of its session, and tells the person when the queue is full.
- `func (b *Bot) allowed(chatID, userID int64, isGroup bool) bool` — allowed applies the access level and the admin-only isolation of the chat.
- `func (b *Bot) markSeen(ctx context.Context, chatID int64)` — markSeen tells MAX the chat has been read, and the person's own message turns from one tick into two - the only "your message reached me" the platform shows without a line of its own in the chat. Best effort: a chat left unread is a worse chat, never a failed turn.
- `func (b *Bot) typingKeepAlive(ctx context.Context, chatID int64) (stop func())` — typingKeepAlive renews the "typing…" hint until the returned stop is called or the turn's context ends: MAX shows the action for a few seconds and a turn lasts minutes. The first action goes out at once, so the chat answers the person's message before the first word of the answer exists.
- `func (b *Bot) sendAction(ctx context.Context, c *Client, chatID int64, action string)` — sendAction posts one chat action, best effort and bounded: a refused hint is a line in the log, and nobody loses an answer over it.
- `func (b *Bot) enqueue(ctx context.Context, in inbound, key string) (accepted, queued bool)` — enqueue hands a message to the worker of its session key, so one session runs one turn at a time. It reports false when the bot is stopping: the message then stays in the stream for the next process. A queued message counts as in flight from here.
- `func (b *Bot) sessionWorker(ctx context.Context, ch chan workerJob)` — _no description in the code_
- `func (b *Bot) processMessage(ctx context.Context, in inbound, key string)` — processMessage runs the turn of one message in the worker of its session key.
- `func (b *Bot) runTurn(ctx context.Context, params acp.SessionPromptParams, sender acp.UpdateSender, opts *session.PromptRunOpts) (*acp.SessionPromptResult, error)` — runTurn runs one turn, waiting out a session whose previous turn has not finished yet.
- `func (b *Bot) answerTarget(in inbound) Target` — answerTarget is where the answer to in goes.
- `func (b *Bot) reply(ctx context.Context, in inbound, text string)` — reply posts a short service message into the chat the message came from.
- `func (b *Bot) ensureSession(ctx context.Context, key string) (*session.State, error)` — ensureSession gets or creates the session for key. A conversation this gateway starts is stamped with where it came from.
- `func (b *Bot) applyInitialModel(ctx context.Context, st *session.State)` — applyInitialModel stamps the gateway's own model on a session that is just beginning: the model last picked on this surface, or the alphabetically first configured one. A session that already chose keeps its model.
- `func newSeenIDs(limit int) *seenIDs` — _no description in the code_
- `func (s *seenIDs) add(id string) bool` — add records id and reports whether it was new.
- `func surfaceSystemPrompt() string { return maxGuidance }` — surfaceSystemPrompt is what the gateway hands the session for one turn.

## external/gateway/max/documents.go

### Types

- `type fileSession interface` — fileSession is the chat's own session as the document guard needs it: the id and the bundle it keeps artefacts in, the working directory its sources are confined to, and the restriction this turn runs under.

### Functions

- `func outgoingKind(name string) string` — outgoingKind says how a file is sent: "" when this gateway does not send that kind at all, else "video", "audio" or "file" - the three attachment types MAX keeps apart.
- `func fileLimit(kind string) int64` — fileLimit is the largest file of this kind that may go out: the platform's own limits for video and sound, our measured one for documents.
- `func (s *Sender) sendFiles(sessionID string, files []session.ToolFile)` — sendFiles posts the documents a finished call of the chat's own session left for it, one message each, captioned with the name and what the client wrote. Only artefacts of this chat's session are sent: a subagent's call belongs to another session and is not the chat's to see, and an id the session does not hold resolves to nothing. A document the messenger refuses is left out and the answer still comes.
- `func (s *Sender) postDocument(sessionDir string, file session.ToolFile)` — postDocument resolves one artefact and sends it, logging what it cost. Every failure is one document's own: the turn goes on.
- `func (s *Sender) sendFileMessage(ctx context.Context, out Outgoing, pauses []time.Duration) (string, int, error)` — sendFileMessage posts a message carrying a document and retries only the message: attachment.not.ready says the upload is still being processed and answers a later attempt, while a repeated upload would leave a second copy.
- `func documentCaption(name, written string) string` — documentCaption is the name first and what the client wrote after it, cut at the message budget with an ellipsis so the API never refuses the length.
- `func documentName(file session.ToolFile, artifact session.Artifact, path string) string` — documentName is what the chat calls the file: the name the client gave, the name the store recorded, or the last resort of the snapshot's own name.
- `func verifySnapshot(handle *os.File, want string) error` — verifySnapshot reads the copy once and puts it back at its start: the digest the manifest recorded is what the session captured, and a copy that differs is refused rather than sent. The gap between this read and the upload is safe because the store writes its copies read-only (0444, a tamper has to chmod first) and nothing in this process writes them.
- `func (s *Sender) confinedRefusal(artifact session.Artifact) string` — confinedRefusal is why a confined turn may not send this artefact, or "": the source it was captured from has to sit inside the session's working directory and outside the agent's home, the same rule a read obeys.
- `func fileRefusal(name, kind string, size int64) string` — fileRefusal is why a file is not sent, or "" when it is: the size the platform keeps to for that kind, and the kinds this gateway sends at all. Both checks run before the upload, because an upload that is then refused leaves a copy in the messenger's storage that nothing can delete. fileRefusal says why a file cannot go out; an empty answer means it may.
- `func kindWord(kind string) string` — kindWord names an attachment kind in Russian, for a sentence to the person.
- `func (s *Sender) refuseFile(name, kind, reason string)` — refuseFile says in the chat why a file did not arrive, naming what it was. The turn goes on: one file's refusal is not the answer's failure.
- `func cleanDocumentName(name, fallback string) string` — cleanDocumentName is a name a chat can show: the base of whatever came in, with control characters dropped and the length bounded.
- `func clipRunes(s string, n int) string` — clipRunes keeps at most n runes of s, marking the cut.

## external/gateway/max/history.go

### Types

- `type transcriptReader func(sessionID string) []llm.Message` — transcriptReader hands the bot the messages of a session it did not author. The gateway adapter never opens the session store itself: the process wires this at startup, so a bot built in a test has no reader and posts no card.

### Functions

- `func (b *Bot) SetTranscript(read transcriptReader)` — SetTranscript hands the bot the reader it asks for a session's messages. A nil reader, or one that knows nothing about a session, leaves /resume as it was: the confirmation alone.
- `func historyCard(title string, msgs []llm.Message) string` — historyCard is the message that opens a switched conversation: the divider, then one line per message of the tail. The card is a single message, and MAX refuses a text longer than maxTextRunes, so the oldest lines give way rather than the whole card: a note counts what stayed behind and names the button that holds the transcript in full.
- `func historyNote(dropped int) string` — historyNote counts the lines that did not fit the card, so a short card reads as a budget rather than as a conversation that had little in it.
- `func historyName(title string) string` — historyName is the title the card shows: a session without one is called by what it is rather than by an empty pair of quotes.
- `func historyCount(n int) string` — historyCount is the Russian count of messages: 1 сообщение, 2 сообщения, 5 сообщений.
- `func historyLines(msgs []llm.Message, limit int) []string` — historyLines renders the tail of a transcript, newest first, one line each: the operator reads a card from the end of the conversation back. A tool step and a system message say nothing a person would recognise, so they are skipped; an assistant answer that is empty (a call without text) is skipped with them.
- `func historyLine(m llm.Message, limit int) string` — historyLine is one message as a single line: when it was said, who said it and what was said, shortened to limit runes.
- `func historyStamp(createdAt string) string` — historyStamp renders when a message was appended, in the shape the operator asked for: DD.MM.YY-hh:mm:ss, in the machine's own zone so it matches the clock on the wall rather than UTC. A message stored before the field existed has no time, and its line simply starts with the speaker.
- `func historyTranscript(msgs []llm.Message) string` — historyTranscript is the whole conversation, for the button: every message with its speaker, in full, so a page can be read like the chat it came from. Newest first, exactly like the card it opens from. The card reads from the end of the conversation back, and opening it used to turn the conversation round: the same messages arrived oldest first, so the last thing said appeared below everything else and the reader had to scroll to the end of a long page to find out where they were. The order is the card's order; the button only shows more of it.
- `func (b *Bot) postHistoryCard(ctx context.Context, c *Client, in inbound, id, title string)` — postHistoryCard puts the card of the conversation the chat was just switched to into that chat. It says nothing when no reader is wired: a switch then reads as it did before.
- `func textUnits(text string) int` — textUnits is the length MAX itself measures: a text is refused when its UTF-16 code units pass the platform limit, not when its characters do. Every emoji a line carries (🤖 for the agent, 👤 for the person) lies outside the basic plane and costs two units, so thirty lines measured in runes fit while the same thirty lines measured in units do not - and the card, promised to the reader, was refused as too long.
- `func clipUnits(text string, budget int) string` — clipUnits cuts text until it fits the budget in the units the platform counts, marking the cut so a reader knows there was more.
- `func shortenRunes(text string, limit int) string` — shortenRunes cuts text to limit runes, marking the cut so a reader knows there was more.

## external/gateway/max/incoming.go

### Types

- `type savedFile struct` — savedFile is one attachment that reached the disk.

### Functions

- `func inboxFolder(kind string) string` — inboxFolder is the folder an attachment of this kind goes to: a document is read, while a picture, a recording and a video are looked at or listened to.
- `func inboxPath(cwd, kind string) string` — inboxPath is the folder of one kind inside a conversation's working directory.
- `func (b *Bot) receiveFiles(ctx context.Context, cwd string, files []InboundFile) ([]savedFile, []string)` — receiveFiles fetches every attachment that is not a picture into the conversation's folder. Pictures arrive already downloaded (the model reads them), so they are saved by saveIncoming instead. Every failure comes back as a sentence for the chat: a person who sent a file has to learn that the bot does not have it.
- `func (b *Bot) receiveFile(ctx context.Context, cwd string, f InboundFile, index int) (savedFile, error)` — receiveFile downloads one attachment to its folder.
- `func (b *Bot) saveIncoming(cwd string, kind, name string, data []byte) (savedFile, error)` — saveIncoming writes a picture the turn already downloaded. The bytes are the ones the model gets, so a picture is not fetched twice.
- `func (b *Bot) videoLink(ctx context.Context, token string) (string, int, error)` — videoLink asks the platform for the file of a video attachment.
- `func inboxLimit(kind string) int64` — inboxLimit is the largest file of this kind the platform itself accepts.
- `func (b *Bot) downloadIncoming(ctx context.Context, rawURL, path string, limit int64) (int64, error)` — downloadIncoming streams one file to path, never holding it in memory: a video of the largest size is not something to load into the bot.
- `func safeInboxName(name, ext string) string` — safeInboxName is the name a saved file gets: the one it arrived with when it is usable on this machine, else the same name repaired. A name is a name, not a path - separators, traversal and the characters Windows refuses never survive - and a name the system keeps for a device is pushed aside.
- `func freeInboxPath(dir, name string) string` — freeInboxPath is name in dir, numbered when that name is taken: an arriving file never replaces the one before it.
- `func inboxExt(path string) string` — inboxExt is the extension of a downloaded file, sniffed from its own bytes: a video arrives without a name, and a name without an extension is a file no tool will open by itself.
- `func inboxExtFor(head []byte) string` — inboxExtFor is the extension of a media type: the picture table first, since MAX sends pictures without a name, then what the system knows of the type.
- `func inboundDuration(kind string, size int64, raws ...int64) int64` — inboundDuration brings a length the platform reports into seconds, or 0 when no reading of the number can be trusted. The units are not consistent: the attachment's field came in milliseconds (a 4-second clip as 4000) and the video-details answer in microseconds (an 11.5-second clip as 11000000, which read as three hours at 2.4 MB). So the number is tried as milliseconds, as microseconds and as seconds, and the first reading that agrees with the file's own size wins; a number no reading fits is dropped rather than printed as a length nobody can believe.
- `func plausibleLength(kind string, size, seconds int64) bool` — plausibleLength says whether a length fits a file of this size for this kind: bytes per second have to stay inside the window of a real recording.
- `func inboxDisplayName(f InboundFile) string` — inboxDisplayName is how a file is named in a sentence to the person.
- `func incomingBlock(saved []savedFile) string` — incomingBlock is what the agent is told about the files a person sent: where each one lies, how big it is and how long it lasts. The gateway does not open them - a recording is not something a text model reads - so the note ends with what to do about it.
- `func inboxKindLabel(kind string) string` — inboxKindLabel names an attachment kind in Russian, for a sentence to the person and for the note to the agent.
- `func durationText(seconds int64) string` — durationText writes a length in seconds the way a person reads it.
- `func humanBytes(n int64) string` — humanBytes writes a size the way a person reads it: 12,3 МБ rather than 12918456 bytes.

## external/gateway/max/media.go

### Types

- `type receivedImage struct` — receivedImage is one picture of an incoming message: the part the model reads and the bytes themselves, which are saved to the conversation's media folder without a second download.

### Functions

- `func multimodalNote(model string) string` — multimodalNote answers a picture sent to a session whose model does not read images.
- `func (b *Bot) inboundImages(m Message) ([]ImageAttachment, error)` — inboundImages lists the image attachments of a message. A malformed attachment is reported through ErrBadUpdate so the caller can log it and carry on with the attachments that parsed.
- `func (b *Bot) downloadImages(ctx context.Context, images []ImageAttachment) ([]receivedImage, string, error)` — downloadImages fetches every image URL with the bot's own HTTP client, so the configured proxy and root certificates apply, and returns them as the session takes attachments. The first failure stops it and names the image.
- `func (b *Bot) downloadImage(ctx context.Context, imageURL string) ([]byte, error)` — downloadImage fetches one image URL and returns its bytes. The download is bounded by size and timeout, and no authentication header is sent: MAX image URLs carry their own token in the query string.
- `func fileType(declared string, data []byte) string` — fileType is the media type a downloaded file goes to the session with: a picture whatever the server declared, since a picture sent as a file is still one for the model; else the declared type, when it is one; else what the bytes look like.
- `func withoutAddress(err error) string` — withoutAddress is the text of a request's error without the URL it went to, which may carry a token.

## external/gateway/max/mirror.go

### Types

- `type mirroredTurn struct` — mirroredTurn fans a turn out to the chat that belongs to it and to the surface that started it. A question or a permission prompt is asked on both: the person may be reading the page or the chat, and a turn that waits for an answer nobody can give stalls where it stands.

### Functions

- `func jobHeader(description string, at time.Time) string` — jobHeader marks a message a scheduled job sends into the conversation: the person must see at once that this is not a reply in the dialogue, but a task that fired on its own, and which one it was.
- `func (b *Bot) reportSession() (string, bool)` — reportSession is the conversation a scheduled job reports into: the chat of an administrator of this gateway, read from the binding that chat already holds. Nothing is minted here - a gateway whose administrator has never written has nowhere to report, and the report then stays in the job's own session.
- `func reportKey(admin int64) string` — reportKey is the binding key of an administrator's private chat: the same one a message typed there is filed under, so a report follows the conversation rather than a number kept beside the job.
- `func (b *Bot) SayJobReport(description string, at time.Time, text string) bool` — SayJobReport shows a scheduled job's report in the conversation of an administrator, headed as the job's own message rather than as a line of the dialogue it lands in. It refuses to invent a conversation: a gateway whose administrator has never written reports that it has nowhere to report, and the caller keeps the text in the job's own session instead of dropping it.
- `func (b *Bot) MirrorTurn(sessionID string, primary acp.UpdateSender) (acp.UpdateSender, func())` — MirrorTurn publishes a turn this bot did not start into the chat its session is bound to, and hands the turn back untouched when the chat owns it already: a message typed in MAX is answered there by the turn itself.
- `func (b *Bot) Say(sessionID, text string)` — Say shows in the bound chat a line written on another surface, so a conversation opened in the browser reads in MAX as it does there.
- `func (b *Bot) chatSender(sessionID string) (*Sender, func())` — chatSender builds the sender of the chat the session is bound to, with nil when this bot serves no such chat or is not connected right now.
- `func (b *Bot) boundChat(sessionID string) (Target, bool)` — boundChat is the chat a session is bound to, read from the map of the chats this bot serves: a direct chat is keyed by its person, a group by its chat.
- `func (m *mirroredTurn) SendSessionUpdate(sessionID string, update interface{}) error` — _no description in the code_
- `func (m *mirroredTurn) RequestPermission(ctx context.Context, params acp.PermissionRequestParams) (*acp.PermissionResult, error)` — _no description in the code_
- `func (m *mirroredTurn) RequestQuestion(ctx context.Context, params acp.QuestionRequestParams) (*acp.QuestionResult, error)` — _no description in the code_
- `func firstAnswer[T any](ctx context.Context, real func(T) bool, asks ...func(context.Context) (T, error)) (T, error)` — firstAnswer runs the same prompt on every surface that can show it and keeps the first answer somebody gave. The prompts the answer did not come from are withdrawn through the cause of the shared context.

## external/gateway/max/model.go

### Types

- `type Update struct` — Update is one event from GET /updates. The message and callback bodies stay raw on purpose: the adapter parses what it needs when it needs it, so an odd field inside a body cannot fail the whole page (see client.go), and a poll pays only for the fields it reads.
- `type Message struct` — Message is the part of a MAX message the bot reads.
- `type Text struct` — Text is a message's text body and any attachments it carries.
- `type Attachment struct` — Attachment is one entry in message.body.attachments. The payload stays raw so an odd attachment cannot fail the whole message body. Name, size, duration and transcription sit beside the payload, not inside it: MAX puts them at the attachment level.
- `type InboundFile struct` — InboundFile is one attachment of an incoming message, ready to be saved: what it is, where to fetch it and how it was called. The gateway saves whatever a person sends - picture, document, sound or video - so the agent works with the file itself instead of being told about it.
- `type ImageAttachment struct` — ImageAttachment is the payload of an attachment whose type is "image".
- `type Recipient struct` — Recipient names where a message arrived. MAX fills the update's own ChatID and UserID for some update types only: for the rest the address is here.
- `type Callback struct` — Callback is a button press.
- `type User struct` — User is the part of a MAX user card the bot reads.
- `type Target struct` — Target addresses one place: a person's chat with the bot, or a chat. MAX takes exactly one of the two.
- `type Button struct` — Button is one key of an inline keyboard. A press of it comes back as a message_callback update carrying Payload verbatim and the CallbackID that answers it. Type is always "callback" - the one kind a bot's own keyboard uses here - and attachments() fills it in when it is left empty.
- `type Outgoing struct` — Outgoing is what the bot posts.

### Functions

- `func (u Update) Body() (Message, error)` — Body parses the message of a message_created or message_edited update.
- `func (u Update) Pressed() (Callback, error)` — Pressed parses the callback of a message_callback update: Payload is what the button carried, CallbackID answers the press.
- `func (u Update) From() User` — From is the person an update came from: the update's own user when it carries one, the message's sender otherwise.
- `func (u Update) MessageKey() string` — MessageKey is the identity of the message an update carries. MAX sends the id of a created message inside message.body.mid and keeps the update's own message_id for other types, so the two are read in that order: a created message keyed on the update's field alone would key on an empty string and be dropped by the de-duplication that reads it.
- `func (m Message) InboundFiles() ([]InboundFile, error)` — InboundFiles lists every attachment of the message the gateway can fetch. A malformed one is reported through ErrBadUpdate, so the caller logs the count and carries on with the attachments that parsed.
- `func (m Message) ImageAttachments() ([]ImageAttachment, error)` — ImageAttachments lists image attachments of the message. A malformed attachment returns the images that parsed together with ErrBadUpdate, so the caller can log the count and carry on rather than drop the message.
- `func (u User) DisplayName() string` — DisplayName names a person in a quote or a log, preferring the card's own name and falling back to the handle.

## external/gateway/max/nopreview.go

### Functions

- `func quietLinks(text string) string` — quietLinks defuses the host-like words of text so MAX stops attaching preview cards to them, and leaves genuine http(s) links alone.
- `func defuseHosts(part string) string` — defuseHosts breaks every host-like word of a span that carries no link. A name can hold more than one dot (README.ru.md), and breaking the first one leaves ru.md behind, which MAX reads as a host just as readily, so the pass repeats until nothing host-like is left.

## external/gateway/max/pictures.go

### Types

- `type pictureSession interface` — pictureSession is the session a chat turn runs in, as far as sending its pictures goes: which session it is, and where its bundle keeps the copies.

### Functions

- `func (s *Sender) sendPictures(sessionID string, images []session.ToolImage)` — sendPictures posts the pictures a finished call of the chat's own session showed the model (read on an image file), one message each, captioned with the file's name, so the chat sees what the agent looked at while the turn runs. The asset is a bare name inside the session's assets directory; any other spelling is not a copy this session made, and is not read. The bytes decide, not the name: a file that is no picture (replaced since the call, damaged) is not uploaded. A picture the messenger refuses is left out, and the answer still comes. A subagent's calls belong to another session and are not the chat's to see.
- `func (s *Sender) postPicture(name string, data []byte) error` — postPicture stores one picture and sends it as a message of its own, captioned with its name. The timeout is per picture, so a slow upload costs one picture and not the turn.

## external/gateway/max/poll.go

### Types

- `type Handler interface` — Handler takes one update. The loop of a bot hands updates over one at a time and in the order the server sent them.

### Functions

- `func (b *Bot) PollLoop(ctx context.Context, c *Client, h Handler) error` — PollLoop reads the update stream until ctx is cancelled, handing every update to h on its own. It returns nil when ctx ends and the error of a failed poll otherwise; a page whose elements did not all parse is delivered and logged rather than returned, and is never fetched twice. The client keeps the position, so neither outcome loses it. The adapter does not call this yet: GET /updates moves the marker as soon as it succeeds, so a poll with nobody to hand the updates to would move it past messages the bot never read and lose them. Start says the same where it waits; the loop arrives in it together with the session handling it serves.
- `func (b *Bot) pollInterval() time.Duration` — pollInterval is the pause the loop keeps between two polls: the configured interval, or the one a test set to poll faster than the one-second floor of the configuration.
- `func pollLoop(ctx context.Context, c *Client, h Handler, interval time.Duration, log *slog.Logger) error` — pollLoop is PollLoop over an interval given outright, so a test can poll faster than the configuration allows.

## external/gateway/max/resume.go

### Types

- `type sessionLister interface` — sessionLister is the part of the session manager /resume needs. It is claimed by a type assertion rather than added to SessionRunner, so a runner that only runs turns keeps working and the menu says it cannot list.
- `type sessionOriginReader interface` — sessionOriginReader reports where a session was started without loading it (session.Manager.SessionOrigin).
- `type turnActiveChecker interface` — turnActiveChecker reports whether a session runs a turn in this process (session.Manager.SessionTurnActiveInProcess).
- `type resumeMenu struct` — resumeMenu is one page of the keyboard with the text above it.

### Functions

- `func (b *Bot) handleResume(ctx context.Context, in inbound, key, args string)` — handleResume lists the conversations to switch to, or switches to the one a query names.
- `func (b *Bot) handleResumeAction(ctx context.Context, c *Client, u Update, rest string) error` — handleResumeAction answers a tap on the resume keyboard: a page turn redraws the menu in place, a session pick binds the chat and replaces the menu with the confirmation, so the keyboard does not outlive the choice.
- `func (b *Bot) confirmResume(ctx context.Context, c *Client, u Update, in inbound, row acp.SessionListInfo)` — confirmResume writes the confirmation of a switch into the message the tap came from, so the menu and the choice do not linger as two messages. A press that carries no message to rewrite is answered with a message of its own.
- `func (b *Bot) resumeSession(ctx context.Context, c *Client, in inbound, id string)` — resumeSession switches the chat of in to id, when only the id is known.
- `func (b *Bot) resumeSessionRow(ctx context.Context, in inbound, row acp.SessionListInfo) (string, [][]Button)` — resumeSessionRow switches the chat of in to row and returns what the chat is now on, with a keyboard that takes the menu away. It posts nothing: the caller sends the text as a message of its own or writes it into the message the choice came from, so a tap on a menu leaves one message behind and not two. The session is loaded first: a bundle that cannot be read is reported here, and the mapping is left as it was, so the chat keeps the conversation it had.
- `func (b *Bot) resumeMenu(rows []acp.SessionListInfo, current string, page int, query string) resumeMenu` — resumeMenu lays out one page of the keyboard over rows: a button per session, the chat's current one marked, and a navigation row when there is more than one page. With a query the rows are the matches and there is no navigation - the query does not fit the callback payload, so a second page could not be asked for - and the text says how many matched instead; a narrower query is the way to the rest.
- `func resumePageCallback(page int) string` — resumePageCallback is the payload of one navigation arrow.
- `func (b *Bot) listSessions(ctx context.Context) ([]acp.SessionListInfo, error)` — listSessions asks the server for the sessions it keeps, newest first. No folder narrows the list: a chat has no working directory of its own, so it sees what a console connected to this server would see.
- `func (b *Bot) sendResumeMenu(ctx context.Context, c *Client, in inbound, menu resumeMenu)` — sendResumeMenu posts the menu into the chat of in.
- `func (b *Bot) resumeReply(ctx context.Context, c *Client, in inbound, text string)` — resumeReply posts one plain message into the chat of in. A confirmation is read on a phone: it carries no markup, so nothing is left unrendered there.
- `func (b *Bot) callbackInbound(u Update, cb Callback) inbound` — callbackInbound reads the chat, the person and the kind of chat out of a callback update: a press carries the message it was made on, and MAX puts the ids in different places for a message and for a press.
- `func (b *Bot) sessionKey(in inbound) string` — sessionKey is the key the chat-to-session map holds for in.
- `func matchResumeSessions(rows []acp.SessionListInfo, query string) []acp.SessionListInfo` — matchResumeSessions returns the sessions query names: the one whose id it is, or otherwise every one whose id starts with it or whose title contains it, case-insensitively. A query is what a person types from memory, so it is read loosely; the exact id is checked first, byte for byte, because an id spelled in full can be nothing else.
- `func resolveResumeCallback(rows []acp.SessionListInfo, payload string) (acp.SessionListInfo, bool)` — resolveResumeCallback maps the payload of a session button back to a row of the current listing. A session deleted since the keyboard was sent resolves to nothing, which is a better answer than binding the chat to a bundle that is gone.
- `func (b *Bot) resumeButtonLabel(row acp.SessionListInfo, current bool) string` — resumeButtonLabel is what a session button shows: the mark of the chat's current conversation, the sign of the surface the session came from, and its title - or the id of a session that has none yet.
- `func (b *Bot) resumeLabel(row acp.SessionListInfo) string` — resumeLabel names a session in prose and on a button: its title, or the id of a session that has none yet, with the sign of where it came from.
- `func (b *Bot) originMark(sessionID string) string` — originMark is the sign of the surface a session came from: a plane for a conversation held in Telegram, a globe for one opened here or in the web UI, a speech bubble for any other messenger. A conversation of this gateway carries no sign - the menu around it already says where it is - and a session whose origin nothing can report stays unmarked rather than being guessed at.
- `func (b *Bot) turnActive(sessionID string) bool` — turnActive reports whether the session runs a turn in this process.
- `func resumeSettingsLine(cfg *config.Config, st *session.State) string` — resumeSettingsLine names what a resumed session runs on, as it keeps it: its model and its mode. It is the session's own, never the model last picked on this bot, which only a fresh chat starts on.
- `func rowTitle(row acp.SessionListInfo) string` — resumeTitle names a session in prose: its title, or a short form of the id of one that has none yet.
- `func shortSessionID(id string) string` — shortSessionID is the readable head of an id, for a session with no title yet: enough to recognise it, short enough for a button.
- `func truncateRunes(s string, n int) string` — truncateRunes cuts s to at most n runes, ending it with an ellipsis when it was longer. Counting runes, not bytes, keeps a Cyrillic title from being cut inside a character.

## external/gateway/max/sender.go

### Types

- `type Sender struct` — Sender implements acp.UpdateSender for one turn in one chat.
- `type presses struct` — presses holds the prompts waiting for a button press, keyed by the token their buttons carry. The zero value is ready to use, so a Bot carries one without a constructor.
- `type pressAnswer struct` — pressAnswer is what a prompt got: the place of the pressed option, a request for a written answer, the written answer itself, or the end of the wait when nobody will answer any more.
- `type press struct` — press is one prompt waiting for its answer: who it was asked of, the options it offered, whether it now waits for a written answer, and the channel its answer arrives on.

### Functions

- `func (s *Sender) Detach() { s.detached.Store(true) }` — Detach stops the turn from writing into its chat. The turn itself is not cancelled: it belongs to its own session, and the session keeps the answer.
- `func (s *Sender) isDetached() bool { return s.detached.Load() }` — isDetached reports whether the chat of this turn now belongs to another conversation.
- `func (s *Sender) sessionID() string` — sessionID names the session of this turn, for the log.
- `func (b *Bot) newSender(ctx context.Context, c *Client, target Target) *Sender` — newSender is the sender of one turn in one chat. Step 5 hands it to the session manager; nothing calls it from Start.
- `func (s *Sender) SendSessionUpdate(sessionID string, update interface{}) error` — SendSessionUpdate handles the agent's streaming events.
- `func outgoingAnswer(text string) string` — outgoingAnswer is answer text as a chat can show it: the placement markers of shared files exist for the web UI, which draws the artifact card where the marker stands, and no messenger renders them. The strip runs on the whole text at once, never on each accumulated chunk - a strip trims whitespace, and doing it per chunk would eat the spaces and newlines between chunks and glue words together. The stored message keeps its markers: only the way out is stripped.
- `func (s *Sender) post(text string)` — post sends a message of its own: a note that does not belong to the answer.
- `func (s *Sender) Flush()` — Flush posts the answer of the turn as its closing messages, continued in further messages when it does not fit in one. Everything the turn did went out before this, so the answer is the last thing the chat receives.
- `func (s *Sender) deliver(ctx context.Context, text string)` — deliver posts one piece of the answer. The pieces go out in order, one after another, so the closing answer reads the way the turn wrote it.
- `func (s *Sender) RequestPermission(ctx context.Context, params acp.PermissionRequestParams) (*acp.PermissionResult, error)` — RequestPermission asks the chat about a tool call of a subagent, with one button per option, and waits for the press. What the chat's own agent asks is allowed without asking: the operator who configured the bot decided that, which is what the mode stamped on the request says.
- `func (s *Sender) RequestQuestion(ctx context.Context, params acp.QuestionRequestParams) (*acp.QuestionResult, error)` — RequestQuestion posts the question and, when it offers options, waits for one of them to be pressed. A question without options has no form here: it goes out as text and the person answers with their next message, which is why such a question contributes no answer.
- `func (s *Sender) askOptions(ctx context.Context, text string, options []string) (int, bool)` — askOptions posts text with one button per option and waits for a press. It answers with the place of the pressed option and true, or -1 and false when nobody pressed anything before the turn ended. The message is settled either way: it says how the prompt ended and loses its keyboard, so no button of a finished prompt can be pressed again.
- `func (s *Sender) askChoice(ctx context.Context, text string, buttons, spoken []string, written bool) (pressAnswer, bool)` — askChoice is askOptions for a prompt whose buttons are shorter than the options they stand for: the settled message names the chosen option in full, so the note does not say "✅ B". With written set it also offers to take the answer as a written message: the browser has a field for that, and the chat must not be poorer than the browser.
- `func customAnswerRow(token string) []Button` — customAnswerRow is the row under the options that turns the prompt into one waiting for a written answer.
- `func listedQuestion(text string, options []string) string` — listedQuestion puts every option under the question, so the whole text of an option is readable where the platform cuts a button's own label down to a word.
- `func optionLetters(n int) []string` — optionLetters names the options A, B, C ... and numbers them past the alphabet, so a prompt of any size still has a button per option.
- `func endedNote(ctx context.Context) string` — endedNote says how a prompt nobody pressed ended. A prompt withdrawn because the same question was answered on another surface says so: "Больше не жду" would read as if the person had let it pass.
- `func (s *Sender) settle(messageID, text, note string)` — settle ends a prompt: the message keeps its text, says how it ended and loses its keyboard (an empty attachments list is how an edit takes one away).
- `func (b *Bot) HandleCallback(ctx context.Context, c *Client, u Update) error` — HandleCallback answers a button press: it confirms the press, so the button stops spinning for whoever pressed it, and hands the choice to the prompt that waits for it. A model-keyboard press changes the session's model and edits the menu in place. A press that belongs to no prompt and no model menu - a stale message whose turn has ended, a payload this bot never wrote - is confirmed too: a button the bot will not act on must not keep the keyboard of the messenger waiting. Step 5 calls this from the poll loop; nothing calls it from Start.
- `func (b *Bot) handleDetailsAction(ctx context.Context, c *Client, u Update, payload string) error` — handleDetailsAction opens a tool call into the message that carries it, turns its pages, or folds it back to one line. The message is edited in place, so the chat keeps one message per call instead of one per page it was read on.
- `func (b *Bot) handleModelAction(ctx context.Context, c *Client, u Update, cb Callback) (string, error)` — handleModelAction resolves and applies a model-button press. It returns the notification shown to the presser, or an error if the update is incomplete.
- `func (b *Bot) resolvePress(u Update, cb Callback) (string, bool)` — resolvePress gives a press to the prompt behind its payload and answers with the notification the presser sees: the label of the button that was pressed, or "" when the press belongs to no prompt that still waits.
- `func pressedBy(u Update, cb Callback) User` — pressedBy is the person who pressed a button: MAX puts them in the callback itself. The update's own user is the fallback, and the message the keyboard sits on is no help at all - the bot wrote it, so a press read from the message came back as the bot and every press was refused as somebody else's.
- `func (p *presses) offer(target Target, options []string) (string, chan pressAnswer)` — offer registers a prompt under a fresh token and returns the token and the channel the answer arrives on.
- `func (p *presses) drop(token string)` — drop withdraws a prompt nobody waits for any more.
- `func (p *presses) resolve(token string, index int, userID int64) (string, bool)` — resolve hands a press to the prompt behind token and answers with the label of the option it chose. It refuses a token nobody waits for, an option the prompt never offered, a prompt already answered, and a press from somebody other than the person a private prompt was asked of; a press in a shared chat is anybody's, since the session there is shared.
- `func (p *presses) arm(token string, userID int64) bool` — arm turns a prompt into one waiting for a written answer: the person pressed the button that says they will write it, and the prompt stays behind to take that message.
- `func (p *presses) armed(userID int64) bool` — armed reports whether this person was told to write the answer to a prompt.
- `func (p *presses) disarm(userID int64)` — disarm withdraws the wait for a written answer: a command is never an answer, and the prompt a person walked away from must not swallow their next question.
- `func (p *presses) write(userID int64, text string) bool` — write hands the message a person typed to the prompt that waits for it. It answers false when no prompt of this person waits for a written answer, and the message goes on to be an ordinary one.
- `func newPromptToken() string` — newPromptToken is the unguessable key of one prompt. A press carries it back and a token nobody waits for is refused, so it is drawn from the system random source rather than counted.
- `func optionRows(token string, options []string) [][]Button` — optionRows lays options out as an inline keyboard, one button per option and at most maxButtonsPerRow in a row. Every payload names the prompt and the place of the option, which is what a press is resolved with.
- `func optionText(name, fallback string, index int) string` — optionText names one option: its own name when it has one, the fallback otherwise. A button cannot be empty - MAX would refuse the keyboard - and an option nobody named is better pressed by its place than not at all.
- `func permissionText(params acp.PermissionRequestParams) string` — permissionText is what the chat is asked about.
- `func questionText(q acp.QuestionPrompt) string` — questionText is what a question says: its header, when it has one, above the question itself.
- `func previewText(text string, limit int) string` — previewText is the tail of the answer, as the live message shows it.
- `func splitText(text string, limit int) []string` — splitText cuts text into pieces of at most limit runes - the size one MAX message takes - preferring a paragraph break, then a line break, then a space, and never cutting a rune in two. The pieces together hold the whole text: an answer that does not fit in one message continues in the next instead of being truncated.
- `func lastBreak(r []rune) int` — lastBreak is where a piece ends: the last paragraph break, line break or space in the run up to the limit - never before its middle, so a piece is not cut shorter than it has to be - or the limit itself when the text has no break in it.

## external/gateway/max/stop.go

### Functions

- `func (b *Bot) handleStop(ctx context.Context, in inbound, key string)` — handleStop ends the turn of this chat at once, together with what is waiting behind it. Waiting messages would start a new turn the moment the stopped one returns, which reads as if the stop did nothing.
- `func (b *Bot) markStopped(key string)` — markStopped records that the person ended the turn of this chat.
- `func (b *Bot) personStopped(key string) bool` — personStopped reports whether the person ended the running turn, and forgets it once asked: the next turn of the chat is nobody's stop but its own.
- `func stopNote(running bool, dropped int) string` — stopNote says what was stopped and how much of the queue went with it.
- `func countMessages(n int) string` — countMessages spells a number of messages the way the rest of the bot does.
- `func (b *Bot) registerTurn(key string, cancel context.CancelFunc)` — registerTurn puts the turn of key on record, so /stop can end it from the intake path of another goroutine.
- `func (b *Bot) unregisterTurn(key string)` — unregisterTurn forgets a turn that ended on its own.
- `func (b *Bot) cancelTurn(key string) bool` — cancelTurn ends the turn of key and reports whether there was one to end.
- `func (b *Bot) dropQueued(key string) int` — dropQueued takes back what waits in the worker queue of this chat. The worker counts every accepted job in inFlight, so a dropped one is released here.

## external/gateway/max/tooldetails.go

### Types

- `type toolDetail struct` — toolDetail is one call: what was run and what came back.
- `type detailStore struct` — detailStore keeps the calls a chat may expand, keyed by the token their buttons carry. The zero value is ready to use.

### Functions

- `func (s *detailStore) put(d toolDetail) string` — put stores a call and returns the token its button carries.
- `func (s *detailStore) setBody(token, body string)` — setBody puts what a call returned into the stored entry. A call whose token has already been dropped is left alone: its button is stale by then, and a press on it says so.
- `func (s *detailStore) get(token string) (toolDetail, bool)` — get returns the stored call.
- `func (s *detailStore) setCommand(token, command string) bool` — setCommand puts what a call was made with into the stored entry, and reports whether the entry took it: the call reports its arguments once, and a second report must not rename a message that already stands in the chat.
- `func detailPages(body string) []string` — detailPages cuts a call's output into pages that each fit one message, preferring to end a page on a line break so a line is not split in two.
- `func detailsCallback(token string, page int) string` — detailsCallback builds the payload of one details button: page 0 opens the call, a positive page turns to that page, pageCollapsed folds it again.
- `func icon(d toolDetail) string` — detailsView renders what a details press shows: the command, one page of its output, where in the output the page is, and the buttons that turn the pages. The collapsed form is the one-line message the call started as. icon is what leads an entry's view: the gear of a tool call unless the entry named its own.
- `func buttonText(d toolDetail) string` — buttonText is what a folded entry offers: the details button unless the entry named its own.
- `func detailsView(token string, d toolDetail, page int) (string, [][]Button)` — _no description in the code_
- `func detailTitle(d toolDetail) string` — detailTitle is the one line a call is called by: what it ran once the arguments are known, the tool's name until then.
- `func oneLine(s string) string` — oneLine folds a command into one line, so the collapsed message stays a single line however the command was written.
- `func callArguments(items []acp.ToolCallResultItem) (string, bool)` — callArguments reads what a call was made with out of the update the agent sends when the call starts: the arguments travel as that update's text, so the chat can name a call the way the web does. It reports false for output that happens to be JSON but names none of the arguments.
- `func (s *Sender) postDetails(token, title string) string` — postDetails puts one call into the chat with the button that opens it.
- `func (s *Sender) renameDetails(token, mid string)` — renameDetails puts the command into the message the call already has, so the chat names a call by what it ran rather than by the tool that ran it, and gains no extra message for it.
- `func toolCallBody(items []acp.ToolCallResultItem) string` — toolCallBody is what a tool call returned, as the ACP update carries it.
