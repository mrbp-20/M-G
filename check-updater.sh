#!/usr/bin/env bash
# Сквозная проверка скрипта обновления на локальных git-репозиториях: ничего не
# качает из сети, Go не требует (подставляет заглушку), настоящую сборку не
# запускает.
#
# Проверяются ровно те случаи, из-за которых скрипт ошибался:
#   1. скачанный пакет того же выпуска — «у вас последний слой», код 0;
#   2. опубликован следующий выпуск — «есть новый слой», код 0;
#   3. копия из другой линии (нашего выпуска в истории нет) — отказ, код 3;
#   4. пустой репозиторий слоя — «обновляться неоткуда», код 5;
#   5. --apply на нормальном обновлении — доходит до наката и сборки, код 0;
#   6. выпуск, где изменился только корневой файл (манифест слоя тот же) —
#      обязан считаться новым выпуском, иначе правка скрипта не доедет;
#   7. опубликованный слой без RELEASE (пакет старой схемы) — сверка идёт по
#      манифесту, а не отказ;
#   8. копия пакета с виндовыми концами строк — опознание не ломается.
#
# Переводы строк проверяются снаружи: checks.sh прогоняет этот файл при
# core.autocrlf=true и false, потому что на Windows git отдаёт CRLF.
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT

fail=0
ok() { printf '  ок: %s\n' "$*"; }
bad() { printf '  ПРОВАЛ: %s\n' "$*"; fail=1; }

mkdir -p "$WORK/repos"

# Ветка по умолчанию у git не задана и на разных машинах разная (master/main).
# Тест обязан проверять скрипт обновления, а не настройку git, поэтому ветка
# ставится явно: main. Иначе «нет ветки main» — верный ответ скрипта на чужую
# настройку.
init_main() {
	git init -q "$1"
	git -C "$1" symbolic-ref HEAD refs/heads/main
}

export GIT_AUTHOR_NAME=check GIT_AUTHOR_EMAIL=check@local
export GIT_COMMITTER_NAME=check GIT_COMMITTER_EMAIL=check@local

# Заглушка go: скрипт проверяет наличие инструмента до начала работы.
mkdir -p "$WORK/bin"
printf '#!/bin/sh\nexit 0\n' >"$WORK/bin/go"
chmod +x "$WORK/bin/go"
export PATH="$WORK/bin:$PATH"

# --- репозиторий Coddy -----------------------------------------------------
CODDY="$WORK/repos/coddy"
mkdir -p "$CODDY"
init_main "$CODDY"
printf 'module coddy\n' >"$CODDY/go.mod"
git -C "$CODDY" add -A >/dev/null
git -C "$CODDY" commit -qm base
git -C "$CODDY" tag 1.3.62

# --- пакет, который «скачал пользователь» ---------------------------------
PKG="$WORK/package"
mkdir -p "$PKG"
cp "$HERE/update.sh" "$PKG/update.sh"
printf 'BASE_TAG=1.3.62\nBASE_COMMIT=deadbeef\nLAYER_SOURCE_COMMIT=cafe1234\nLAYER_FILES=1\nLAYER_NEW=1\nLAYER_MODIFIED=0\nLAYER_DATE=2026-10-30\n' >"$PKG/VERSION"
cat >"$PKG/MANIFEST.tsv" <<'EOF'
# path	kind	base_sha256 (Coddy base)	our_sha256
overlay/one.txt	new	-	1111111111111111111111111111111111111111111111111111111111111111
EOF
printf '# release 1\none\n' >"$PKG/RELEASE"

MANIFEST_NEXT="$WORK/manifest-next"
cat >"$MANIFEST_NEXT" <<'EOF'
# path	kind	base_sha256 (Coddy base)	our_sha256
overlay/one.txt	new	-	1111111111111111111111111111111111111111111111111111111111111111
overlay/two.txt	new	-	2222222222222222222222222222222222222222222222222222222222222222
EOF
RELEASE_NEXT="$WORK/release-next"
printf '# release 2\none\ntwo\n' >"$RELEASE_NEXT"

