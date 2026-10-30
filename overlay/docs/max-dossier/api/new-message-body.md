# `NewMessageBody` and the `TextFormat` enum

**Source:** official reference, <https://dev.max.ru/docs-api/objects/NewMessageBody>
**Taken:** 2026-10-18
**Kind:** official documentation, plus our own probe (marked)

## Where the object is used

> Объект используется при отправке нового сообщения в чат или канал `POST /messages`, редактировании
> существующего сообщения или поста `PUT /messages`, а также callback-отправке сообщения пользователю
> при нажатии им кнопки в чате или канале `POST /answers`

> Параметры объекта содержат: текст и способ его форматирования, вложения, ссылку на связанное
> сообщение (ответ или пересылка), настройку PUSH-уведомлений для участников чата

## Fields

| Field | Type | Notes from the reference |
|---|---|---|
| `text` | string | The message text; the schema caps it at **4000** characters (see below) |
| `attachments` | `AttachmentRequest[]` Nullable | Empty or `null` leaves them alone; an empty array removes them all |
| `link` | `NewMessageLink` Nullable | A reply or a forward; links to channel comments are not supported |
| `notify` | boolean, default `true` | `false` means no push notifications; channels require `true` or the field absent |
| `format` | enum `TextFormat` Nullable optional | `"markdown"` or `"html"` |

> `format` - enum `TextFormat` Nullable optional. Возможные значения в enum: `"markdown"` `"html"`.
> Разметка текста сообщения

## Our finding: three formats, not four (live probe, 2026-10-18)

The same markup was sent five ways to a live chat, then read back through the API:

| What was sent | HTTP | What MAX stored |
|---|---|---|
| no `format` | 200 | `'формат-проба 1: **жирный** и \`код\`'` - markers stayed text |
| `format: markdown` | 200 | `'формат-проба 2: жирный и код'` - markers consumed |
| `format: html` | 200 | `'формат-проба 3: жирный и код'` - tags consumed |
| `format: text` | **400** `Can't deserialize body` | - |
| `format: HTML` | 200 | worked as `html`: the value is case-insensitive |

So there are exactly three states: the field absent (plain text), `markdown`, `html`. Any other value is
refused with `400 proto.payload` and a message about deserialization.

**A consequence worth remembering:** for `markdown` and `html`, MAX stores the *rendered* text, without
the markers. The original markup cannot be recovered from the API afterwards.

## Our finding: what the gateway sends

| Surface | Format |
|---|---|
| the answer, the live turn message, `/help`, the `/resume` list | `markdown` |
| the tool-call message with "🔍 Подробности", permission and question prompts, notes, refusals, picture captions | plain text (no `format`) |

`html` is supported by the platform and unused by the gateway: it would allow tables and `<pre>`, and we
deliberately did not move there - see [../findings/2026-10-18-link-previews.md](../findings/2026-10-18-link-previews.md)
for why formatting turned out not to change anything about previews.

## Our finding: the text limit is 4000 (schema, 2026-10-19)

The method page (`https://dev.max.ru/docs-api/methods/POST/messages`, fetched 2026-10-19) states the
fields and the two-messages-per-second rule but **does not print a length limit in prose**. The
number is in the official OpenAPI schema we keep beside this file:
`api/schema.yaml:2193` (`NewMessageBody.text`) and `:2228` (`EditMessageBody.text`) both carry
`maxLength: 4000`.

Consequences worth remembering:

- a caption longer than 4000 characters is refused by the API, so a sender must cut it before the
  call and not learn about it from a `400`;
- the schema counts **characters**, and no separate byte limit is stated — Cyrillic costs the same as
  Latin here;
- this is read from the schema, **not probed live**: nobody has yet sent a 4001-character message to
  see the answer. When that probe happens, this line gets the result.
