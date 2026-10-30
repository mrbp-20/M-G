#!/usr/bin/env bash
# Накладывает слой MAX на клон Coddy и собирает шлюз.
#
# Схема: клиент берёт Coddy из его собственного репозитория на той версии, под
# которую сделан слой (см. VERSION), заменяет файлы из overlay/ и собирает бинарь
# с тегом шлюза. Скрипт сначала сверяет исходные файлы по sha256: если версия
# Coddy другая, файлы разойдутся и наложение молча сломает сборку — об этом лучше
# узнать до, а не после.
#
# Примеры:
#   ./apply.sh --clone ~/coddy-src          # склонировать Coddy нужной версии и наложить слой
#   ./apply.sh --coddy ~/coddy-src --check  # только проверить совместимость, ничего не писать
#   ./apply.sh --coddy ~/coddy-src          # наложить слой
#   ./apply.sh --coddy ~/coddy-src --build  # наложить и собрать бинарь
set -u

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
MANIFEST="$HERE/MANIFEST.tsv"
OVERLAY="$HERE/overlay"
VERSION_FILE="$HERE/VERSION"

# shellcheck disable=SC1090
[ -f "$VERSION_FILE" ] && . "$VERSION_FILE"

BASE_TAG="${BASE_TAG:-1.3.48}"
BASE_COMMIT="${BASE_COMMIT:-64dfce52a23c628adeadd89c2fbdd7c535caa995}"
UPSTREAM_URL="${UPSTREAM_URL:-https://github.com/coddy-project/coddy-agent.git}"
TAGS="${TAGS:-http,scheduler,memory,cli,gateway.max,swarm}"
CHECK_ONLY=0
DO_BUILD=0
CODDY=""
CLONE=""

say()  { printf '%s\n' "$*"; }
fail() { printf 'ошибка: %s\n' "$*" >&2; exit 1; }

while [ $# -gt 0 ]; do
	case "${1}" in
	--coddy)  CODDY="${2:-}"; shift 2 ;;
	--clone)  CLONE="${2:-}"; shift 2 ;;
	--check)  CHECK_ONLY=1; shift ;;
	--build)  DO_BUILD=1; shift ;;
	--tags)   TAGS="${2:-}"; shift 2 ;;
	-h|--help) sed -n '2,20p' "${BASH_SOURCE[0]}"; exit 0 ;;
	*) fail "неизвестный ключ: ${1} (см. --help)" ;;
	esac
done

[ -f "$MANIFEST" ] || fail "нет $MANIFEST"
[ -d "$OVERLAY" ] || fail "нет каталога $OVERLAY"

# Каталог из --clone мог остаться от прошлого запуска: первый раз сборка
# оборвалась (например, не было Go), файлы уже наложены. Отказывать в этом
# случае бессмысленно — надо проверить, тот ли это Coddy, и продолжить с него.
clone_state() {
	local dir="$1"
	[ -e "$dir" ] || { echo missing; return; }
	[ -f "$dir/go.mod" ] || { echo not-coddy; return; }
	[ -d "$dir/.git" ] || { echo no-git; return; }
	local head
	head="$(git -C "$dir" rev-parse HEAD 2>/dev/null || true)"
	if [ -z "$head" ]; then
		echo no-head
		return
	fi
	if [ "$head" = "$BASE_COMMIT" ]; then
		echo ready
	else
		echo other-version
	fi
}

if [ -n "$CLONE" ]; then
	case "$(clone_state "$CLONE")" in
	missing)
		say "клонирую Coddy ${BASE_TAG} в $CLONE"
		GIT_TERMINAL_PROMPT=0 git clone --depth 1 --branch "$BASE_TAG" "$UPSTREAM_URL" "$CLONE" \
			|| fail "клон не удался (нет сети или нет такой метки)"
		;;
	ready)
		say "клон уже на месте: $CLONE (Coddy ${BASE_TAG}) — продолжаю с ним"
		;;
	no-git)
		say "в $CLONE нет .git: версию по git не проверить, сверю файлы по хешам манифеста"
		;;
	other-version)
		got="$(git -C "$CLONE" describe --tags --always 2>/dev/null || echo неизвестно)"
		say "ошибка: в $CLONE уже лежит Coddy $got, а нужен ${BASE_TAG} (${BASE_COMMIT:0:8})." >&2
		say "" >&2
		say "Это дерево другой версии, и накатывать на него слой нельзя. Возьмите нужную:" >&2
		say "  git -C \"$CLONE\" fetch --tags && git -C \"$CLONE\" checkout ${BASE_TAG}" >&2
		say "или укажите другой путь: --clone <путь>, либо готовый клон: --coddy <путь>" >&2
		exit 2
		;;
	no-head)
		say "ошибка: не смог прочитать версию в $CLONE (git rev-parse не ответил)." >&2
		say "Проверьте клон сами: git -C \"$CLONE\" describe --tags --always" >&2
		exit 2
		;;
	*)
		say "ошибка: $CLONE существует, но это не исходники Coddy (нет go.mod)." >&2
		say "Укажите пустой путь для клона или готовое дерево через --coddy" >&2
		exit 2
		;;
	esac
	CODDY="$CLONE"
