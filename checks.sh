#!/usr/bin/env bash
# Проверки пакета слоя: то, что можно проверить без Coddy, Go и сборки.
# Запускается локально (bash checks.sh) и в CI пакета (.github/workflows/).
#
# Проверяется: целостность манифеста против overlay, числа в VERSION против
# манифеста, отсутствие внутренних разборов и известных образцов секретов,
# отсутствие путей рабочих машин, синтаксис накатчиков, BOM у PowerShell-файлов,
# обязательные ссылки на исключённые брифы, совпадение версии Coddy в README.
#
# Код выхода 1 — есть провал. Каждая проверка печатает «ок» или «ПРОВАЛ».
set -uo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$HERE"

fail=0
ok() { printf '  ок: %s\n' "$*"; }
bad() { printf '  ПРОВАЛ: %s\n' "$*"; fail=1; }
need() { [ -e "$1" ] && ok "$1 на месте" || bad "нет $1"; }

# Список публикации — рабочий файл пакета, в репозиторий он не идёт. В
# опубликованном клоне его нет, и там список публикации — это сам индекс git:
# по нему и проверяем, чтобы сторож работал и у клиента, и в CI пакета.
PUBLISH_LIST=".publish-allowlist"
TEMP_LIST=""
if [ ! -f "$PUBLISH_LIST" ] && [ -d .git ] && git rev-parse --git-dir >/dev/null 2>&1; then
	PUBLISH_LIST="$(mktemp)"
	TEMP_LIST="$PUBLISH_LIST"
	git ls-files | sort >"$PUBLISH_LIST"
fi
trap '[ -n "$TEMP_LIST" ] && rm -f "$TEMP_LIST"' EXIT

echo "== состав пакета =="
for f in VERSION MANIFEST.tsv apply.sh apply.ps1 apply.cmd update.sh update.ps1 update.cmd LICENSE README.md README.ru.md; do
	need "$f"
done
[ -d overlay ] && ok "overlay/ на месте" || bad "нет каталога overlay/"

BASE_TAG="$(sed -n 's/^BASE_TAG=//p' VERSION 2>/dev/null)"
BASE_COMMIT="$(sed -n 's/^BASE_COMMIT=//p' VERSION 2>/dev/null)"
LAYER_FILES="$(sed -n 's/^LAYER_FILES=//p' VERSION 2>/dev/null)"
LAYER_NEW="$(sed -n 's/^LAYER_NEW=//p' VERSION 2>/dev/null)"
LAYER_MODIFIED="$(sed -n 's/^LAYER_MODIFIED=//p' VERSION 2>/dev/null)"
echo "  версия: Coddy ${BASE_TAG:-?} (${BASE_COMMIT:0:8}), файлов ${LAYER_FILES:-?}"

echo "== манифест против overlay =="
if command -v sha256sum >/dev/null 2>&1; then sha() { tr -d '\r' <"$1" | sha256sum | cut -d' ' -f1; }; else sha() { tr -d '\r' <"$1" | shasum -a 256 | cut -d' ' -f1; }; fi
rows=0; news=0; mods=0; missing=0; wrong=0; badrow=0
while IFS=$'\t' read -r path kind base_sha our_sha; do
	case "$path" in \#*|"") continue ;; esac
	rows=$((rows + 1))
	[ "$(printf '%s' "$path" | awk -F'\t' '{print NF}')" = "1" ] || badrow=$((badrow + 1))
	case "$kind" in
	new) news=$((news + 1)) ;;
	modified) mods=$((mods + 1)) ;;
	*) badrow=$((badrow + 1)) ;;
	esac
	[ -f "overlay/$path" ] || { missing=$((missing + 1)); continue; }
	[ "$(sha "overlay/$path")" = "$our_sha" ] || wrong=$((wrong + 1))
done < MANIFEST.tsv
[ "$badrow" -eq 0 ] && ok "строки манифеста разобраны ($rows)" || bad "строк с неверным числом полей или типом: $badrow"
[ "$missing" -eq 0 ] && ok "все файлы слоя на месте" || bad "нет файлов слоя: $missing"
[ "$wrong" -eq 0 ] && ok "все sha256 совпали с манифестом" || bad "не совпали sha256: $wrong"

