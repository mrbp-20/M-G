# Register of MAX defects: bugs, errors, and what is wrong from a security view

**Started:** 2026-10-18, by the operator's instruction: "write down every bug, error and security problem
found, so that a claim can be formed once the gateway work is finished - who knows what else will surface."
**Rule of this file:** one entry per finding, with the evidence that proves it, so a complaint never rests on
memory. Entries are added the day they are found, the same rule as the rest of this dossier.
**How entries are written:** `D-NN`, area, what is wrong, how it was proven, severity, what we do meanwhile.
A finding is only an entry here once it was seen live, read in official sources, or both - a guess goes to
`BACKLOG.md` instead.

Severity is judged by what it costs a bot author: **high** - silent data loss, a security exposure, or a
behaviour that breaks a reasonable contract; **medium** - a documented contract that is not what the server
does; **low** - friction, unclear wording, missing deprecation path.

## Summary

| ID | Area | What is wrong | Severity |
|---|---|---|---|
| D-01 | Server | A preview card is attached to **any** message, a person's included; no global switch exists | high |
| D-02 | Client | Any `word.ext` is drawn as a hyperlink, with no API control at all | high |
| D-03 | Documentation | The schema contradicts itself on the polarity of `disable_link_preview` | medium |
| D-04 | Documentation | `PUT /messages` honours the parameter the reference does not list | medium |
| D-05 | API | Documented `DELETE /messages` form answers 404; `200` does not mean success | medium |
| D-06 | API | `format` accepts undocumented values and rejects a plausible one with a 400 | medium |
| D-07 | API | Rendered markdown/html replaces the original text irreversibly | high |
| D-08 | Server | Text alone makes MAX fetch a third-party URL (privacy leak, spam and phishing vector) | high |
| D-09 | API | A bot can forge a `share` card with any title, description and image | high |
| D-10 | API | `GET /chats` was removed with no replacement; a missed `bot_removed` leaves stale chats | medium |
| D-11 | API | `PATCH /me/commands` trims or refuses a list with no diagnostics | low |
| D-12 | Webhooks | Port 443 only, a trusted CA certificate required, silent auto-unsubscribe after 8 h | medium |
| D-13 | API | `POST /uploads` races with `attachment.not.ready` | low |
| D-14 | API | Deprecated fields are still returned without a removal plan | low |
| D-15 | Limits | A bot may delete only its own messages in a dialog, 2 per second, and `200` may say no | medium |
| D-16 | Limits | An edit window of 7 days, undocumented consequences of an expired edit | low |
| D-17 | Auth | The raw token goes in `Authorization` without `Bearer`, while SDK generators add it | low |
| D-18 | Bot API | Reactions and polls exist in the platform but no bot interface exposes them | medium |

## The entries

### D-01 A preview card is attached to any message, a person's included

**Found:** 18.10.2026 · **Severity:** high

**What is wrong.** MAX fetches an address it reads in a message and attaches a `share` attachment to that
message. It does this to a bot and to a person alike, so a chat partner cannot avoid it, and a bot author can
only avoid it per request.

**Proof.** The operator's own message carrying `https://3dmk.ru` came back from
`GET /messages?chat_id=<chat id>&count=15` with `type: share`, `url: https://3dmk.ru`, title
"3D Мастерская - Профессиональные услуги 3D печати в Краснодаре" and an `image_url` on `i.oneme.ru`. That
message never passed through this gateway. The full probe is in
[findings/2026-10-18-what-max-does-to-any-text.md](findings/2026-10-18-what-max-does-to-any-text.md).

**Consequence / risk.** Every participant of a chat gets cards pointing at third-party pages, and a bot author can do nothing for the person's side.

**No global switch.** The official schema is 13 paths with no settings resource; `disable_link_preview`
appears three times, always as a query parameter of one request. `GET /me` and `BotInfo` carry no display
field, and the only patch on `/me`-anything is `PATCH /me/commands`. See
[api/global-settings.md](api/global-settings.md).

**What we do meanwhile.** `disable_link_preview=true` on send and on every edit
([api/editing-messages.md](api/editing-messages.md)).

