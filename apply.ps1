<#
.SYNOPSIS
  Накладывает слой MAX на клон Coddy и собирает шлюз (Windows, PowerShell).

.DESCRIPTION
  Схема та же, что у apply.sh: клиент берёт Coddy из его собственного репозитория
  на той версии, под которую сделан слой (см. VERSION), заменяет файлы из
  overlay/ и собирает бинарь с тегом шлюза. Сначала сверяются исходные файлы по
  sha256: на другой версии Coddy они разойдутся, и наложение молча сломало бы
  сборку — об этом лучше узнать до, а не после.

.EXAMPLE
  .\apply.ps1 -Clone C:\coddy-src
  Склонировать Coddy нужной версии и наложить слой.

.EXAMPLE
  .\apply.ps1 -Coddy C:\coddy-src -Check
  Только проверить совместимость, ничего не записывая.

.EXAMPLE
  .\apply.ps1 -Coddy C:\coddy-src -Build
  Наложить слой и собрать бинарь.
#>
[CmdletBinding()]
param(
    [string]$Coddy = "",
    [string]$Clone = "",
    [switch]$Check,
    [switch]$Build,
    [string]$Tags = "http,scheduler,memory,cli,gateway.max,swarm",
    [switch]$Help
)

$ErrorActionPreference = "Stop"

# Windows PowerShell 5.1 по умолчанию пишет кириллицу в кодировке консоли, и при
# перенаправлении вывода она превращается в мусор. UTF-8 делает вывод читаемым и в
# консоли, и в файле, и при вызове из другого скрипта.
try { [Console]::OutputEncoding = [System.Text.Encoding]::UTF8 } catch { }
$OutputEncoding = [System.Text.Encoding]::UTF8

if ($Help) {
    Get-Help $MyInvocation.MyCommand.Path -Detailed
    exit 0
}

$here = Split-Path -Parent $MyInvocation.MyCommand.Path
$manifestPath = Join-Path $here "MANIFEST.tsv"
$overlayPath = Join-Path $here "overlay"
$versionPath = Join-Path $here "VERSION"

function Say([string]$text) { Write-Host $text }
function Fail([string]$text) {
    [Console]::Error.WriteLine("ошибка: $text")
    exit 1
}

# --- версия базы из VERSION -------------------------------------------------
if (-not (Test-Path $versionPath)) { Fail "нет $versionPath" }
$version = @{}
foreach ($line in Get-Content -LiteralPath $versionPath) {
    $trimmed = $line.Trim()
    if ($trimmed -eq "" -or $trimmed.StartsWith("#")) { continue }
    $parts = $trimmed.Split("=", 2)
    if ($parts.Count -eq 2) { $version[$parts[0].Trim()] = $parts[1].Trim() }
}
$baseTag = if ($version.ContainsKey("BASE_TAG")) { $version["BASE_TAG"] } else { "1.3.60" }
$baseCommit = if ($version.ContainsKey("BASE_COMMIT")) { $version["BASE_COMMIT"] } else { "unknown" }
$layerCommit = if ($version.ContainsKey("LAYER_SOURCE_COMMIT") -and $version["LAYER_SOURCE_COMMIT"]) { $version["LAYER_SOURCE_COMMIT"] } elseif ($version.ContainsKey("LAYER_COMMIT")) { $version["LAYER_COMMIT"] } else { "unknown" }
$upstreamUrl = if ($env:UPSTREAM_URL) { $env:UPSTREAM_URL } else { "https://github.com/coddy-project/coddy-agent.git" }

if (-not (Test-Path $manifestPath)) { Fail "нет $manifestPath" }
if (-not (Test-Path $overlayPath)) { Fail "нет каталога $overlayPath" }

# --- клон нужной версии Coddy ----------------------------------------------
# Каталог из -Clone мог остаться от прошлого запуска: первый раз сборка оборвалась
# (например, не было Go), файлы уже наложены. Отказывать в этом случае
# бессмысленно — надо проверить, тот ли это Coddy, и продолжить с него.
function Get-CloneState([string]$dir) {
    if (-not (Test-Path $dir)) { return "missing" }
    if (-not (Test-Path (Join-Path $dir "go.mod"))) { return "not-coddy" }
    if (-not (Test-Path (Join-Path $dir ".git"))) { return "no-git" }
    $head = (& git -C $dir rev-parse HEAD 2>$null)
    if ($LASTEXITCODE -ne 0 -or -not $head) { return "no-head" }
    if ("$head".Trim() -eq $baseCommit) { return "ready" }
    return "other-version"
}