echo "== числа в VERSION против манифеста =="
[ "$rows" = "$LAYER_FILES" ] && ok "LAYER_FILES=$LAYER_FILES" || bad "LAYER_FILES=$LAYER_FILES, а строк в манифесте $rows"
[ "$news" = "$LAYER_NEW" ] && ok "LAYER_NEW=$LAYER_NEW" || bad "LAYER_NEW=$LAYER_NEW, а новых $news"
[ "$mods" = "$LAYER_MODIFIED" ] && ok "LAYER_MODIFIED=$LAYER_MODIFIED" || bad "LAYER_MODIFIED=$LAYER_MODIFIED, а заменяемых $mods"

echo "== список публикации =="
# Разборы аудита лежать в каталоге пакета могут — оператор решил, что удалять их
# заранее не нужно. Провал не в том, что файл есть, а в том, что он попал в
# список публикации: выкладка идёт по allowlist, а не «git add .».
if [ -f "$PUBLISH_LIST" ]; then
	listed=0; gone=0
	while IFS= read -r path; do
		[ -n "$path" ] || continue
		listed=$((listed + 1))
		[ -e "$path" ] || { gone=$((gone + 1)); printf '  нет в пакете: %s\n' "$path"; }
	done <"$PUBLISH_LIST"
	[ "$gone" -eq 0 ] && ok "все $listed файлов из списка на месте" || bad "из списка публикации отсутствует: $gone"
	if grep -qE '(^|/)PRE-PUBLICATION-' "$PUBLISH_LIST"; then
		bad "в список публикации попал разбор аудита"
	else
		ok "разборов в списке публикации нет"
	fi
	if grep -qE 'GATEWAY-BRIEF' "$PUBLISH_LIST"; then
		bad "в список публикации попал бриф"
	else
		ok "брифа в списке публикации нет"
	fi
	extra="$(find . -maxdepth 1 -type f -printf '%f\n' | sort | grep -vxF -f "$PUBLISH_LIST" | grep -v '^\.publish-allowlist$' | tr '\n' ' ')"
	[ -n "$extra" ] && printf '  не публикуется (лежит в папке): %s\n' "$extra"
else
	bad "нет .publish-allowlist: список публикации не сформирован"
fi
[ ! -f overlay/docs/GATEWAY-BRIEF.md ] && ok "брифа в overlay нет" || bad "в overlay попал бриф"

echo "== внутренние материалы не публикуются =="
# DOCS_W и разборы аудита — наше внутреннее обсуждение (Верста, Кими). В
# публичный репозиторий они попасть не должны ни файлом, ни строкой в списке.
if grep -qE 'DOCS_W|PRE-PUBLICATION' "$PUBLISH_LIST" 2>/dev/null; then
	bad "в списке публикации внутренние материалы (DOCS_W или разборы)"
else
	ok "внутренних материалов в списке публикации нет"
fi
if [ -d .git ]; then
	tracked="$(git ls-files | sort)"
	listed="$(sort "$PUBLISH_LIST")"
	if [ "$tracked" = "$listed" ]; then
		ok "в индексе git ровно публикуемые файлы ($(printf '%s\n' "$tracked" | wc -l | tr -d ' '))"
	else
		bad "индекс git расходится со списком публикации"
		printf '  лишнее в индексе: %s\n' "$(comm -23 <(printf '%s\n' "$tracked") <(printf '%s\n' "$listed") | tr '\n' ' ')"
		printf '  нет в индексе:   %s\n' "$(comm -13 <(printf '%s\n' "$tracked") <(printf '%s\n' "$listed") | tr '\n' ' ')"
	fi
	if printf '%s\n' "$tracked" | grep -qE '^(DOCS_W/|PRE-PUBLICATION)'; then
		bad "внутренний материал отслеживается git"
	else
		ok "git не отслеживает DOCS_W и разборы"
	fi
else
	printf '  пропуск: каталог пакета ещё не репозиторий\n'
fi

