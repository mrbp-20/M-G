#!/usr/bin/env bash
# Обновление шлюза MAX: проверить новинки в двух репозиториях, скачать, собрать,
# перезапустить. Тот же порядок, каким это делается руками, только одной командой.
#
#   update.sh --check     только сказать, что нового (ничего не ставит)
#   update.sh --apply     скачать и собрать, службу не трогать
#   update.sh --install   скачать, собрать, поставить бинарник и перезапустить
#
# Ключи: --dir <каталог>   где держать клоны (по умолчанию ~/.cache/max-gate)
#        --tags "<теги>"   теги сборки, по умолчанию как в пакете
#        --bin-dir <кат>   куда ставить бинарник (по умолчанию ~/.local/bin)
#        --start <скрипт>  чем перезапускать (по умолчанию start_coddy.sh рядом)
#
# Две вещи, которые скрипт НЕ делает молча и это его главное свойство:
#   1. он не накатывает слой на чужую версию Coddy — слой прибит хешами к одной;
#   2. он не перезапускает службу под идущим разговором без ключа --install.
#
# Версия установленного слоя опознаётся по содержимому: по sha256 файла
# MANIFEST.tsv, а не по хешу коммита. Хеш коммита не может лежать в самом
# коммите, поэтому «впишем свой коммит в VERSION» невыполнимо; манифест же
# описывает файлы слоя и от VERSION не зависит. Скачали пакет, сверили манифесты:
# совпали — у вас последний слой (в том числе при первом запуске, вместо отказа);
# разошлись — смотрим, встречался ли ваш манифест в истории опубликованного
# слоя: встречался — вы отстали, обновляемся; нет — вы из другой линии или
# новее опубликованного, и обновление откатило бы вас, поэтому отказ.
set -euo pipefail

CODDY_REPO="${MAXGW_CODDY_REPO:-https://github.com/coddy-project/coddy-agent}"
LAYER_REPO="${MAXGW_LAYER_REPO:-https://github.com/mrbp-20/M-G}"

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DIR="${MAXGW_WORK_DIR:-$HOME/.cache/max-gate}"
TAGS="${MAXGW_TAGS:-http,scheduler,memory,cli,gateway.max,swarm}"
BIN_DIR="${MAXGW_BIN_DIR:-$HOME/.local/bin}"
START_SCRIPT="${MAXGW_START_SCRIPT:-$HOME/start_coddy.sh}"
MODE=check

say() { printf '%s\n' "$*"; }
fail() { printf 'ошибка: %s\n' "$*" >&2; exit 2; }
sha() { if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi; }
sha_of_stdin() { if command -v sha256sum >/dev/null 2>&1; then sha256sum | cut -d' ' -f1; else shasum -a 256 | cut -d' ' -f1; fi; }

# Хеш содержимого без учёта переводов строки. На машине с core.autocrlf=true
# git отдаёт файл с CRLF, а из истории — с LF, и сырые хеши одного и того же
# файла расходятся: сверка объявляла бы чужую линию на ровном месте.
canon() { tr -d '\r' <"$1" | sha_of_stdin; }
canon_history() { git -C "$LAYER_DIR" show "$1:$2" 2>/dev/null | tr -d '\r' | sha_of_stdin; }

while [ $# -gt 0 ]; do
	case "$1" in
	--check) MODE=check ;;
	--apply) MODE=apply ;;
	--install) MODE=install ;;
	--dir) DIR="${2:?--dir требует каталог}"; shift ;;
	--tags) TAGS="${2:?--tags требует строку}"; shift ;;
	--bin-dir) BIN_DIR="${2:?--bin-dir требует каталог}"; shift ;;
	--start) START_SCRIPT="${2:?--start требует путь}"; shift ;;
	--help | -h)
		sed -n '2,19p' "${BASH_SOURCE[0]}" | sed 's/^# \{0,1\}//'
		exit 0
		;;
	*) fail "неизвестный ключ: $1 (--help покажет, что есть)" ;;
	esac
	shift
done

command -v git >/dev/null 2>&1 || fail "git не найден в PATH"
command -v go >/dev/null 2>&1 || fail "go не найден в PATH (нужен Go 1.27.3 или новее)"

