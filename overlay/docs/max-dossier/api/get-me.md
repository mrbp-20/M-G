# `GET /me` - the bot's own profile

**Source:** official reference, <https://dev.max.ru/docs-api/methods/GET/me>
**Taken:** 2026-10-18
**Kind:** official documentation

## What it does

> Метод возвращает информацию о боте, который идентифицируется с помощью токена доступа `access_token`. В
> ответе приходит объект `User` с вариантом наследования `BotInfo`, который содержит идентификатор бота,
> его название, никнейм, время последней активности, описание и аватар (при наличии)

## Fields

| Field | Type | Notes |
|---|---|---|
| `user_id` | integer `<int64>` | Идентификатор пользователя или бота |
| `name` | string | Отображаемое имя |
| `last_name` | string Nullable optional | Для ботов это поле не возвращается |
| `username` | string | Никнейм бота; у пользователя может быть `null` |
| `last_activity_time` | integer `<int64>` | Unix timestamp в миллисекундах; **помечено устаревшим, скоро будет удалено** |
| `description` | string Nullable optional | до `16000` символов |
| `avatar_url` | string optional | Уменьшенный аватар |
| `full_avatar_url` | string optional | Аватар в полном размере |
| `commands` | `BotCommand[]` Nullable optional | до `32` элементов - команды, поддерживаемые ботом |

## Codes

> `200` - информация о боте; `401` - ошибка авторизации; `500` - внутренняя ошибка сервера

## Our findings

1. **`GET /me` is the cheapest way to prove a token works.** The gateway calls it at startup and logs the
   answer as `max bot connected name=... user_id=...` - that line is the first thing to look for when the
   bot seems silent.
2. **`commands` comes back here too**, so the published command list can be checked in the same call that
   proves the token. Two `PATCH /me/commands` problems - a trimmed list and a refused one - would show up
   as a difference between what we sent and what `/me` reports.
3. **`last_activity_time` is marked deprecated**, so nothing should be built on it: a "bot is alive"
   indicator has to come from a poll or a send, not from this field.
4. `name` and `username` are the fields the gateway logs; they belong to the bot the token points at, so a
   swapped token shows up as a different name in that same line.

## Example from the reference

```sh
curl -X GET "https://platform-api2.max.ru/me" -H "Authorization: {access_token}"
```

```json
{"user_id": 1, "first_name": "My Bot", "name": "My Bot", "username": "my_bot", "is_bot": true, "last_activity_time": 1737500130100}
```

## Where it lands

`external/gateway/max/bot.go` - `Start` calls `Me` before the poll loop starts; `internal/gateway/max`'s
live checks use the same call.
