# `POST /subscriptions` - webhooks, the production path

**Source:** official reference, <https://dev.max.ru/docs-api/methods/POST/subscriptions>
**Taken:** 2026-10-18
**Kind:** official documentation

## What it does

> Метод настраивает доставку событий бота через Webhook - основной механизм получения событий в
> продуктовых интеграциях. При активной подписке Long Polling не работает

> Для повышения безопасности **с 25 мая** прекращается поддержка получения вебхуков по HTTP, а также
> самоподписных сертификатов. Рекомендуем заранее перейти на HTTPS и сертификаты от доверенных центров,
> в том числе сертификаты Минцифры

## How an event is delivered

> 1. При наступлении события выполняется вызов Webhook-endpoint
> 2. Выполняется TLS-валидация целевого endpoint для безопасной передачи данных
> 3. На endpoint отправляется HTTPS-запрос
> 4. Если при создании подписки указан `secret`, проверяется заголовок `X-Max-Bot-Api-Secret`
> 5. При успешной валидации возвращается HTTP `200 OK`
> 6. Выполняется обработка события
> 7. Инициируются вызовы MAX API

## Requirements for the endpoint

> Webhook-endpoint должен быть доступен по HTTPS на порту 443. Ваш сервер должен прослушивать этот порт.
> Порт в URL не указывается

> Поддерживается только порт 443. Если endpoint недоступен, события не доставляются

> Webhook-endpoint удостоверен TLS-сертификатом, выданным доверенным центром сертификации, или
> TLS-сертификатом Минцифры. Самоподписанные сертификаты не поддерживаются

> Доменное имя в URL Webhook-endpoint совпадает с CN или SAN сертификата

> Сервер предоставляет полную цепочку сертификатов

> Webhook-endpoint должен возвращать **HTTP 200** в течение 30 секунд. Любой другой код ответа или
> превышение тайм-аута считается ошибкой доставки

**Our finding:** "only port 443, no port in the URL" is stricter than it looks - a listener on 8443
behind a proxy is fine only if the public name is on 443. This is the single biggest difference from the
long-polling setup the gateway runs today: a public name, a trusted certificate, and an inbox that answers
within 30 seconds.

## Retries and the automatic unsubscribe

> Если доставка не удалась, выполняется до 10 повторных попыток с экспоненциально растущим интервалом:
> 1-я попытка: через 60 секунд
> 2-я попытка: через 150 секунд (60 × 2,5)
> 3-я попытка: через 375 секунд (150 × 2,5)
> и так далее

> Если в течение 8 часов от Webhook-endpoint не получен успешный ответ, бот автоматически от него
> отписывается

**Our finding:** the factor is 2.5, not 2, and the whole ladder fits inside 8 hours. A gateway that is
down for a working day does not lose the subscription - it loses it after eight hours without a single
successful answer.

## The secret

> Параметр `secret` позволяет убедиться, что Webhook-запросы приходят от MAX, а не от третьей стороны.
> Это необязательный параметр, но мы настоятельно рекомендуем указывать его. Проверяйте значение
> заголовка `X-Max-Bot-Api-Secret` на Webhook-сервере и отклоняйте запросы при несоответствии

> `secret` - string optional, `^[a-zA-Z0-9_-]{5,256}$`, от `5` до `256` символов

**Our finding:** the character class is narrow - no dots, no base64 padding, no `/` or `+`. A generated
secret that happens to be base64 would be refused at subscription time, not at delivery time.

## Body and result

> `url` - string `SubscriptionUrl`. URL HTTPS-endpoint вашего бота. Должен начинаться с `https://`
> `update_types` - string[] optional. Список типов событий; полный список в объекте `Update`
> Результат: `true`, если запрос был успешным, `false` - в противном случае

## Example from the reference

```sh
curl -X POST "https://platform-api2.max.ru/subscriptions" \
  -H "Authorization: {access_token}" \
  -H "Content-Type: application/json" \
  -d '{"url": "https://your-domain.com/webhook", "update_types": ["message_created", "bot_started"], "secret": "your_secret"}'
```

## Open questions

1. How is an existing subscription changed - the same method with a new body, or a `DELETE` first?
2. Does a webhook delivery count against the 30 rps of the API? The upload page carries that ceiling, this
   one does not mention it.
3. What does a delivery look like when the endpoint answers `200` slowly - is the 30 seconds the whole
   handler or the connection?