# --- что у нас есть сейчас -------------------------------------------------
[ -f "$HERE/VERSION" ] || fail "рядом со скриптом нет VERSION — это не пакет слоя"
[ -f "$HERE/MANIFEST.tsv" ] || fail "рядом со скриптом нет MANIFEST.tsv — это не пакет слоя"
# RELEASE описывает весь выпуск пакета (и корневые скрипты тоже), MANIFEST.tsv —
# только совместимость с версией Coddy. Основное опознание — по RELEASE; если в
# опубликованном слое его ещё нет, сверяемся по манифесту, как раньше.
ID_FILE="MANIFEST.tsv"
[ -f "$HERE/RELEASE" ] && ID_FILE="RELEASE"
BASE_TAG="$(sed -n 's/^BASE_TAG=//p' "$HERE/VERSION" | tr -d '\r')"
LAYER_SOURCE="$(sed -n 's/^LAYER_SOURCE_COMMIT=//p' "$HERE/VERSION" | tr -d '\r')"
[ -n "$BASE_TAG" ] || fail "в VERSION нет BASE_TAG"
LOCAL_ID="$(canon "$HERE/$ID_FILE")"

say "слой: собран под Coddy $BASE_TAG${LAYER_SOURCE:+, из ${LAYER_SOURCE:0:8}}, выпуск ${LOCAL_ID:0:8} ($ID_FILE)"

# --- что есть у них --------------------------------------------------------
newest_tag() {
	git ls-remote --tags --refs "$1" 2>/dev/null |
		sed 's#.*refs/tags/##' |
		grep -E '^[0-9]+\.[0-9]+\.[0-9]+$' |
		sort -V | tail -1
}

remote_head() {
	git ls-remote "$1" "${2:-main}" 2>/dev/null | head -1 | cut -f1
}

CODDY_NEW="$(newest_tag "$CODDY_REPO")" || fail "не смог спросить теги Coddy (сеть?)"
[ -n "$CODDY_NEW" ] || fail "у Coddy не нашлось ни одной метки версии"

# Пустой репозиторий слоя — не ошибка сети, а другая новость, и звучать она должна
# иначе: человеку нужно знать, что обновляться пока неоткуда, а не что он ошибся.
LAYER_NEW="$(remote_head "$LAYER_REPO" main)" || true
if [ -z "$LAYER_NEW" ]; then
	say ""
	say "в репозитории слоя ($LAYER_REPO) нет ветки main — он пуст или ещё не опубликован."
	say "Обновляться неоткуда. Если слой лежит в другом репозитории, укажите его:"
	say "  MAXGW_LAYER_REPO=<адрес> update.sh --check"
	exit 5
fi

say "coddy: последняя $CODDY_NEW"
say "слой:  последний коммит ${LAYER_NEW:0:8}"

# Слой прибит к одной версии Coddy: сверка хешей в apply.sh всё равно откажет.
# Сказать об этом прямо здесь честнее, чем падать на середине наката.
if [ "$CODDY_NEW" != "$BASE_TAG" ]; then
	say ""
	say "Coddy выпустила $CODDY_NEW, а этот слой собран под $BASE_TAG."
	say "Слой на чужую версию не ложится — накатчик откажет по сверке хешей."
	say "Нужен новый слой: дождитесь выпуска или соберите его из репозитория шлюза."
	exit 3
fi

# --- скачать ---------------------------------------------------------------
mkdir -p "$DIR"
LAYER_DIR="$DIR/layer"
CODDY_DIR="$DIR/coddy"

if [ -d "$LAYER_DIR/.git" ]; then
	say "обновляю клон слоя в $LAYER_DIR"
	# Адрес мог измениться (MAXGW_LAYER_REPO или переезд репозитория), а клон
	# остался от прежнего: без этой строки fetch тянет старый и коммит не находится.
	git -C "$LAYER_DIR" remote set-url origin "$LAYER_REPO" 2>/dev/null || true
	git -C "$LAYER_DIR" fetch --quiet origin
	git -C "$LAYER_DIR" reset --hard --quiet "$LAYER_NEW"
else
	say "клонирую слой в $LAYER_DIR"
	git clone --quiet "$LAYER_REPO" "$LAYER_DIR"
	git -C "$LAYER_DIR" reset --hard --quiet "$LAYER_NEW"
fi

if [ ! -f "$LAYER_DIR/$ID_FILE" ]; then
	if [ "$ID_FILE" = "RELEASE" ]; then
		# Опубликованный слой старше той схемы, где появился RELEASE.
		say "в опубликованном слое нет RELEASE — сверяюсь по MANIFEST.tsv"
		ID_FILE="MANIFEST.tsv"
		LOCAL_ID="$(canon "$HERE/$ID_FILE")"
	fi
	[ -f "$LAYER_DIR/$ID_FILE" ] || fail "в опубликованном слое нет ни RELEASE, ни MANIFEST.tsv"
fi
REMOTE_ID="$(canon "$LAYER_DIR/$ID_FILE")"