echo "== RELEASE против пакета =="
# RELEASE — опознание выпуска: он обязан перечислять ровно публикуемые файлы
# (кроме себя) и не врать про их хеши. Иначе устаревший RELEASE спрячет
# изменённый файл — та же тишина, что была с невидимыми корневыми правками.
if [ -f RELEASE ] && [ -f "$PUBLISH_LIST" ]; then
	rel_count=0; rel_wrong=0; rel_missing=0
	while read -r rel_hash rel_path; do
		case "$rel_hash" in \#*|"") continue ;; esac
		rel_count=$((rel_count + 1))
		if [ ! -f "$rel_path" ]; then
			rel_missing=$((rel_missing + 1))
			printf '  нет файла: %s\n' "$rel_path"
			continue
		fi
		if [ "$(sha "$rel_path")" != "$rel_hash" ]; then
			rel_wrong=$((rel_wrong + 1))
			printf '  хеш не совпал: %s\n' "$rel_path"
		fi
	done <RELEASE
	want="$(grep -vxF 'RELEASE' "$PUBLISH_LIST" | sort)"
	got="$(grep -v '^#' RELEASE | awk '{$1=""; sub(/^ +/, ""); print}' | sort)"
	if [ "$want" = "$got" ]; then
		ok "RELEASE перечисляет ровно публикуемые файлы ($rel_count)"
	else
		bad "состав RELEASE расходится со списком публикации"
	fi
	[ "$rel_wrong" -eq 0 ] && ok "все хеши RELEASE совпадают с файлами" || bad "хешей не совпало: $rel_wrong"
	[ "$rel_missing" -eq 0 ] && ok "все файлы RELEASE на месте" || bad "файлов из RELEASE нет: $rel_missing"
else
	bad "нет RELEASE или списка публикации"
fi

echo "== свежесть справочника API =="
# Справочник объявлен производным от комментариев кода. Правка комментария без
# пересборки раньше проходила все проверки — теперь это провал.
if command -v python3 >/dev/null 2>&1 && [ -f overlay/scripts/gen-max-api-reference.py ]; then
	gentmp="$(mktemp -d)"
	cp -a overlay/. "$gentmp/"
	if ( cd "$gentmp" && python3 scripts/gen-max-api-reference.py >/dev/null 2>&1 ); then
		same=1
		for f in docs/reference/max-gateway-api.md docs/ru/reference/max-gateway-api.md; do
			if [ ! -f "$gentmp/$f" ]; then
				bad "генератор не создал $f"
				same=0
				continue
			fi
			# Ни переводы строк, ни разделители путей не должны решать, свежий ли
			# справочник: на Windows генератор может выдать «\», и это не расхождение.
			if ! cmp -s <(tr -d '\r' <"overlay/$f" | tr '\\' '/') <(tr -d '\r' <"$gentmp/$f" | tr '\\' '/'); then
				bad "справочник не пересобран: $f"
				same=0
			fi
		done
		[ "$same" -eq 1 ] && ok "оба справочника совпадают со свежей сборкой"
	else
		bad "генератор справочника не отработал"
	fi
	rm -rf "$gentmp"
else
	printf '  пропуск: нет python3 или генератора\n'
fi

echo "== образцы секретов и путей рабочих машин =="
if grep -rIlE 'ghp_[A-Za-z0-9]{20,}|AKIA[0-9A-Z]{16}|BEGIN [A-Z ]*PRIVATE KEY' overlay README.md README.ru.md apply.sh apply.ps1 2>/dev/null | head -3 | grep -q .; then
	bad "найдены образцы секретов"
else
	ok "секретов не найдено"
fi
if grep -rIlE '/home/vag|/mnt/gguf|/mnt/f/|C:\\\\Users' overlay README.md README.ru.md 2>/dev/null | head -3 | grep -q .; then
	bad "найдены пути рабочих машин"
else
	ok "путей рабочих машин не найдено"
fi

echo "== синтаксис накатчиков =="
for f in apply.sh update.sh checks.sh; do
	[ -f "$f" ] || continue
	if bash -n "$f" 2>/dev/null; then ok "$f разбирается"; else bad "$f не разбирается"; fi
done
for f in apply.ps1 update.ps1; do
	[ -f "$f" ] || continue
	if command -v pwsh >/dev/null 2>&1; then
		if pwsh -NoProfile -Command "\$e=\$null; [void][System.Management.Automation.Language.Parser]::ParseFile('$HERE/$f',[ref]\$null,[ref]\$e); exit \$(if(\$e){1}else{0})" 2>/dev/null; then
			ok "$f разбирается (pwsh)"
		else
			bad "$f не разбирается (pwsh)"
		fi
	else
		printf '  пропуск: %s (нет pwsh)\n' "$f"
	fi
done

