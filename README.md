# MAX gateway for Coddy — an overlay layer

This is not a copy of Coddy: a layer of 160 files adds a gateway for the MAX messenger to Coddy. Coddy
itself comes from its own repository —
[coddy-project/coddy-agent](https://github.com/coddy-project/coddy-agent). The layer is applied on top,
after which a build with the `gateway.max` tag produces a binary with the gateway.

## What is inside

| Item | Why |
|---|---|
| `overlay/` | 160 files at the same paths as in Coddy: 112 new and 48 replaced |
| `MANIFEST.tsv` | for every file: path, kind, sha256 of the Coddy 1.3.62 file, sha256 of ours |
| `apply.sh` | version check, layer application, build (Linux, WSL2) |
| `apply.ps1`, `apply.cmd` | the same for Windows: PowerShell and a cmd wrapper |
| `VERSION` | the Coddy version the layer fits, and the layer's own version |
| `LICENSE` | MIT |

Beside the code the layer carries its documents: `docs/surfaces/max.md` with its Russian twin,
`docs/MAX-OVERLAY.md` (the layer map: what lies where and why), `docs/max-dossier/` (the MAX platform
dossier), the checks in `.github/workflows/`, and the acceptance test `features/gateway_max.feature`.

## What you need

- **Coddy 1.3.62** — tag `1.3.62`, commit `902ee08f`. The layer is pinned to it: it replaces files of the
  original, and on another version they diverge.
- **Go 1.27.3 or newer** — `go.mod` declares `go 1.26.0` and `toolchain go1.27.3`.
  Download: <https://go.dev/dl/>.
- **Node.js 26** (the build is verified on 22.22.1 too) — only if you build the web interface:
  <https://nodejs.org/en/download>.
- **git** — to fetch Coddy from its repository.
- **A MAX bot token** and your MAX id; the **MinCifry root certificate** — without it the TLS connection
  to the MAX API fails (details in `docs/surfaces/max.md` after the overlay).
- About 2 GB of free disk space and internet access.

## How to install: four steps

You need two things: our layer and the Coddy version the layer was made for.

Step 1. Take the layer. It lives in the public repository <https://github.com/mrbp-20/M-G> — as a clone
or as an archive, whichever suits you. The folder can sit anywhere: `apply.sh` looks for `MANIFEST.tsv`
and `overlay/` beside itself, and the Coddy clone is passed with `--coddy`.

```bash
# as a clone (git required)
git clone --depth 1 https://github.com/mrbp-20/M-G.git M-G
cd M-G

# or as an archive, without git: download and unpack
curl -L -o M-G.zip https://github.com/mrbp-20/M-G/archive/refs/heads/main.zip
unzip M-G.zip && cd M-G-main
```

Every command below runs from the layer folder.

Step 2. Take the Coddy version the layer was made for:

```bash
git clone --depth 1 --branch 1.3.62 https://github.com/coddy-project/coddy-agent.git coddy-src
```

Step 3. Check compatibility without writing anything:

```bash
bash apply.sh --coddy coddy-src --check
```

Step 4. Apply the layer and build the binary:

```bash
bash apply.sh --coddy coddy-src --build
```

Steps 2–4 can be replaced by one command: it clones the right Coddy version itself, applies the layer
and builds the binary.

```bash
bash apply.sh --clone ~/coddy-src --build
```

### The same on Windows

First path, the built-in PowerShell (Windows 10 and 11), without WSL. The steps are the same and so are
the keys; beside `apply.sh` sit `apply.ps1` and the `apply.cmd` wrapper.

Step 1. Take the layer — from the same public repository:

```powershell
# as a clone (git required)
git clone --depth 1 https://github.com/mrbp-20/M-G.git C:\M-G
cd C:\M-G

# or as an archive, without git: download and unpack
Invoke-WebRequest -Uri https://github.com/mrbp-20/M-G/archive/refs/heads/main.zip -OutFile M-G.zip
Expand-Archive -Path M-G.zip -DestinationPath . ; cd M-G-main
```

Step 2. Take the Coddy version the layer was made for:

```powershell
git clone --depth 1 --branch 1.3.62 https://github.com/coddy-project/coddy-agent.git C:\coddy-src
```

Step 3. Check compatibility without writing anything:

```powershell
powershell -ExecutionPolicy Bypass -File .\apply.ps1 -Coddy C:\coddy-src -Check
```

Step 4. Apply the layer and build the binary:

```powershell
powershell -ExecutionPolicy Bypass -File .\apply.ps1 -Coddy C:\coddy-src -Build
```

Steps 2–4 in one command:

```powershell
powershell -ExecutionPolicy Bypass -File .\apply.ps1 -Clone C:\coddy-src -Build
```

Shorter still through the wrapper, when the script execution policy already allows it:

```bat
apply.cmd -Clone C:\coddy-src -Build
```

Second path, WSL2 (Ubuntu inside Windows): exactly the same bash commands as above work, with one
correction — keep the layer, the clone and the build in a Linux folder rather than on `/mnt/c`. On
`/mnt/c` the build is noticeably slower and file permissions break:

```bash
cd ~ && git clone --depth 1 https://github.com/mrbp-20/M-G.git M-G && cd M-G
bash apply.sh --clone ~/coddy-src --build
```

The ready binary is `coddy-src/build/coddy` on Linux and WSL2, and `C:\coddy-src\build\coddy.exe` on
Windows. Everything after that is the same: set `gateways.max` in `~/.coddy/config.yaml`, check it with
`./build/coddy serve --dry-run --test-config`, and run `./build/coddy serve`.

## Windows: keys and what success looks like

On Windows `apply.ps1` and the `apply.cmd` wrapper do what `apply.sh` does, and take the same keys —
`-Coddy`, `-Clone`, `-Check`, `-Build`, `-Tags`, `-Help`; `-Help` prints the usage. The commands
themselves are in the "How to install" section above. It runs on the built-in PowerShell 5.1 (Windows 10
and 11) and on PowerShell 7.

What has actually been tested, honestly. The installer has been run on a live PowerShell 5.1: the hash
check and the overlay worked on a fresh Coddy clone. The build has been run in WSL2. The native Windows
build (`apply.ps1 -Build`, that is `go build` into `build\coddy.exe`) is described and should work, but
it has never been run on a real Windows: the machine that prepared the layer keeps Go in WSL2. If it
does not go, that is the first thing to report - then the layer is built in WSL2 and the ready
`coddy.exe` is carried over.

What it prints on success.

The compatibility check — four lines, nothing written, exit code 0:

```
слой: 161 строка манифеста (с заголовком), база Coddy 1.3.62
клон: C:\coddy-src
сверка: все 160 файлов слоя совместимы с этим клоном
проверка пройдена (-Check: ничего не изменено)
```

Applying the layer adds how many files were replaced:

```
наложено файлов: 160

дальше:
  .\apply.ps1 -Coddy "C:\coddy-src" -Build   # или собрать вручную:
  go build -tags "http,scheduler,memory,cli,gateway.max,swarm" -o build\coddy.exe .\cmd\coddy\
```

The build — three lines, and the last one is the proof that the gateway is in the binary:

```
собираю: go build -tags "..." -ldflags "..." -o build\coddy.exe .\cmd\coddy\
готово: C:\coddy-src\build\coddy.exe
1.3.62-max-layer
```

If the Coddy version is a different one, the script stops before copying anything, changes nothing and
returns code 2:

```
СЛОЙ НЕ ПОДХОДИТ К ЭТОЙ ВЕРСИИ CODDY
  изменённых файлов не совпало: 7 из 160, отсутствует: 0
    internal/agent/react.go
    ...
Нужен Coddy 1.3.62 (коммит 902ee08f).
Проверьте версию:  git -C "C:\coddy-src" describe --tags --always
```

The lines are Russian because the installer speaks to the person in Russian; this page is their English
translation: "layer: 161 manifest rows (with the header), Coddy base 1.3.62", "clone: …", "check: all 160
files of the layer match this clone", "check passed (-Check: nothing was changed)", "files applied: 160",
"building", "done", and, on a mismatch, "THE LAYER DOES NOT FIT THIS CODDY VERSION".

## What apply.sh does

1. **Checks the version.** For each of the 160 layer files it computes sha256 and compares it with the
   one recorded in `MANIFEST.tsv` as the Coddy 1.3.62 file. A mismatch means the layer is not applied at
   all: the script prints which files diverged and suggests the version to take.
2. **Copies** the `overlay/` files into the clone and, after each copy, compares sha256 with ours.
3. **Builds** on your `--build` key: `go build -tags "http,scheduler,memory,cli,gateway.max,swarm"`.

Useful keys: `--check` (check only), `--tags "..."` (your own tag set), `--help`.

Building by hand, without the script:

```bash
cd coddy-src
go build -tags "http,scheduler,memory,cli,gateway.max,swarm" -o build/coddy ./cmd/coddy/

# with the web interface (needs Node and npm)
make build TAGS="http,ui,scheduler,memory,cli,gateway,swarm"
```

## Modularity: the gateway is optional

Like every other Coddy gateway, MAX is switched on by a build tag. Without the tag its code is not in the
binary at all — a stub takes the adapter's place and answers honestly: "MAX is not built in".

| Build tags | What ends up in the binary |
|---|---|
| no `gateway*` tags | no gateway at all: the core, the HTTP server and the web interface |
| `gateway.telegram` | Telegram only |
| `gateway.max` | MAX only |
| `gateway.telegram,gateway.max` | exactly two: Telegram and MAX, without Pachca |
| `gateway` | all gateways: Telegram, MAX and Pachca |

## After the overlay

The gateway's description is `docs/surfaces/max.md` (and the Russian version `docs/ru/surfaces/max.md`):
settings, behaviour, a live configuration example. The product README of Coddy in the clone is replaced
by ours as well — it carries the requirements, the modularity table, the root certificate and the
troubleshooting.

The short version of starting it:

```yaml
gateways:
  max:
    enable: true
    ca_file: "/home/<user>/.coddy/mincifry-root.crt"
    admins: [<your MAX id>]
```

```bash
./build/coddy serve --dry-run --test-config -config ~/.coddy/config.yaml   # checks the token
./build/coddy serve                                                       # runs
```

## Updating

One command for what is otherwise done by hand. `update.sh` (Unix) and `update.ps1` with the
`update.cmd` wrapper (Windows) sit beside this file. They ask the Coddy repository for its newest tag,
the layer repository for its newest commit, and compare the published release with the one in hand:

```sh
bash update.sh --check      # check: ask both repositories and download the layer itself to read its
                            # release; builds nothing, installs nothing, leaves the service alone
bash update.sh --apply      # download and build, leave the service alone
bash update.sh --install    # download, build, install, restart
```

A release is identified by content, not by a number: the `RELEASE` file lists the sha256 of every
published file, which is how the script tells whether what is published is newer. A commit hash cannot
do this job - it cannot live inside the commit it names. Two things are kept apart: `MANIFEST.tsv` binds
the replaced files to one Coddy version, while `RELEASE` says which package release you hold, and
therefore notices a change to a root script too.

On Windows the same: `update.cmd -Check`, `-Apply`, `-Install`; `-Dir` picks the clone folder, `-BinDir`
where the binary goes, `-StartScript` what restarts it.

Two refusals are built in on purpose. When Coddy has a tag newer than `BASE_TAG`, the script stops with
exit code 3 and says a new layer is needed: it will not apply a layer to a version it was not built for,
the hash check would refuse anyway, and it is better to hear that at once. And a restart happens only
with `--install` / `-Install` - the other two keys never touch a running service.

- **A new Coddy is out.** This layer will not fit it: `apply.sh` refuses to apply it and names the files
  that diverged. You need a layer built for the new version (in `VERSION` its `BASE_TAG` will differ).
- **A new layer is out.** It is applied over the same Coddy version: `--apply` builds it, `--install`
  builds, installs and restarts.

## Troubleshooting

- **"THE LAYER DOES NOT FIT THIS CODDY VERSION".** The clone is not 1.3.62. Check it with
  `git -C coddy-src describe --tags --always`.
- **`go: not found`.** Go is not in `PATH`: `export PATH=$PATH:/usr/local/go/bin`.
- **`x509: certificate signed by unknown authority`.** `ca_file` with the MinCifry root is not set.
- **The interface does not build.** Node/npm is missing or older than 22; build without the interface by
  leaving out the `ui` tag.
- **The bot answers in a private chat but stays silent in a group.** Look at `default_access` and `chats`
  in the configuration.

## License and authors

MIT. Coddy — `Copyright (c) 2026 Pavel Rykov`.
Max-Gate-Coddy — `Copyright (c) 2026 Versta-AI-Coder and Vladimir A.G.`

`LICENSE` sits in two places: the package root and inside `overlay/LICENSE`. The text is the same. The
root one is the licence of the package itself; the inner one replaces `LICENSE` in the Coddy tree when
the layer is applied, so the built product carries the same authorship line. Change one, change the
other, or they drift apart.
