# MAX gateway

Coddy can answer in [MAX](https://max.ru/) (МАКС), the Russian messenger, as a bot. A person writes to the bot in a direct chat or mentions it in a group, and the message starts or continues an agent turn; the answer streams into one message of the chat. The bot runs inside `coddy serve` next to the HTTP server and the Telegram bot, and every conversation is an ordinary Coddy session, so the web UI shows it live.

Like the [Pachca gateway](pachca.md), the adapter is deliberately smaller than the [Telegram gateway](gateway.md): it reads the bot's `GET /updates` stream by long polling instead of receiving a webhook, so the machine running Coddy needs no public address. It covers chat turns, a few commands, buttons for subagent permission prompts, and per-chat access and isolation.

## Contents

- [Set up the bot in MAX](#set-up-the-bot-in-max)
- [Configuration](#configuration)
- [Running it](#running-it)
- [How the bot behaves](#how-the-bot-behaves)
- [Commands](#commands)
- [What somebody who is not an admin may do](#what-somebody-who-is-not-an-admin-may-do)
- [Answers, limits and formatting](#answers-limits-and-formatting)
- [Incoming files pile up](#incoming-files-pile-up)
- [Permissions](#permissions)
- [What is not supported](#what-is-not-supported)
- [Troubleshooting](#troubleshooting)

## Set up the bot in MAX

1. Create a bot in MAX and copy its API token. The adapter sends the token raw in the `Authorization` header of every request, exactly as the MAX Bot API expects.
2. Point `ca_file` at the MinCifry root certificate bundle. MAX serves its API over a certificate signed by the Russian national root, which is not in the system trust store of most hosts; without `ca_file` the client fails with `x509: certificate signed by unknown authority`. The bundle is added to the system store, never a replacement for it.
3. Put your MAX user id in `admins`, otherwise the bot lets nobody in but approves nothing for anybody, and `--dry-run` warns about it.

The bot token is a secret: leave `token` empty and export `MAX_BOT_TOKEN` (for example in `~/.coddy/.env`) to keep it out of `config.yaml`.

## Configuration

```yaml
gateways:
  max:
    enable: true
    token: ""                    # or MAX_BOT_TOKEN in the environment or ~/.coddy/.env
    proxy: ""                    # inherit (default), none, or an http(s)/socks5(h) proxy URL
    ca_file: ""                  # PEM bundle of the MinCifry root (required in practice)
    poll_interval_seconds: 1     # 1 to 60; pause between two polls
    poll_timeout_seconds: 30     # 1 to 60; how long one poll may be held open
    admins: [12345]              # MAX user ids
    default_access: all          # all | admins | group:<name>
    default_isolation: individual  # individual | shared | admin
    user_groups:
      - name: devs
        user_ids: [12345, 67890]
    chats:
      - chat_id: 334
        isolation: shared
        access: group:devs
```

Access and isolation read exactly like the Telegram ones ([Access levels](gateway.md#access-levels), [Session isolation modes](gateway.md#session-isolation-modes)): `default_access` decides who may talk to the bot, `default_isolation` whether a group chat has one session per person, one for everyone, or one for the admins only, and `chats[]` overrides both per chat. A direct chat (a `dialog` in MAX) always has a session of its own.

The same keys are on the **Gateways** tab of the web settings, and a token or any other key changed there rebuilds the MAX bot in place, leaving a Telegram bot in the same process running. The field reference is in [config.md](../reference/config.md).

## Running it

The adapter is behind the `gateway` build tag (or `gateway.max` for this adapter alone); the release binaries, the packages and the Docker image carry it.

```bash
make build TAGS="gateway.max"
```

```bash
coddy serve --dry-run
```

`--dry-run` checks the token with `GET /me`: an error for a missing or revoked token, a warning when `admins` is empty. Then start the server:

```bash
coddy serve
```

The banner names the bot (`gateway     max`), and the log says `max bot connected` with its name and user id. `CODDY_MAX_API_BASE` points the bot and the dry run at another API base, a test server for example; the default is the MAX Bot API host.

## How the bot behaves

- **Direct chat**: every message is for the bot.
- **Group chat or channel**: the bot answers a message from anyone the access level lets in. Mention gating (answering only a message that names the bot) is not implemented yet, so a group the bot is added to should keep `default_access` to `admins` or a named group.
- **Order and catch-up**: the bot long-polls `GET /updates` every `poll_interval_seconds`. The cursor moves only after a whole page is handled, so a stop in the middle of a page replays it on the next start instead of losing the rest; a message id is de-duplicated so a replayed page still runs one turn. The position is kept in `<sessions>/gateway_max_cursor`, so a restart answers what was written while the bot was down.
- **One turn at a time per session**: messages of one conversation queue up behind the running turn, and a stop or a restart waits up to 20 seconds for the running turn and the queued ones before the bot goes. A message that finds the queue full (32 waiting) is not taken, and the chat is told to send it again.
- **The bot never answers itself**: its own messages arrive back in the stream and are told apart by the `is_bot` flag of their sender.
- The chat-to-session map lives in `<sessions>/gateway_max_sessions.json`, apart from the Telegram and Pachca ones.

## Commands

| Command | What it does |
|---------|--------------|
| `/help`, `/start` | The command list and a greeting. |
| `/clear` | Starts a new session for the conversation; the old one stays on disk. |
| `/resume` | Lists the sessions of this server - eight per page, the one the chat is on marked ▶️ - and switches the conversation to the one a tap names; `/resume <id or part of a title>` finds it by id prefix or by a fragment of the title, and a single match switches at once. The origin is a pictogram: ✈️ Telegram, 🌐 web or console, 💬 another gateway, nothing for MAX itself, and `/help` carries the legend. The session is loaded before the chat is bound to it, so a bundle that cannot be read leaves the chat where it was. Admins only, as on the Telegram side. |
| `/stop` | Ends the turn running in this conversation at once, through the same session cancel the web UI and the CLI use (`session/cancel`, so the session knows a person stopped it and the core ends the turn quietly instead of reporting an interrupted generation) - the model's work is cancelled, not merely hidden - and drops whatever waits behind it in the queue, since a waiting message would start a new turn the moment the stopped one returns. It is answered on intake, before the burst wait and before the queue, so it never waits for the answer it stops. In a group this is the admins'. |
| `/model` | Opens an inline menu with one row per configured model; the current model is marked with ✓. A tap switches the model for the conversation and moves the checkmark to the new choice. `/model <id>` still sets the model directly by text. The menu is offered to admins in every chat, a personal one included; a non-admin tap is answered with *Менять настройки в этом чате могут только админы бота.* An admin's pick is also remembered as the bot's last model, so the next new conversation of the bot starts on it; anybody else's pick stays in their own session. |
| `/context` | The context window usage of the conversation's session. |
| `/goal <objective>`, `/goal`, `/goal pause`, `/goal resume`, `/goal clear` | The session goal, handed to the session like on every surface ([Session goal and supervisor](../features/session-supervisor.md)). |
| `/agent`, `/plan`, `/ask`, `/think`, `/nothink`, `/reasoning <level>` | Settings commands, handed to the session; `--once` and `--count=N` limit them to the next messages. |

Every command is read in any case: `/stop`, `/STOP` and `/Stop` are one and the same, and so are the rest - the word is lowercased when the text is parsed.

A switch of the conversation is invisible in a messenger: the messages stay where they are and the session behind them becomes another one. So `/resume` posts a **history card** right after the confirmation - a divider with the conversation's title and how many messages it holds, then up to thirty of its last messages, newest first, one per line: a `DD.MM.YY-hh:mm:ss` stamp in the machine's own zone, then `👤` for the person or `🤖` for the agent, then the text shortened to 120 characters - and a **📜 Развернуть** button that pages the whole transcript the way the details button pages a tool output. The transcript is read from the session, not from the chat: MAX stores what it rendered, without the markdown markers. A card has to fit one message, and its length is counted in
the same units as the platform's limit - UTF-16 code units, where the `👤` and `🤖` markers cost two: counting
characters produced a card that passed our own check and hit the platform's
(`Field 'text' size (4014) must be at most 4000`), and the person saw the confirmation with no history under it.
A long conversation therefore loses its oldest lines and gets the "…и ещё N строк" note, and if the platform
refuses the message itself, a line saying so goes to the chat: a promise does not disappear in silence. Nothing is deleted on a switch - in a dialog the bot may remove only its own messages, and a screen half-wiped reads worse than a full one.

What changes the settings - the settings commands, `/model` and `/clear` - is the admins' (`admins`) in every chat: anybody else is answered *Менять настройки в этом чате могут только админы бота.* The modes and the reasoning level stay available to everybody where the chat is not shared; in a group they are the admins' too, and a direct chat has no members to share anything with. Anything else that starts with a slash is somebody else's command and is ignored.

The client offers these when somebody types `/`: at every start the bot publishes its list through `PATCH /me/commands` (at most 32 commands, a name up to 64 characters, a description up to 128; an empty list takes the hints away). The descriptions are in Russian, because MAX is a Russian product and the person reads them there, while the command names stay in Latin since they are what gets typed. The published list is the short one a person meets first: `think`, `nothink`, `reasoning` and `toolset` work and are handed to the session, but stay out of the hints, as the aliases `effort` and `no_think` do. A refused publish is a warning in the log, not a failure: the bot answers without hints and publishes again at the next start.

## What somebody who is not an admin may do

The bot sees who wrote every message. Its admins (`admins`) can do everything; anybody else gets the conversation and nothing that changes the agent: only reading inside the session's working directory, the documentation, a web search, the plan and a question back; everything else - MCP tools included - is refused, and every call that needs approval is refused, whatever the session's permission mode and its grants - the same rule as the Telegram bot. With an empty `admins` list everybody only chats, and `--dry-run` warns ([What somebody who is not an admin may do](gateway.md#what-somebody-who-is-not-an-admin-may-do)). MAX has no web UI entry: the chat is the whole surface.

## Answers, limits and formatting

A message that arrives while the session's previous turn is still running waits for its place: the turns of one session are serialized, and the message is taken as soon as the turn ends. Only when the wait runs out - about half a minute - the chat is told, in Russian, to send the message again; the internal "session busy" text never reaches the person.

The gateway is transparent both ways. A message typed in MAX runs in the session the chat is bound to, and a turn somebody starts on another surface - the browser, the console - is published into that chat as well, with the person's own line shown there first: an answer without the question it replies to reads as a monologue. A turn this chat started is left alone, so nothing is shown twice. A question with options and a permission prompt go to both surfaces at once: the first answer decides, and the other prompt is withdrawn with the note «✅ Ответ дан в другом окне.», so a keyboard nobody can use any more does not hang there.

A question card also carries the button «✍️ Свой ответ» - the same thing the browser's free-answer field is. Pressed, the card is rewritten: the buttons come off, «✍️ Свой ответ: напишите его следующим сообщением.» appears at the bottom, and the next message of this chat becomes the answer to the question. Once only: the message after that is an ordinary turn again. A command is never an answer - it runs as a command and the wait ends. The wait lasts exactly as long as the question: the turn ends, the card goes with «Больше не жду.». A message that carries no text, a picture or a file, is not the answer either: the card says the same thing again and waits for text.

A line and the picture it is about are two messages of MAX, a second or two apart, and the bot holds both for a moment - three seconds, restarted with every new message of the chat - so they run as one turn: the answer covers the text and the picture together, instead of answering the text and then taking the picture as a separate question. A command never waits: it is answered at once, and whatever the chat held before it goes first, so the order of what was written stays the order of what is answered. The wait only delays the start of the turn; what is held has already passed the polling cursor, so a stop hands it over instead of losing it.

The answer is posted as one message at its first words and edited in place while it is written, with the running tool's name under it; at the end the message is replaced by the full answer. An answer longer than 4000 runes is split into further messages without cutting a rune. The sender keeps to about two writes a second, so the answer is delivered in chunks rather than refused.

Pictures the agent reads during a turn are posted into the chat as the calls happen, not with the answer: each one is a message of its own, captioned with the file's name.

Everything a person sends is saved, not described. Each attachment of an incoming message lands in the conversation's own `Max-in` folder inside its working directory - documents in `Max-in/Docs`, pictures, recordings and videos in `Max-in/Media` - and the prompt of the turn names every saved file with its size, its duration, its full path and, when the platform transcribed a recording, that text too. The gateway does not open a video or a recording: what to do with them is the agent's business, and it decides with the tools it has. A name is kept as sent when the platform gave one and the operating system accepts it; separators, traversal and the characters Windows refuses are repaired, a name the system keeps for a device is pushed aside, and a name already taken gets a number instead of replacing the file. The limits are the platform's own, so a file MAX accepted is never refused here: 250 MB for a video, 256 MB for a recording, 4 GB for a document, 50 MB for a picture. A video's payload is a player page, not the file, so the bytes come from `GET /videos/{token}`, best resolution first. A length is read, not trusted: the platform reports it in milliseconds in the attachment and in microseconds in the video details answer, so the number is tried both ways and as plain seconds, and the reading that agrees with the file's own size wins - a video is believed between 40 kbit/s and 16 Mbit/s, a recording between 8 and 800 kbit/s. A length no reading fits is left out instead of being shown as "183 min 20 s" for an 11-second clip. A file that could not be fetched, or that the platform would not hand over, is named in the chat with the reason, and the turn goes on.

A document the agent produces reaches the chat the same way, as a message of its own: a call that captured a workspace file as an artefact publishes it in the call's meta, and the gateway sends it when the call completes. MAX takes it in three calls - `POST /uploads?type=file` answers an address, the bytes go there as multipart in a field named `data` with no authorization, and `POST /messages` carries the token that the second answer returned; the first answer carries none. One message carries one document, captioned with its name and, when whoever produced it wrote anything about it, that text after it, cut at the same 4000-rune budget. Nine document kinds go out - pdf, doc, docx, xls, xlsx, csv, txt, zip, md - up to 25 MiB, which is the ceiling Coddy puts on a file a tool call captures as an artefact (100 MiB for a whole session); the platform itself takes 4 GB, but a turn lasts ten minutes and the upload inside it five, so the smaller of the two is the one that decides, and the refusal names the limit it hit. A video and a recording go out as ordinary files: the upload and the message are the ones a document takes, and the chat gets a file it keeps and can save. That is deliberate - the video upload answers XML rather than JSON and issues its token together with the address, while the file upload is a plain answer whose token never failed - and the price is the player the chat would have offered. Their kinds are still told apart, because they are bigger and the platform prepares them after the upload: mp4, m4v, f4v, mov, mkv, webm and avi up to 250 MB, mp3, wav, m4a, ogg, aac, flac and opus up to 256 MB, with the longer wait they need - eight minutes on the wire, and the readiness repeat at five, ten, fifteen and twenty seconds, five attempts at most. The send list follows what the gateway takes in, so a file a person sent can always be returned: f4v and m4v are the same MP4 container under another name. Anything else is refused before a byte is uploaded, in Russian, in the chat. The bytes are the copy the session captured, checked against its digest and, for a chat that is not the admin's, against that session's own working directory, so a path the model wrote is never opened. `attachment.not.ready` - the platform still processing the upload - is waited out by repeating the message only, three, six and nine seconds apart, four attempts at most, because a repeated upload would leave a second copy nobody can delete. The picture is uploaded to `POST /uploads?type=image` and sent as an image attachment, and it is the copy the session kept of what the model was shown - not the workspace file, which may have changed since. A picture MAX refuses, or one that cannot be read, is left out, and the answer still comes.

A tool call is a message of its own: the command it ran, with a 🔍 Подробности button under it. The button edits that same message into the command and what it returned, page by page when the output is long, and Свернуть puts it back to one line. The call is named by what it ran, not by the tool that ran it: the agent reports a call's arguments together with the call, so the message heads with the command - one line, cut at 220 runes - and the opened view prints the command in full above the output, as the web does. A short command is not repeated: when it already fits the title, the opened view goes straight to the output.

The call itself is kept by the bot, not in the message: a press after the turn has ended still finds it, and the store holds the last 200 calls.

MAX attaches a link preview to any word that reads like a host name, and a tool call is full of them: `README.md` is a domain - `readme.md`, a parked one that serves ad spam - and `burst.go` was read as one too, so the chat filled with cards for pages nobody sent. Every message and every edit therefore goes out with the query parameter `disable_link_preview=true`, which is the switch the reference documents on `POST /messages` and the only thing that works: the same name in the request body is dropped without a word, and neither `format` nor a code span changes anything. The API honours it on `PUT /messages` as well, although the reference does not list it there - an edit without it put the card back. The flag stops the card, not the link: the client would still draw a file name as a hyperlinked address, which the operator caught in a history card on 18.10. So the guard stays as well - a zero-width space after the dot of a host-like word, invisible to a reader and enough to stop MAX reading it as a host. A link somebody wrote with `http://` or `https://` keeps its words and still opens.

The answer goes out with `format: markdown`, so MAX draws what the agent wrote: bold, italic, strikethrough, code and headings arrive as formatting rather than as markers. Our own texts - menus, notes and confirmations - go as plain text, because a session or model id in them holds underscores that markdown would read as emphasis.

## Incoming files pile up

Attachments are saved in the `Max-in` folder with no overall quota and no automatic cleanup. Watch the
free space on the disk, especially under `default_access: all` and when large documents or videos come
in. Do not give the bot to unvetted users without a separate assessment of that risk. A quota and
automatic cleanup rules are under development.

The folder belongs to the conversation and sits inside its working directory: documents in
`Max-in/Docs`, pictures, video and recordings in `Max-in/Media`. The only per-file limit is the
platform's - MAX takes a document up to 4 GB. If the working directory is a git repository the folder
shows up in `git status`, so keep it ignored.

## Permissions

An admin's own agent is allowed what it asks, like in Telegram: the operator configured the bot deliberately; for anybody else nothing is approved (above). A subagent whose definition narrows what it may do is asked about in the chat with **Allow** and **Reject** buttons, and the click is answered back with `AnswerCallback` so the button does not hang. A background subagent's prompt and a woken turn (a background task the agent started with `notify_on_finish`) are not delivered to the chat yet.

## What is not supported

Webhook intake (the bot only long-polls `/updates`), `/mcp`, reactions and forms, replies and quoting, group mention gating, detached subagent permission prompts, woken turns. A person editing a message does not start a turn. A message with no text and no attachment is answered with a note that the bot reads text or pictures; pictures are only passed to models that declare `multimodal: true`. Transcribing a recording is not the gateway's job either: the platform's own transcription is quoted when it sends one, and anything else is left to the agent's tools.

## Troubleshooting

- **`--dry-run` fails with `x509: certificate signed by unknown authority`.** Set `ca_file` to the MinCifry root bundle; it is added to the system store, not a replacement for it.
- **The bot answers in a direct chat but not in a group.** Check that `default_access` or the chat's override lets the person in.
- **Messages are missed across a restart.** The cursor is kept in `<sessions>/gateway_max_cursor`; make sure the state directory is on disk the next run sees.
- **Debug output for this bot alone**: `logger.levels` with `component: gateway.max` and `level: debug`, or `--log-level "info,gateway.max=debug"`. Every ignored update is logged at debug with a reason.