echo "== BOM у PowerShell-файлов =="
for f in apply.ps1 update.ps1; do
	[ -f "$f" ] || continue
	if [ "$(head -c3 "$f" | od -An -tx1 | tr -d ' \n')" = "efbbbf" ]; then
		ok "$f с BOM"
	else
		bad "$f без BOM: Windows PowerShell 5.1 не прочитает кириллицу"
	fi
done

echo "== обязательные ссылки на исключённые брифы =="
if [ -f overlay/docs/GATEWAY-PERSONA.md ] && grep -q 'GATEWAY-BRIEF' overlay/docs/GATEWAY-PERSONA.md; then
	bad "персона ссылается на бриф, которого в пакете нет"
else
	ok "персона читается без брифа"
fi

echo "== версия Coddy в README против VERSION =="
if [ -n "$BASE_TAG" ]; then
	stale="$(grep -l "Coddy 1\.3\.[0-9]*" README.md README.ru.md 2>/dev/null | head -2)"
	versions="$(grep -ho "Coddy 1\.3\.[0-9]*" README.md README.ru.md 2>/dev/null | sort -u)"
	versions2="$(grep -ho "Coddy версии 1\.3\.[0-9]*" README.ru.md 2>/dev/null | sort -u)"
	if printf '%s\n%s\n' "$versions" "$versions2" | grep -v "1\.3\." | grep -q . ; then :; fi
	if printf '%s\n' "$versions" | grep -q . && printf '%s\n' "$versions" | grep -qv "$BASE_TAG"; then
		bad "в README другая версия Coddy: $(printf '%s' "$versions" | tr '\n' ' ')"
	else
		ok "README называют Coddy $BASE_TAG"
	fi
	[ -z "$stale" ] || true
else
	bad "в VERSION нет BASE_TAG"
fi

echo "== числа слоя в корневых README =="
rows=$((LAYER_FILES + 1))
for f in README.ru.md README.md; do
	if [ ! -f "$f" ]; then
		bad "нет $f"
		continue
	fi
	text="$(tr '\n' ' ' <"$f" | tr -s ' ')"
	missing=""
	printf '%s' "$text" | grep -qE "($LAYER_FILES файл|$LAYER_FILES files)" || missing="$missing файлов"
	printf '%s' "$text" | grep -qE "($LAYER_NEW новых|$LAYER_NEW new)" || missing="$missing новых"
	printf '%s' "$text" | grep -qE "($LAYER_MODIFIED заменяемых|$LAYER_MODIFIED replaced)" || missing="$missing заменяемых"
	printf '%s' "$text" | grep -qE "($rows строка манифеста|$rows строк манифеста|$rows manifest rows)" || missing="$missing строк-манифеста"
	if [ -z "$missing" ]; then
		ok "$f: числа совпадают с VERSION ($LAYER_FILES / $LAYER_NEW / $LAYER_MODIFIED, $rows строка)"
	else
		bad "$f: не хватает чисел:$missing"
	fi
	if printf '%s' "$text" | grep -qE '(158 файл|158 files|111 новых|111 new|47 заменяемых|47 replaced|159 строк|159 manifest)'; then
		bad "$f: остались прежние числа слоя"
	else
		ok "$f: прежних чисел нет"
	fi
done

echo "== скрипт обновления на локальных репозиториях =="
# Два прогона: git на Windows по умолчанию переводит концы строк, и сырые хеши
# одного и того же файла расходятся. Проверка обязана это воспроизводить, иначе
# дефект поймает клиент, а не мы.
if [ -f check-updater.sh ]; then
	cfg="$(mktemp)"
	for mode in false true; do
		printf '[core]\n\tautocrlf = %s\n' "$mode" >"$cfg"
		tmpout="$(mktemp)"
		if GIT_CONFIG_GLOBAL="$cfg" GIT_CONFIG_NOSYSTEM=1 bash check-updater.sh >"$tmpout" 2>&1; then
			ok "все случаи пройдены при core.autocrlf=$mode"
		else
			bad "при core.autocrlf=$mode: $(tail -3 "$tmpout" | tr '\n' ' ')"
		fi
		rm -f "$tmpout"
	done
	rm -f "$cfg"
else
	bad "нет check-updater.sh"
fi

echo
if [ "$fail" -eq 0 ]; then
	echo "все проверки пакета пройдены"
else
	echo "проверки пакета не пройдены"
fi
exit "$fail"
