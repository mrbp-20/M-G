# Our overlay on upstream Coddy

This repository is a fork of [`coddy-project/coddy-agent`](https://github.com/coddy-project/coddy-agent): the default branch `main` carries our work as a rebase-able overlay on `upstream/main`, and the branch `upstream` keeps the untouched upstream tree for comparison.

This file exists for one moment only: **when an upstream update conflicts**. It answers "why do we touch this file, and what has to survive the conflict", so the resolution is a decision taken with the reason in view instead of a guess.

Keep it current: a file added to the overlay, or a reason that changed, belongs here in the same commit.

## The rule

Two rules make updates cheap, and both are older than this document:

1. **MAX-specific code lives in new files.** A new file cannot conflict — nobody upstream changes a file that does not exist there. Today 112 of our 160 files are new, and they are the whole adapter: `external/gateway/max/*`, `external/gateway/serve_max.go`, `external/gateway/serve_max_stub.go`, `external/httpserver/chat_mirror.go`, `internal/session/session_origin.go` (which reads where a session started without loading it) and its test, `features/gateway_max.feature`, `scripts/maxgw-update.sh`, `scripts/maxgw-update-all.sh` with its Windows twins `scripts/maxgw-update-all.ps1` and `.cmd` (the whole cycle: look for a new Coddy and a new layer, download, build, restart), the two CI workflows (`.github/workflows/tests-max.yaml` and `security-max.yaml`), and the documentation set: `ARCHITECTURE.md`, `ARCHITECTURE.ru.md`, `README.ru.md`, `docs/surfaces/max.md` with its Russian twin, the two handover briefs, `docs/MAX-OVERLAY.md` and `docs/max-dossier/*`.
2. **A change to somebody else's file is a hook, not a rewrite.** Two lines of registration beat a restructured function: upstream keeps moving those functions, and every line we add there is a line we pay for at the next rebase.

When a change to a shared file turns out to be generic — useful to any gateway, not only to MAX — it belongs upstream as a pull request. A merged pull request removes the file from the overlay for good. That is also the road to Pavel taking the gateway itself.

## Three states, not one

The same file lives in three places at once, and every number below belongs to exactly one of them.
Mixing them is how a document starts lying without anyone editing a wrong digit.

| State | What it is | Where the number comes from |
|---|---|---|
| The maintainer repository | this fork, rebased onto `upstream/main`; the only place a change is made | `git diff --name-status upstream/main...HEAD -- . ':(exclude)ui'` |
| The published layer | `overlay/` plus the manifest, built **from** the maintainer repository and nothing else | `LAYER_FILES` / `LAYER_NEW` / `LAYER_MODIFIED` in `VERSION` |
| After applying | a Coddy tree with the layer on top, which is what a client compiles | `apply.sh --check` on that tree |

The two differ by the files the package leaves out on purpose: `.gitignore`, the two handover briefs
they hold the operator's identifiers, and the maintainer-only build script. Those never reach a client,
so they are counted in the first state and not in the second.

## Files we share with upstream

48 files: every MAX change that had to land inside a file upstream also owns, does. Each of them conflicts the day Pavel touches it: the rebase onto 1.3.62 found twenty-two of them carrying his changes too, and the rows added since — the chat-mirror seam below and `AGENTS.md` — are the newest part of the surface.

### The document seam: two files

| File | Ours | Why |
|---|---:|---|
| `internal/agent/react.go` | +2 | one call beside the pictures' meta writer: a finished `share_file` call also publishes `coddy.files`, so a surface sends the document by artefact id (the MAX gateway's `documents.go`) |
| `internal/session/tool_files.go` | new | `ToolFile`: the artefact a surface may send - it carries no path, so a model has no way to name one |

The new file cannot conflict. The two lines in `react.go` are the whole price, and they sit inside the block that already publishes `coddy.artifacts` for the same call: one artefact, two descriptions, one for the web row and one for the messengers.

### The artefact record: two files

