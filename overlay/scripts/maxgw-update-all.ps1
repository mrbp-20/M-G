# Обновление шлюза MAX под Windows: проверить новинки в двух репозиториях,
# скачать, собрать, поставить и перезапустить. Тот же порядок, каким это
# делается на сервере, только одной командой.
#
#   update.ps1 -Check     только сказать, что нового (ничего не ставит)
#   update.ps1 -Apply     скачать и собрать, службу не трогать
#   update.ps1 -Install   скачать, собрать, поставить и перезапустить
#
# Ключи: -Dir <каталог>   где держать клоны (по умолчанию %LOCALAPPDATA%\MaxGate)
#        -Tags "<теги>"   теги сборки, по умолчанию как в пакете
#        -BinDir <кат>    куда ставить coddy.exe (по умолчанию каталог скрипта)
#        -StartScript     чем перезапускать (например C:\coddy\start-coddy.ps1)
#
# Версия установленного слоя опознаётся по содержимому: по манифесту
# MANIFEST.tsv, а не по хешу коммита. Хеш коммита не может лежать в самом
# коммите, поэтому «впишем свой коммит в VERSION» невыполнимо. Совпали
# манифесты — у вас последний слой; разошлись — ищем ваш манифест в истории
# опубликованного: нашли, значит отстали и обновляемся; нет — копия из другой
# линии, и обновление откатило бы её.
#
# Две вещи, которые скрипт НЕ делает молча и это его главное свойство:
#   1. он не накатывает слой на чужую версию Coddy — слой прибит хешами к одной;
#   2. он не перезапускает службу без ключа -Install.
[CmdletBinding()]
param(
    [switch]$Check,
    [switch]$Apply,
    [switch]$Install,
    [switch]$Help,
    [string]$Dir = "$env:LOCALAPPDATA\MaxGate",
    [string]$Tags = "http,scheduler,memory,cli,gateway.max,swarm",
    [string]$BinDir = "",
    [string]$StartScript = "",
    [string]$CoddyRepo = "https://github.com/coddy-project/coddy-agent",
    [string]$LayerRepo = "https://github.com/mrbp-20/M-G"
)

$ErrorActionPreference = "Stop"
$OutputEncoding = [System.Text.Encoding]::UTF8
try { [Console]::OutputEncoding = [System.Text.Encoding]::UTF8 } catch { }

$Here = Split-Path -Parent $MyInvocation.MyCommand.Path
if (-not $BinDir) { $BinDir = $Here }

function Say($text) { Write-Host $text }
function Fail($text) { Write-Host "ошибка: $text" -ForegroundColor Red; exit 2 }

if ($Help) {
    # Справка — это шапка файла. Режем её по началу кода, а не по числу строк:
    # иначе в вывод попадают [CmdletBinding()] и первые ключи.
    Get-Content $MyInvocation.MyCommand.Path |
        ForEach-Object {
            if ($_ -match '^\s*\[CmdletBinding\(\)\]') { break }
            if ($_ -notmatch '^#') { break }
            $_ -replace '^# ?', ''
        }
    exit 0
}

$Mode = "check"
if ($Apply) { $Mode = "apply" }
if ($Install) { $Mode = "install" }

# --- что у нас есть сейчас -------------------------------------------------
$VersionFile = Join-Path $Here "VERSION"
$Manifest = Join-Path $Here "MANIFEST.tsv"
if (-not (Test-Path $VersionFile)) { Fail "рядом со скриптом нет VERSION — это не пакет слоя" }
if (-not (Test-Path $Manifest)) { Fail "рядом со скриптом нет MANIFEST.tsv — это не пакет слоя" }

# RELEASE описывает весь выпуск пакета (включая корневые скрипты), MANIFEST.tsv —
# только совместимость с версией Coddy. Основное опознание — по RELEASE; если в
# опубликованном слое его ещё нет, сверяемся по манифесту, как раньше.
$IdFile = "MANIFEST.tsv"
if (Test-Path (Join-Path $Here "RELEASE")) { $IdFile = "RELEASE" }
$version = @{}
foreach ($line in Get-Content $VersionFile) {
    if ($line -match '^([A-Z_]+)=(.*)$') { $version[$Matches[1]] = $Matches[2].Trim() }
}
$BaseTag = $version["BASE_TAG"]
$LayerSource = $version["LAYER_SOURCE_COMMIT"]
if (-not $BaseTag) { Fail "в VERSION нет BASE_TAG" }

$sourceNote = ""
if ($LayerSource) { $sourceNote = ", из $($LayerSource.Substring(0,8))" }