if ($Clone -ne "") {
    switch (Get-CloneState $Clone) {
        "missing" {
            Say "клонирую Coddy $baseTag в $Clone"
            & git clone --depth 1 --branch $baseTag $upstreamUrl $Clone
            if ($LASTEXITCODE -ne 0) { Fail "клон не удался (нет сети или нет такой метки)" }
        }
        "ready" {
            Say "клон уже на месте: $Clone (Coddy $baseTag) — продолжаю с ним"
        }
        "no-git" {
            Say "в $Clone нет .git: версию по git не проверить, сверю файлы по хешам манифеста"
        }
        "other-version" {
            $got = (& git -C $Clone describe --tags --always 2>$null)
            if (-not $got) { $got = "неизвестно" }
            Say ""
            Say "ошибка: в $Clone уже лежит Coddy $got, а нужен $baseTag ($($baseCommit.Substring(0,8)))."
            Say ""
            Say "Это дерево другой версии, и накатывать на него слой нельзя. Возьмите нужную:"
            Say "  git -C `"$Clone`" fetch --tags; git -C `"$Clone`" checkout $baseTag"
            Say "или укажите другой путь: -Clone <путь>, либо готовый клон: -Coddy <путь>"
            exit 2
        }
        "no-head" {
            Fail "не смог прочитать версию в $Clone (git rev-parse не ответил). Проверьте: git -C `"$Clone`" describe --tags --always"
        }
        default {
            Fail "$Clone существует, но это не исходники Coddy (нет go.mod). Укажите пустой путь для клона или готовое дерево через -Coddy"
        }
    }
    $Coddy = $Clone
}

if ($Coddy -eq "") { Fail "укажите -Coddy <путь к клону Coddy> или -Clone <новый путь>" }
if (-not (Test-Path $Coddy)) { Fail "нет каталога $Coddy" }
if (-not (Test-Path (Join-Path $Coddy "go.mod"))) {
    Fail "$Coddy не похож на исходники Coddy (нет go.mod)"
}

# --- манифест ---------------------------------------------------------------
$rows = @()
foreach ($line in Get-Content -LiteralPath $manifestPath) {
    if ($line.StartsWith("#") -or $line.Trim() -eq "") { continue }
    $parts = $line.Split("`t")
    if ($parts.Count -lt 4) { continue }
    $rows += [pscustomobject]@{
        Path    = $parts[0]
        Kind    = $parts[1]
        BaseSha = $parts[2]
        OurSha  = $parts[3]
    }
}

function Get-Sha([string]$path) {
    # Хеш без учёта переводов строки: выгрузка с core.autocrlf=true не делает
    # файл другим по смыслу, и отказывать ему в накате из-за CR было бы ошибкой.
    $bytes = [System.IO.File]::ReadAllBytes($path)
    $kept = New-Object System.IO.MemoryStream
    foreach ($b in $bytes) { if ($b -ne 13) { $kept.WriteByte($b) } }
    $sha = [System.Security.Cryptography.SHA256]::Create()
    return (($sha.ComputeHash($kept.ToArray()) | ForEach-Object { $_.ToString("x2") }) -join "")
}

Say "слой: $($rows.Count + 1) строк манифеста (с заголовком), база Coddy $baseTag"
Say "клон: $Coddy"

# --- сверка версии ----------------------------------------------------------
$mismatch = 0
$missing = 0
$broken = 0
$already = 0
$unknown = 0
$unknownList = New-Object System.Collections.Generic.List[string]
$brokenList = New-Object System.Collections.Generic.List[string]
$drift = New-Object System.Collections.Generic.List[string]
foreach ($row in $rows) {
    # Сам слой проверяется до первой записи: иначе испорченный или недокачанный
    # файл обнаружится на середине наката, и дерево Coddy останется наполовину
    # нашим. Это не обещание атомарности при отказе диска — только того, что
    # неполного слоя на диске не будет.
    $source = Join-Path $overlayPath $row.Path
    if (-not (Test-Path $source)) {
        $broken++
        $brokenList.Add("$($row.Path) (нет в слое)")
        continue
    }
    if ((Get-Sha $source) -ne $row.OurSha) {
        $broken++
        if ($broken -le 8) { $brokenList.Add("$($row.Path) (хеш слоя не совпал с манифестом)") }
        continue
    }
    # Файл уже наш — значит прошлый запуск дошёл до него и оборвался позже.
    # Это штатное состояние повторного запуска, а не расхождение версий.
    $targetPath = Join-Path $Coddy $row.Path
    if ((Test-Path $targetPath) -and ((Get-Sha $targetPath) -eq $row.OurSha)) {
        $already++
        continue
    }
    if ($row.Kind -ne "modified") { continue }
    $target = Join-Path $Coddy $row.Path
    if (-not (Test-Path $target)) {
        $missing++
        $drift.Add("$($row.Path) (нет в клоне)")
        continue
    }
    # Содержимое не наше и не исходное. Что это значит, решает не файл, а дерево:
    # если в нём уже есть наши файлы, прошлый запуск состоялся, и такой файл просто
    # испорчен после него — восстановим. Если наших файлов нет вовсе, это чужое
    # дерево другой версии, и накатывать нельзя.
    if ((Get-Sha $target) -ne $row.BaseSha) {
        $unknown++
        if ($unknown -le 8) { $unknownList.Add($row.Path) }
    }
}

if ($unknown -gt 0 -and $already -eq 0) {
    $mismatch = $unknown
    foreach ($item in $unknownList) { $drift.Add($item) }
}

if ($broken -gt 0) {
    Say ""
    Say "СЛОЙ ПОВРЕЖДЁН"
    Say "  файлов слоя не на месте или с чужим хешем: $broken из $($rows.Count)"
    foreach ($item in $brokenList) { Say "    $item" }
    Say ""
    Say "Ничего не изменено. Скачайте пакет заново: файлы слоя и MANIFEST.tsv"
    Say "должны быть из одной выкладки."
    exit 2
}

if ($missing -gt 0 -or $mismatch -gt 0) {
    Say ""
    Say "СЛОЙ НЕ ПОДХОДИТ К ЭТОЙ ВЕРСИИ CODDY"
    Say "  изменённых файлов не совпало: $mismatch из $($rows.Count), отсутствует: $missing"
    foreach ($item in $drift) { Say "    $item" }
    Say ""
    Say "Нужен Coddy $baseTag (коммит $baseCommit)."
    Say "Проверьте версию:  git -C `"$Coddy`" describe --tags --always"
    Say "Совместимое дерево:  .\apply.ps1 -Clone C:\coddy-src"
    exit 2
}

if ($already -eq $rows.Count) {
    Say "сверка: все $($rows.Count) файлов слоя уже на месте — прошлый запуск дошёл до конца"
} elseif ($unknown -gt 0 -and $already -gt 0) {
    Say "сверка: $($rows.Count) файлов слоя совместимы, $already уже наложено, $unknown испорчено — восстановлю"
} elseif ($already -gt 0) {
    Say "сверка: $($rows.Count) файлов слоя совместимы с этим клоном, из них уже наложено $already"
} else {
    Say "сверка: все $($rows.Count) файлов слоя совместимы с этим клоном"
}

if ($Check) {
    if ($already -eq $rows.Count) {
        Say "проверка пройдена: слой уже наложен полностью (-Check: ничего не изменено)"
    } else {
        Say "проверка пройдена: осталось наложить $($rows.Count - $already) из $($rows.Count)"
    }
    exit 0
}

# --- наложение --------------------------------------------------------------
$copied = 0
foreach ($row in $rows) {
    # В overlay путь хранится с прямыми слэшами, как в Coddy: Join-Path это принимает.
    $source = Join-Path $overlayPath $row.Path
    if (-not (Test-Path $source)) { Fail "в слое нет файла $($row.Path)" }
    $target = Join-Path $Coddy $row.Path
    if ((Test-Path $target) -and ((Get-Sha $target) -eq $row.OurSha)) { continue }
    $dir = Split-Path -Parent $target
    if (-not (Test-Path $dir)) { New-Item -ItemType Directory -Path $dir -Force | Out-Null }
    Copy-Item -LiteralPath $source -Destination $target -Force
    if ((Get-Sha $target) -ne $row.OurSha) { Fail "после копирования $($row.Path) не совпал по sha256" }
    $copied++
}

Say "наложено файлов: $copied"
if ($already -gt 0) {
    Say "уже были на месте (прошлый запуск): $already"
}
if ($unknown -gt 0 -and $already -gt 0) {
    Say "восстановлено после обрыва: $unknown"
}

if (-not $Build) {
    Say ""
    Say "дальше:"
    Say "  .\apply.ps1 -Coddy `"$Coddy`" -Build   # или собрать вручную:"
    Say "  go build -tags `"$Tags`" -o build\coddy.exe .\cmd\coddy\"
    exit 0
}

# --- сборка ----------------------------------------------------------------
if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    Fail ("go не найден в PATH (нужен Go 1.26 или новее; набор инструментов " + "go1.27.3 проект подтянет сам)")
}

$ver = "$baseTag-max-layer"
$ldflags = "-X github.com/EvilFreelancer/coddy-agent/internal/version.Version=$ver"
$ldflags = "$ldflags -X github.com/EvilFreelancer/coddy-agent/internal/version.Revision=$layerCommit"
Say "собираю: go build -tags `"$Tags`" -ldflags `"...`" -o build\coddy.exe .\cmd\coddy\"

Push-Location $Coddy
try {
    if (-not (Test-Path "build")) { New-Item -ItemType Directory -Path "build" | Out-Null }
    & go build -tags $Tags -ldflags $ldflags -o "build/coddy.exe" "./cmd/coddy/"
    if ($LASTEXITCODE -ne 0) { Fail "сборка не прошла" }
} finally {
    Pop-Location
}

Say "готово: $Coddy\build\coddy.exe"
& (Join-Path $Coddy "build\coddy.exe") -v
