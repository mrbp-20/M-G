# Text formatting: Markdown and HTML

**Source:** official reference, <https://dev.max.ru/docs-api/use-cases/sending-messages/text-formatting>
**Taken:** 2026-10-18
**Kind:** official documentation, plus our own probes (marked)

## To use either

> Если для форматирования текста сообщения вы хотите использовать разметку Markdown, при отправке
> запроса на создание сообщения установите для параметра `format` значение `markdown`
> Если ... HTML, ... установите для параметра `format` значение `html`

> **Обратите внимание**: в тексте комментариев не поддерживаются гиперссылки и упоминание пользователей

## Markdown

| Display | Markdown |
|---|---|
| *курсив* | `*emphasized*` or `_emphasized_` |
| **жирный** | `**strong**` or `__strong__` |
| ~~зачёркнутый~~ | `~~strikethrough~~` |
| подчёркнутый | `++underline++` |
| `моноширинный` | `` `code` `` - переводы строк внутри этого блока обрабатываются как пробелы |
| [ссылка](https://dev.max.ru/) | `[Inline URL](https://dev.max.ru/)` |
| @упоминание пользователя | `[Имя Фамилия](max://user/user_id)` |
| выделенный | `^^выделенный^^` |
| заголовок | `# заголовок` - заголовки всех уровней отображаются одинаково |
| цитата | `> Цитата` |

**Our finding:** the line about newlines inside a code span becoming spaces is the reason a multi-line
tool output must not be put in backticks - it would collapse into one long line. The gateway keeps such
output as plain text instead.

## HTML

| Display | HTML |
|---|---|
| *курсив* | `<i>` or `<em>` |
| **жирный** | `<b>` or `<strong>` |
| зачёркнутый | `<del>` or `<s>` |
| подчёркнутый | `<ins>` or `<u>` |
| `моноширинный` | `<pre>` or `<code>` |
| [ссылка](https://dev.max.ru/) | `<a href="https://dev.max.ru">Docs</a>` |
| @упоминание | `<a href="max://user/user_id">Имя Фамилия</a>` |
| выделенный | `<mark>выделенный</mark>` |
| заголовок | `<h1>`, `<h2>`, `<h3>`, `<h4>` - all levels render the same |
| цитата | `<blockquote> Цитата </blockquote>` |

## Our finding: formatting has nothing to do with link previews

Five live probes, each carrying the word `readme.md`:

| Probe | Card attached |
|---|---|
| plain text (no `format`) | yes, `http://readme.md` |
| `format: markdown` | yes |
| `format: html`, the word inside `<code>` | yes |
| `format: markdown`, the word inside backticks | yes |
| plain text, a zero-width space after the dot | **no** |

MAX parses the markup first and then walks the resulting text, so neither `<code>` nor a code span
protects a word. The only things that help are the `disable_link_preview` query parameter (the proper
way, see [sending-messages.md](sending-messages.md)) or breaking the word itself.

## Our finding: rendered text is what is stored

After a `format: markdown` send, the API returns the text with the markers already consumed. Reading a
conversation back gives the rendered words, not the original markup.
