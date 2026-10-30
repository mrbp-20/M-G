# Getting a `chat_id`

**Source:** official reference, <https://dev.max.ru/docs-api/use-cases/getting-chat-id>
**Taken:** 2026-10-18
**Kind:** official documentation

## The problem the page solves

> `chat_id` - идентификатор чата или канала, в который добавлен бот. Он нужен большинству методов API,
> которые работают в контексте конкретного чата

> Сейчас API MAX не предоставляет готовой возможности для получения списка групповых чатов и каналов, в
> которые добавлен бот

> В документации встречаются два написания: chat_id (query-параметры) и chatId (path-параметры). Это
> один и тот же идентификатор

**Our finding:** the retired `GET /chats` (removed in June 2026) has no replacement call. There is nothing
to ask "which chats is my bot in" - the platform tells you only as events happen, and keeping the list is
the developer's job. For the gateway this means a `chat_id` is known only after the chat has spoken at
least once, which is exactly how our session store behaves today.

## How to get one

| Object | How |
|---|---|
| Чат или канал | Only through a subscription: `POST /subscriptions` or `GET /updates`; the `chat_id` arrives in the `Update` object for the chosen events - for example `bot_added` or `bot_started` |
| Мини-приложение | Either through the subscription, or on the client through `window.WebApp.initData` of the MAX Bridge library |

## The three duties the page names

> - разверните на своей стороне хранилище данных
> - при получении каждого события с `chat_id` - сохраните его, если он ещё не сохранён
> - обработайте возможные дубли (одно и то же событие может прийти повторно)
> - удалите `chat_id` при возникновении соответствующих событий, например `bot_removed`

**Our finding:** "handle possible duplicates" is stated as a duty, not a footnote - and it is why the
gateway de-duplicates on `message_id` and rewrites the cursor rather than trusting a single delivery. The
"remove on `bot_removed`" duty is the one the gateway does **not** do yet: a chat the bot was removed from
stays in the session store.

> Использование Long Polling для получения списка чатов и каналов для бота не предусмотрено

## Where it lands

`external/gateway/max/dispatch.go` - the chat id comes from the update and becomes the session key
(`max:chat:<id>` or `max:user:<id>` through the session store); `resume.go` - the list of conversations is
built from that store, because the platform cannot be asked for it.
