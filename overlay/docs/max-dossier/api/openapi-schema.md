# OpenAPI schema and the client generators

**Source:** the official schema repository, <https://github.com/max-messenger/api-schema>
(schema file: <https://github.com/max-messenger/api-schema/blob/main/schema.yaml>)
**Taken:** 2026-10-18
**Kind:** official documentation (repository README)

## Why it matters more than any single object page

Four object pages we wanted (`Attachment`, `ShareAttachment`, `InlineKeyboardAttachmentRequest`,
`CallbackUpdate`) answer 404 on the documentation site. The schema is one YAML file that carries every
object and every field, so it is the shortest route to "what fields does X have".

> В репозитории размещена спецификация OpenAPI для работы с API MAX в формате `.YAML`

> Обратите внимание, спецификация содержит краткое описание параметров и эндпоинтов. Подробнее о работе
> API и конкретных методов читайте в документации API MAX

**Our finding:** the schema is a structural answer, not a prose one - field names, types and required
flags, without the "why". Both belong in this dossier: the reference pages for behaviour, the schema for
shape.

## The generator caveats, which are the useful part of the README

The team generated clients from the schema and tested every method on production; these are the traps they
recorded per language.

| Language | Generator | Trap |
|---|---|---|
| TypeScript | `openapi-generator typescript-fetch` 7.25.0 | Does not create `package.json`/`tsconfig.json`; must pass `basePath`, otherwise the client calls `http://localhost`; the token goes in `apiKey` **without** a `Bearer` prefix |
| Java | `openapi-generator java --library native` 7.25.0 | The default Gson-based libraries serialize discriminators as class names instead of values like `message_created`, producing wrong messages; `--library native` fixes it. Set the server address with `updateBaseUri()` **before** creating API objects |
| C# | `NSwag openapi2csclient` 14.7.1 | Only a `.cs` file is produced - a minimal .NET 8.0 project is needed; the server wants repeated query parameters as `?ids=1,2`, so lists must be `string[]`, not `List<string>` |
| Python | `openapi-python-client` 0.29.1 | Needs Python **3.10+**; adds `Bearer` to every token by default, while the server takes the raw token in the header |

**Our finding:** two of the four traps are the same fact we measured ourselves - the token is sent raw,
without `Bearer` - and one (`?ids=1,2` rather than `?ids=1&ids=2`) matches the "comma-separated
`message_ids`" wording on the `GET /messages` page. Our hand-written client already does both.

## The precondition for having a bot at all

> Создание бота и взаимодействие с API MAX доступно пока только для юрлиц, ИП и самозанятых, которые
> являются резидентами РФ

> Чтобы создать бота и получить его токен: подключитесь к платформе MAX для партнёров, создайте там
> профиль организации, ИП или самозанятого и дождитесь его верификации; создайте бота и дождитесь
> окончания модерации

**Our finding:** this is an onboarding fact, not an API one, and it is worth remembering when somebody
asks "how do I get a token" - there is a verification and a moderation queue before the first request.

## Recommended next steps for this dossier

1. Download `schema.yaml` and read the four objects whose pages 404.
2. Check our own models against the schema's required flags - a field we treat as optional that the schema
   marks required is a latent 400.
