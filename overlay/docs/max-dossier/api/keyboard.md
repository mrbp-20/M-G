# Inline keyboard - buttons under a message

**Source:** official reference, <https://dev.max.ru/docs-api/use-cases/sending-messages/keyboard>
**Taken:** 2026-10-18
**Kind:** official documentation

## The limits, and the layout rules

> Inline-клавиатура позволяет разместить под сообщением бота до `210` кнопок, сгруппированных в `30`
> рядов - до `7` кнопок в каждом (до `3`, если это кнопки типа `link`, `open_app`,
> `request_geo_location` или `request_contact`)

> - Текст на кнопке выравнивается по центру и обрезается, если выходит за её границы
> - Кнопки в одной строке всегда одинаковой ширины
> - Ширина каждого ряда кнопок равна ширине клавиатуры
> - Высота у всех кнопок по умолчанию одинаковая

> Если переслать сообщение с кнопками из чат-бота в любой другой чат, кнопки не будут пересланы

**Our finding:** buttons do not travel with a forward - a person who forwards a menu forwards a message
with no menu. Two consequences for the gateway: a permission prompt must be answered in the chat where it
was asked, and the "🔍 Подробности" button has to be re-created rather than forwarded if we ever forward.

## Button types

| Type | What it does |
|---|---|
| `callback` | MAX sends a `message_callback` event; the bot answers through `POST /answers`. Used for choosing a model, approving a permission |
| `link` | Opens a link in a new tab; **the URL is limited to 2048 characters** |
| `request_contact` | Asks the person to share their contact and phone number (see the hash below) |
| `request_geo_location` | Asks for the location |
| `open_app` | Opens a mini app inside the bot |
| `message` | Sends a predefined text to the bot - a quick reply or a command |
| `clipboard` | Copies the text in `payload` to the clipboard |

**Our finding:** `message` is the button kind that would let a menu work without any callback plumbing - it
types a command into the chat, which the gateway already parses. Worth remembering the next time a menu
has to show something that cannot afford a round trip.

## `request_contact` and the hash that proves the number

> Сообщение с контактом содержит поле `hash` - оно позволяет проверить, что пользователь поделился
> номером телефона, совпадающим с его номером в МАКС

> - Значение поля `hash`, полученное в сообщении в массиве `attachments.payload`
> - Значение функции `HMAC-SHA256(access_token, vcf_info)`
>   - `access_token` - токен чат-бота
>   - `vcf_info` - информация о контакте в формате: `"BEGIN:VCARD\r\nVERSION:3.0\r\nPRODID:ez-vcard 0.10.3\r\nTEL;TYPE=cell:79990000000\r\nFN:Ivan Ivanov\r\nEND:VCARD\r\n"`

> Обратите внимание: перед хешированием необходимо преобразовать символы `\r\n` поля `vcf_info` в реальные
> переносы строк

**Our finding:** this is the only documented way to verify a phone number the bot receives, and the
comparison uses the **bot token as the HMAC key**. A verification routine therefore handles the token in a
place where an error message could print it - worth a careful implementation if that feature is ever taken.

## How to attach a keyboard

> В теле запроса передайте объект `attachments` с типом `inline_keyboard` и массивом кнопок
> `payload.buttons`. Для каждой кнопки обязательно укажите её текст в параметре `text`

```json
{"text": "Это сообщение с кнопкой-ссылкой", "attachments": [{"type": "inline_keyboard", "payload": {"buttons": [[{"type": "link", "text": "Откройте сайт", "url": "https://example.com"}]]}}]}
```

## Where it lands

`external/gateway/max/commands_menu.go` (the model menu, one row per model, `callback` buttons),
`tooldetails.go` (a button under every tool call), `sender.go` (permission and question keyboards, each
row built from the options the agent asked for).
