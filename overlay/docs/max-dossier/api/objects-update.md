# `Update` - the event object and its types

**Source:** official reference, <https://dev.max.ru/docs-api/objects/Update>
**Taken:** 2026-10-18
**Kind:** official documentation

## Where it comes from

> Может возвращаться в следующих случаях:
> - Вы подписались на обновления через Webhook - при наступлении события MAX пришлёт POST-запрос
>   `/subscriptions`, который содержит объект `Update`
> - Вы отправили GET-запрос `/updates` для получения обновлений через Long Polling

## The event types, as listed

> - `bot_added` - бот добавлен в чат или канал
> - `bot_started` - пользователь впервые начал общение с ботом или возобновил после остановки
> - `bot_stopped` - пользователь остановил или удалил бота; при удалении вместе с ним приходит `dialog_removed`
> - `bot_removed` - бот удалён из чата или канала
> - `chat_title_changed` - пользователь изменил название чата или канала
> - `dialog_cleared` - пользователь очистил историю диалога с ботом
> - `dialog_muted` / `dialog_unmuted` - уведомления в диалоге выключены / включены
> - `dialog_removed` - пользователь удалил диалог; вместе с ним приходит `bot_stopped`
> - `message_callback` - пользователь нажал на кнопку в чате или канале
> - `message_created` - пользователь отправил новое сообщение или опубликовал пост
> - `message_edited` - пользователь отредактировал сообщение или пост
> - `message_removed` - пользователь удалил сообщение или пост
> - `comment_created` / `comment_edited` / `comment_removed` - комментарии; **на свои действия бот событий не получает**
> - `user_added` / `user_removed` - участники чата или канала
> - `bot_admin_permissions_changed` - изменены права бота-администратора. **Возвращается пока только через
>   Webhook - для Long Polling недоступно**

## Properties

> Unix timestamp в миллисекундах, когда произошло событие
> Пользователь, добавивший бота в чат
> Указывает, что бот добавлен в канал, а не в чат

## Example from the reference

```json
{"update_type": "bot_added", "timestamp": 0, "chat_id": 0, "user": {...}, "is_channel": true}
```

## Our findings

1. **The gateway listens to two types only** - `message_created` and `message_callback`. Everything else
   is delivered and dropped by `dispatch.go`. That is a deliberate simplification, and it means events
   such as `dialog_cleared` or `bot_stopped` are invisible to us: a person who stops the bot just stops
   getting answers, with nothing in the log to explain it.
2. **`bot_admin_permissions_changed` is webhook-only.** A permission change that matters for a group chat
   never reaches a long-polling bot. Worth knowing before promising anybody a permission-driven feature.
3. **"A bot gets no events for its own actions"** (stated for comments, and it matches what we see for
   messages) is why the gateway also filters by `sender.is_bot`: the filter is defence in depth, not the
   only guard.

## Dead end recorded while filing this

`https://dev.max.ru/docs-api/objects/CallbackUpdate` answers **404** - no such object page. The callback
shape has to be read from the `message_callback` section of this page and from the `POST /answers`
reference, not from an object page of its own.
