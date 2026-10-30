# Reactions never reach a bot

**Date:** 2026-10-18
**Asked by:** the operator, who put a 👍 on a gateway message and asked whether the assistant can see it.
**Answer:** no. The reaction stays entirely in the client - MAX neither delivers it as an event nor exposes it
on the message a bot can read.

## What was checked

**1. The official schema** (`api/schema.yaml` in this folder, 3814 lines): the word `reaction` appears
**zero** times. The update union lists exactly these types:

```
message_created     message_callback     message_edited     message_removed
bot_added           bot_removed          bot_started        dialog_cleared
chat_title_changed
```

There is no update type for a reaction, and none for a read receipt either.

**2. A live read of the message that was reacted to** - `GET /messages?chat_id=<id>&count=3` on the bot's own
chat. Every message of the bot comes back with these keys only:

```
message: [body, recipient, sender, timestamp]
body:    [attachments, mid, seq, text]
```

No `reactions`, no counters, no reader list. The message object a bot can see simply has no place to keep them.

**3. What our gateway listens to:** `message_created` and `message_callback`, nothing else
(`external/gateway/max/dispatch.go`). So even if the platform started sending a reaction event, it would fall
through until a handler exists.

## What follows

- A 👍 from the operator is invisible to the assistant. The only way it becomes known is a screenshot or a
  sentence, which is exactly how it became known on 2026-10-18 at 12:44.
- Nothing on our side can change this: it is not a polling interval, a missing field in a request, or a switch.
  The data does not exist for the bot.
- **If MAX ever adds it** (a `message_reaction` update or a `reactions` field), the work is small: a new case in
  `dispatch.go` plus a log line, and optionally a count on the details store. Until then, do not build UX on
  reactions - a button press (`message_callback`) is the only feedback a bot really sees.

## Related

- The keyboard is the reliable feedback channel: every press arrives as `message_callback` with its payload -
  see [../api/answering-callbacks.md](../api/answering-callbacks.md).
- What a bot can learn about a chat at all: [../api/reading-messages.md](../api/reading-messages.md).

## Correction to the title: invisible to a *bot*, not absent from MAX

The operator pushed back on 2026-10-18: "MAX cannot have missed this - it is opinion statistics, and a
platform built on a state order would not ignore such a counter". He was right, and the honest finding is
narrower than the title: reactions **exist in MAX**, and so do polls. What does not exist is any way for a
**bot** to see them.

### What exists in the platform

`PyMax` (<https://github.com/MaxApiTeam/PyMax>, `maxapi-python`, MIT) is a library for MAX's **internal**
API - not the bot API. It authenticates as a person (phone + SMS over TCP, or the web client by QR over
WebSocket) and its own README says what it can do:

> **PyMax** - асинхронная Python-библиотека для внутреннего API Max. Она умеет авторизоваться в аккаунте,
> слушать события, отправлять сообщения, работать с чатами, пользователями, файлами, сессиями и доменными
> типами Max через TCP или WebSocket.

> Сообщения: отправка и планирование, ответы, **реакции**, pin, read, delete и история.

Its `docs/messages.rst` (taken 2026-10-18) shows the reaction and poll surface in concrete code:

```python
elif message.text == "/like":
    await message.react("👍")
elif message.text == "/reactions":
    reactions = await message.get_reactions()
```

> Методы `add_reaction()`, `get_reactions()` и `remove_reaction()` принимают ID сообщений только как `int`.

```python
@client.on_reaction_update()
async def reactions(event: ReactionUpdateEvent, client: Client) -> None:
    print(event.message_id, event.total_count)
```

So reactions are a first-class message feature **with a running total** (`total_count`, `reaction_info` on a
message), exactly the "statistics of opinions" the operator described.

**Polls exist too**, with the settings one would expect:

```python
await client.send_message(chat_id=<chat id>, attachments=[
    Poll(title="Какой вариант выбрать?", answers=[PollAnswer(text="Первый"), PollAnswer(text="Второй")],
         settings=(PollFlags.FLAG_SETTINGS_ANONYMOUS | PollFlags.FLAG_SETTINGS_REVOTE)),
])
```

> Для голосования нужны ID сообщения, опроса и вариантов из входящего `PollAttachment` ... `state.total`

Anonymous polls, revoting, and a `total` of votes - so the capability the operator expected is not missing
from the product, only from the bot interface.

### What the Bot API has instead: nothing

- The official schema (3814 lines, in this folder as `api/schema.yaml`): `reaction` - **0 occurrences**;
  `poll` appears only inside the words "long polling". The `AttachmentRequest` union carries `image`, `video`,
  `audio`, `file` (plus keyboard/share/contact/location) - no poll.
- The update union: `message_created`, `message_callback`, `message_edited`, `message_removed`, `bot_added`,
  `bot_removed`, `bot_started`, `dialog_cleared`, `chat_title_changed` - no reaction, no vote.
- The 13 paths of the API contain no method for reactions or polls.
- A live `GET /messages` on the bot's own chat carries no reaction field.

### The route exists, and it is the one the operator rejected

Reading or counting reactions is possible **only** through the internal user API, which means acting as a
person rather than as a bot. The library itself is honest about the price:

> PyMax использует неофициальный внутренний API Max. API может измениться без предупреждения, а
> использование библиотеки может нарушать условия сервиса. Вы используете PyMax на свой риск; авторы и
> контрибьюторы не несут ответственности за блокировки аккаунтов, потерю данных или другие последствия.

The operator's own verdict on that road, on 2026-10-16, was: «явный костыль и не наш метод». So the honest
answer stands, but for the right reason: **the counter exists, MAX did not miss it, and a bot may not touch
it.** The gap is in the Bot API only.

### What this means in practice

1. Do not build anything on reactions until the Bot API publishes them: a bot cannot read, send or count them.
2. The reliable feedback channel remains the keyboard press (`message_callback`).
3. If opinion statistics are wanted for real, the lever is a feature request to MAX (their `dev.max.ru`
   feedback and the `max-messenger` organisation on GitHub), not a workaround on our side. Filed nowhere yet:
   it needs the operator's word, since it is a public action.
