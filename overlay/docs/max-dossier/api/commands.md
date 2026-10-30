# `PATCH /me/commands` - the bot's command hints

**Source:** official reference, <https://dev.max.ru/docs-api/methods/PATCH/me/commands>
**Taken:** 2026-10-18
**Kind:** official documentation

## What it does

> Метод добавляет, изменяет или удаляет команды бота, отображаемые пользователю в качестве подсказок в
> диалогах и групповых чатах:
> - При вводе «/» - на платформах iOS, Android, десктоп
> - При нажатии кнопки с символом « / » в правом нижнем углу диалога с ботом - на платформе iOS

> Отображение команд **в каналах не поддерживается**. Функциональность доступна на платформах:
> - **Для диалогов** с ботом - iOS, Android, десктоп
> - **Для групповых чатов** с ботом - iOS, Android

> Чтобы удалить все команды, передайте пустой массив `commands`

## Body

> `commands` - `BotCommand[]` optional, до `32` элементов. Список команд и их описаний, отображаемых
> пользователю в подсказках при вводе « `/` »

## Result

> `commands` - `BotCommand[]` optional, до `32` элементов. Команды, которые поддерживает бот

**Our finding:** the answer carries the full list, so `PublishCommands` can be checked against what the
platform actually kept - worth a log line, because a rejected or trimmed list is otherwise invisible from
our side. The 32-element ceiling is well above the ten commands the gateway publishes, and the ceiling on
a single description is described on the `BotCommand` object page (still to be filed).

## Codes

> `200` - информация о командах бота; `401` - ошибка авторизации; `500` - внутренняя ошибка сервера

## Example from the reference

```sh
curl -X PATCH "https://platform-api2.max.ru/me/commands" \
  -H "Authorization: {access_token}" \
  -H "Content-Type: application/json" \
  -d '{"commands": [{"name": "string", "description": "string"}]}'
```

## Where it lands

`external/gateway/max/commands_menu.go` - the menu published once at start (logged as
`max: commands published count=10`), and `external/gateway/max/commands.go` - the `/help` text that has
to agree with it. A command in the hints that `/help` does not describe is a defect on our side, and so is
the reverse.
