# Remark: the file-placement marker leaks into the MAX chat

*Handed over from the household session on 19.10.2026. Written from a live observation, not
from reading the code alone. **The fix belongs to the gateway session.***

**Status, 19.10 19:40 МСК: отправка сделана, утечка — нет.** Исходящие документы реализованы
(`documents.go`, 315 строк) и работают в бою как `1.3.17-max.83`; живая отправка —
`~/coddy/logs/coddy.log:1049`:
`msg="max: document sent" name=example.docx size=33538 mid=mid.<message id>
attempts=1 took=1.497s`. Маркерное сообщение пришло из того же хода.

**Исправлено в МАКСе, 19.10 20:30 МСК.** В `sender.go` появился один хелпер
`outgoingAnswer` (зовёт `session.StripAssistantContent(text, false, false)`) и две точки, где текст
ответа становится сообщением: живое превью (`stream`) и финальная доставка (`Flush`, до
`splitText`). Буфер остаётся сырым, поэтому в хранилище маркер выживает и веб ставит карточку.
Тест — `sender_test.go`, `TestSender_StripsFilePlacementMarkers`: маркер впереди текста, позади,
два маркера, и текст, который лишь похож на маркер (`<coddy_file/>` без `id`, угловые скобки) —
остаётся как есть. Остаётся живая проверка в чате.

## What the operator sees

Sending `example.docx` from the chat produced the document correctly, and then a second,
unrelated message arrived carrying the internal placement marker as plain text:

```
<coddy_file id="0123456789abcdef01234567"/>Е
```

MAX does not hide it, does not turn it into a link and does not fold it into the file card: the
line is simply visible in the conversation. A screenshot from the operator confirms it (19.10,
19:35 МСК; the line is the last, partly cut off one).

## Reproduction

1. In a chat bound to the gateway, produce an artifact from a tool call — `share_file` on any
   workspace file is enough.
2. The gateway posts the document (this part works: `~/coddy/logs/coddy.log:1049`
   — `msg="max: document sent" name=example.docx size=33538
   mid=mid.<message id> attempts=1 took=1.497s`; the gateway logs at
   `gateway.max: debug` into `~/coddy/logs/coddy.log`, not into `~/.coddy/logs`).
3. A message with the literal marker follows. In the API dump:
   `ts=1792427592621`, text `<coddy_file id="0123456789abcdef01234567"/>Е`, no attachments.

The trailing `Е` in the observed text suggests the marker sat inside a longer answer that was
cut when the message was split, so it is not a marker-only message in general — stripping has to
work on arbitrary answer text.

## What was re-checked against the tree (19.10, after the sending side landed)

Every reference in this file was re-measured, not carried over: `sender.go` grew to 814 lines when
`documents.go` (315) landed, so its numbers were the most likely to rot. All of them hold.

| Claim | Verified |
|---|---|
| marker regexp | `artifact_markers.go:11` `coddyFileMarker` |
| strippers and the trim rules | `:17` `StripArtifactMarkers`, `:28` `StripAssistantContent`, rules at `:21` |
| marker built and appended | `ArtifactMarkers` `:82`, built `:86`, appended to the answer `:147` |
| MAX sender path | accumulate `sender.go:159`, `post` `:300`, `Flush` `:308`, `splitText` call `:332`, `deliver` `:345` |
| strip calls in the adapters | none in `external/gateway/max`, none in `external/gateway/telegram` (outside tests) |
| web strip and its test | `httpserver/server.go:1159`, `last_assistant_content_test.go:42` case "markers stay out of the reply" |
| other callers | `react.go:675`, `:1642`, `:1740`, `:2720`, `scheduled_run.go:307` |
| a file to hold the new case | `external/gateway/max/sender_test.go` exists |

## Root cause

`<coddy_file id="…"/>` is a UI-only placement marker: it tells the web UI where in the answer the
artifact card goes. It is rendered for the web and **stripped before history leaves the server**
for surfaces that cannot render it.

- The marker is produced by `internal/session/artifact_markers.go:82` `ArtifactMarkers`
  (built at line 86, `coddyFileMarker` regexp at line 11), used from
  `internal/agent/react.go:1642`.
