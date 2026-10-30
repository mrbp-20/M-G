#!/usr/bin/env python3
"""Собирает справочник по коду адаптера MAX из комментариев в самом коде.

Разбирает файлы external/gateway/max/*.go (без тестов), берёт объявления типов и
функций вместе с их описанием и складывает в docs/reference/max-gateway-api.md
(и в русский двойник docs/ru/reference/max-gateway-api.md).

Запуск из корня Coddy-дерева, куда наложен слой:

    python3 scripts/gen-max-api-reference.py            # оба языка
    python3 scripts/gen-max-api-reference.py --lang en  # только английский
"""

import argparse
import os
import re
import sys

ADAPTER = os.path.join("external", "gateway", "max")
TARGETS = {
    "en": os.path.join("docs", "reference", "max-gateway-api.md"),
    "ru": os.path.join("docs", "ru", "reference", "max-gateway-api.md"),
}

TEXT = {
    "en": {
        "title": "# The MAX gateway adapter: a reference by file",
        "intro": (
            "Generated from the code itself by `scripts/gen-max-api-reference.py`: every type and "
            "function of the adapter with the comment that stands above it, grouped by the file it "
            "lives in. The comments are the source of truth and stay as written there; this file only "
            "collects them. Regenerate after changing the adapter rather than editing it by hand.\n\n"
            "Reading order for a newcomer: `bot.go` (the adapter and its settings), `dispatch.go` "
            "(what happens to an incoming message), `sender.go` (what the chat sees), then "
            "`documents.go` and `pictures.go` (files and pictures), `incoming.go` (files the person "
            "sends), `client.go` (the API), and `model.go` (the wire types).\n"
        ),
        "toc": "## Contents",
        "toc_line": "- [{file}](#{anchor}) — {types} types, {funcs} functions",
        "types": "Types",
        "funcs": "Functions",
        "no_doc": "_no description in the code_",
        "files": "Files",
        "counts": (
            "{files} files, {types} types, {funcs} functions; {documented} carry a description in the "
            "code."
        ),
        "file_purpose": "What this file is for",
    },
    "ru": {
        "title": "# Адаптер MAX: справочник по файлам",
        "intro": (
            "Собрано из самого кода скриптом `scripts/gen-max-api-reference.py`: каждый тип и каждая "
            "функция адаптера вместе с комментарием над ними, по файлам. Комментарии в коде — источник "
            "правды и приводятся как написаны; файл их только собирает. После правки адаптера "
            "пересоберите его заново, а не правьте руками.\n\n"
            "Порядок чтения для новичка: `bot.go` (адаптер и его настройки), `dispatch.go` (что "
            "происходит с входящим сообщением), `sender.go` (что видит чат), затем `documents.go` и "
            "`pictures.go` (файлы и картинки), `incoming.go` (файлы от человека), `client.go` (API) и "
            "`model.go` (типы обмена).\n"
        ),
        "toc": "## Содержание",
        "toc_line": "- [{file}](#{anchor}) — типов: {types}, функций: {funcs}",
        "types": "Типы",
        "funcs": "Функции",
        "no_doc": "_описания в коде нет_",
        "files": "Файлы",
        "counts": (
            "Файлов: {files}, типов: {types}, функций: {funcs}; с описанием в коде: {documented}."
        ),
        "file_purpose": "Зачем этот файл",
    },
}

DECL = re.compile(r"^(type|func)\s+(?:\(\s*\w+\s+\*?(\w+)\s*\)\s*)?(\w+)")


def comment_block(lines, index):
    """Возвращает блок комментариев прямо над строкой index."""
    out = []
    i = index - 1
    while i >= 0 and lines[i].lstrip().startswith("//"):
        out.append(lines[i].lstrip()[2:].strip())
        i -= 1
    out.reverse()

    return " ".join(part for part in out if part and not part.startswith("go:build"))


def file_purpose(lines):
    """Ведущий комментарий файла — до строки package."""
    out = []
    for line in lines:
        if line.startswith("package "):
            break
        if line.startswith("//"):
            text = line[2:].strip()
            if text and not text.startswith("go:build"):
                out.append(text)
    return " ".join(out)