# Хеш содержимого без учёта переводов строки: на машине с core.autocrlf=true файл
# приходит с CRLF, а из истории — с LF, и сырые хеши одного и того же файла
# расходятся. Строки склеиваем через перевод строки, поэтому оба источника дают
# одно и то же.
function Get-ContentHash([string[]]$lines) {
    $text = ($lines -join "`n")
    $sha = [System.Security.Cryptography.SHA256]::Create()
    $bytes = [System.Text.Encoding]::UTF8.GetBytes($text)
    return (($sha.ComputeHash($bytes) | ForEach-Object { $_.ToString("x2") }) -join "")
}

$LocalLines = @(Get-Content (Join-Path $Here $IdFile))
$LocalId = Get-ContentHash $LocalLines
if (-not $LocalId) { Fail "не смог посчитать хеш $IdFile" }

Say "слой: собран под Coddy $BaseTag$sourceNote, выпуск $($LocalId.Substring(0,8)) ($IdFile)"

# --- что есть у них --------------------------------------------------------
function NewestTag($repo) {
    $tags = & git ls-remote --tags --refs $repo 2>$null
    if ($LASTEXITCODE -ne 0) { Fail "не смог спросить теги Coddy (сеть?)" }
    $names = $tags | ForEach-Object { ($_ -split 'refs/tags/')[-1] } |
        Where-Object { $_ -match '^\d+\.\d+\.\d+$' }
    if (-not $names) { Fail "у Coddy не нашлось ни одной метки версии" }
    return ($names | Sort-Object { [version]$_ })[-1]
}

function RemoteHead($repo) {
    $head = & git ls-remote $repo main 2>$null
    if ($LASTEXITCODE -ne 0) { Fail "не смог спросить слой (сеть?)" }
    if (-not $head) { return "" }
    return (($head | Select-Object -First 1) -split "`t")[0]
}

$CoddyNew = NewestTag $CoddyRepo
$LayerNew = RemoteHead $LayerRepo

# Пустой репозиторий слоя — не ошибка сети, а другая новость, и звучать она должна
# иначе: человеку нужно знать, что обновляться пока неоткуда, а не что он ошибся.
if (-not $LayerNew) {
    Say ""
    Say "в репозитории слоя ($LayerRepo) нет ветки main — он пуст или ещё не опубликован."
    Say "Обновляться неоткуда. Если слой лежит в другом репозитории, укажите его:"
    Say "  update.ps1 -Check -LayerRepo <адрес>"
    exit 5
}

Say "coddy: последняя $CoddyNew"
Say "слой:  последний коммит $($LayerNew.Substring(0,8))"

# Слой прибит к одной версии Coddy: сверка хешей в apply.ps1 всё равно откажет.
if ($CoddyNew -ne $BaseTag) {
    Say ""
    Say "Coddy выпустила $CoddyNew, а этот слой собран под $BaseTag."
    Say "Слой на чужую версию не ложится — накатчик откажет по сверке хешей."
    Say "Нужен новый слой: дождитесь выпуска или соберите его из репозитория шлюза."
    exit 3
}

# --- скачать ---------------------------------------------------------------
if (-not (Test-Path $Dir)) { New-Item -ItemType Directory -Path $Dir -Force | Out-Null }
$LayerDir = Join-Path $Dir "layer"
$CoddyDir = Join-Path $Dir "coddy"

if (Test-Path (Join-Path $LayerDir ".git")) {
    Say "обновляю клон слоя в $LayerDir"
    # Адрес мог измениться, а клон остался от прежнего репозитория.
    & git -C $LayerDir remote set-url origin $LayerRepo 2>$null
    & git -C $LayerDir fetch --quiet origin
    & git -C $LayerDir reset --hard --quiet $LayerNew
} else {
    Say "клонирую слой в $LayerDir"
    & git clone --quiet $LayerRepo $LayerDir
    & git -C $LayerDir reset --hard --quiet $LayerNew
}
if ($LASTEXITCODE -ne 0) { Fail "не удалось получить слой" }

$RemoteIdPath = Join-Path $LayerDir $IdFile
if (-not (Test-Path $RemoteIdPath) -and $IdFile -eq "RELEASE") {
    # Опубликованный слой старше той схемы, где появился RELEASE.
    Say "в опубликованном слое нет RELEASE — сверяюсь по MANIFEST.tsv"
    $IdFile = "MANIFEST.tsv"
    $RemoteIdPath = Join-Path $LayerDir $IdFile
    $LocalId = Get-ContentHash @(Get-Content (Join-Path $Here $IdFile))
}
if (-not (Test-Path $RemoteIdPath)) { Fail "в опубликованном слое нет ни RELEASE, ни MANIFEST.tsv" }
$RemoteId = Get-ContentHash @(Get-Content $RemoteIdPath)
if (-not $RemoteId) { Fail "не смог посчитать хеш $IdFile слоя" }

if ($RemoteId -eq $LocalId) {
    Say ""
    Say "обновлять нечего: у вас последний слой (выпуск $($LocalId.Substring(0,8)), $IdFile)."
    exit 0
}

