# `POST /uploads` - getting an upload URL for media

**Source:** official reference, <https://dev.max.ru/docs-api/methods/POST/uploads>
**Taken:** 2026-10-18
**Kind:** official documentation

## What it does

> Метод возвращает URL для загрузки медиафайла и последующей отправки его во вложении к сообщению в чате
> или канале. После загрузки файлу присваивается токен, который нужно передать в запросе `POST /messages`
> или `PUT /messages` в параметре `attachments.payload.token`

> По URL-ссылке, которая вернётся в ответ на запрос, можно загрузить только один файл. Если вы хотите
> загрузить ещё, отправьте повторно запрос `POST /uploads` и используйте новую URL-ссылку

## Types and their limits

> `image` - изображение (JPG, JPEG, PNG, GIF, TIFF, BMP, HEIC). **Максимальный размер** одного
> изображения: до 50 МБ или не более 7680 x 7680 px - должны выполняться оба критерия

> `video` - видеофайл (MP4, MOV, MKV, WEBM). **Максимальный размер**: до 250 МБ

> `audio` - аудиофайл (MP3, M4A и другие). **Максимальный размер**: до 256 МБ или длительностью не более
> 60 мин - должны выполняться оба критерия

> `file` - файл, поддерживаются распространённые форматы (например, TXT, DOC и другие).
> **Максимальный размер**: до 4 ГБ

> При передаче неподдерживаемого типа файла вернётся ошибка `File extension is forbidden`

> Значение `photo` больше не поддерживается. Если вы использовали `type=photo` в ранее созданных
> интеграциях - замените его на `type=image`

**Our finding:** the deprecation of `photo` is silent - a client that still sends `type=photo` gets an
error that reads like a bad extension, not like a renamed argument. Our client sends `type=image`
(`external/gateway/max/pictures.go`).

**Our finding (19.10.2026):** the token does not arrive with the address. `POST /uploads` answers
`{"url": …}` and nothing else; the token comes in the answer to the bytes posted at that url - at the
top level (`{"token": …}`) for `type=file`, under a photo id
(`{"photos": {"<id>": {"token": …}}}`) for `type=image`. Measured on the live API with a 33 KB PDF:
the address in 173 ms, the token in 137 ms, and the first `POST /messages` answered
`400 attachment.not.ready` while the second, three seconds later, landed. The page also names a
second upload shape, **resumable**, chosen when `Content-Type` is not `multipart/form-data`: the file
goes in parts and resumes from the last good one. Our client uses multipart - one request, no resume
(`external/gateway/max/client.go`, `uploadMedia`).

## The answer

> URL для загрузки медиафайла. Срок жизни ссылки не ограничен

> `200` - возвращает URL для загрузки вложения и токен для загрузки медиафайла

Two ways to upload, from the same page:

- **Resumable upload** - when `Content-Type` is not `multipart/form-data`; parts can be resumed:

  ```sh
  curl -X POST "%UPLOAD_URL%" -H "Authorization: {access_token}" -F "data=@example.mp4"
  ```

- **Multipart upload** - `Content-Type: multipart/form-data`, one request, no resume:

  ```sh
  curl -i -X POST -H "Content-Type: multipart/form-data" -F "data=@movie.pdf" "%UPLOAD_URL%"
  ```

**Our finding:** our client sends the bytes with `PUT` and a plain content type - which falls in the
resumable branch, and it works in production. Worth remembering that the reference's own example uses
`POST` for the upload URL; the platform accepts `PUT` for the resumable path (measured, not read).

## The race that matters: `attachment.not.ready`

> После успешной загрузки сервер обрабатывает файл. Файлы от нескольких мегабайт обрабатываются дольше

> Если отправить сообщение с вложением сразу после загрузки, может возникнуть ошибка:
> `{"code": "attachment.not.ready", "message": "Key: errors.process.attachment.file.not.processed"}`

> - После загрузки файла сделайте паузу перед отправкой сообщения
> - Если отправка не удалась, повторите попытку через некоторое время. Увеличивайте интервал с каждой попыткой
> - Загружайте часто используемые файлы заранее и переиспользуйте токен

**Our finding:** this is the most likely reason a picture "sometimes does not arrive" while the text does.
Retry with a growing interval, not a fixed one.

## Rate limit of the whole API

> Для стабильной работы сервисов MAX убедитесь, что максимальное количество запросов в секунду на
> platform-api2.max.ru - 30 rps

**Our finding:** 30 requests per second for the whole bot, on top of the documented "two messages per
second per chat". The gateway's `writeGap` holds to the per-chat rule; a burst of edits across many chats
would be governed by the 30 rps one.

## Where it lands

`external/gateway/max/pictures.go` (`UploadImage` -> token -> the message's `image` attachment) and the
same file's `downloadImage` for the other direction.