### D-02 Any `word.ext` is drawn as a hyperlink, with no API control at all

**Found:** 18.10.2026 · **Severity:** high

**What is wrong.** The client takes a word of the shape `name.ext` and renders it as a link, prefixing the
scheme itself. The text stored by the API carries no markup, so the link is created at render time and no
request field can suppress it. Cyrillic is no obstacle, and neither is a word inside a sentence.

**Proof.** From the same probe: `Макс.md` became `https://Макс.md`, and the fragment `и.ru` inside
"Просто текст и.ru" became `https://и.ru`; the API text for both stayed plain. The operator's screenshots
show MAX's own "Ссылка ведёт на сторонний сайт" warning for both.

**Consequence / risk.** Harmless text becomes clickable; a file name cannot be shown as text at all, and only the sender's own text can be defused.

**Seen on another platform (2026-10-18, operator's own check).** Telegram does the same: the same four
cases sent in Telegram were drawn as links too (`Макс.md`, `https://3dmk.ru`, `+7 900 000-00-00`, `и.ru`). So this
is a habit of messengers rather than a MAX quirk, and the complaint has to say so - otherwise it reads as if
MAX invented the behaviour. Telegram's Bot API carries the same design as well: `disable_web_page_preview`
(legacy) and `link_preview_options`, both per request, never global.

Where MAX is the better one, and it is worth saying in the same breath: it shows a "Ссылка ведёт на сторонний
сайт" warning before leaving the app, while Telegram opens the link without asking. Recorded because a claim
that mixes a common habit with a real defect loses the defects along with it.

**What we do meanwhile.** A zero-width space after the dot of a host-like word (`quietLinks`, see
[nopreview.go](../../external/gateway/max/nopreview.go)); genuine `http(s)` links are left alone. Cost of the
workaround is recorded below, in "Our own costs".

### D-03 The schema contradicts itself on the polarity of `disable_link_preview`

**Found:** 18.10.2026 · **Severity:** medium

**What is wrong.** For `post /messages` (schema line 1084) and `post /messages/{id}/comments` (line 1274) the
schema says "If `false`, server will not generate media preview for links in text". For `post /answers`
(line 1454) it says "If `true`, ...". One of the two must be wrong.

**Proof.** Live probes on 18.10: a message sent with `true` got no card, a message without the parameter got
one, and an edit with `true` kept a message clean. `true` is the value that disables.

**Consequence / risk.** A developer follows the wording, sends `false`, still gets a card - which is exactly the day lost here on 17.10.

**Why it matters.** A developer who follows the `false` wording will send `false` and still get a card, then
spend time on a guard that was never needed - which is exactly what happened here on 17.10.

### D-04 `PUT /messages` honours a parameter the reference does not list

**Found:** 18.10.2026 · **Severity:** medium

**What is wrong.** The reference for `PUT /messages` does not list `disable_link_preview`, but the server
accepts it and needs it: an edit without the parameter puts a preview card back onto a message that was
originally sent without one.

**Proof.** Probe D on 18.10: send with the flag, edit without it - the card appeared; the same edit with the
flag stayed clean. Recorded in [api/editing-messages.md](api/editing-messages.md).

**Consequence / risk.** An edit silently re-adds a card to a message that was clean, so card spam returns through a path nobody guards.

**What we do meanwhile.** Every edit carries the parameter.

### D-05 The documented `DELETE /messages` form answers 404, and `200` does not mean success

**Found:** 18.10.2026 · **Severity:** medium

**What is wrong.** The documented call `DELETE /messages?message_id=<mid>` answers 404; the form that works is
`DELETE /messages?chat_id=<id>&message_id=<mid>`. On top of that, a `200` response can carry
`{"success": false}`, so a status code alone proves nothing.

**Proof.** Measured with probe messages against the live bot: chained id form produced
`200 {"success":true}` after several 404 attempts. Recorded in
[api/deleting-messages.md](api/deleting-messages.md).

**Consequence / risk.** Cleanup code believes a delete happened while the message stays; a history rewrite quietly does nothing.

### D-06 `format` accepts undocumented values and rejects a plausible one

**Found:** 18.10.2026 · **Severity:** medium

**What is wrong.** Three behaviours exist: no `format` (plain text), `format: markdown`, `format: html`. A
request with `format: text` is refused with HTTP 400 `Can't deserialize body`, while `format: HTML` in upper
case is accepted - neither the rejection nor the case-insensitivity is documented.

**Proof.** Live probes on the history of this gateway, recorded in
[api/new-message-body.md](api/new-message-body.md) and [api/text-formatting.md](api/text-formatting.md).

**Consequence / risk.** A plausible `format: text` answers 400 with no hint, and case sensitivity differs between values without a word.

### D-07 Rendered markdown/html replaces the original text irreversibly

**Found:** 18.10.2026 · **Severity:** high

**What is wrong.** When a message is sent as markdown or HTML, MAX stores the **rendered** result. Reading
the message back returns something that is not what was sent, and the original markup cannot be recovered
through the API. For a bot that keeps its own record of what it sent, the platform's copy silently diverges.

**Proof.** Read-back probes on 18.10: the stored text of a markdown message came back rendered. Recorded in
[api/text-formatting.md](api/text-formatting.md).

**Consequence / risk.** What the bot recorded and what the platform holds diverge; a repair after a failed edit has nothing trustworthy to compare with.

**Why it matters.** Anything built on "read my own message back and compare" breaks; a message repair after
a failed edit has nothing trustworthy to compare against.

### D-08 Text alone makes MAX fetch a third-party URL

**Found:** 17.10.2026 · **Severity:** high

**What is wrong.** To build a card the server fetches an address found in the text. A chat participant
therefore cannot send a bare word without causing MAX's infrastructure to make a request to a host the
participant chose - a privacy leak (someone else's server learns that MAX fetched at that moment) and, worse,
a spam and phishing vector: the fetched page decides what the card shows.

**Proof.** On 17.10 a gateway message holding the file name `README.md` came back with a card for
`http://readme.md` - a parked domain serving ad spam - and `burst.go` was read as a host too. Images on the
cards are proxied through `i.oneme.ru`. Recorded in
[findings/2026-10-18-link-previews.md](findings/2026-10-18-link-previews.md).

**Consequence / risk.** A person's harmless word makes MAX's servers contact a host of their choosing - a privacy leak and a way to plant a spam card in someone's chat.

**What we do meanwhile.** Both layers of the guard, and that is why they matter beyond cosmetics: the point
is to keep a tool call from pulling an arbitrary domain into a person's chat.

### D-09 A bot can forge a `share` card with any title, description and image

**Found:** 18.10.2026 · **Severity:** high

**What is wrong.** The `share` attachment is supplied by the sender as data - `url`, `title`, `description`,
`image_url` - and MAX renders it as the card of a link. The card looks like a preview of the named site, but
every part of it is attacker-chosen. A user has no way to tell a forged card from a fetched one.

**Proof.** The schema's `ShareAttachment` shape and the observed `share` payload on a real message (D-01);
`POST /messages` accepts `attachments` from the caller. Not exploited by us - the finding is that nothing
prevents it.

**Consequence / risk.** A card can look like a trusted site while its title, description and picture are chosen by the sender - phishing a reader cannot detect.

**Status.** Worth naming in any complaint: it is the cheapest phishing surface in the platform.

### D-10 `GET /chats` was removed with no replacement

**Found:** 18.10.2026 · **Severity:** medium

**What is wrong.** Since June 2026 there is no way to list the chats a bot belongs to. A `chat_id` can only be
learned from an event, so a bot that misses a `bot_removed` event keeps a stale chat forever and has no way to
reconcile its list.

**Proof.** Reference page recorded in [api/getting-chat-id.md](api/getting-chat-id.md).

**Consequence / risk.** A bot that missed one event keeps a dead chat forever, with no way to reconcile its list.

**What we do meanwhile.** Chats are stored as they are met; we do not yet delete a chat on `bot_removed` -
this is a gap on our side too.

### D-11 `PATCH /me/commands` trims or refuses a list with no diagnostics

**Found:** 18.10.2026 · **Severity:** low

**What is wrong.** The commands list is capped at 32 entries and names/descriptions have length limits; a
list that breaks them is silently reduced or refused, and the answer does not say which entry was dropped.

**Proof.** Recorded in [api/commands.md](api/commands.md); the ceiling of 32 is also in the schema.

**Consequence / risk.** A dropped command stays invisible until somebody notices a hint missing.

**Suggestion.** Compare what was sent against what `GET /me` reports - the platform gives no other signal.

### D-12 Webhooks: port 443 only, a trusted CA certificate, silent auto-unsubscribe

**Found:** 18.10.2026 · **Severity:** medium

**What is wrong.** A webhook receiver must listen on port 443 with a certificate from a trusted CA (Mincifry
root), and after a run of failures MAX unsubscribes by itself after about 8 hours. The secret arrives in
`X-Max-Bot-Api-Secret` and must be compared in constant time, which the reference does not say.

**Proof.** Reference page recorded in [api/subscriptions-webhooks.md](api/subscriptions-webhooks.md).

**Consequence / risk.** A self-hosted bot behind a normal port cannot use webhooks at all, and the failure mode is silence rather than an error.

**Why it matters.** A self-hosted bot behind a normal port or a self-signed certificate simply cannot use
webhooks, and the failure mode is silence rather than an error.

### D-13 `POST /uploads` races with `attachment.not.ready`

**Found:** 18.10.2026 · **Severity:** low

**What is wrong.** An upload can answer `attachment.not.ready` before the media is usable; a sender must
retry. The reference does not say how long to wait or how many times.

**Proof.** Recorded in [api/uploads.md](api/uploads.md).

**Consequence / risk.** A retry policy is guesswork: too eager duplicates media, too slow stalls a turn.

### D-14 Deprecated fields are still returned without a removal plan

**Found:** 18.10.2026 · **Severity:** low

**What is wrong.** `last_activity_time` is marked deprecated "soon to be removed", and `GET /chats` is gone
with no replacement - the platform removes surface without a migration path, while responses keep the
deprecated field so nothing warns a developer.

**Proof.** [api/get-me.md](api/get-me.md), [api/getting-chat-id.md](api/getting-chat-id.md).

**Consequence / risk.** Responses keep looking complete while surfaces disappear underneath, with no migration path offered.

### D-15 A bot may delete only its own messages, 2 per second, and `200` may say no

**Found:** 18.10.2026 · **Severity:** medium

**What is wrong.** In a dialog a bot can delete only what it sent itself; deletions are rate-limited to about
two per second; the response may report `success: false` under a `200`. There is no bulk clear of a chat, so
"start a fresh screen" is not possible for a bot.

**Proof.** [api/deleting-messages.md](api/deleting-messages.md),
[findings/2026-10-18-clearing-the-screen.md](findings/2026-10-18-clearing-the-screen.md).

**Consequence / risk.** A bot cannot give a person a clean screen, and its delete may silently fail - any 'fresh start' needs another design.

**What we do meanwhile.** A history card on `/resume` instead of deleting anything.

### D-16 An edit window of 7 days, and an expired edit is not explained

**Found:** 18.10.2026 · **Severity:** low

**What is wrong.** A message can be edited for 7 days; what happens after that is not documented beyond the
limit itself, so a sender learns the hard way.

**Proof.** [api/editing-messages.md](api/editing-messages.md).

**Consequence / risk.** An old message cannot be corrected and the platform does not say what to expect, so long-lived notices rot.

### D-17 The raw token goes in `Authorization` without `Bearer`

**Found:** 17.10.2026 · **Severity:** low

**What is wrong.** The header is `Authorization: <token>` - no scheme. Client generators add `Bearer` by
default, so generated SDKs fail against production until patched, and the documentation does not warn about
it. Our own transports send the raw token.

**Proof.** Recorded in [api/openapi-schema.md](api/openapi-schema.md) (generator traps) and in the library
audit of 17.10.

**Consequence / risk.** Generated clients fail against production until patched, and the error looks like bad credentials rather than a wrong header.

### D-18 Reactions and polls exist in the platform, and no bot interface exposes them

**Found:** 18.10.2026 · **Severity:** medium

**What is wrong.** MAX has message reactions with a running total and polls with anonymous and revote
settings - people use them - and a bot can neither read nor create either. The Bot API has no reaction event,
no reaction field on a message, no poll attachment and no method for votes.

**Proof.** Two independent sides, both recorded in
[findings/2026-10-18-reactions-are-invisible.md](findings/2026-10-18-reactions-are-invisible.md):

- the bot side is empty: `reaction` has 0 occurrences in the 3814-line schema, the update union carries no
  reaction or vote event, the 13 API paths have no such method, and a live `GET /messages` shows no field;
- the platform side is not: `PyMax` (<https://github.com/MaxApiTeam/PyMax>) works with reactions
  (`message.react()`, `get_reactions()`, the `ReactionUpdateEvent.total_count` event) and with polls
  (`Poll`, `PollAttachment`, `vote_poll`, anonymous and revote flags), by talking to MAX's **internal** API as
  a person.

**Consequence / risk.** Opinion statistics - the obvious reason to want reactions - cannot be built for a
bot at all. The only route is the unofficial user API, which the operator rejected on 2026-10-16 as "явный
костыль и не наш метод", and whose own authors warn about account bans. So a whole class of bot scenarios is
closed until the platform publishes it.

**What we do meanwhile.** Nothing technical is possible: the feedback a bot really sees is a keyboard press
(`message_callback`). A feature request to MAX is the honest lever, and it needs the operator's word before
being filed.

## Same behaviour on other platforms (context for the complaint)

| Platform | Draws a bare `word.ext` as a link | Warns before opening | Bot-side control |
|---|---|---|---|
| **MAX** | yes (measured 2026-10-18) | yes, "Ссылка ведёт на сторонний сайт" | per message and per edit: `disable_link_preview`; nothing for the client link |
| **Telegram** | yes (operator's check, 2026-10-18) | no, opens the link straight away | per message: `link_preview_options`, legacy `disable_web_page_preview`; nothing for the client link |

Two things follow. First, D-02 is a property of messengers, not a MAX defect, and a complaint must present it
that way. Second, the part that is genuinely worth raising is not the link itself but D-08: the server fetching
an address because of plain text, without the sender asking for it, and D-09: a bot being able to forge the
card that the fetch produces.

## Our own costs (recorded, not blamed on MAX)

| Cost | Why it is acceptable | What would remove it |
|---|---|---|
| A zero-width space inside a file name a person may copy | it is invisible, and it only breaks words that read as hosts; a real link is untouched | a platform switch for the client link - does not exist (D-02) |
| Historic messages already sent with a full-width separator stay that way | re-editing them would mean a mass rewrite for cosmetics | nothing (D-07: the original markup is gone anyway) |
| The guard changes text the model wrote | it changes rendering, not meaning: the reader sees the same characters | the same nonexistent switch |

**`nopreview.go` is not a leftover.** It was built on 17.10 on the false conclusion "MAX has no switch",
and it looked like a workaround that had to be deleted once the switch was found. On 18.10 the operator's
screenshot showed why it stays: the switch covers the card, the guard covers the link, and neither covers the
other (D-02). So it is deliberate code in the current build, and what remains a cost is the invisible character
it puts inside a file name - that is the row above, not a defect of its own.

## Checked and found correct (do not put in a complaint)

- TLS is verified; no `InsecureSkipVerify` anywhere in our client.
- The token lives in an env file, never in a URL, never in a log line.
- The webhook secret is compared in constant time by us, and the secret format is constrained by the platform.
- Errors from the API do not repeat the token - measured on the library and on our own transport.
- No `Bearer` prefix is sent, which is what the server wants (D-17 is about the confusion, not about us).

## When this becomes a complaint

The operator's decision of 18.10: no claim is filed until the gateway work is finished, "who knows what else
will surface". When the time comes, the complaint is assembled from this file: entries grouped by area, each
with its proof and its suggested fix, with D-03 (schema polarity), D-08 (unsolicited fetch) and D-09 (forged
card) as the ones that matter most.
