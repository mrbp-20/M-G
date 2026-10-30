# Community notes - what the articles say, and how far we trust them

**Taken:** 2026-10-18
**Kind:** community sources, read as search snippets and summaries. **None of these has been read in full
yet** - that is stated per line on purpose, because a snippet is not a source.

The rule for this file: an entry here is a lead, not a fact. Anything that matters gets read in full and
filed as its own page in `api/`, with quotes.

## Read as snippets, worth a full read

| Source | URL | What it claims | Status |
|---|---|---|---|
| vc.ru: «Как убрать превью ссылок в МАХ» | <https://vc.ru/social/2949553-kak-ubrat-prevyu-silok-v-max> | The preview of a link can be removed by a bot; the article describes a bot that does it | **Snippet only.** It points at the same annoyance we solved with `disable_link_preview`; the article's route is probably for a person, not for a bot author |
| vc.ru: MAX Bot API against Telegram Bot API, method by method | <https://vc.ru/telegram/2799410-sravnenie-max-bot-api-i-telegram-bot-api> | `chat_id` goes in the query for MAX and in the body for Telegram; the text goes in a `NewMessageBody` object | **Snippet only.** The shape matches the reference; read in full before quoting any count of methods |
| Хабр: «Пишем своего бота для обратной связи. Часть 1: MVP» | <https://habr.com/ru/articles/1016164/> | A worked example with `format: 'markdown'` and keeping the `mid` of a sent message | **Snippet only.** The `mid` habit matches what we do for the live message and the details button |
| `K1rL3s/maxo`, PR #132 `feat: hide_link` | <https://github.com/K1rL3s/maxo/pull/132> | MAX has no invisible-anchor HTML trick; the native preview mechanism is `ShareAttachment`, and `hide_link(url)` builds one on purpose | **Snippet only.** The claim about the absence of an HTML trick matches our own probes exactly |
| messangermax.ru, API chapter | <https://messangermax.ru/o-messendzhere/api/> | The format is set in `format` of `NewMessageBody`; Markdown and HTML are supported | **Snippet only.** Agrees with the reference |
| webappdev.ru, MAX Bot API guide | <https://www.webappdev.ru/max/bot-api/> | Covers webhooks, long polling, payments, architecture | **Snippet only.** The most promising article for an architecture view; read before designing a webhook migration |
| wiki.twin24.ai: sending a MAX notification | <https://wiki.twin24.ai/ru/chats/use-cases/max-notification> | A recipe: 4000-character limit, HTML and Markdown supported | **Snippet only.** The 4000-character limit matches the reference's own wording |
| modx.pro: sending a message to a MAX bot | <https://modx.pro/development/25531> | A bare `curl` example with no SDK | **Snippet only.** Little new |
| `BushlanovDev/max-bot-api-client-php` docs | <https://github.com/BushlanovDev/max-bot-api-client-php/blob/master/docs/README.md> | Documents `disableLinkPreview` as an optional field of the message body | **Snippet only, and it is wrong about the place** - the API wants it in the query string; see [../findings/2026-10-18-link-previews.md](../findings/2026-10-18-link-previews.md) |

## Code worth reading when something behaves oddly

| Source | URL | Why |
|---|---|---|
| `max-messenger/max-bot-api-client-ts` | <https://github.com/max-messenger/max-bot-api-client-ts> | The official TS client; its issue list is where behaviour the reference omits shows up |
| `max-messenger/max-bot-api-client-go` | <https://github.com/max-messenger/max-bot-api-client-go> | The official Go client; the folder we audited for our own transport decisions |
| `etsft/max-bot-api-java` | <https://github.com/etsft/max-bot-api-java> | Java 21 client; its README discusses the webhook delivery deadline, a corner we have not read into |
| `maxbot` (Rust) | <https://lib.rs/crates/maxbot> | Notes the 2 messages/second per chat limit, matching the reference |

## How to promote a line above

1. Read the source in full.
2. If it adds a fact about the platform, write it into the matching `api/` page with a verbatim quote and
   delete the line here.
3. If it only confirms what we already know, leave it here with "confirms", so the next person does not
   re-read it to find that out.