- The stripper already exists: `internal/session/artifact_markers.go:17`
  `StripArtifactMarkers(content string) string`, plus `StripAssistantContent` at line 28 for the
  case where a truncated answer is joined with its continuation.
- The web uses it: `external/httpserver/server.go` `lastAssistantContent` returns
  `session.StripArtifactMarkers(session.ContinuedAnswer(msgs, i))`; pinned by
  `external/httpserver/last_assistant_content_test.go:42`, case "markers stay out of the reply".
  Other callers: `internal/agent/scheduled_run.go:307`, `internal/agent/react.go:1740`, and
  `react.go:675` / `2720` use `StripAssistantContent`.
- **The MAX adapter calls none of them.** `grep -n 'StripArtifactMarkers\|StripAssistantContent\|coddy_file'
  external/gateway/max/*.go` outside tests returns nothing: the text travels from the update into
  `s.responseBuf.WriteString(u.Content.Text)` (`sender.go:159`) and out through
  `post`/`Flush`/`deliver` (`sender.go:300`, `308`, `345`) untouched.

The Telegram adapter has no strip calls either (`external/gateway/telegram/*.go`, outside tests),
but that adapter is **not ours**: its code and its behaviour belong to upstream's author. Nothing
here is claimed about Telegram, and nothing is changed there.

## Proposed fix

Strip at the edge, where answer text is turned into an outgoing message, never earlier — and
strip **once per outgoing text, not per chunk**.

`StripArtifactMarkers` ends in `strings.TrimSpace` (`artifact_markers.go:18`), while the sender
accumulates provider chunks (`sender.go:159`) and only later splits the whole answer at 4000 runes
(`splitText`, `:332`). A trimming strip applied to every chunk would eat the spaces and newlines
that sit between chunks, and words would run together at the joins. So:

1. Keep `responseBuf` raw, exactly as the store keeps the message: the marker has to survive on the
   stored assistant message so the web keeps placing the card.
2. Strip in one small helper of `external/gateway/max` that calls
   `session.StripAssistantContent(text, keepStart, keepEnd)` — never the trimming
   `StripArtifactMarkers` — and use it at the two places that turn buffer text into a message: the
   live preview (`post` `:300`, `deliver` `:345`) and the final delivery (`Flush` `:308`, before
   `splitText`), so the preview and the answer never disagree.
3. The flags today are `false, false`: the MAX sender never joins a truncated answer with its
   continuation — that join happens in the agent (`react.go:675`, `:2720`) — and its texts are whole.
   If the gateway ever joins, pass the flags with the reasoning documented at `:21`.
4. Reuse the session helper rather than a second regexp: the marker syntax stays defined in one
   place (`internal/session`).
5. Do **not** strip in the session store or in history: stripping happens only on the way out to a
   surface that cannot render the marker.

## Test to add

Mirror the web case: `external/httpserver/last_assistant_content_test.go:42`
("markers stay out of the reply"). In `external/gateway/max/sender_test.go`, send an answer whose
text carries a marker — alone, and with the marker inside a longer text (the observed case) — and
assert the outgoing message text contains no `<coddy_file`. Pin as well that ordinary text is not
mangled: a sentence with angle brackets or the word `coddy_file` without an `id` attribute stays
as it is (the regexp requires the `id` attribute, so this should pass for free — worth pinning).

## Acceptance

- `go test -tags gateway.max -race -count=1 ./external/gateway/max/` green, new case included.
- `gofmt -s -l .` and `go vet` clean.
- Live: `share_file` on any file → the document arrives **and no marker message appears**. This is
  the only live check in scope: the MAX gateway is ours, and Telegram is not.

## Notes for whoever picks it up

- Severity: medium — visible to every chat user, does not break delivery of the document itself.
- Evidence kept in the household session's memory: `memory/max-otpravka-faylov-2026-10-19.md`
  (live `mid`, API dump, screenshot description).
- Numbers that matter are in this file: `max.83` in production, `documents.go` 315 lines,
  `mid.<message id>`. They were measured, not remembered.
