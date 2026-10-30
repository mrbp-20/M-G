# `PUT /messages` - editing a message the bot sent

**Source:** official reference, <https://dev.max.ru/docs-api/methods/PUT/messages>
**Taken:** 2026-10-18
**Kind:** official documentation

## What it does

> Редактирует сообщения и посты, отправленные ботом

## Parameters

> `message_id` - от `1` символа. ID редактируемого сообщения

**Our finding (2026-10-18):** `disable_link_preview` is **not** listed here, but the API honours it anyway.
A probe: a message sent with the parameter, then edited without it, got the preview card back; the same
edit with `disable_link_preview=true` in the query stayed clean. So the gateway sends the parameter on
every edit. See [../findings/2026-10-18-link-previews.md](../findings/2026-10-18-link-previews.md).

## Restrictions

> Есть особенности редактирования сообщений в диалогах с ботом, связанные со сроком давности:
> - сообщения с кнопками `inline_keyboard` редактируются независимо от срока давности
> - остальные сообщения редактируются, если они отправлены менее 7 суток назад

> В групповых чатах и каналах любые сообщения редактируются независимо от срока давности

> Можно редактировать не более двух сообщений в секунду в одном диалоге, групповом чате или канале

**Our finding:** the 7-day rule is why the "🔍 Подробности" message - it always carries an
`inline_keyboard` - can be rewritten long after the turn ended, while a plain answer cannot. The gateway
therefore keeps its own copy of a call's output in memory instead of relying on the message.

## Body

The same `NewMessageBody` as a send: `attachments`, `link`, `notify`, `format`
(see [new-message-body.md](new-message-body.md)). One difference that matters:

> `attachments`: Если поле пустое или равно `null`, изменений не произойдет. Если массив пуст, все
> вложения будут удалены

So "leave the attachments alone" is *a missing key*, and "take them away" is *an empty list*. The Go
client models this with `nil` versus an empty non-nil slice.

## Response

> `true`, если запрос был успешным, `false` - в противном случае

## Example from the reference

```sh
curl -X PUT "https://platform-api2.max.ru/messages?message_id=message_id" \
  -H "Authorization: {access_token}" \
  -H "Content-Type: application/json" \
  -d '{"text": "**Новый текст**", "attachments": [{"type": "inline_keyboard", "payload": {"buttons": [[{"type": "link", "text": "Откройте сайт", "url": "https://example.com"}]]}}], "link": {"type": "reply", "mid": "mid.message_id"}, "notify": false, "format": "markdown"}'
```
