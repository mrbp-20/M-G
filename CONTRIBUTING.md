# Как участвовать

Слой — это набор файлов поверх Coddy: шлюз MAX и те вставки, которые ему нужны в общих файлах.
Здесь описано, как проверить свои изменения, не сломав пакет.

## Что проверить перед отправкой

```bash
bash checks.sh
```

Проверка смотрит состав пакета, манифест, версию, список публикации, отсутствие внутренних
материалов, свежесть справочника API, синтаксис скриптов, поведение скрипта обновления в обоих
режимах переноса строк и докатку накатчика на макете. Всё это работает без Coddy и без Go.

Настоящая сборка проверяется отдельным рабочим процессом **Layer build**: он клонирует Coddy той
версии, что записана в `VERSION`, накладывает слой, проверяет форматирование, собирает бинарник и
прогоняет тесты шлюза. Если вы меняете код, прогоните то же самое у себя.

## Что стоит знать

- **Слой подходит одной версии Coddy.** Она записана в `VERSION` (`BASE_TAG`, `BASE_COMMIT`), а
  каждый заменяемый файл сверяется по `sha256` из `MANIFEST.tsv`. Не подгоняйте хеши вручную: пакет
  пересобирается скриптом из дерева Coddy.
- **Публикация идёт по списку разрешённого**, а не `git add .`: внутренние материалы не должны
  уезжать. Список — `.publish-allowlist`, его создаёт сборщик пакета.
- **Документация двуязычная.** Правка в русском тексте проверяется и в английском, это же держит
  `checks.sh`.
- **Токен бота — секрет.** Пустое `gateways.max.token` и `MAX_BOT_TOKEN` в окружении или в
  `~/.coddy/.env`; в репозиторий токен не попадает никогда.

## Имена и стиль

Комментарии объясняют причину, а не механику: «почему так» живёт дольше, чем «что тут написано».
Сообщения коммитов — по делу, одной строкой; язык — тот, на котором удобнее, обычно русский.

## Уязвимости

Не через issue: см. [SECURITY.md](SECURITY.md).

---

# Contributing

Run `bash checks.sh` before sending anything: it covers the package composition, the manifest, the
version, the publish list, the absence of internal material, the freshness of the generated API
reference, script syntax and the updater behaviour. The real build is checked by the **Layer build**
workflow: it clones the Coddy version named in `VERSION`, applies the layer, checks formatting builds
the binary and runs the gateway tests.

The layer fits one Coddy version — do not edit `MANIFEST.tsv` by hand, the package is rebuilt from the
Coddy tree. Publication goes by the allowlist (`.publish-allowlist`), never `git add .`. Report
vulnerabilities through [SECURITY.md](SECURITY.md), not issues.
