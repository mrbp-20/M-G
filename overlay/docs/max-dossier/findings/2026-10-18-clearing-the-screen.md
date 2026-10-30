# Clearing the dialog screen - what a bot can and cannot do

**Date:** 2026-10-18
**Kind:** our own finding, from the reference and our probes
**Sources:** <https://dev.max.ru/docs-api/methods/DELETE/messages>, <https://dev.max.ru/docs-api/objects/Update>,
<https://dev.max.ru/docs-api/methods/GET/messages>, the help pages for the person's own
"Очистить историю" (<https://help.max.ru/help/messages/kak-ochistit-istoriyu-chata>)

## The short answer

There is **no** "clear the chat" or "clear the screen" call for a bot. The API offers three different
things that look adjacent, and none of them is a bulk wipe:

| What exists | What it actually does |
|---|---|
| `DELETE /messages?chat_id=&message_id=` | Removes **one** message. In a dialog the bot may remove **only its own**; in a group or a channel - any |
| `dialog_cleared` (an `Update` event) | Tells the bot that **the person** cleared the history. It is their action, not ours |
| `DELETE /chats/{chatId}` | Makes the bot **leave** a group chat or a channel. The history stays |

**Our finding:** a "clean screen" button in the gateway can only be built as a loop over
`DELETE /messages`, and the loop is bounded three ways: only the bot's own messages in a dialog,
at most **two deletions per second per chat**, and an answer of `200` that does not prove success -
the reference says the body may carry an error, so `success` has to be read.

## What the person sees from their side

The MAX client has "Очистить историю" for a person, and the help pages are explicit:

> история переписки удалится только у вас

> В групповом чате пункт «Очистить историю» удалит переписку у всех участников - но только если у вас есть
> права администратора

**Our finding:** the only true "clear the screen" in MAX is the person doing it themselves. For a dialog
that clears their copy only; for a group with admin rights it clears it for everybody. A bot cannot do
either of those in one step.

## What a working implementation would cost

To delete the bot's own messages in one dialog:

1. read the tail of the chat with `GET /messages?chat_id=...&count=N` (works in our own dialog - we have
   used exactly this call all day to inspect messages);
2. keep only what the bot sent (`sender.is_bot`);
3. delete one by one with `DELETE /messages?chat_id=...&message_id=...`, at most two per second.

**The arithmetic:** 20 messages take ~10 seconds, 60 take ~30 seconds, 200 take under two minutes. A chat
that has been talking all day cannot be wiped faster than the rate limit allows, and every deleted message
is one more write against the same per-chat budget the answer uses.

**A cheaper alternative we already have:** the "🔍 Подробности" message is editable, and a message that
carries an `inline_keyboard` is editable regardless of age. A screen can therefore be *emptied* by editing
the button message into "cleared" and deleting only the few text messages, rather than by walking the whole
history. Whether that is worth it is a design question, not an API one.

## What the gateway does today

`/clear` in the chat resets the **session** - a new conversation with an empty context - and posts
"Начата новая беседа.". It deletes nothing from the screen, and the dossier keeps that distinction
visible on purpose: the two needs ("forget the context" and "wipe the screen") are different, and only the
first one is implemented.

## Before writing any of this

The operator's standing rule applies: before anything that says "стереть / удалить / очистить", name the
object and the scope and wait for an explicit yes. For a wipe feature that means answering three questions
first:

1. **Whose messages?** Only the bot's (the only option in a dialog) or everything (groups and channels, and
   only with admin rights)?
2. **How far back?** The current session, the last N, or everything the API will still hand over?
3. **What if it is slow?** 200 messages take two minutes and block the chat's write budget meanwhile.
