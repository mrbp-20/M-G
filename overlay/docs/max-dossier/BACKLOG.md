# What should be filed next

Updated 2026-10-18, third filing pass.

## Filed already (do not read these again for the same question)

| Source | Filed as |
|---|---|
| `POST /messages` | [api/sending-messages.md](api/sending-messages.md) |
| `PUT /messages` | [api/editing-messages.md](api/editing-messages.md) |
| `NewMessageBody`, `TextFormat` | [api/new-message-body.md](api/new-message-body.md) |
| Text formatting | [api/text-formatting.md](api/text-formatting.md) |
| `GET /updates` | [api/updates.md](api/updates.md) |
| `PATCH /me/commands` | [api/commands.md](api/commands.md) |
| `POST /uploads` | [api/uploads.md](api/uploads.md) |
| `GET /messages` | [api/reading-messages.md](api/reading-messages.md) |
| `DELETE /messages` | [api/deleting-messages.md](api/deleting-messages.md) |
| `POST /answers` | [api/answering-callbacks.md](api/answering-callbacks.md) |
| Attachment types and limits | [api/attachment-types.md](api/attachment-types.md) |
| Inline keyboard, button kinds | [api/keyboard.md](api/keyboard.md) |
| Sending media, the upload race | [api/media.md](api/media.md) |
| `Message` object | [api/objects-message.md](api/objects-message.md) |
| `Update` object and event types | [api/objects-update.md](api/objects-update.md) |
| `POST /subscriptions` (webhooks) | [api/subscriptions-webhooks.md](api/subscriptions-webhooks.md) |
| Getting a `chat_id` | [api/getting-chat-id.md](api/getting-chat-id.md) |
| Chatbot commands | [api/chatbot-commands.md](api/chatbot-commands.md) |
| OpenAPI schema and generators | [api/openapi-schema.md](api/openapi-schema.md) |
| `GET /me` | [api/get-me.md](api/get-me.md) |

## Dead ends: do not look for these again

- `https://dev.max.ru/docs-api/objects/Attachment` - 404
- `https://dev.max.ru/docs-api/objects/ShareAttachment` - 404
- `https://dev.max.ru/docs-api/objects/InlineKeyboardAttachmentRequest` - 404
- `https://dev.max.ru/docs-api/objects/CallbackUpdate` - 404
- `https://dev.max.ru/docs-api/objects/BotCommand` - answers with the use-cases index, not the object

The objects live inside the method and use-case pages; for field-level shape read
`schema.yaml` from [api/openapi-schema.md](api/openapi-schema.md).

## Still to file

| Page | URL | Why |
|---|---|---|
| Stickers and contacts | <https://dev.max.ru/docs-api/use-cases/sending-messages/another-attachments> | The attachment types the gateway never sends |
| Bots in chats and channels | <https://dev.max.ru/docs-api/use-cases/bot-in-chats-and-channels> | Member versus administrator rights - the reason a group stays silent |
| Recommendations for the API | <https://dev.max.ru/docs-api/use-cases/event-notifications> | The page both long polling and webhooks point at for rate advice |
| Official Go library | <https://github.com/max-messenger/max-bot-api-client-go> | What it does better or worse than our hand-written client |
| MAX Bridge | <https://dev.max.ru/docs/webapps/bridge> | Mini apps; `window.WebApp.initData` is the other way to learn a chat id |
| Bot creation and moderation | <https://dev.max.ru/docs/chatbots/bots-create/create> | Who may create a bot, and how long moderation takes |
| vc.ru: how to remove a link preview in MAX | <https://vc.ru/social/2949553-kak-ubrat-prevyu-silok-v-max> | The community route; read in full before quoting |
| vc.ru: MAX Bot API against Telegram Bot API | <https://vc.ru/telegram/2799410-sravnenie-max-bot-api-i-telegram-bot-api> | The migration map |
| Хабр: a MAX bot in 15 minutes | <https://habr.com/ru/articles/1016164/> | A worked end-to-end example |
| `K1rL3s/maxo`, PR #132 | <https://github.com/K1rL3s/maxo/pull/132> | Building a `ShareAttachment` on purpose |

## A suggestion for the next pass

**Done 2026-10-18:** `schema.yaml` is in this folder as [api/schema.yaml](api/schema.yaml).
It answers "what fields does X have" without the internet. The four object pages that 404 on the
reference site can now be read from it. Original note:
of the site. That closes four 404s at once and makes the dossier independent of the documentation's URL
scheme.