def strip_comment(line):
    """Строка без хвостового комментария: «//» вне кавычек — конец объявления.

    Хвостовой комментарий в сигнатуру не входит: раньше он попадал туда вместе со
    следующим объявлением, и справочник показывал синтаксически неверный код.
    """
    quote = None
    i = 0
    while i < len(line):
        ch = line[i]
        if quote:
            if ch == "\\":
                i += 2
                continue
            if ch == quote:
                quote = None
        elif ch in "\"'`":
            quote = ch
        elif ch == "/" and i + 1 < len(line) and line[i + 1] == "/":
            return line[:i]
        i += 1
    return line


def balanced(text):
    """Сошлись ли круглые, квадратные и фигурные скобки."""
    return all(text.count(op) == text.count(cl) for op, cl in (("{", "}"), ("(", ")"), ("[", "]")))


def signature(lines, index):
    """Сигнатура объявления в одну строку.

    Границы объявления различаются по виду:

    - с телом: кончается на строке, открывающей тело («… {»), либо там, где тело
      открылось и закрылось на одной строке;
    - без тела (`type Option func(*Client)`, `type ID = int`, `type N int`):
      фигурных скобок нет вовсе, поэтому объявление кончается на своей строке —
      там, где сошлись скобки и строка не обещает продолжения. Раньше остановки
      не было, и в сигнатуру затягивался комментарий со следующим объявлением.
    """
    buf = []
    for line in lines[index:]:
        code = strip_comment(line).strip()
        if not code:
            break
        buf.append(code)
        text = " ".join(buf)
        if "{" in text:
            if text.count("{") - text.count("}") <= 0:
                break
            if code.endswith("{"):
                break
            continue
        if balanced(text) and not code.endswith((",", "(", "[", "{", ".", "|", "+")):
            break
    sig = " ".join(buf).strip()
    # Тело только открылось — сама скобка в сигнатуру не входит. Однострочное
    # объявление закрыто на той же строке и остаётся целиком.
    if sig.endswith("{"):
        sig = sig[:-1].rstrip()
    return re.sub(r"\s+", " ", sig)


def self_test():
    """Проверка генератора на выдуманном коде: новых файлов не заводит."""
    block = [
        "func New(cfg Config) *Bot {",
        "\treturn &Bot{cfg: cfg}",
        "}",
    ]
    one_line = [
        "func WithClient(hc *http.Client) Option { return func(c *Client) { c.http = hc } }",
        "// WithBaseURL points the client elsewhere.",
        "func WithBaseURL(base string) Option {",
        "\treturn nil",
        "}",
    ]
    bodyless = [
        "type Option func(*Client)",
        "// WithHTTPClient sets the HTTP client.",
        "func WithHTTPClient(hc *http.Client) Option {",
        "\treturn nil",
        "}",
    ]
    bodyless_reader = [
        "type transcriptReader func(sessionID string) []llm.Message",
        "// historyCard opens with where the chat landed.",
        "func historyCard(title string, msgs []llm.Message) string {",
        "\treturn title",
        "}",
    ]
    named = [
        "type ID int",
        "func (i ID) String() string {",
        "\treturn \"x\"",
        "}",
    ]
    trailing = [
        "type Option func(*Client) // WithHTTPClient sets the HTTP client.",
        "func WithHTTPClient(hc *http.Client) Option { return nil }",
    ]
    cases = [
        (block, 0, "func New(cfg Config) *Bot"),
        (one_line, 0, "func WithClient(hc *http.Client) Option { return func(c *Client) { c.http = hc } }"),
        (one_line, 2, "func WithBaseURL(base string) Option"),
        (bodyless, 0, "type Option func(*Client)"),
        (bodyless_reader, 0, "type transcriptReader func(sessionID string) []llm.Message"),
        (named, 0, "type ID int"),
        (trailing, 0, "type Option func(*Client)"),
    ]
    failed = 0
    for lines, index, want in cases:
        got = signature(lines, index)
        if got != want:
            print("НЕ СОВПАЛО:\n  ожидалось: %s\n  получено:  %s" % (want, got))
            failed += 1
        if "//" in got:
            print("В СИГНАТУРЕ КОММЕНТАРИЙ:\n  %s" % got)
            failed += 1
    if posix("external\\gateway\\max\\client.go") != "external/gateway/max/client.go":
        print("НЕ СОВПАЛО: разделители путей не приведены к «/»")
        failed += 1
    if failed:
        print("самопроверка генератора: провалов %d" % failed)
        return 1
    print("самопроверка генератора: пройдена")
    return 0


