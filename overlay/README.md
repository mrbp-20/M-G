# Coddy MAX Gateway

A fork of the [Coddy agent](https://github.com/coddy-project/coddy-agent) that adds a bot for the Russian messenger [MAX](https://max.ru/) (МАКС). Every message to the bot starts or continues a Coddy session, and the agent's answer streams back into the same chat. The bot runs inside `coddy serve`, alongside the HTTP server, the web UI and the other messenger gateways.

The default branch `main` is the gateway. The upstream Coddy overview, install options and feature list are still valid for everything except the MAX gateway; see the original [README](https://github.com/coddy-project/coddy-agent/blob/main/README.md) and this repo's `docs/` directory.

## Table of contents

- [What it does](#what-it-does)
- [Get a bot token and your MAX user id](#get-a-bot-token-and-your-max-user-id)
- [Build from source](#build-from-source)
- [Configure the gateway](#configure-the-gateway)
- [Check and run](#check-and-run)
- [Where to host it](#where-to-host-it)
- [What is not supported yet](#what-is-not-supported-yet)
- [Troubleshooting](#troubleshooting)

## What it does

- The bot long-polls the MAX Bot API `GET /updates` endpoint, so the host running Coddy does **not** need a public IP or webhook.
- Each direct chat, group chat override, or shared group session becomes a normal Coddy session. If the HTTP server is enabled, the same conversation is visible live in the web UI.
- Admins (`gateways.max.admins`) have full access; everyone else can chat and do read-only research, but cannot run tools that change files, call MCP tools, or approve permission prompts.
- Messages are de-duplicated and the poll cursor is saved to disk, so a restart does not lose updates that arrived while the bot was down.
- A line and the picture sent right after it arrive as two MAX messages; the bot holds them for a moment and runs them as one turn, so the answer covers both.
- A tool call is a message of its own, named by what it ran when the agent reports it - the command of a run, the path of a read: `🔍 Подробности` opens the command and what it returned, page by page.
- The conversation is transparent both ways: a turn started in the web UI is published into the chat its session is bound to, with the person's own line marked `🌐`.

## Get a bot token and your MAX user id

1. **Create the bot in MAX and copy its API token.** The exact UI path in MAX is not documented in this repository; create a bot through the MAX interface for developers and copy the token it gives you. The adapter sends the token raw in the `Authorization` header of every request, exactly as the MAX Bot API expects.
2. **Find your MAX user id** and write it down; you will need it in the `admins` list. In MAX you can usually see your own id in your profile or account settings; check the current MAX documentation for the exact location.
3. **Download the MinCifry root certificate bundle** as a PEM file. MAX serves its API over a certificate signed by the Russian national root, which is not in the system trust store of most hosts. Without `ca_file` the bot fails to connect with `x509: certificate signed by unknown authority`. The bundle is added to the system store, not a replacement for it.

Keep the token secret: leave `gateways.max.token` empty and put it in `~/.coddy/.env` as `MAX_BOT_TOKEN=...` instead.

## Build from source

```bash
git clone https://github.com/mrbp-20/M-G      # the MAX gateway layer
git clone https://github.com/coddy-project/coddy-agent coddy
cd M-G
bash apply.sh --coddy ../coddy --check        # every replaced file must match
bash apply.sh --coddy ../coddy --build        # apply, then build
```

The layer carries the whole MAX adapter and the hooks it needs in the shared files; Coddy itself is
taken from Pavel's repository, never copied here. `apply.sh --check` refuses a Coddy whose files moved
on, which is the point: the layer fits one version, and the version is written in `VERSION`.

## Configure the gateway

Add a `gateways.max` block to `~/.coddy/config.yaml`:

```yaml
gateways:
  max:
    enable: true
    token: ""                    # empty: read MAX_BOT_TOKEN from env or ~/.coddy/.env
    proxy: ""                    # inherit (default), none, or an http(s)/socks5(h) URL
    ca_file: "/path/to/mincifry-root.pem"   # PEM bundle of the MinCifry root (required in practice)
    poll_interval_seconds: 1     # 1 to 60; pause between two polls
    poll_timeout_seconds: 30     # 1 to 60; how long one poll may be held open
    admins: [12345]              # your MAX user id(s)
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

Field reference from `internal/config/config.schema.json`:

| Field | Meaning |
|---|---|
| `enable` | Run the MAX bot in this `coddy serve` process. |
| `token` | Bot token. Empty reads `MAX_BOT_TOKEN` from the environment or `~/.coddy/.env`. |
| `proxy` | `inherit` (default), `none`, or a proxy URL (`http://`, `https://`, `socks5://`, `socks5h://`). |
| `ca_file` | PEM bundle of additional roots for the MAX API, typically the MinCifry root. |
| `poll_interval_seconds` | Pause between two polls. |
| `poll_timeout_seconds` | How long one poll may be held open. |
| `admins` | MAX user ids with elevated rights. |
| `default_access` | Who may talk to the bot: `all`, `admins`, or `group:<name>`. |
| `default_isolation` | Session isolation for group chats: `individual`, `shared`, or `admin`. |
| `user_groups` | Named sets of user ids referenced as `group:<name>`. |
| `chats` | Per-chat overrides keyed by MAX chat id. |

A direct chat (a `dialog` in MAX) always has its own session. Access and isolation work exactly like the Telegram gateway; see `docs/surfaces/gateway.md` for the full semantics.

You can also change these settings on the **Gateways** tab of the web UI. Changing the token or any other key there rebuilds the MAX bot in place without disturbing other surfaces.

## Check and run

1. Validate the config and probe the token:

```bash
./build/coddy serve --dry-run --test-config -config ~/.coddy/config.yaml
```

A working setup prints a line like:

```
ok       gateways.max: token accepted by MAX, bot <bot name> (<user_id>)
```

If `admins` is empty, `--dry-run` warns that nobody can administer the bot.

2. Start the server:

```bash
./build/coddy serve
```

The startup banner names the bot (`gateway     max`) and the log contains:

```
max bot connected ... name=<bot name> user_id=<id>
```

3. Open MAX, write to the bot, and it will answer.

## Where to host it

Any host with network access to the MAX Bot API works. A public address is **not** required: the bot pulls updates with a long-polling `GET /updates` request. Just make sure the state directory (where `<sessions>/gateway_max_cursor` and `gateway_max_sessions.json` live) persists across restarts, otherwise missed messages will not be replayed.

## What is not supported yet

Webhook intake, `/mcp`, reactions and forms, replies and quoting, group mention gating, detached
subagent permission prompts, woken turns from background tasks. A person editing a message does not
start a new turn. A message with no text and no attachment is answered with a note saying what the bot
reads.

See `docs/surfaces/max.md` for the complete behaviour reference.

## What is still being worked out

Group mode, access for several clients and creating groups are **still being worked out**. They work,
but how rights are handed out there has not settled: what is closed in one place today may be open in
another tomorrow - exactly what happened with the model menu, which stayed open in a personal chat
while `/resume` was already closed. So be careful with groups and with handing out rights: try it on a
test account, and use it at your own risk. No dates are promised: a separate pass with its own plan
comes after the first release.

## Incoming files pile up

Attachments are saved in the `Max-in` folder with no overall quota and no automatic cleanup. Watch the
free space on the disk, especially under `default_access: all` and when large documents or videos come
in. Do not give the bot to unvetted users without a separate assessment of that risk. A quota and
automatic cleanup rules are under development.

Where it lives: each conversation has its own `Max-in` folder inside its working directory - documents
in `Max-in/Docs`, pictures, video and recordings in `Max-in/Media`. The only per-file limit is the
platform's (MAX takes a document up to 4 GB). If the working directory is a git repository, the folder
shows up in `git status` and can end up in a commit: keep it in `.gitignore`.

## Troubleshooting

- **`--dry-run` fails with `x509: certificate signed by unknown authority`.** Set `ca_file` to the MinCifry root PEM bundle.
- **`--dry-run` says the token was rejected.** Check that the token is current; a revoked or mistyped token is answered with HTTP 401/403.
- **The bot answers in a direct chat but not in a group.** Check `default_access` or the per-chat override for that group.
- **Messages are missed across a restart.** Make sure Coddy's state directory is on persistent disk and is the same path on the next run.
- **Debug logging just for this bot:** add `logger.levels` with `component: gateway.max`, `level: debug`, or run with `--log-level "info,gateway.max=debug"`. Every ignored update is logged at debug with a reason.