# Манифесты разошлись. Значит либо мы отстали, либо наша копия из другой линии и
# обновление откатило бы её. Решает история: ищем наш манифест среди прошлых
# версий опубликованного слоя.
$found = $false
$history = & git -C $LayerDir log --format=%H $LayerNew -- $IdFile 2>$null
foreach ($commit in @($history | Select-Object -First 200)) {
    if (-not $commit) { continue }
    $lines = & git -C $LayerDir show "${commit}:$IdFile" 2>$null
    if ($LASTEXITCODE -ne 0) { continue }
    if ((Get-ContentHash @($lines)) -eq $LocalId) { $found = $true; break }
}

if (-not $found) {
    Say ""
    Say "вашего выпуска ($IdFile $($LocalId.Substring(0,8))) нет в истории опубликованного."
    Say "Значит копия из другой линии или новее опубликованной — обновление откатило бы её."
    Say "Обновитесь вручную. Если публикация отстала, выложите свою копию в репозиторий слоя."
    exit 3
}

$NewVersion = Join-Path $LayerDir "VERSION"
$NewBase = ""
if (Test-Path $NewVersion) {
    foreach ($line in Get-Content $NewVersion) {
        if ($line -match '^BASE_TAG=(.*)$') { $NewBase = $Matches[1].Trim() }
    }
}
if ($NewBase -ne $CoddyNew) {
    Say ""
    Say "свежий слой собран под Coddy $NewBase, а последняя — $CoddyNew."
    Say "Возьмите слой, собранный под $CoddyNew."
    exit 3
}

Say ""
Say "есть новый слой: ваш выпуск $($LocalId.Substring(0,8)), опубликован $($RemoteId.Substring(0,8)) ($IdFile)"
if ($Mode -eq "check") {
    Say "поставить и собрать:  update.ps1 -Apply"
    Say "поставить, собрать и перезапустить:  update.ps1 -Install"
    exit 0
}

if (Test-Path (Join-Path $CoddyDir ".git")) {
    Say "обновляю дерево Coddy в $CoddyDir до $BaseTag"
    & git -C $CoddyDir remote set-url origin $CoddyRepo 2>$null
    & git -C $CoddyDir fetch --quiet --tags origin
    & git -C $CoddyDir checkout --quiet $BaseTag
    & git -C $CoddyDir reset --hard --quiet $BaseTag
} else {
    Say "клонирую Coddy $BaseTag в $CoddyDir"
    & git clone --quiet --branch $BaseTag --depth 1 $CoddyRepo $CoddyDir
}
if ($LASTEXITCODE -ne 0) { Fail "не удалось получить дерево Coddy $BaseTag" }

# --- накатить и собрать ----------------------------------------------------
Say "накатываю слой и собираю"
$applyPs1 = Join-Path $LayerDir "apply.ps1"
if (-not (Test-Path $applyPs1)) { Fail "в слое нет apply.ps1" }
& powershell -NoProfile -ExecutionPolicy Bypass -File $applyPs1 -Coddy $CoddyDir -Build -Tags $Tags
if ($LASTEXITCODE -ne 0) { Fail "накат или сборка не удались (код $LASTEXITCODE)" }

$Binary = Join-Path $CoddyDir "build\coddy.exe"
if (-not (Test-Path $Binary)) { Fail "сборка не оставила бинарник $Binary" }

if ($Mode -eq "apply") {
    Say ""
    Say "готово: $Binary"
    Say "поставить и перезапустить:  update.ps1 -Install"
    exit 0
}

# --- поставить и перезапустить --------------------------------------------
$Target = Join-Path $BinDir "coddy.exe"
if (-not (Test-Path $BinDir)) { New-Item -ItemType Directory -Path $BinDir -Force | Out-Null }
if (Test-Path $Target) {
    $stamp = Get-Date -Format "yyyyMMdd-HHmmss"
    $oldVersion = (& $Target -v 2>$null | Select-Object -Last 1)
    if (-not $oldVersion) { $oldVersion = "unknown" }
    Copy-Item $Target "$Target.$oldVersion.bak-$stamp"
    Say "прежний бинарник сохранён рядом с меткой .bak-$stamp"
}
Copy-Item $Binary $Target -Force
Say "поставлено: $Target ($(& $Target -v 2>$null | Select-Object -Last 1))"

if ($StartScript) {
    if (-not (Test-Path $StartScript)) { Fail "скрипта перезапуска нет: $StartScript" }
    Say "перезапуск через $StartScript"
    & $StartScript
} else {
    Say ""
    Say "скрипт перезапуска не указан (-StartScript)."
    Say "Остановите работающий coddy.exe и запустите новый сами — бинарник уже на месте."
    exit 4
}
