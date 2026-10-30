# Link previews: the card MAX attaches by itself

**Date:** 2026-10-18 (the wrong conclusion is from 2026-10-17, kept on purpose)
**Kind:** our own finding, based on the official reference and live probes
**Sources:** <https://dev.max.ru/docs-api/methods/POST/messages> (the parameter),
<https://dev.max.ru/docs-api/methods/PUT/messages> (the edit), live bot API at
`https://platform-api2.max.ru` (the probes)

## The symptom

The operator's chat filled with preview cards nobody sent: "AI Tools Directory - dealsbe.com" under a
tool-call message. The message's text held the file name `README.md`.

## What MAX does

It reads any word of the shape `name.ext` as a host name, fetches the address itself and attaches a
`share` attachment. `README.md` is a real domain (the `.md` zone of Moldova), parked and serving ad spam -
hence the card and its far-away title. `burst.go` was read as a host too. The fetching happens on MAX's
side, not ours: the card held the destination's own title and description.

## The wrong turn (2026-10-17), kept as a lesson

Seven probes were sent, each with the flag in the request **body**:

| Probe | What was sent | Card |
|---|---|---|
| A | `text` with `README.md` | yes |
| B | `format: markdown`, the word in backticks | yes |
| C | `format: html`, the word in `<code>` | yes |
| D | `attachments: []` | yes |
| E | `disable_link_preview: true` **in the body** | yes |
| F | `link_preview: false` in the body | yes |
| G | an escaped dot `README\.md` | yes |

The conclusion drawn was "MAX has no switch", and a guard was built: `nopreview.go` inserted a zero-width
space (U+200B) after the dot of every host-like word, in `Send`, `EditMessage` and `EditMarkdown`
(commit `bd5f60a7`). It worked - the cards stopped - and it was still wrong: it left an invisible
character inside file names, which breaks a copy into a terminal.

## The answer (2026-10-18), from the reference

The reference documents the switch in the **parameters of the request**, not the body:

> `disable_link_preview` - boolean optional. Если `true`, сервер не будет генерировать превью для
> ссылок в тексте сообщения или поста

The earlier probes never reached it: an unknown body field is dropped without a word.

Live re-probe, with the parameter in the query string:

| Probe | Request | Card |
|---|---|---|
| A | `POST /messages` without the flag | `http://readme.md` |
| B | `POST /messages?disable_link_preview=true` | none |
| C | the same, plus `https://example.com` in the text | none |
| D | `PUT /messages` without the flag (an edit) | the card came back |
| E | `PUT /messages?disable_link_preview=true` | none |

So the flag also holds an edit, although the reference for `PUT /messages` does not list it - that part is
measured, not read.

## What the code does now (commit `b35b716c`, build `1.3.17-max.49`)

- `Client.Send` sets `q.Set("disable_link_preview", "true")`;
- `EditMessage` and `EditMarkdown` carry the same parameter;
- `nopreview.go` and its tests are **deleted** - text leaves untouched again;
- `linkpreview_test.go` holds it: the flag on a send, the flag on both kinds of edit, and a real
  `https://github.com/...` link arriving word for word.

## Lessons

1. **Read the method's documentation whole, and keep the query string and the body apart.** One look
   would have saved a day and a guard.
2. **"Proven by experiment" is only as good as the experiment.** A probe that feeds the wrong place
   proves nothing about the platform - it proves something about the probe.
3. **A workaround that hides an invisible character in a file name is worse than the disease** for
   anybody who copies that name.

## Correction, same day: the flag stops the card, not the link

**Caught by the operator on 2026-10-18 at 09:49**, in the history card a `/resume` posted: the line
holding the file name `Tata/ЖУРНАЛ.md` came out as a blue underlined hyperlink. No preview card - the flag
did its work - but the word was clickable.

So MAX does two separate things to a host-like word, and they need two separate cures:

| What MAX does | What stops it |
|---|---|
| fetches the address and attaches a `share` preview card (**server** side) | `disable_link_preview=true` in the query, on `POST` and on `PUT` |
| draws the word as a hyperlink (**client** side) | breaking the word itself: a zero-width space after the dot |

The guard with the zero-width space had been removed on the strength of the flag alone, which is exactly
the half-fix this file warned about in another form: a cure proven against one symptom, applied as if it
covered both.

Both layers are now in the code: `Client.Send`, `EditMessage` and `EditMarkdown` set the query parameter
**and** pass the text through `quietLinks` (`nopreview.go`), captions under pictures included. A real
`http://` or `https://` link keeps its words, so a link somebody meant still opens.

**Tests that moved with this:** `linkpreview_test.go` now expects the defused text rather than the text
untouched (it had asserted the opposite), and the three picture checks in `pictures_test.go` and
`bdd_test.go` compare the caption against `quietLinks(name)`.

**Lesson worth keeping:** a platform flag that names one symptom (`link_preview`) need not cover the other
(`link`). When a report says "the card is gone but the link stayed", the fix is not to choose between the
two cures - it is to keep both.
