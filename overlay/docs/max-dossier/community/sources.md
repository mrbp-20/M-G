# Community, articles and code worth reading

**Taken:** 2026-10-18
**Kind:** links gathered with a web search; each is described by what it actually offered in the search
snippet, and none has been read in full yet unless a note says so.

The rule for this file: a link stays even when it turned out useless - "we looked here and found
nothing" saves the next search.

## Official, beyond the API reference

| Source | URL | Why it is here |
|---|---|---|
| MAX for developers | <https://dev.max.ru/docs/chatbots/bots-coding/prepare> | Scenarios, message management, deep links, the "prepare" chapter of the bot guide |
| Bot coding, JavaScript | <https://dev.max.ru/docs/chatbots/bots-coding/js> | Mentions the two formatting types (Markdown and HTML) and shows the official JS client in use |
| Bot coding, JS library | <https://dev.max.ru/docs/chatbots/bots-coding/library/js> | The official TypeScript/JavaScript library page and its formatting example |
| Deep links | <https://dev.max.ru/help/deeplinks> | Opening a bot or a mini app with parameters |
| MAX Bridge | <https://dev.max.ru/docs/webapps/bridge> | Mini apps: `text` and `link` parameters of the share call |
| Help for bots | <https://help.max.ru/help/bots> | User-facing bot documentation, useful for what a person sees |

## Code and SDKs

| Source | URL | Why it is here |
|---|---|---|
| `BushlanovDev/max-bot-api-client-php` | <https://github.com/BushlanovDev/max-bot-api-client-php> | PHP client; its `docs/README.md` documented `disableLinkPreview` before we found it in the reference - a second witness of the field name |
| `max-messenger-bot/max-bot-api-php` | <https://github.com/max-messenger-bot/max-bot-api-php> | Another PHP client with `NewMessageBody` modelled closely on the schema |
| `max-messenger/max-bot-api-client-ts` | <https://github.com/max-messenger/max-bot-api-client-ts> | The official TS client; issues are worth watching for behaviour the reference omits |
| `K1rL3s/maxo`, PR #132 `feat: hide_link` | <https://github.com/K1rL3s/maxo/pull/132> | States plainly that MAX has no invisible-anchor HTML trick and that the native preview mechanism is `ShareAttachment`; `hide_link(url)` builds one on purpose |
| `maxbot` (Rust) | <https://lib.rs/crates/maxbot> | Rust HTTP client; mentions a 2 messages/second per chat limit, the same number the reference gives |

## Articles

| Source | URL | Why it is here |
|---|---|---|
| Хабр: «Пишем своего бота для обратной связи. Часть 1: MVP» | <https://habr.com/ru/articles/1016164/> | A worked example: `POST /messages` with `format: 'markdown'`, keeping a `mid` of a sent message |
| vc.ru: MAX Bot API против Telegram Bot API, метод за методом | <https://vc.ru/telegram/2799410-sravnenie-max-bot-api-i-telegram-bot-api> | Migration-shaped comparison: `chat_id` in the query for MAX against the body for Telegram; a map of the differences |
| vc.ru: «Как убрать превью ссылок в МАХ» | <https://vc.ru/social/2949553-kak-ubrat-prevyu-silok-v-max> | Community answer to the same annoyance we hit: the preview is removable, but through a bot - adjacent to our `disable_link_preview` finding, worth reading in full next time |
| messangermax.ru, API chapter | <https://messangermax.ru/o-messendzhere/api/> | Restates that the format goes in `format` of `NewMessageBody`, with Markdown and HTML |
| webappdev.ru, MAX Bot API guide | <https://www.webappdev.ru/max/bot-api/> | Webhooks, long polling, payments, architecture notes |
| wiki.twin24.ai: sending a MAX notification | <https://wiki.twin24.ai/ru/chats/use-cases/max-notification> | A practical recipe: 4000-character limit, HTML and Markdown supported |
| modx.pro: sending a message to a MAX bot | <https://modx.pro/development/25531> | A bare `curl` example, no SDK |
| Telegram answers on the same topic | <https://ru.stackoverflow.com/questions/1465796/> | Not about MAX, but shows the `disable_web_page_preview` analogue in Telegram - useful when reading migration notes |

## Reading order suggested by what we already know

1. The reference pages filed in `api/` - they are the ground truth.
2. The two SDK sources when the reference is silent about a field name (the PHP client named
   `disableLinkPreview` while the reference was unclear to us about where the field lives).
3. The vc.ru migration comparison for the shape of the API as a whole.
4. Everything else when a specific question comes up.
