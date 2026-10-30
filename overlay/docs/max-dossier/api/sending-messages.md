# `POST /messages` - sending a message

**Source:** official reference, <https://dev.max.ru/docs-api/methods/POST/messages>
**Taken:** 2026-10-18
**Kind:** official documentation

## Parameters (the query string)

> `user_id` - integer `<int64>` optional. Если вы хотите отправить сообщение пользователю, укажите ID
> этого пользователя

> `chat_id` - integer `<int64>` optional

> `disable_link_preview` - boolean optional. Если `true`, сервер не будет генерировать превью для
> ссылок в тексте сообщения или поста

**Our finding (2026-10-18):** this is where the switch against link preview cards lives, and it has to be
a **query parameter**. The same name in the request body is dropped without an error, which is how a day
was spent building a workaround for a switch that existed. A live probe: with the parameter the card is
gone, without it the card is there. See
[../findings/2026-10-18-link-previews.md](../findings/2026-10-18-link-previews.md).

## Body

> `attachments` - `AttachmentRequest[]` Nullable. Вложения сообщения. Если поле пустое или равно `null`,
> изменений не произойдет. Если массив пуст, все вложения будут удалены

> `link` - object `NewMessageLink` Nullable. Ссылка на сообщение в чате или пост в канале. Ссылки на
> комментарии к постам в каналах не поддержаны

> `notify` - по умолчанию `true`. Если `false`, участники чата не получат push-уведомления. Для каналов
> необходимо отправлять запрос с `notify = true` или без этого поля

> `format` - enum `TextFormat` Nullable optional. Возможные значения в enum: `"markdown"` `"html"`.
> Разметка текста сообщения

## Attachment types the message may carry

> `image` - изображение (JPG, JPEG, PNG, GIF, TIFF, BMP, HEIC)
> `video` - видео (MP4, MOV, MKV, WEBM, MATROSKA)
> `audio` - аудио (MP3, M4A и другие)
> `file` - файл для загрузки в (TXT, DOC и другие)
> `sticker` - стикер
> `contact` - контакт
> `inline_keyboard` - сообщение или пост с кнопкой
> `share` - контент, прикрепленный по внешнему URL
> `location` - локация

**Our finding:** `share` is the card itself. MAX attaches it on its own when the text holds a word of the
shape `name.ext`, and it fetches that address from MAX's own infrastructure - the card we saw held the
title and description of the far page, not our bytes.

## Limits

> Можно отправлять не более двух сообщений в секунду в один диалог, групповой чат или канал. При
> превышении этого лимита сообщения следует ставить в очередь или делать задержку перед отправкой

## Authorization

> Передача токена через query-параметры больше не поддерживается - используйте заголовок
> `Authorization: <access_token>`

**Our finding:** the header carries the raw token, without a `Bearer` prefix.

## Host and certificate

> Для корректной работы ваших чат-ботов и мини-приложений направляйте запросы на домен
> `platform-api2.max.ru` вместо `platform-api.max.ru`. Также убедитесь, что добавили сертификат
> Минцифры в список доверенных

## Response codes

> `200` - Информация о созданном сообщении или опубликованном посте
> `401` - Ошибка авторизации. Токен `access_token` указан некорректно или недействителен
> `500` - Внутренняя ошибка сервера

## Example from the reference

```sh
curl -X POST "https://platform-api2.max.ru/messages?user_id={user_id}" \
  -H "Authorization: {access_token}" \
  -H "Content-Type: application/json" \
  -d '{"text": "Это сообщение с кнопкой-ссылкой", "attachments": [{"type": "inline_keyboard", "payload": {"buttons": [[{"type": "link", "text": "Откройте сайт", "url": "https://example.com"}]]}}]}'
```
