# Политика безопасности

## Как сообщить об уязвимости

Пожалуйста, **не открывайте публичный issue**. Воспользуйтесь приватным каналом GitHub:

**Security → Advisories → Report a vulnerability** в репозитории
<https://github.com/mrbp-20/M-G>.

Если этот канал недоступен, напишите владельцу репозитория через его профиль на GitHub —
<https://github.com/mrbp-20> — и попросите открыть приватный advisory.

## Что полезно приложить

- версию слоя (`VERSION`) и версию Coddy, на которую он наложен;
- как собрано (теги `-tags`, версия Go);
- шаги воспроизведения и то, что вы ожидали увидеть;
- журнал, если он есть, — **без токена бота**: строка `MAX_BOT_TOKEN` и содержимое
  `~/.coddy/.env` не должны попадать в переписку.

## Что мы делаем дальше

Подтверждаем получение в течение нескольких дней, разбираемся и готовим выпуск слоя с исправлением.
О сроках сообщаем в том же приватном канале.

## Область

В этой политике — код слоя и скрипты пакета. Уязвимости самого Coddy сообщайте, пожалуйста,
в его репозиторий: <https://github.com/coddy-project/coddy-agent>.

---

# Security policy

## Reporting a vulnerability

Please do **not** open a public issue. Use GitHub's private channel:
**Security → Advisories → Report a vulnerability** in <https://github.com/mrbp-20/M-G>.

Include the layer version (`VERSION`), the Coddy version it is applied to, the build tags and Go
version, the steps to reproduce, and any log **without the bot token** — neither `MAX_BOT_TOKEN` nor
the contents of `~/.coddy/.env` should ever be sent.

Vulnerabilities in Coddy itself belong to <https://github.com/coddy-project/coddy-agent>.