def posix(path):
    """Путь для текста справочника — всегда через «/».

    Иначе справочник, собранный на Windows, отличается от собранного на Linux
    одними разделителями, и проверка свежести честно кричит о расхождении там,
    где содержимое одинаково. Заодно файлы выходят одинаковыми на любой системе.
    """
    return path.replace("\\", "/")


def collect(path):
    with open(path, encoding="utf-8") as handle:
        lines = handle.read().splitlines()
    types, funcs = [], []
    for index, line in enumerate(lines):
        if line.startswith("//"):
            continue
        match = DECL.match(line)
        if not match:
            continue
        kind, receiver, name = match.group(1), match.group(2), match.group(3)
        doc = comment_block(lines, index) or TEXT["en"]["no_doc"]
        item = {
            "name": name + (" (method)" if receiver else "") if kind == "func" else name,
            "raw": name,
            "sig": signature(lines, index),
            "doc": doc,
            "documented": doc != TEXT["en"]["no_doc"],
        }
        (types if kind == "type" else funcs).append(item)
    return {
        "path": posix(path),
        "purpose": file_purpose(lines),
        "types": types,
        "funcs": funcs,
    }


def anchor(text):
    """Якорь в стиле GitHub: строчные, без знаков препинания, пробелы через дефис."""
    return re.sub(r"[^a-z0-9 _-]", "", text.lower()).replace(" ", "-")


def render(files, lang):
    text = TEXT[lang]
    total_types = sum(len(f["types"]) for f in files)
    total_funcs = sum(len(f["funcs"]) for f in files)
    documented = sum(1 for f in files for group in ("types", "funcs") for d in f[group] if d["documented"])
    total = total_types + total_funcs

    out = [text["title"], "", text["intro"]]
    out.append(text["counts"].format(
        files=len(files), types=total_types, funcs=total_funcs, documented=f"{documented} из {total}" if lang == "ru" else f"{documented} of {total}",
    ))
    out += ["", text["toc"], ""]
    for f in files:
        out.append(text["toc_line"].format(
            file=f["path"], anchor=anchor(f["path"]), types=len(f["types"]), funcs=len(f["funcs"]),
        ))
    out.append("")
    for f in files:
        out += [f"## {f['path']}", ""]
        if f["purpose"]:
            out += [f"**{text['file_purpose']}.** {f['purpose']}", ""]
        for group, heading in (("types", text["types"]), ("funcs", text["funcs"])):
            if not f[group]:
                continue
            out += [f"### {heading}", ""]
            for item in f[group]:
                doc = item["doc"] or text["no_doc"]
                out.append(f"- `{item['sig']}` — {doc}")
            out.append("")
    return "\n".join(out).rstrip() + "\n"


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--lang", choices=["en", "ru", "both"], default="both")
    parser.add_argument(
        "--self-test",
        action="store_true",
        help="проверить сам генератор на выдуманном коде и выйти",
    )
    args = parser.parse_args()
    if args.self_test:
        sys.exit(self_test())
    if not os.path.isdir(ADAPTER):
        sys.exit(f"нет каталога {posix(ADAPTER)}: запускать из корня Coddy-дерева со слоем")

    paths = sorted(
        posix(os.path.join(ADAPTER, name))
        for name in os.listdir(ADAPTER)
        if name.endswith(".go") and not name.endswith("_test.go")
    )
    files = [collect(path) for path in paths]
    for lang in (["en", "ru"] if args.lang == "both" else [args.lang]):
        body = render(files, lang)
        target = TARGETS[lang]
        os.makedirs(os.path.dirname(target), exist_ok=True)
        with open(target, "w", encoding="utf-8") as handle:
            handle.write(body)
        print(f"{posix(target)}: {len(body)} байт, файлов {len(files)}")


if __name__ == "__main__":
    main()
