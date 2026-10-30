# Sending media

**Source:** official reference, <https://dev.max.ru/docs-api/use-cases/sending-messages/media>
**Taken:** 2026-10-18
**Kind:** official documentation

## The two steps, always

> Перед отправкой медиафайлов во вложении к сообщению - типы `image`, `video`, `audio`, `file` -
> предварительно их нужно загрузить с помощью метода `POST /uploads`. В результате загрузки вы получите
> `token`, который понадобится при отправке в сообщении через `POST /messages`. При этом одному токену
> должен соответствовать один медиафайл. Токен можно сохранить и переиспользовать

> Для изображений в качестве альтернативы токену вы можете использовать `url` - прямую ссылку на
> изображение в интернете. Для аудио, видео и других файлов отправка по `url` недоступна

**Our finding:** images are the only type that may be sent by a link. The gateway uploads everything
(`pictures.go`), which is the more reliable branch - the reference itself warns that a URL the bot does not
control can change or vanish.

## The upload answer and the send

> `{"photos": {"photoIds": {"token": "mediafile_token"}}}`

> `curl --location 'https://platform-api2.max.ru/messages?chat_id={chat_id}' --header 'Authorization:
> {access_token}' --data '{"attachments": [{"text": "Изображение", "type": "image", "payload": {"token":
> "{mediafile_token}"}}]}'`

**Our finding:** the token sits one level deeper than the obvious place - under `photos.photoIds.token`.
A decoder that looks for a top-level `token` finds nothing and posts an image with an empty token, which
the API refuses with a message about the attachment rather than about the token.

## How many in one message

> В одном запросе можно отправить максимум 12 изображений. Можно отправить одним из двух способов - с
> помощью токена и по URL-ссылке - или используя оба в одном запросе

## Formats and limits

| Type | Formats | Limit |
|---|---|---|
| `image` | JPG, JPEG, PNG, GIF, TIFF, BMP, HEIC | up to 50 MB **or** no more than 7680x7680 px - both must hold |
| `video` | MP4, MOV, MKV, WEBM | up to 250 MB |
| `audio` | MP3, M4A and others | up to 256 MB **or** no longer than 60 min - both must hold |
| `file` | TXT, DOC, PDF and other common formats | up to 4 GB |

> Обратите внимание, параметр `type=photo` больше не поддерживается - используйте `type=image`

## The processing delay

> После успешной загрузки сервер обрабатывает файл. Файлы от нескольких мегабайт обрабатываются дольше.
> Если отправить сообщение с вложением сразу после загрузки, может возникнуть ошибка

**Our finding:** this is the documented cause of "the picture sometimes does not arrive". The cure the
reference prescribes is a pause before sending and a retry with a growing interval, not a fixed one - see
[uploads.md](uploads.md) for the error code (`attachment.not.ready`).

## Where it lands

`external/gateway/max/pictures.go`: `UploadImage` (POST /uploads, the bytes, the token) and `downloadImage`
(the other direction - an image the agent reads). The message that carries a picture is built in
`sender.go`, and a picture always travels as **its own message** with the file name as the caption.
