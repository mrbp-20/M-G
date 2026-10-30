# What MAX does to any text, and where each half comes from

**Date:** 2026-10-18
**Asked by:** the operator, who sent four deliberate cases into the chat and then read the result.
**Why it matters:** it separates what our gateway controls from what it does not, so nobody hunts a bug
in our code that lives in the platform.

## The experiment

One message from the operator (a person, not the bot), sent to the bot's direct chat:

```
1. Макс.md
2. https://3dmk.ru
3. +7 900 000-00-00
4. Просто текст и.ru
```

## What happened, case by case

| Case | Client draws a link | Server attaches a card | What the API holds |
|---|---|---|---|
| `Макс.md` | yes, as `https://Макс.md` | no | text only |
| `https://3dmk.ru` | yes | **yes** | one `share` attachment |
| `+7 900 000-00-00` | no | no | text only; the phone sheet ("Открыть с помощью: Телефон, ПСБ") is the OS, not MAX |
| `и.ru` inside a sentence | yes, as `https://и.ru` | no | text only |

## The evidence from the API

`GET /messages?chat_id=<id>&count=15` returned the operator's message with **one attachment**:

```json
{
  "type": "share",
  "payload": {
    "url": "https://3dmk.ru",
    "token": "<share token>"
  },
  "title": "3D Мастерская - Профессиональные услуги 3D печати в Краснодаре",
  "description": "Мы предлагаем высококачественные услуги 3D печати с индивидуальным подходом в Краснодаре и Краснодарском крае.",
  "image_url": "https://i.oneme.ru/i?r=B0zLc4bX..."
}
```

So the card is **server side** and it is attached to a person's message exactly as it is to a bot's: the
gateway is not involved at all in this message.

The **text stored by the API is unchanged** - no markup, no `<a>`, no protocol added. Therefore the blue
underlining in the client is drawn at render time from the raw text, and there is no request field that
could stop it: it is not a property of the message, it is a property of the client.

## The two halves, named

1. **Preview card** - the server fetches an address it found in the text and attaches a `share`
   attachment. Triggered by something the server reads as a URL: `https://3dmk.ru` did, `Макс.md` and
   `и.ru` did not (no card for them).
2. **Hyperlink** - the client draws any word of the shape `name.ext` as a link, and prefixes the scheme
   itself: `Макс.md` became `https://Макс.md`, a sentence fragment `и.ru` became `https://и.ru`. Cyrillic
   is no obstacle. This is why the gateway's zero-width guard is about words, not about URLs.

## What this means for the gateway

- The guard (`quietLinks`) and the flag (`disable_link_preview`) cover **only what the bot sends**. A
  person's own text never passes through the gateway, so nothing we do can change how it is drawn - and
  nothing should.
- Proof that our side works, from the same probe: the bot message that carried `gateway.max` (a text the
  client would happily linkify) came back with **zero attachments**, and its text held the zero-width
  separator.
- A person who sees a link in their own message is seeing MAX's own behaviour. That is not a defect in
  this gateway, and the honest answer is to name it rather than to promise a fix.

## Open question worth checking later

Whether the `share` token can be rejected (an empty `attachments` list on `PUT`) to *remove* a card from an
already-previewed message. Not tested: a bot may edit only its own messages, so this matters only for its
own text, which the flag already covers.
