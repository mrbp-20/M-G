# `POST /answers` - answering a button press

**Source:** official reference, <https://dev.max.ru/docs-api/methods/POST/answers>
**Taken:** 2026-10-18
**Kind:** official documentation

## What it does

> Отправляет ответ после того, как пользователь нажал на кнопку. Ответом может быть обновленное
> сообщение и/или одноразовое уведомление для пользователя

> Можно отправлять не более двух ответов в секунду в один диалог, групповой чат или канал

## Parameters

> `callback_id` - string, от `1` символа. Идентификатор кнопки, на которую нажал пользователь

> Идентификатор можно получить в обновлениях о событиях через Webhook или Long Polling. Когда
> пользователь нажмёт на кнопку, MAX отправит событие, содержащее объект `Update` с типом
> `message_callback` и идентификатором кнопки в поле `updates[i].callback.callback_id`

> `disable_link_preview` - boolean optional. Если `true`, сервер не будет генерировать превью для
> ссылок в тексте сообщения или поста

## Body

> `message` - object `NewMessageBody` Nullable optional. Заполните это, если хотите изменить текущее
> сообщение

## Result

> `true`, если запрос был успешным, `false` - в противном случае

## Our finding: this endpoint carries the same preview switch

`disable_link_preview` is documented here too, and it is the same query parameter as on `POST /messages`.
An answer that rewrites a message - a "🔍 Подробности" page, a model menu after a tap - can therefore add
a preview card just as a send can. Worth a check in the gateway: if `AnswerCallback` ever carries text
with a file name in it, the parameter belongs there as well, or the card comes back through this door.

## Codes

> `200` - успешный или неуспешный результат; `401` - ошибка авторизации; `405` - метод не разрешён;
> `500` - внутренняя ошибка сервера

## Where it lands

`external/gateway/max/sender.go` - the press is acknowledged so MAX stops showing the spinner, and
`dispatch.go` - a `message_callback` becomes a `HandleCallback`. The message id of the pressed button
lives in the update's `message.body.mid`, not in a field of the callback itself - a mistake that cost a
live bug once and is worth keeping in mind when reading the `CallbackUpdate` object.

## Example from the reference

```sh
curl -X POST "https://platform-api2.max.ru/answers?callback_id=callback_id" \
  -H "Authorization: {access_token}" \
  -H "Content-Type: application/json" \
  -d '{"message": {"text": "Это сообщение с кнопкой-ссылкой", "attachments": [{"type": "inline_keyboard", "payload": {"buttons": [[{"type": "link", "text": "Откройте сайт", "url": "https://example.com"}]]}}]}}'
```
