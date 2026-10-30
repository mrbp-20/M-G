# Is there a global switch? The exhaustive check

**Question asked by:** the operator, 2026-10-18: "it is on MAX's side - dig the documentation, maybe there is
something in the global settings".
**Answer:** there is not. The platform offers exactly one control over previews, it is per request, and it
lives in three methods. Everything else that a person sees is the client drawing raw text.

## Sources

- Official schema: <https://github.com/max-messenger/api-schema/blob/main/schema.yaml> - taken 2026-10-18,
  kept in this folder as [schema.yaml](schema.yaml) (126 817 B, 3814 lines,
  sha256 `7e2fd072a67c6f83...`). Refresh with:
  `curl -sL https://raw.githubusercontent.com/max-messenger/api-schema/main/schema.yaml -o api/schema.yaml`
- <https://dev.max.ru/docs-api/methods/POST/messages>
- <https://dev.max.ru/docs/chatbots/bots-create/manage> - "Поменять данные бота можно только на платформе MAX
  для партнёров (отредактировать их в мессенджере MAX не получится) и только после прохождения модерации."
- <https://help.max.ru/help/bots> - the user-facing bot help, no display setting.

## What the schema holds

The whole API is **13 paths**, and there is no settings resource among them:

```
/me                       GET    (PATCH is /me/commands, not /me)
/me/commands              PATCH
/chats/{chatId}           GET, PATCH (editChat), POST (sendAction)
/subscriptions            ...
/uploads                  ...
/messages                 GET, POST
/messages/{messageId}     PUT, DELETE
/messages/{messageId}/comments        ...
/messages/{messageId}/comments/{id}   ...
/videos/{videoToken}      ...
/answers                  POST
/updates                  ...
```

`disable_link_preview` occurs **exactly three times** in 3814 lines:

| Where | Line | Wording in the schema |
|---|---|---|
| `post /messages` | 1084 | "If `false`, server will not generate media preview for links in text" |
| `post /messages/{messageId}/comments` | 1274 | "If `false`, server will not generate media preview for links in text" |
| `post /answers` | 1454 | "If `true`, server will not generate media preview for links in updated message text" |

**The schema contradicts itself on the polarity.** Two of the three say `false` turns previews off, one says
`true`, and our live probes on 2026-10-18 settled it: **`true` turns them off**. The `false` wording is a
documentation error - and a trap, because a developer who follows it sends `false` and gets a card.

## The bot profile has nowhere to put such a setting

`GET /me` on the live bot answers with `first_name`, `name`, `username`, `description`, `avatar_url`,
`full_avatar_url`, `is_bot`, `last_activity_time`, `commands` and `user_id`. The `BotInfo` object in the
schema carries the same fields. There is no `settings`, no `preferences`, no display flag - and no
`PATCH /me`: the only patch on `/me`-anything is `PATCH /me/commands`, whose body is a command list up to 32
entries.

## What this leaves

1. **The card** (server side) is controlled only per request, and only in three methods. On the edit path the
   reference does not even list the parameter, but the API honours it - see
   [../findings/2026-10-18-link-previews.md](../findings/2026-10-18-link-previews.md).
2. **The hyperlink** (client side) has no server-side control at all: the text comes back from the API
   unchanged, so the link is drawn at render time from the raw text. The only lever is the text itself - the
   zero-width space after the dot, which is what `nopreview.go` does. Measured evidence, including a person's
   own message getting a `share` attachment, is in
   [../findings/2026-10-18-what-max-does-to-any-text.md](../findings/2026-10-18-what-max-does-to-any-text.md).
3. **The client settings** the operator might have meant have no documented switch either: what people do
   instead is forward a post to a third-party bot that strips the preview (vc.ru, 2026-07-14), which is the
   workaround of a missing setting, not a setting.

## Worth reporting upstream

The `false`/`true` contradiction above is a defect in the official schema, not in this gateway. An issue on
`max-messenger/api-schema` would be the honest thing to file; it is not filed yet and needs the operator's
word, because it is a public action.
