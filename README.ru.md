# Шлюз MAX для Coddy — слой для наложения

Это не копия Coddy целиком: слой из 160 файлов добавляет в Coddy шлюз для мессенджера MAX.
Сам Coddy берётся из своего репозитория —
[coddy-project/coddy-agent](https://github.com/coddy-project/coddy-agent).
Слой накладывается поверх, после чего сборка с тегом `gateway.max` даёт бинарник со шлюзом.

## Что внутри

| Что | Зачем |
|---|---|
| `overlay/` | 160 файлов по тем же путям, что в Coddy: 112 новых и 48 заменяемых |
| `MANIFEST.tsv` | на каждый файл: путь, тип, sha256 исходного файла Coddy 1.3.62 и sha256 нашего |
| `apply.sh` | сверка версии, наложение слоя, сборка (Linux, WSL2) |
| `apply.ps1`, `apply.cmd` | то же для Windows: PowerShell и обёртка для cmd |
| `VERSION` | версия Coddy, на которую ложится слой, и версия самого слоя |
| `LICENSE` | MIT |

Кроме кода в слое — его документы: `docs/surfaces/max.md` с русским двойником, `docs/MAX-OVERLAY.md`
(карта слоя: что где лежит и почему), `docs/max-dossier/` (разбор платформы MAX), проверки в
`.github/workflows/`, сценарий приёмочных тестов `features/gateway_max.feature`.

## Что нужно

- **Coddy версии 1.3.62** — метка `1.3.62`, коммит `902ee08f`. Слой привязан к ней: он заменяет
  файлы оригинала, и на другой версии они разойдутся.
- **Go 1.26 или новее** — в `go.mod` объявлены `go 1.26.0` и `toolchain go1.27.3`: если ваш Go
  старше 1.27.3, набор инструментов подтянется сам (нужен доступ в Интернет). Скачать:
  <https://go.dev/dl/>.
- **Node.js 26** (сборка проверена и на 22.22.1) — только если собираете веб-интерфейс:
  <https://nodejs.org/en/download>.
- **git** — чтобы взять Coddy из его репозитория.
- **Токен бота MAX** и ваш id в MAX; **корневой сертификат Минцифры** — без него TLS-соединение с
  API MAX не проходит (подробно — в `docs/surfaces/max.md` после наложения).
- Около 2 ГБ свободного места на диске и доступ к Интернет.

### Проверка версий и что делать, если ниже нужной

Проверьте до установки — на этом ловятся почти все неудачи:

```sh
go version      # нужен 1.26 или новее; 1.27.0 подойдёт — 1.27.3 подтянется сам
node -v         # 22 или новее; нужен только для интерфейса
npm -v
```

Совсем без Go сборка не начнётся, поэтому он нужен до первого запуска. Версия ниже 1.27.3 не
помешает: `go.mod` просит набор инструментов `go1.27.3`, и Go начиная с 1.26 подтянет его сам — но
только при доступе в Интернет. Так установленный `winget`-ом Go 1.27.0 работает: он докачает 1.27.3
при первой сборке. Если в вашем дистрибутиве версия заметно старше, берите официальный архив с
<https://go.dev/dl/>.

**Windows.** Проще всего штатным установщиком пакетов:

```powershell
winget install --id GoLang.Go -e --accept-source-agreements --accept-package-agreements
```

Если нужна ровно 1.27.3 — MSI с той же страницы, от администратора:
`msiexec /i go1.27.3.windows-amd64.msi /quiet`. Без прав администратора — архив
`go1.27.3.windows-amd64.zip`: распаковать в `%LOCALAPPDATA%\Programs\Go` и один раз дописать в PATH:

```powershell
[Environment]::SetEnvironmentVariable('PATH',
  [Environment]::GetEnvironmentVariable('PATH','User') + ';' + "$env:LOCALAPPDATA\Programs\Go\bin", 'User')
```

**Linux и WSL2** — одинаково, официальный архив (для ARM-машин `linux-arm64`):

```sh
curl -LO https://go.dev/dl/go1.27.3.linux-amd64.tar.gz
sudo rm -rf /usr/local/go && sudo tar -C /usr/local -xzf go1.27.3.linux-amd64.tar.gz
echo 'export PATH=/usr/local/go/bin:$PATH' >> ~/.profile
export PATH=/usr/local/go/bin:$PATH
```

**macOS** — если работаете на нём: `brew install go` или пакет `.pkg` с той же страницы.

После установки откройте **новое** окно консоли: старое новый PATH не увидит. Проверка — `go version`,
ответ должен быть `go1.27.3` или новее.

**Если Go поставлен, а накатчик его не видит.** Установщик прописывает PATH в системе, но уже
открытое окно терминала держит ту среду, с которой стартовало, — `go` в нём просто не находится.
Убедиться, что дело именно в этом:

```powershell
where.exe go                                   # пусто?
Test-Path 'C:\Program Files\Go\bin\go.exe'    # True?
```

Пустой `where.exe` при существующем файле и означает «PATH не подхвачен». Лечится двумя способами:

- **открыть новое окно** терминала и повторить команду;
- **обновить PATH в текущем окне** одной строкой:

```powershell
$machinePath = [Environment]::GetEnvironmentVariable('Path','Machine')
$userPath    = [Environment]::GetEnvironmentVariable('Path','User')
$env:Path    = "$machinePath;$userPath"
go version
```

На Linux и WSL2 то же самое: `command -v go` и `ls /usr/local/go/bin/go` для проверки, а обновление —
`export PATH=/usr/local/go/bin:$PATH` (либо новое окно, где прочитается `~/.profile`).

Накатчики ищут Go сами: сначала в PATH, затем в обычных местах — `C:\Program Files\Go\bin`,
`%LOCALAPPDATA%\Programs\Go\bin`, `/usr/local/go/bin`, `~/go/bin`. Найденный Go берётся только для
этой сборки, в системе ничего не меняется, и об этом печатается строка «Go не было в PATH, но
найден…». Если Go лежит совсем в необычном месте, укажите его явно:

```powershell
$env:MAXGW_GO = 'D:\tools\go\bin\go.exe'
```

```sh
MAXGW_GO=/opt/go/bin bash apply.sh --coddy ~/coddy-src --build
```

Совсем не найден — накатчик остановится и напечатает, как поставить, как проверить и что накат слоя уже
сделан, повторять его не нужно.

**Node.js ниже 22.** Нужен только для сборки интерфейса. Поставить: `winget install OpenJS.NodeJS.LTS`
на Windows, на Linux и WSL2 — архив с <https://nodejs.org/en/download> или `nvm`. Если ставить не
хочется, соберите без интерфейса, убрав тег `ui`:

```sh
bash apply.sh --clone ~/coddy-src --build --tags "http,scheduler,memory,cli,gateway.max,swarm"
```

```powershell
powershell -ExecutionPolicy Bypass -File .\apply.ps1 -Clone C:\coddy-src -Build `
  -Tags "http,scheduler,memory,cli,gateway.max,swarm"
```

## Как накатить: четыре шага

Нужны две вещи: наш слой и Coddy той версии, под которую он сделан.

Шаг 1. Взять слой. Он лежит в публичном репозитории <https://github.com/mrbp-20/M-G> — клоном или
архивом, как удобнее. Папку можно положить куда угодно: `apply.sh` ищет `MANIFEST.tsv` и `overlay/`
рядом с собой, а клон Coddy получает ключом `--coddy`.

```bash
# клоном (нужен git)
git clone --depth 1 https://github.com/mrbp-20/M-G.git M-G
cd M-G

# или архивом, без git: скачать и распаковать
curl -L -o M-G.zip https://github.com/mrbp-20/M-G/archive/refs/heads/main.zip
unzip M-G.zip && cd M-G-main
```

Дальше все команды — из папки слоя.

Шаг 2. Взять Coddy той версии, под которую сделан слой:

```bash
git clone --depth 1 --branch 1.3.62 https://github.com/coddy-project/coddy-agent.git coddy-src
```

Шаг 3. Проверить совместимость, ничего не записывая:

```bash
bash apply.sh --coddy coddy-src --check
```

Шаг 4. Наложить слой и собрать бинарь:

```bash
bash apply.sh --coddy coddy-src --build
```

Шаги 2–4 можно заменить одной командой: она сама клонирует нужную версию Coddy, наложит слой и
соберёт бинарь.

```bash
bash apply.sh --clone ~/coddy-src --build
```

### То же в Windows

Путь первый, встроенный PowerShell (Windows 10 и 11), без WSL. Шаги те же, ключи те же; рядом с
`apply.sh` лежат `apply.ps1` и обёртка `apply.cmd`.

Шаг 1. Взять слой — из того же публичного репозитория:

```powershell
# клоном (нужен git)
git clone --depth 1 https://github.com/mrbp-20/M-G.git C:\M-G
cd C:\M-G

# или архивом, без git: скачать и распаковать
Invoke-WebRequest -Uri https://github.com/mrbp-20/M-G/archive/refs/heads/main.zip -OutFile M-G.zip
Expand-Archive -Path M-G.zip -DestinationPath . ; cd M-G-main
```

Шаг 2. Взять Coddy той версии, под которую сделан слой:

```powershell
git clone --depth 1 --branch 1.3.62 https://github.com/coddy-project/coddy-agent.git C:\coddy-src
```

Шаг 3. Проверить совместимость, ничего не записывая:

```powershell
powershell -ExecutionPolicy Bypass -File .\apply.ps1 -Coddy C:\coddy-src -Check
```

Шаг 4. Наложить слой и собрать бинарь:

```powershell
powershell -ExecutionPolicy Bypass -File .\apply.ps1 -Coddy C:\coddy-src -Build
```

Шаги 2–4 одной командой:

```powershell
powershell -ExecutionPolicy Bypass -File .\apply.ps1 -Clone C:\coddy-src -Build
```

Через обёртку короче, если политика запуска скриптов уже разрешена:

```bat
apply.cmd -Clone C:\coddy-src -Build
```

Путь второй, WSL2 (Ubuntu в Windows): работают ровно те же команды bash, что выше, с одной поправкой —
слой, клон и сборку держать в linux-папке, а не в `/mnt/c`. На `/mnt/c` сборка заметно медленнее и
ломаются права файлов:

```bash
cd ~ && git clone --depth 1 https://github.com/mrbp-20/M-G.git M-G && cd M-G
bash apply.sh --clone ~/coddy-src --build
```

**Первая сборка идёт в интернет и занимает время.** Go докачивает набор инструментов (`go1.27.3`) и
зависимости — это несколько минут и заметный трафик. Обрыв сети посреди скачивания накат не портит:
файлы слоя уже на месте, а скачанное остаётся в кэше модулей, поэтому повтор той же команды продолжает
с того же места. Что делать, если рвётся раз за разом, — «Сборка оборвалась на скачивании модулей» в
разделе «Устранение неполадок».

Готовый бинарник — `coddy-src/build/coddy` в Linux и WSL2, `C:\coddy-src\build\coddy.exe` в Windows.
Дальше одинаково: настроить `gateways.max` в `~/.coddy/config.yaml`, проверить
`./build/coddy serve --dry-run --test-config` и запустить `./build/coddy serve`.

## Windows: ключи и что печатается при успехе

Для Windows рядом лежат `apply.ps1` и обёртка `apply.cmd`: те же действия, что у `apply.sh`, и те же
ключи — `-Coddy`, `-Clone`, `-Check`, `-Build`, `-Tags`, `-Help`, `-Help` печатает справку. Сами
команды — в разделе «Как накатить» выше. Работает на встроенном PowerShell 5.1 (Windows 10 и 11) и на
PowerShell 7.

Честная оговорка о том, что проверено. Накатчик проверен на живом PowerShell 5.1: сверка хешей и
наложение слоя отработали на чистом клоне Coddy. Нативная сборка под Windows (`apply.ps1 -Build`,
`go build` в `build\coddy.exe`) выполнена на настоящем Windows 30.10.2026: бинарник собрался и ответил
`1.3.62-max-layer`; первая сборка при этом докачивала набор инструментов `go1.27.3` и зависимости.
Сборка в WSL2 тоже проверена. Если
она не пойдёт, это первое, о чём стоит написать — тогда слой собирается в WSL2, а в Windows
переносится готовый `coddy.exe`.

Что печатается при успехе.

Проверка совместимости — четыре строки, ничего не записано, код возврата 0:

```
слой: 161 строка манифеста (с заголовком), база Coddy 1.3.62
клон: C:\coddy-src
сверка: все 160 файлов слоя совместимы с этим клоном
проверка пройдена (-Check: ничего не изменено)
```

Наложение слоя — к этим строкам добавляется, сколько файлов заменено:

```
наложено файлов: 160

дальше:
  .\apply.ps1 -Coddy "C:\coddy-src" -Build   # или собрать вручную:
  go build -tags "http,scheduler,memory,cli,gateway.max,swarm" -o build\coddy.exe .\cmd\coddy\
```

Сборка — три строки, последняя и есть проверка, что шлюз в бинаре есть:

```
собираю: go build -tags "..." -ldflags "..." -o build\coddy.exe .\cmd\coddy\
готово: C:\coddy-src\build\coddy.exe
1.3.62-max-layer
```

Если версия Coddy другая, скрипт останавливается до копирования, ничего не меняет и возвращает код 2:

```
СЛОЙ НЕ ПОДХОДИТ К ЭТОЙ ВЕРСИИ CODDY
  изменённых файлов не совпало: 7 из 160, отсутствует: 0
    internal/agent/react.go
    ...
Нужен Coddy 1.3.62 (коммит 902ee08f).
Проверьте версию:  git -C "C:\coddy-src" describe --tags --always
```

## Повторный запуск после обрыва

Первый запуск может оборваться на середине: не было Go, пропала сеть, закрыли окно. Повторять ту же
команду можно и нужно — накатчик смотрит, что уже сделано, и продолжает:

- каталог из `--clone` / `-Clone` он больше не считает помехой: если это Coddy нужной версии, работа
  продолжается с него («клон уже на месте… продолжаю с ним»); если там другая версия, скажет об этом
  и подскажет команду;
- файлы, которые уже на месте и совпадают со слоем, повторно не копируются — в итоге будет
  «наложено файлов: 0» и «уже были на месте (прошлый запуск): 160»;
- файл, испорченный после наката, восстанавливается («восстановлено после обрыва: N»);
- `--check` / `-Check` на готовом дереве отвечает «слой уже наложен полностью».

## Что делает apply.sh

1. **Сверяет версию.** Для каждого из 160 файлов слоя считает sha256 и сравнивает с тем, что
   записан в `MANIFEST.tsv` как «файл Coddy 1.3.62». Не совпало — слой не накладывается вообще:
   скрипт печатает, какие файлы разошлись, и подсказывает, какую версию взять.
2. **Копирует** файлы `overlay/` в клон и после каждого копирования сверяет sha256 с нашим.
3. **Собирает** по вашему ключу `--build`: `go build -tags "http,scheduler,memory,cli,gateway.max,swarm"`.

Полезные ключи: `--check` (только проверка), `--tags "..."` (свой набор тегов), `--help`.

Сборка вручную, без скрипта:

```bash
cd coddy-src
go build -tags "http,scheduler,memory,cli,gateway.max,swarm" -o build/coddy ./cmd/coddy/

# с веб-интерфейсом (нужен Node и npm)
make build TAGS="http,ui,scheduler,memory,cli,gateway,swarm"
```

## Модульность: шлюз можно не подключать

Как и остальные шлюзы Coddy, MAX включается тегом сборки. Без тега его кода в бинарнике нет вовсе —
вместо адаптера подставляется заглушка, которая честно отвечает: «MAX is not built in».

| Теги сборки | Что попадает в бинарник |
|---|---|
| без тегов `gateway*` | ни одного шлюза: ядро, HTTP-сервер и веб-интерфейс |
| `gateway.telegram` | только Телеграм |
| `gateway.max` | только MAX |
| `gateway.telegram,gateway.max` | ровно два: Телеграм и MAX, без Пачки |
| `gateway` | все шлюзы: Телеграм, MAX и Pachca |

## После наложения

Описание шлюза — `docs/surfaces/max.md` (и русская версия `docs/ru/surfaces/max.md`): настройки,
поведение, живой пример конфигурации. Продуктовый README Coddy в клоне тоже заменяется нашим — в нём
требования, модульность, корневой сертификат и устранение неполадок.

Коротко о запуске:

```yaml
gateways:
  max:
    enable: true
    ca_file: "/home/<пользователь>/.coddy/mincifry-root.crt"
    admins: [<ваш id в MAX>]
```

```bash
./build/coddy serve --dry-run --test-config -config ~/.coddy/config.yaml   # проверка токена
./build/coddy serve                                                       # работа
```

## Обновление

Одной командой — то, что иначе делается руками. Рядом лежат `update.sh` (Юникс) и `update.ps1` с
обёрткой `update.cmd` (Винда). Они спрашивают у репозитория Coddy последнюю метку версии, у
репозитория слоя — последний коммит, и сравнивают опубликованный выпуск со своим:

```sh
bash update.sh --check      # проверить: спросить оба репозитория и скачать сам слой, чтобы
                            # прочитать его выпуск; не собирает, не ставит, службу не трогает
bash update.sh --apply      # скачать и собрать, службу не трогать
bash update.sh --install    # скачать, собрать, поставить и перезапустить
```

Выпуск опознаётся по содержимому, а не по номеру: файл `RELEASE` перечисляет sha256 всех публикуемых
файлов, отсюда и видно, новее ли опубликованное. Хеш коммита для этого не годится — он не может лежать
внутри самого коммита. Две вещи не путаются: `MANIFEST.tsv` привязывает заменяемые файлы к версии
Coddy, а `RELEASE` говорит, какой выпуск пакета перед вами, и потому замечает и правку корневого
скрипта.

На Винде то же самое: `update.cmd -Check`, `-Apply`, `-Install`; каталог для клонов задаётся ключом
`-Dir`, куда ставить бинарник — `-BinDir`, чем перезапускать — `-StartScript`.

Два отказа встроены намеренно. Если у Coddy появилась метка новее, чем `BASE_TAG`, скрипт
останавливается с кодом 3 и говорит, что нужен новый слой: накатывать слой на чужую версию он не
будет, сверка хешей всё равно откажет, и лучше узнать об этом сразу. И перезапуск бывает только по
`--install` / `-Install` — остальные два ключа работающую службу не трогают.

- **Вышел новый Coddy.** Этот слой к нему не подойдёт: `apply.sh` откажется накладывать его и назовёт
  разошедшиеся файлы. Нужен слой, собранный под новую версию (в `VERSION` будет другой `BASE_TAG`).
- **Вышел новый слой.** Кладётся поверх той же версии Coddy: `--apply` соберёт, `--install` соберёт,
  поставит и перезапустит.

## Устранение неполадок

- **«СЛОЙ НЕ ПОДХОДИТ К ЭТОЙ ВЕРСИИ CODDY».** В клоне не 1.3.62. Проверьте:
  `git -C coddy-src describe --tags --always`.
- **`go: not found`.** Go не в `PATH`: `export PATH=$PATH:/usr/local/go/bin`.
- **`x509: certificate signed by unknown authority`.** Не указан `ca_file` с корнем Минцифры.
- **Сборка оборвалась на скачивании модулей.** В выводе — `wsarecv: An existing connection was
  forcibly closed by the remote host` или `connection reset by peer`: сеть, VPN или антивирус с
  проверкой TLS оборвали передачу. Это не слой и не испорченная установка — уже скачанное остаётся в
  кэше, поэтому сначала просто повторите ту же команду. Если рвётся снова, докачайте модули отдельно:
  в bash `for i in 1 2 3 4 5 6; do go mod download && break; sleep 5; done`, в PowerShell
  `1..6 | ForEach-Object { go mod download; if ($LASTEXITCODE -eq 0) { break } }`, затем снова
  `--build` / `-Build`. Не помогает — пойдите другим путём: `GOPROXY=direct` (в PowerShell
  `$env:GOPROXY = 'direct'`) идёт к первоисточникам на GitHub; можно и зеркало
  `https://goproxy.io,direct`, но это сторонний сервис. Понять, кто именно рвёт, помогает обычный
  `curl -I` по адресу архива: если curl рвётся так же — дело в сети или антивирусе, а не в Go.
- **Интерфейс не собирается.** Нет Node/npm или он старше 22; без интерфейса собирайте без тега `ui`.
- **Бот отвечает в личном чате, но молчит в группе.** Смотрите `default_access` и `chats` в конфиге.

## Лицензия и авторы

MIT. Coddy — `Copyright (c) 2026 Pavel Rykov`. 
Max-Gate-Coddy —`Copyright (c) 2026 Versta-AI-Coder and Vladimir A.G.`

`LICENSE` лежит в двух местах: в корне пакета и внутри `overlay/LICENSE`. Текст у них один и тот же.
Корневой — лицензия самого пакета; внутренний при накате заменяет `LICENSE` в дереве Coddy, чтобы
собранный продукт нёс ту же строку авторства. Правя один, поправьте и второй, иначе они разойдутся. 
