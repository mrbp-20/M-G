# `GET /updates` - long polling for events

**Source:** official reference, <https://dev.max.ru/docs-api/methods/GET/updates>
**Taken:** 2026-10-18
**Kind:** official documentation

## What the reference says first

> Получение обновлений с помощью Long Polling ограничено по скорости и сроку хранения событий - этот
> способ не подходит для production-окружения. Рекомендуем на всех этапах работы использовать Webhook

> Метод `GET /updates` можно использовать для получения обновлений при разработке и тестировании, если
> ваш бот не подписан на Webhook. **Для production-окружения используйте только Webhook**

**Our finding:** the gateway runs on long polling (`external/gateway/max/poll.go`), so this is not a
footnote for us - it names two limits we live with (rate and how long an event is kept) and a migration
path we have not taken (`POST /subscriptions`). It also explains the shape of our cursor: see below.

## The marker

> Каждое обновление со списком событий имеет свой порядковый номер. Свойство `marker` в ответе
> указывает на следующее ожидаемое обновление. После того, как вы передали `marker` с указателем на
> конкретное обновление, все предыдущие считаются прочитанными

> Если параметр `marker` **не передан** или передано значение `null`, вы получите только последнее
> обновление

**Our finding:** "all previous are considered read" is why the gateway saves the marker only after a
whole page has been handled (`pollLoop` -> `saveCursor`), and why a restart replays the page instead of
losing it. Passing no marker at all - the "only the latest" branch - is exactly what a fresh install must
avoid, otherwise a burst that arrived while the bot was down is lost.

## Parameters

| Parameter | Type | Default | Meaning |
|---|---|---|---|
| `limit` | integer `[1-1000]` | `100` | Maximum number of updates in the answer |
| `timeout` | integer `[0-90]` | `30` | Long-poll timeout in seconds |
| `marker` | integer `<int64>` Nullable | - | `null` or absent gives only the latest update; the previous answer's marker gives everything since |
| `types` | `string[]` Nullable | - | Event types to receive, e.g. `types=message_created,message_callback`; the list is in the `Update` object |

## Result

> Список обновлений о событиях в чатах и каналах, в которые добавлен бот. Обратите внимание, чтобы
> получать события из групповых чатов и каналов, бот должен быть администратором

> `marker` - integer `<int64>` Nullable. Указатель на следующую страницу данных

**Our finding:** the "bot must be an administrator" line is the reason a group chat stays silent until the
bot is added as one - a support question worth answering from this file rather than by guessing.

## Codes

> `200` - список обновлений с указателем; `401` - ошибка авторизации; `405` - метод не разрешён;
> `500` - внутренняя ошибка сервера

## Example from the reference

```sh
curl -X GET "https://platform-api2.max.ru/updates" -H "Authorization: {access_token}"
```

## Open questions

1. How long is an event kept? The reference says the storage is limited but does not name the window.
2. What is the actual rate limit of long polling, as opposed to the 30 rps of the API as a whole?
3. What would a webhook migration cost the gateway - a public IP, a TLS certificate, a secret check - and
   what does `POST /subscriptions` require exactly? That page is in `../BACKLOG.md`.
