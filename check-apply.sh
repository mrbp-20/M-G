#!/usr/bin/env bash
# Проверка накатчика на макете: свой «Coddy» и свой «пакет» из трёх файлов.
#
# Зачем: первый запуск может оборваться на середине (не было Go, пропала сеть), и
# второй запуск обязан проверить состояние и продолжить, а не отказать «каталог
# уже существует». Здесь проверяются все состояния повторного запуска: чистое
# дерево, полностью наложенное, файл испорченный после наката, чужой файл рядом,
# клон другой версии и каталог, не похожий на Coddy.
#
# PowerShell-накатчик повторяет те же правила; проверить его на Linux нельзя, поэтому
# паритет держится глазами и одинаковыми сообщениями.
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
[ -f "$HERE/apply.sh" ] || { echo "нет apply.sh рядом: проверять нечего"; exit 2; }
W="$(mktemp -d)"; trap 'rm -rf "$W"' EXIT
export GIT_AUTHOR_NAME=t GIT_AUTHOR_EMAIL=t@t GIT_COMMITTER_NAME=t GIT_COMMITTER_EMAIL=t@t
fail=0; ok(){ printf '  ок: %s\n' "$*"; }; bad(){ printf '  ПРОВАЛ: %s\n' "$*"; fail=1; }
sha(){ sha256sum "$1" | cut -d' ' -f1; }

# --- «Coddy» ---------------------------------------------------------------
C="$W/coddy"; mkdir -p "$C/sub"; cd "$C"
printf 'module coddy\n' > go.mod
printf 'base-a\n' > a.txt; printf 'base-b\n' > sub/b.txt; printf 'keep\n' > keep.txt
git init -q; git symbolic-ref HEAD refs/heads/main
git add -A; git commit -qm base; git tag 1.3.62
COMMIT="$(git rev-parse HEAD)"

# --- «пакет» --------------------------------------------------------------
P="$W/pkg"; mkdir -p "$P/overlay/sub"
cp "$HERE/apply.sh" "$P/apply.sh"
printf 'BASE_TAG=1.3.62\nBASE_COMMIT=%s\nLAYER_SOURCE_COMMIT=deadbeef\n' "$COMMIT" > "$P/VERSION"
printf 'our-a\n' > "$P/overlay/a.txt"; printf 'our-b\n' > "$P/overlay/sub/b.txt"
printf 'new-file\n' > "$P/overlay/new.txt"
{ printf '# path\tkind\tbase_sha256\tour_sha256\n'
  printf 'a.txt\tmodified\t%s\t%s\n' "$(sha "$C/a.txt")" "$(sha "$P/overlay/a.txt")"
  printf 'sub/b.txt\tmodified\t%s\t%s\n' "$(sha "$C/sub/b.txt")" "$(sha "$P/overlay/sub/b.txt")"
  printf 'new.txt\tnew\t-\t%s\n' "$(sha "$P/overlay/new.txt")"
} > "$P/MANIFEST.tsv"

run(){ bash "$P/apply.sh" "$@" >"$W/out" 2>&1; echo $?; }
tail_out(){ tail -3 "$W/out" | tr '\n' ' '; }

echo "== 1. --check на нетронутом дереве =="
code="$(run --clone "$C" --check)"
grep -q 'осталось наложить 3 из 3' "$W/out" && [ "$code" = 0 ] && ok "код 0, «осталось наложить 3 из 3»" || bad "код $code: $(tail_out)"

echo "== 2. первый накат =="
code="$(run --clone "$C")"
grep -q 'наложено файлов: 3' "$W/out" && [ "$code" = 0 ] && ok "код 0, наложено 3" || bad "код $code: $(tail_out)"

echo "== 3. повторный запуск после обрыва: всё на месте =="
code="$(run --clone "$C")"
grep -q 'клон уже на месте' "$W/out" && grep -q 'все 3 файлов слоя уже на месте' "$W/out" \
  && grep -q 'наложено файлов: 0' "$W/out" && grep -q 'уже были на месте (прошлый запуск): 3' "$W/out" \
  && [ "$code" = 0 ] && ok "код 0, клон переиспользован, накладывать нечего" || bad "код $code: $(tail_out)"

echo "== 4. файл испорчен после обрыва =="
printf 'мусор\n' > "$C/a.txt"
code="$(run --clone "$C")"
grep -q 'наложено файлов: 1' "$W/out" && grep -q 'уже были на месте (прошлый запуск): 2' "$W/out" \
  && [ "$(cat "$C/a.txt")" = "our-a" ] && [ "$code" = 0 ] && ok "код 0, восстановлен ровно один файл" || bad "код $code: $(tail_out)"

echo "== 5. --check на полностью наложенном дереве =="
code="$(run --clone "$C" --check)"
grep -q 'слой уже наложен полностью' "$W/out" && [ "$code" = 0 ] && ok "код 0, «слой уже наложен полностью»" || bad "код $code: $(tail_out)"

echo "== 6. чужой файл, которого нет в манифесте, не мешает =="
printf 'чужое\n' > "$C/keep.txt"
code="$(run --clone "$C")"
grep -q 'наложено файлов: 0' "$W/out" && [ "$code" = 0 ] && ok "код 0, посторонний файл не тронут" || bad "код $code: $(tail_out)"

echo "== 7. клон другой версии =="
C2="$W/coddy-old"; cp -a "$C" "$C2"; printf 'x\n' >> "$C2/keep.txt"
( cd "$C2" && git add -A && git commit -qm "другая версия" ) >/dev/null 2>&1
code="$(run --clone "$C2")"
grep -q 'уже лежит Coddy' "$W/out" && [ "$code" = 2 ] && ok "код 2, отказ с объяснением" || bad "код $code: $(tail_out)"

echo "== 8. каталог есть, но это не Coddy =="
C3="$W/not-coddy"; mkdir -p "$C3"; printf 'x\n' > "$C3/readme.txt"
code="$(run --clone "$C3")"
grep -q 'не исходники Coddy' "$W/out" && [ "$code" = 2 ] && ok "код 2, «не исходники Coddy»" || bad "код $code: $(tail_out)"

echo
[ "$fail" -eq 0 ] && echo "докатка проверена на макете" || echo "докатка НЕ проверена"
exit "$fail"
