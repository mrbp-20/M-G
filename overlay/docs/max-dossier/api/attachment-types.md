# Attachment types and their limits

**Source:** official reference, <https://dev.max.ru/docs-api/use-cases/sending-messages/attachment-types>
**Taken:** 2026-10-18
**Kind:** official documentation

## What the reference lists

> Помимо текста сообщения могут содержать вложения, которые передаются в объекте `attachments` запроса
> `POST /messages`

| Type | What it is and its limits |
|---|---|
| `image` | Изображения. JPG, JPEG, PNG, GIF, TIFF, BMP, HEIC. **До 50 МБ или не более 7680x7680 px** - both criteria must hold |
| `video` | MP4, MOV, MKV, WEBM. До 250 МБ |
| `audio` | MP3, M4A и другие. **До 256 МБ или не длиннее 60 мин** - both criteria must hold |
| `file` | Другие медиафайлы до 4 ГБ: TXT, DOC, PDF и другие распространённые форматы |
| `sticker` | Стикеры |
| `inline_keyboard` | Кнопки клавиатуры. **Максимальное количество: `210` кнопок, сгруппированных в `30` рядов - до `7` кнопок в каждом (до 3, если это кнопки типа `link`, `open_app`, `request_geo_location` или `request_contact`)** |
| `location` | Геолокация |
| `share` | Медиафайлы с превью |
| `contact` | Контактные данные |

## Our findings

1. **The keyboard ceiling is two numbers, not one.** 210 buttons in total, but 30 rows and 7 buttons
   per row - so a menu of 210 buttons in one row is refused. The model menu
   (`external/gateway/max/commands_menu.go`) fits a row per model and is far from any of the three.
2. **Link buttons shrink the row to 3.** A row of `link` (or `open_app`, `request_geo_location`,
   `request_contact`) buttons holds at most three, unlike the seven of callback buttons. Mixing them in
   one row is the easiest way to hit an undocumented-looking refusal.
3. **`share` is in the same list as `image`.** The preview card is an attachment like any other, which is
   why it can be seen in `body.attachments` of a read-back message and why `disable_link_preview` is the
   only way to stop MAX from making one.
4. **Two independent limits on an image:** size and resolution. A 40 MB picture at 9000x9000 px is
   refused for the second reason, not the first.

## Where it lands

`external/gateway/max/pictures.go` (images out and in), `commands_menu.go` (the keyboard),
`tooldetails.go` (a button under every tool call), `sender.go` (permission and question keyboards).

## Dead ends recorded while filing this

These reference pages answer **404** - the objects are documented elsewhere (inside the method pages and
the use-case pages), so do not spend time guessing their URLs again:

- `https://dev.max.ru/docs-api/objects/Attachment`
- `https://dev.max.ru/docs-api/objects/ShareAttachment`
- `https://dev.max.ru/docs-api/objects/InlineKeyboardAttachmentRequest`
- `https://dev.max.ru/docs-api/objects/CallbackUpdate`

What does exist and is filed: [`objects-message.md`](objects-message.md), [`objects-update.md`](objects-update.md).