fi

[ -n "$CODDY" ] || fail "укажите --coddy <путь к клону Coddy> или --clone <новый путь>"
[ -d "$CODDY" ] || fail "нет каталога $CODDY"
[ -f "$CODDY/go.mod" ] || fail "$CODDY не похож на исходники Coddy (нет go.mod)"

# Хеш содержимого без учёта переводов строки: файл, прошедший через выгрузку с
# core.autocrlf=true, остаётся тем же файлом по смыслу, и отказывать ему в накате
# только из-за CR было бы ошибкой.
sha() { if command -v sha256sum >/dev/null 2>&1; then tr -d '\r' <"$1" | sha256sum | cut -d' ' -f1; else tr -d '\r' <"$1" | shasum -a 256 | cut -d' ' -f1; fi; }

say "слой: $(wc -l <"$MANIFEST" | tr -d ' ') строк манифеста (с заголовком), база Coddy ${BASE_TAG}"
say "клон: $CODDY"

mismatch=0
missing=0
broken=0
already=0
unknown=0
unknown_list=""
drift_list=""
broken_list=""
total=0
while IFS=$'\t' read -r path kind base_sha our_sha; do
	case "$path" in \#*|"") continue ;; esac
	total=$((total + 1))
	target="$CODDY/$path"
	src="$OVERLAY/$path"
	# Сам слой проверяется до первой записи: иначе испорченный или недокачанный
	# файл обнаружится на середине наката, и дерево Coddy останется наполовину
	# нашим. Эта проверка не обещает атомарности при отказе диска — только что
	# неполного слоя на диске не будет.
	if [ ! -f "$src" ]; then
		broken=$((broken + 1))
		broken_list="${broken_list}${path} (нет в слое)\n"
		continue
	fi
	if [ "$(sha "$src")" != "$our_sha" ]; then
		broken=$((broken + 1))
		[ "$broken" -le 8 ] && broken_list="${broken_list}${path} (хеш слоя не совпал с манифестом)\n"
		continue
	fi
	# Файл уже наш — значит прошлый запуск дошёл до него и оборвался позже.
	# Это штатное состояние повторного запуска, а не расхождение версий.
	if [ -f "$target" ] && [ "$(sha "$target")" = "$our_sha" ]; then
		already=$((already + 1))
		continue
	fi
	if [ "$kind" = "modified" ]; then
		if [ ! -f "$target" ]; then
			missing=$((missing + 1))
			drift_list="${drift_list}${path} (нет в клоне)\n"
			continue
		fi
		# Содержимое не наше и не исходное. Что это значит — решает не файл, а
		# дерево: если в нём уже есть наши файлы, прошлый запуск состоялся, и такой
		# файл просто испорчен после него; восстановим. Если наших файлов нет
		# вовсе — это чужое дерево другой версии, и накатывать нельзя.
		if [ "$(sha "$target")" != "$base_sha" ]; then
			unknown=$((unknown + 1))
			[ "$unknown" -le 8 ] && unknown_list="${unknown_list}${path}\n"
		fi
	fi
done < "$MANIFEST"

if [ "$unknown" -gt 0 ] && [ "$already" -eq 0 ]; then
	mismatch=$unknown
	drift_list="$unknown_list"
fi