| File | Ours | Why |
|---|---:|---|
| `internal/session/artifacts.go` | +37 −8 | re-sharing the same bytes used to return the first name the artefact was ever captured under, so a chat got a name its file no longer had: `artifactByDigest` finds the record by sha256, and `takeLatestShare` gives it the name and path of the latest share |
| `internal/session/artifacts_test.go` | +49 | `TestCaptureArtifactTakesTheNameOfTheLatestShare`: the record follows the file, not its first capture |

Both carry Pavel's own capture code untouched around ours: the digest was already computed there, so the lookup reuses it instead of hashing the file again.

### Build tags: twelve files, one identical line

`external/gateway/access/acl.go` · `external/gateway/access/acl_test.go` · `external/gateway/access/turn.go` · `external/gateway/gateway.go` · `external/gateway/hub.go` · `external/gateway/hub_test.go` · `external/gateway/proxyutil/proxy.go` · `external/gateway/proxyutil/proxy_test.go` · `external/gateway/replyquote/replyquote.go` · `external/gateway/replyquote/replyquote_test.go` · `external/gateway/sessionstore/sessionstore.go` · `external/gateway/sessionstore/sessionstore_test.go`

Each carries `//go:build gateway || gateway.telegram || gateway.pachca || gateway.max` where upstream has the same line without `|| gateway.max`. Resolution: **add `|| gateway.max` to whatever line upstream has.** `access/turn.go` and `gateway.go` differ in one more line each (an interface assertion, a doc note); same rule.

`.claude/rules/gateway.md` and `.cursor/rules/gateway.mdc` carry that list as prose for the two agent rule trees: add `gateway.max`, keep their wording.

None of these fourteen conflicts today; all of them will the day Pavel touches the tag list. It is the cheapest conflict there is.

### Registration and config plumbing: six files

| File | Ours | Why |
|---|---:|---|
| `internal/config/gateway.go` | +190 | `MaxGatewayConfig` and the `max` field of the gateway struct |
| `external/gateway/serve.go` | +20 | the MAX case in the gateway constructor: build the bot from config, or skip it |
| `cmd/coddy/serve.go` | +19 | the `serve.KindGatewayMax` descriptor and its banner line — *Pavel too* |
| `internal/config/config.go` | +6 | `cfg.Gateways.Max.Normalize()` and `.Validate()` — *Pavel too* |
| `internal/serve/subsystem.go` | +1 | `KindGatewayMax Kind = "gateway.max"` — *Pavel too* |
| `internal/logger/component.go` | +1 | `ComponentGatewayMax = "gateway.max"` |

Keep upstream's structure and re-add our hook at the same logical place. In `cmd/coddy/serve.go` that already meant adopting Pavel's own form: the banner writes to an `io.Writer` now, not to `os.Stderr`.

### Config schema and the settings UI: four files

| File | Ours | Why |
|---|---:|---|
| `internal/config/config.schema.json` | +117 | the `gateways.max` block — *Pavel too* |
| `internal/config/jsondto.go` | +39 −2 | `MaxGatewayJSON` mirroring the config — *Pavel too* |
| `internal/config/ui_schema.go` | +64 −2 | the MAX settings block and `maxUserGroupProps` — *Pavel too* |
| `internal/config/testdata/ui_schema.json` | +150 −2 | the testdata the UI-schema test compares against — *Pavel too* |

Before merging by hand, check whether the schema or the testdata is generated: a regenerated file beats a hand-merged one.

### Dry-run probes: three files, all additive

`internal/dryrun/network.go` (+107: `maxProbes()`, `GET /me`, and MAX takes the raw `Authorization` without `Bearer`) · `internal/dryrun/dryrun_test.go` (+61) · `internal/dryrun/dryrun.go` (+1: `probes = append(probes, r.maxProbes()...)`). All three are on Pavel's side too; our blocks are self-contained and re-add cleanly.

### Chat mirrors: five files, all additive

