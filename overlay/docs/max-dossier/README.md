# The MAX dossier

Everything we have learned about MAX, the Russian messenger, in one place, so that work on the gateway
starts from our own notes instead of a search engine.

## Rules

1. **A file names its source at the top** - the URL, what kind of source it is (official reference,
   forum, article, code) and the date the content was taken. Without a source a file does not go in.
2. **Quotes are verbatim.** Anything the source says is set as a quote and kept in the source's wording.
   Our own conclusions are marked **Our finding** and dated.
3. **A source is filed the moment it is read**, even when it was read for another task, and even when it
   turned out to be useless - "we looked here and found nothing" saves the next search.
4. **This folder is read first, the internet second.** A search is allowed only when the folder has no
   answer, and the answer goes back into the folder the same day.
5. **No secrets.** No bot tokens, no private chat or user ids, no keys. Public ids that appear in the
   official reference are fine.

## Why it exists

On 17.10 the gateway grew a guard against MAX's link previews because a probe "showed" the platform had
no switch for them. On 18.10 the reference showed the switch: `disable_link_preview`, a **query**
parameter that the earlier probe had sent in the request **body**, where MAX silently drops it. The
knowledge was in the official documentation the whole time - it just was not ours. See
[findings/2026-10-18-link-previews.md](findings/2026-10-18-link-previews.md).

## Sections

| Path | What is inside |
|---|---|
| [api/sending-messages.md](api/sending-messages.md) | `POST /messages`: parameters, body, limits, attachment types |
| [api/editing-messages.md](api/editing-messages.md) | `PUT /messages`: parameters, the 7-day rule, what an edit resets |
| [api/new-message-body.md](api/new-message-body.md) | The `NewMessageBody` object and the `TextFormat` enum, with a live probe of all three formats |
| [api/text-formatting.md](api/text-formatting.md) | Markdown and HTML: what MAX draws, what it flattens, what it linkifies |
| [api/updates.md](api/updates.md) | `GET /updates`: the marker, the limits, and the reference's own "webhook only in production" |
| [api/commands.md](api/commands.md) | `PATCH /me/commands`: the 32-command ceiling and where the hints show |
| [api/uploads.md](api/uploads.md) | `POST /uploads`: types, size and resolution limits, the `attachment.not.ready` race |
| [api/reading-messages.md](api/reading-messages.md) | `GET /messages`: `chat_id` or `message_ids`, the reversed `from`/`to` |
| [api/deleting-messages.md](api/deleting-messages.md) | `DELETE /messages`: who may delete what, and the form that actually works |
| [api/answering-callbacks.md](api/answering-callbacks.md) | `POST /answers`: the press, and the same preview switch |
| [api/global-settings.md](api/global-settings.md) | The exhaustive check for a global switch: no settings resource among the 13 paths, `disable_link_preview` in three methods only, and the schema's own `false`/`true` contradiction |
| [api/attachment-types.md](api/attachment-types.md) | Every attachment type with its limits; the keyboard's 210/30/7 and 3-for-links |
| [api/objects-message.md](api/objects-message.md) | The `Message` object: `body.mid`, what a read-back shows |
| [api/objects-update.md](api/objects-update.md) | The `Update` object and all event types, with the ones we do not listen to |
| [community/sources.md](community/sources.md) | Forums, articles, SDKs and code worth reading, each with a URL and what to look for |
| [findings/](findings/) | Our own measurements and conclusions, dated |
| [DEFECTS.md](DEFECTS.md) | The register of MAX defects: bugs, documentation errors and security problems, each with its proof, kept for the complaint the operator will form later |
| [findings/2026-10-18-reactions-are-invisible.md](findings/2026-10-18-reactions-are-invisible.md) | Reactions and read receipts never reach a bot: not in the update union, not on the message object |
| [BACKLOG.md](BACKLOG.md) | Pages that should be filed next, with URLs |
| [TEMPLATE.md](TEMPLATE.md) | The shape a new entry takes |
| [index.ru.md](index.ru.md) | Русская точка входа: правила и карта папки |

## How this connects to the code

| Note | Where it lands in the gateway |
|---|---|
| `disable_link_preview` | `external/gateway/max/client.go`, `Send` / `EditMessage` / `EditMarkdown` |
| `text` limit of 4000 | `external/gateway/max/sender.go`, the splitting of a long answer |
| Two writes per second per chat | `external/gateway/max/sender.go`, the `writeGap` |
| `format: markdown` | `external/gateway/max/sender.go`, the answer and the live message |
| `inline_keyboard` payload limit | `external/gateway/max/commands_menu.go`, the model menu |
| `DELETE /messages?chat_id=&message_id=` | the way we take back a probe message from a live chat |
