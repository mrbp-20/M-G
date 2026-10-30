# `GET /messages` - reading messages back

**Source:** official reference, <https://dev.max.ru/docs-api/methods/GET/messages>
**Taken:** 2026-10-18
**Kind:** official documentation

## What it does

> Возвращает информацию о сообщении или массив сообщений из чата или посты из каналов. Для выполнения
> запроса нужно указать один из параметров - `chat_id` или `message_ids`

> `chat_id` - метод возвращает массив сообщений или постов из указанного чата или канала, только если
> чат-бот в них является администратором. Сообщения возвращаются в обратном порядке: последние сообщения
> будут первыми в массиве

> `message_ids` - метод возвращает информацию о запрошенных сообщениях или постах. Можно указать один
> идентификатор или несколько

## Parameters

| Parameter | Type | Default | Meaning |
|---|---|---|---|
| `chat_id` | integer `<int64>` optional | - | Required unless `message_ids` is given; the bot must be an administrator of that chat |
| `message_ids` | array `<string>` optional | - | A comma-separated list; required unless `chat_id` is given |
| `from` | integer `<int64>` optional | - | Up to this time, from the first published message; Unix timestamp in **milliseconds** |
| `to` | integer `<int64>` optional | - | From this time up to the last published message; Unix timestamp in **milliseconds** |
| `count` | integer `[1-100]` optional | `50` | Maximum number of messages in the answer |

**Our finding:** `from` and `to` are the reverse of what the names suggest at a glance - `from` is the
**upper** bound and `to` is the lower one. A paging loop that swaps them silently returns an empty page.

## Codes

> `200` - информация о сообщении или массиве сообщений; `401` - ошибка авторизации;
> `403` - пользователь приостановил бота или у него нет доступа к чату; `500` - внутренняя ошибка

**Our finding:** `403` is the one to expect when a person blocks the bot - not `404`, and not an empty
list. A gateway that treats it as "no messages" would keep polling a dead chat.

## Why it matters for the dossier

Two uses are already known: reading a conversation back when the gateway needs the tail of it (the same
data the web UI has), and confirming what MAX actually stored after a send - the rendered text of a
`format: markdown` message, the attachments it really attached, the preview card it added on its own.

## Example from the reference

```sh
curl -X GET "https://platform-api2.max.ru/messages?chat_id={chat_id}" -H "Authorization: {access_token}"
curl -X GET "https://platform-api2.max.ru/messages?message_ids={message_id1},{message_id2}" -H "Authorization: {access_token}"
```