# Заглушка накатчика: помечает, что её вызвали, и оставляет «бинарник».
stub_apply() {
	cat >"$1/apply.sh" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
coddy=""
while [ $# -gt 0 ]; do
	case "$1" in
	--coddy) coddy="$2"; shift ;;
	esac
	shift || true
done
[ -n "$coddy" ] || { echo "заглушке нужен --coddy" >&2; exit 2; }
mkdir -p "$coddy/build"
printf '#!/bin/sh\necho 1.3.62-max-test\n' >"$coddy/build/coddy"
chmod +x "$coddy/build/coddy"
printf 'applied\n' >"$coddy/.applied"
EOF
	chmod +x "$1/apply.sh"
}

# $1 каталог, $2 манифест пакета или свой, $3 RELEASE или свой, $4 признак
# «без RELEASE» (тогда файл не кладём — так выглядит пакет старой схемы)
make_layer() {
	local dir="$1" manifest="$2" release="$3" without="${4:-}"
	mkdir -p "$dir"
	init_main "$dir"
	# Настоящий слой несёт .gitattributes с «* -text»: пакет сверяется по хешам, и
	# git не должен переводить ему концы строк. Без этого тест проверял бы не
	# скрипт обновления, а перевод строки в apply.sh.
	printf '* -text\n' >"$dir/.gitattributes"
	cp "$PKG/VERSION" "$dir/VERSION"
	cp "$manifest" "$dir/MANIFEST.tsv"
	[ -n "$without" ] || cp "$release" "$dir/RELEASE"
	stub_apply "$dir"
	git -C "$dir" add -A >/dev/null
	git -C "$dir" commit -qm "layer $(basename "$dir")"
}

# Копия пакета, прошедшая через перевод концов строк: так выглядит каталог,
# распакованный Windows-средством или сохранённый через редактор. Содержимое то
# же, хеши сырых байтов разные — опознание обязано это выдержать.
crlf_package() {
	local dst="$WORK/package-crlf"
	rm -rf "$dst"
	cp -a "$PKG" "$dst"
	for f in VERSION MANIFEST.tsv RELEASE; do
		[ -f "$dst/$f" ] && sed -i 's/$/\r/' "$dst/$f"
	done
	echo "$dst"
}

run() { # $1 репозиторий слоя, $2 рабочий каталог, дальше ключ
	MAXGW_CODDY_REPO="$CODDY" MAXGW_LAYER_REPO="$1" MAXGW_WORK_DIR="$2" \
		bash "$PKG/update.sh" "${3:-}" >"$WORK/out" 2>&1
	echo $?
}

echo "== 1. свежескачанный пакет: тот же выпуск =="
L1="$WORK/repos/layer-same"
make_layer "$L1" "$PKG/MANIFEST.tsv" "$PKG/RELEASE"
code="$(run "$L1" "$WORK/w1" --check)"
if [ "$code" = "0" ] && grep -q 'последний слой' "$WORK/out"; then
	ok "код 0, «последний слой»"
else
	bad "код $code; вывод: $(tail -2 "$WORK/out" | tr '\n' ' ')"
fi

echo "== 2. опубликован следующий выпуск =="
L2="$WORK/repos/layer-next"
make_layer "$L2" "$PKG/MANIFEST.tsv" "$PKG/RELEASE"
cp "$MANIFEST_NEXT" "$L2/MANIFEST.tsv"
cp "$RELEASE_NEXT" "$L2/RELEASE"
git -C "$L2" add -A >/dev/null && git -C "$L2" commit -qm next
code="$(run "$L2" "$WORK/w2" --check)"
if [ "$code" = "0" ] && grep -q 'есть новый слой' "$WORK/out"; then
	ok "код 0, «есть новый слой»"
