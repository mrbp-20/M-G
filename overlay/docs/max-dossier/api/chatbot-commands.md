# Commands for a chatbot - the `/` hints

**Source:** official reference, <https://dev.max.ru/docs-api/use-cases/chatbot-commands>
**Taken:** 2026-10-18
**Kind:** official documentation

## What a person sees

> Команды отображаются пользователю в виде подсказок в диалоге или групповом чате, когда он вводит « `/` »
> - доступно на платформах iOS, Android, десктоп, или нажимает соответствующую кнопку в интерфейсе -
> только на платформе iOS

> Отображение команд в каналах не поддерживается

| Chat kind | Platforms where the hints appear |
|---|---|
| Диалог с ботом | iOS, Android, десктоп |
| Групповой чат с ботом | iOS, Android |
| Канал с ботом | недоступно |

## The shape of a command

> `name` - от 1 до 64 символов
> `description` - string, опционально, от 1 до 128 символов
> максимум 32 элемента в массиве `commands`

**Our finding:** 128 characters for a description is the number that shapes the `/help` text and the menu:
a Russian sentence of two lines fits, three does not. The gateway publishes ten commands, well under the
ceiling of 32, so there is room for `/goal`, `/plan` and others without rethinking the list.

## Two ways a command can be invoked

> После добавления команд вы можете настроить логику сценариев работы чат-бота так, чтобы он выполнял
> необходимые вам действия в ответ на эти команды или другие слова, отправленные в чат

> Также вы можете добавить команды в кнопки с типом `callback`, которые чат-бот будет присылать сообщением

**Our finding:** a hint is only a hint - MAX does not call anything when a person picks one; it types the
command into the message box. The gateway therefore cannot tell "picked from the menu" from "typed by
hand", which is why a command's handling has to tolerate a trailing space and a missing argument.

## Example from the reference

```sh
curl -X PATCH "https://platform-api2.max.ru/me/commands" \
  -H "Authorization: {access_token}" \
  -H "Content-Type: application/json" \
  -d '{"commands": [{"name": "name", "description": "string"}]}'
```

## Where it lands

`external/gateway/max/commands_menu.go` - the published list; `external/gateway/max/commands.go` - the
parsing and the `/help` text that must agree with it.
