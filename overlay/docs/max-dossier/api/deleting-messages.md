# `DELETE /messages` - removing a message

**Source:** official reference, <https://dev.max.ru/docs-api/methods/DELETE/messages>
**Taken:** 2026-10-18
**Kind:** official documentation, plus our own probe (marked)

## What it does

> Удаляет сообщения в диалоге, групповом чате или канале

## Restrictions

> - Бот должен быть администратором и иметь право на удаление сообщений
> - Бот может удалять сообщения:
>   - в диалоге - только сообщения, отправленные самим ботом
>   - в групповом чате - любые сообщения
>   - в канале - любые сообщения
> - Можно удалять не более двух сообщений в секунду в одном диалоге, групповом чате или канале

**Our finding:** "in a dialog only the bot's own messages" is the rule that makes a probe message
removable at once, and it is also the reason the gateway can never take back something the person wrote.

## Parameters

> `message_id` - от `1` символа. ID удаляемого сообщения

## Result

> `true`, если запрос был успешным, `false` - в противном случае

> `200` - объект удалён или запрос завершился ошибкой (причина ошибки описана в `message`);
> `401` - ошибка авторизации; `403` - нет прав на доступ к этому ресурсу; `500` - внутренняя ошибка

## Our finding: the reference example does not work as written

The reference shows:

```sh
curl -X DELETE "https://platform-api2.max.ru/messages?message_id={message_id}" -H "Authorization: {access_token}"
```

Sent that way on 2026-10-17, the API answered **404**. What worked was the message id **with the chat
id**:

```sh
curl -X DELETE "https://platform-api2.max.ru/messages?chat_id={chat_id}&message_id={message_id}"
```

The answer was `200 {"success":true}`. Seven probe messages were removed this way, one after another.

**A second subtlety:** `200` is returned even when the deletion failed - the reference says so plainly
("объект удалён **или** запрос завершился ошибкой"), so a caller must read `success` in the body, not the
status code, before claiming a message is gone.

## Is there a bulk "clear the screen"?

No. The API has no wipe call - only this one-message deletion, the `dialog_cleared` event (which reports
that **the person** cleared the history) and `DELETE /chats/{chatId}` (which makes the bot leave a group).
A screen-clearing feature has to be a loop over this method, bounded by "own messages only" in a dialog and
by two deletions per second. The arithmetic and the design questions are written out in
[../findings/2026-10-18-clearing-the-screen.md](../findings/2026-10-18-clearing-the-screen.md).

## Where it lands

No production code yet: the gateway deletes nothing. It is filed because every live probe we send to a
chat has to be taken back afterwards, and the working form is not the one in the reference.