if [ "$broken" -gt 0 ]; then
	say ""
	say "СЛОЙ ПОВРЕЖДЁН"
	say "  файлов слоя не на месте или с чужим хешем: $broken из $total"
	printf '%b' "$broken_list" | sed 's/^/    /'
	say ""
	say "Ничего не изменено. Скачайте пакет заново: файлы слоя и MANIFEST.tsv"
	say "должны быть из одной выкладки."
	exit 2
fi

if [ "$missing" -gt 0 ] || [ "$mismatch" -gt 0 ]; then
	say ""
	say "СЛОЙ НЕ ПОДХОДИТ К ЭТОЙ ВЕРСИИ CODDY"
	say "  изменённых файлов не совпало: $mismatch из $total, отсутствует: $missing"
	printf '%b' "$drift_list" | sed 's/^/    /'
	say ""
	say "Нужен Coddy ${BASE_TAG} (коммит ${BASE_COMMIT})."
	say "Проверьте версию:  git -C \"$CODDY\" describe --tags --always"
	say "Совместимое дерево:  ./apply.sh --clone ~/coddy-src"
	exit 2
fi

if [ "$already" -eq "$total" ]; then
	say "сверка: все ${total} файлов слоя уже на месте — прошлый запуск дошёл до конца"
elif [ "$unknown" -gt 0 ] && [ "$already" -gt 0 ]; then
	say "сверка: ${total} файлов слоя совместимы, ${already} уже наложено, ${unknown} испорчено — восстановлю"
elif [ "$already" -gt 0 ]; then
	say "сверка: ${total} файлов слоя совместимы с этим клоном, из них уже наложено ${already}"
else
	say "сверка: все ${total} файлов слоя совместимы с этим клоном"
fi

if [ "$CHECK_ONLY" -eq 1 ]; then
	if [ "$already" -eq "$total" ]; then
		say "проверка пройдена: слой уже наложен полностью (--check: ничего не изменено)"
	else
		say "проверка пройдена: осталось наложить $((total - already)) из ${total}"
	fi
	exit 0
fi

copied=0
while IFS=$'\t' read -r path kind base_sha our_sha; do
	case "$path" in \#*|"") continue ;; esac
	src="$OVERLAY/$path"
	target="$CODDY/$path"
	[ -f "$src" ] || fail "в слое нет файла $path"
	if [ -f "$target" ] && [ "$(sha "$target")" = "$our_sha" ]; then
		continue
	fi
	mkdir -p "$(dirname "$target")"
	cp -p "$src" "$target" || fail "не удалось записать $target"
	got="$(sha "$target")"
	[ "$got" = "$our_sha" ] || fail "после копирования $path не совпал по sha256"
	copied=$((copied + 1))
done < "$MANIFEST"

say "наложено файлов: $copied"
if [ "$already" -gt 0 ]; then
	say "уже были на месте (прошлый запуск): $already"
fi
if [ "$unknown" -gt 0 ] && [ "$already" -gt 0 ]; then
	say "восстановлено после обрыва: $unknown"
fi

if [ "$DO_BUILD" -eq 0 ]; then
	say ""
	say "дальше:"
	say "  bash \"$HERE/apply.sh\" --coddy \"$CODDY\" --build   # накат и сборка одной командой"
	say "  # или собрать вручную, уже после наката:"
	say "  cd \"$CODDY\" && go build -tags \"$TAGS\" -o build/coddy ./cmd/coddy/"
	exit 0
fi

command -v go >/dev/null 2>&1 || fail "go не найден в PATH (нужен Go 1.26 или новее; точный набор инструментов go1.27.3 проект подтянет сам)"
VER="${BASE_TAG}-max-layer"
REV="${LAYER_SOURCE_COMMIT:-${LAYER_COMMIT:-unknown}}"
LDFLAGS="-X github.com/EvilFreelancer/coddy-agent/internal/version.Version=${VER}"
LDFLAGS="$LDFLAGS -X github.com/EvilFreelancer/coddy-agent/internal/version.Revision=${REV}"
say "собираю: go build -tags \"$TAGS\" -ldflags \"...\" -o build/coddy ./cmd/coddy/"
( cd "$CODDY" && mkdir -p build && go build -tags "$TAGS" -ldflags "$LDFLAGS" -o build/coddy ./cmd/coddy/ ) \
	|| fail "сборка не прошла"
say "готово: $CODDY/build/coddy"
"$CODDY/build/coddy" -v 2>/dev/null || true