else
	bad "код $code; вывод: $(tail -2 "$WORK/out" | tr '\n' ' ')"
fi

echo "== 3. копия из другой линии =="
L3="$WORK/repos/layer-foreign"
make_layer "$L3" "$MANIFEST_NEXT" "$RELEASE_NEXT"
code="$(run "$L3" "$WORK/w3" --check)"
if [ "$code" = "3" ] && grep -q 'нет в истории опубликованного' "$WORK/out"; then
	ok "код 3, отказ от отката"
else
	bad "код $code; вывод: $(tail -2 "$WORK/out" | tr '\n' ' ')"
fi

echo "== 4. пустой репозиторий слоя =="
L4="$WORK/repos/layer-empty"
git init -q --bare "$L4"
code="$(run "$L4" "$WORK/w4" --check)"
if [ "$code" = "5" ] && grep -q 'Обновляться неоткуда' "$WORK/out"; then
	ok "код 5, «обновляться неоткуда»"
else
	bad "код $code; вывод: $(tail -2 "$WORK/out" | tr '\n' ' ')"
fi

echo "== 5. --apply доходит до наката и сборки =="
code="$(run "$L2" "$WORK/w5" --apply)"
if [ "$code" = "0" ] && [ -f "$WORK/w5/coddy/.applied" ] && [ -f "$WORK/w5/coddy/build/coddy" ]; then
	ok "код 0, накатчик вызван, «бинарник» на месте"
else
	bad "код $code; накат: $([ -f "$WORK/w5/coddy/.applied" ] && echo да || echo нет); вывод: $(tail -2 "$WORK/out" | tr '\n' ' ')"
fi

echo "== 6. изменился только корневой файл (манифест слоя тот же) =="
L6="$WORK/repos/layer-root"
make_layer "$L6" "$PKG/MANIFEST.tsv" "$PKG/RELEASE"
printf '# release 1\none\nfixed-update-script\n' >"$L6/RELEASE"
git -C "$L6" add -A >/dev/null && git -C "$L6" commit -qm "root only"
code="$(run "$L6" "$WORK/w6" --check)"
if [ "$code" = "0" ] && grep -q 'есть новый слой' "$WORK/out"; then
	ok "код 0, правка корневого файла видна как новый выпуск"
else
	bad "код $code; вывод: $(tail -2 "$WORK/out" | tr '\n' ' ')"
fi

echo "== 7. опубликованный слой без RELEASE (пакет старой схемы) =="
L7="$WORK/repos/layer-old"
make_layer "$L7" "$PKG/MANIFEST.tsv" "$PKG/RELEASE" without
code="$(run "$L7" "$WORK/w7" --check)"
if [ "$code" = "0" ] && grep -q 'нет RELEASE — сверяюсь по MANIFEST.tsv' "$WORK/out" && grep -q 'последний слой' "$WORK/out"; then
	ok "код 0, сверка по манифесту, «последний слой»"
else
	bad "код $code; вывод: $(tail -3 "$WORK/out" | tr '\n' ' ')"
fi

echo "== 8. копия пакета с виндовыми концами строк =="
PKG_CRLF="$(crlf_package)"
code="$(MAXGW_CODDY_REPO="$CODDY" MAXGW_LAYER_REPO="$L1" MAXGW_WORK_DIR="$WORK/w8" \
	bash "$PKG_CRLF/update.sh" --check >"$WORK/out" 2>&1; echo $?)"
if [ "$code" = "0" ] && grep -q 'последний слой' "$WORK/out"; then
	ok "код 0, концы строк не ломают опознание"
else
	bad "код $code; вывод: $(tail -2 "$WORK/out" | tr '\n' ' ')"
fi

echo
if [ "$fail" -eq 0 ]; then
	echo "скрипт обновления проверен на локальных репозиториях"
else
	echo "проверки скрипта обновления не пройдены"
fi
exit "$fail"