if [ "$REMOTE_ID" = "$LOCAL_ID" ]; then
	say ""
	say "обновлять нечего: у вас последний слой (выпуск ${LOCAL_ID:0:8}, $ID_FILE)."
	exit 0
fi

# Манифесты разошлись. Значит либо мы отстали, либо наша копия из другой линии и
# обновление откатило бы её. Решает история: ищем наш манифест среди прошлых
# версий опубликованного слоя. Нашли — мы просто отстали.
found=0
while IFS= read -r commit; do
	[ -n "$commit" ] || continue
	if [ "$(canon_history "$commit" "$ID_FILE")" = "$LOCAL_ID" ]; then
		found=1
		break
	fi
done < <(git -C "$LAYER_DIR" log --format=%H "$LAYER_NEW" -- "$ID_FILE" 2>/dev/null | head -200)

if [ "$found" -eq 0 ]; then
	say ""
	say "вашего выпуска (${ID_FILE} ${LOCAL_ID:0:8}) нет в истории опубликованного."
	say "Значит копия из другой линии или новее опубликованной — обновление откатило бы её."
	say "Обновитесь вручную. Если публикация отстала, выложите свою копию в репозиторий слоя."
	exit 3
fi

# Клон мог приехать с другой базой (слой выпустили под новую Coddy одновременно
# с нами) — тогда сверяем заново, а не верим своему VERSION.
NEW_BASE="$(sed -n 's/^BASE_TAG=//p' "$LAYER_DIR/VERSION" 2>/dev/null | tr -d '\r' || true)"
if [ "$NEW_BASE" != "$CODDY_NEW" ]; then
	say ""
	say "свежий слой собран под Coddy $NEW_BASE, а последняя — $CODDY_NEW."
	say "Возьмите слой, собранный под $CODDY_NEW."
	exit 3
fi

say ""
say "есть новый слой: ваш выпуск ${LOCAL_ID:0:8}, опубликован ${REMOTE_ID:0:8} ($ID_FILE)"
if [ "$MODE" = check ]; then
	say "поставить и собрать:  update.sh --apply"
	say "поставить, собрать и перезапустить:  update.sh --install"
	exit 0
fi

if [ -d "$CODDY_DIR/.git" ]; then
	say "обновляю дерево Coddy в $CODDY_DIR до $BASE_TAG"
	git -C "$CODDY_DIR" remote set-url origin "$CODDY_REPO" 2>/dev/null || true
	git -C "$CODDY_DIR" fetch --quiet --tags origin
	git -C "$CODDY_DIR" checkout --quiet "$BASE_TAG"
	git -C "$CODDY_DIR" reset --hard --quiet "$BASE_TAG"
else
	say "клонирую Coddy $BASE_TAG в $CODDY_DIR"
	git clone --quiet --branch "$BASE_TAG" --depth 1 "$CODDY_REPO" "$CODDY_DIR"
fi

# --- накатить и собрать ----------------------------------------------------
say "накатываю слой и собираю"
( cd "$LAYER_DIR" && bash apply.sh --coddy "$CODDY_DIR" --build --tags "$TAGS" ) | tail -6

BINARY="$CODDY_DIR/build/coddy"
[ -f "$BINARY" ] || fail "сборка не оставила бинарник $BINARY"

if [ "$MODE" = apply ]; then
	say ""
	say "готово: $BINARY"
	say "поставить и перезапустить:  update.sh --install"
	exit 0
fi

# --- поставить и перезапустить --------------------------------------------
mkdir -p "$BIN_DIR"
STAMP="$(date +%Y%m%d-%H%M%S)"
if [ -f "$BIN_DIR/coddy" ] && ! cmp -s "$BINARY" "$BIN_DIR/coddy"; then
	cp -p "$BIN_DIR/coddy" "$BIN_DIR/coddy.$("$BIN_DIR/coddy" -v 2>/dev/null | tail -1).bak-$STAMP" 2>/dev/null || true
	say "прежний бинарник сохранён рядом с меткой .bak-$STAMP"
fi
install -m 0755 "$BINARY" "$BIN_DIR/coddy"
say "поставлено: $BIN_DIR/coddy ($("$BIN_DIR/coddy" -v 2>/dev/null | tail -1))"

if [ -x "$START_SCRIPT" ] || [ -f "$START_SCRIPT" ]; then
	say "перезапуск через $START_SCRIPT"
	bash "$START_SCRIPT"
else
	say ""
	say "скрипта перезапуска нет: $START_SCRIPT"
	say "остановите и поднимите службу сами — бинарник уже на месте."
	exit 4
fi
