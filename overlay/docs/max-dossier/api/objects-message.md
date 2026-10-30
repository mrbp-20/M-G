# `Message` - the message object

**Source:** official reference, <https://dev.max.ru/docs-api/objects/Message>
**Taken:** 2026-10-18
**Kind:** official documentation

## What it is

> Содержит общую информацию о сообщении в чате или посте в канале: данные об отправителе и получателе,
> время создания сообщения, содержимое (текст и вложения), контекст связи с другими сообщениями (ответ или
> пересылка), а также публичную ссылку и статистику для постов в каналах

> Возвращается в ответ на вызовы методов группы `/messages` и `/chats/{chatId}/pin`

## Fields

| Field | Type | Notes from the reference |
|---|---|---|
| `sender` | `User` optional | The sender: a person or a bot. **Absent when the message went to a channel** |
| `recipient` | `Recipient` | The receiver: a person or a bot in a dialog, a chat or a channel |
| `timestamp` | integer | Creation time, Unix timestamp in **milliseconds** |
| `link` | `LinkedMessage` Nullable optional | A forwarded or a replied-to message |
| `body` | object | The content: text plus attachments. May be `null` when the message carries only a forward |
| `stat` | `MessageStat` Nullable optional | Views and reposts - **channels only** |
| `url` | string Nullable optional | A public link to a channel post. Absent for dialogs and group chats |

## Our finding: `body.mid` is the handle everything hangs on

Edits, deletes and button answers all need the message id, and it lives in `body.mid` of the message. A
callback update carries the pressed message the same way - under `message.body.mid`, not as a top-level
field of the callback. Reading the wrong field once cost a live bug: the model menu's checkmark never
moved because the edit went to an empty id and the API answered 400 while the model itself had already
changed.

## Our finding: what a read-back shows

Reading a message back is how we confirmed three things MAX does not put in our request:

1. the text of a `format: markdown` message comes back already rendered, without the markers;
2. a `share` attachment appears in `body.attachments` when MAX decided to preview a word;
3. an image travels as its own message, so a text and a picture sent together are two messages here.

## Example from the reference

```json
{"sender": {...}, "recipient": {...}, "timestamp": 0, "link": {...}, "body": {...}, "stat": {...}, "url": "string"}
```