`external/httpserver/bridge.go` (+12 −1) · `external/httpserver/serve.go` (+21) ·
`external/httpserver/serve_http.go` (+1) · `external/httpserver/server.go` (+13 −1) ·
`internal/serve/runtime.go` (+92) — the way back: a turn started in the web UI is
published into the MAX chat its session is bound to. `internal/serve/runtime.go`
carries `chatMirrors` with `AddTurnMirror`/`TurnToChats`/`SayToChats`; the http
server gets `Options.ChatMirrors`, `Server.chatMirrors` and `Sender.extra`, declared
in a file **without a build tag** (a tagged declaration broke the stub build), and
forwards to the mirror on both paths that start a turn. All five are on Pavel's side
too; our blocks are self-contained and re-add cleanly.

### Documents: nine files

`README.md` (+108 −106) is our fork's own front page: **ours by intent — take our side on a conflict**, and read their diff only for structural changes (`git diff <base>..upstream/main -- README.md`). Today that cost two of Pavel's paragraphs (Homebrew, a Metrics row); a fork's README is not the place to track them.

Seven documentation rows are additive: the page `surfaces/max.md`, its navigation entry and the `gateways.max` reference rows, in both languages — `docs/README.md`, `docs/nav.yaml`, `docs/reference/config.md`, `docs/ru/README.md`, `docs/ru/nav.yaml`, `docs/ru/reference/config.md`, and `docs/ru/config-descriptions.yaml` (+51, the generator's source). All seven are on Pavel's side too; conflicts here are textual and cheap.

`AGENTS.md` (+2) is **ours by intent**: the rule that the MAX dossier is read before the internet, and that what the web gave us goes back into it the same day. It belongs in the repository's own instructions, because a rule that lives only in memory does not survive a fresh session.

### Two one-liners

| File | Ours | Why |
|---|---:|---|
| `internal/skills/bundled/configure-coddy/SKILL.md` | +1 −1 | MAX named among the gateways — *Pavel too* |
| `Makefile` | +3 −1 | `gateway.max` in `TEST_TAG_SETS` and in the tag comment — *Pavel too* |

## One command for the whole cycle

`scripts/maxgw-update-all.sh` (Unix) and `scripts/maxgw-update-all.ps1` with its `update.cmd` wrapper
(Windows) do what used to be done by hand: ask the Coddy repository for its newest tag, ask the layer
repository for its newest commit, compare the published release with the one in hand, download what
changed, apply the layer, build, install and restart.

Two identities are kept apart on purpose. `MANIFEST.tsv` binds the replaced files to the one Coddy
version they were built for. `RELEASE` names the package release: the sha256 of every published file,
its own excluded, because a file cannot carry its own digest. The split exists because a release that
only touched a root script - an update script, a README, the publication list - changes no manifest row,
and the updater would have reported "nothing to do" while the fix stayed undelivered.

Two refusals are built in on purpose. A layer applies to exactly one Coddy version - the manifest
carries a sha256 of every file it replaces - so the script stops with exit code 3 when upstream has a
tag newer than `BASE_TAG`, and says a new layer is needed instead of half-applying one. And a restart
happens only with `--install` / `-Install`: `--check` and `--apply` never touch a running service.

## The generated reference

`docs/reference/max-gateway-api.md` and its Russian twin are **generated from the adapter's own source
comments** by `scripts/gen-max-api-reference.py` (`--lang en|ru|both`, run from the Coddy tree root).
Nobody edits them by hand. Change a signature, a constant or a doc comment in `external/gateway/max/*.go`
and regenerate both files in the same commit, or the reference starts describing code that no longer
exists — and a reference that lies is worse than no reference, because it is believed.

## Files we add outright

  The count is in the opening rule above and is recomputed on every release; a second copy of it here
  would only be a second number to keep true. What the new files are: `external/gateway/max/*`
  (transport, client, poll, sender, dispatch, commands, the published hints, `/stop`, `/resume`, the
  history card, incoming files, media, model, tests, benchmarks), `external/gateway/serve_max.go`,
  `external/gateway/serve_max_stub.go`, `external/gateway/sessionstore/*`,
  `internal/session/session_origin.go`, `internal/session/tool_files.go`,
  `internal/session/turn_answer.go`, `internal/agent/tool_files.go`, `features/gateway_max.feature`,
  `docs/surfaces/max.md`, `docs/ru/surfaces/max.md`, `ARCHITECTURE.md`, `ARCHITECTURE.ru.md`,
  `README.ru.md`, `scripts/maxgw-update.sh`, `scripts/maxgw-update-all.sh` with its Windows twins, and
  the two files of this procedure: `docs/MAX-OVERLAY.md` and the generated
  `docs/reference/max-gateway-api.md` with its Russian twin.

## CI: the upstream security workflow is disabled

`.github/workflows/security.yaml` came with the mirror and watches `main`, which
here is a copy of upstream. Its last publishing steps push SARIF into GitHub code
scanning, and code scanning is not available for a private repository on the Free
and Pro plans: every trusted run of that workflow ended red with `Code scanning is
not enabled for this repository` while `make security` itself passed. A
permanently red check hides a real failure behind itself, so the workflow is
turned off in this repository:

```sh
# what was done, and how to undo it
PUT /repos/mrbp-20/M-G/actions/workflows/security.yaml/disable
PUT /repos/mrbp-20/M-G/actions/workflows/security.yaml/enable
```

`.github/workflows/security-max.yaml` takes its place: the same `make security`
(one script, one gate, pinned versions and thresholds), run over this branch
instead of the mirror, with the reports artifact and the job summary kept. SARIF
publication there is opt-in through the repository variable
`ENABLE_CODE_SCANNING` — set it to `true` where code scanning exists (a public
repository) and the two upload steps start running; until then the job summary
states that the publication was skipped, so the skip cannot pass unnoticed.

What the scanners said on 17.10.2026, for the record: trivy and semgrep clean,
and govulncheck one module-level finding — `GO-2026-5932`, the unmaintained
`golang.org/x/crypto/openpgp` package. Nothing imports it (this code uses
`argon2` from the same module), so the gate, which trips at `symbol` level,
correctly stays green.

## When the conflict actually happens

```sh
git rebase upstream/main            # stops on a conflict
git status                          # the files below
# open this file, read the row of the file, keep their structure and re-add our hook
git add <file> && git rebase --continue
```

`git rerere` is enabled in this clone (`rerere.enabled`, `rerere.autoupdate`), so a conflict we have already resolved once is resolved by git the next time.

If a file here stops needing our change — because upstream took it, or because the feature moved into a new file — delete its row in the same commit. A stale map is worse than no map.

## Updating

`scripts/maxgw-update.sh` owns the cycle; its header lists the modes.

- `--check` and `--dry` are safe unattended: the first reports what is new upstream and how many files would conflict (exit 1 when behind), the second rehearses the rebase in a throwaway worktree and leaves the real tree alone.
- `--apply` rebases this branch onto `upstream/main`; `--build` proves the result (gofmt, vet, the release build, the adapter tests); `--install` backs up the installed binary, installs the fresh one and restarts the service from a detached shell, because Coddy runs the script from inside the very process it replaces.
- The version stamp is `<nearest upstream tag>-max.<our commit count>`, e.g. `1.3.17-max.17`: `coddy -v` says which upstream release this build sits on, and semver comparisons order our builds correctly.

**Never run `coddy update -y` against the official repository.** It installs an official release, which has no MAX gateway, and the running service would lose the gateway. `coddy update --check` is safe and useful — it only reports Pavel's latest release.

Once our own releases exist in a public repository, `coddy update --repo <ours>` becomes the native path: the updater takes its releases from `-repo`, expects `coddy_<version>_<goos>_<goarch>.tar.gz` with `SHA256SUMS` beside them, and sends no token — hence the release repository has to be public.
