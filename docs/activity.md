# activity

Show your Slack Activity feed — the same items shown in the Activity panel in the Slack client.

Includes @mentions, emoji reactions on your messages, thread replies, DMs, keyword alerts, and channel invites. Message text is fetched automatically so each item is immediately actionable.

## Usage

```
slackcli activity [flags]
```

## Flags

```
      --cursor string      Pagination cursor from a previous response
      --json               Output as NDJSON (one object per line)
  -n, --count int          Items per request (1–50, default 20)
  -t, --type string        Filter by type alias or raw API name (comma-separated)
      --unread             Show only unread activity
  -w, --workspace string   Workspace (required if >1 saved)
  -h, --help               help for activity
```

## Type aliases

The `--type` flag accepts user-friendly shorthand names as well as raw Slack API type names.

| Alias | Expands to |
|---|---|
| `reaction` | `message_reaction` |
| `thread` | `thread_v2` |
| `mention` | `at_user` |
| `dm` | `dm` |
| `keyword` | `keyword` |
| `group_mention` | `at_user_group` |
| `channel_mention` | `at_channel,at_everyone` |
| `invite` | `internal_channel_invite,external_channel_invite` |

Multiple values are comma-separated: `--type reaction,mention,thread`

Raw API type names (`list_record_edited`, `bot_dm_bundle`, etc.) also work verbatim. When `--type` is omitted all activity types are returned, matching Slack's default UI behaviour.

## Plain-text output

```
[1] #ops · Alice Johnson reacted :thumbsup: · 2025-05-14 09:23
    your message text here (truncated to ~200 runes)...
    → slackcli read C012ABC3:1718197800.000100

[2] #dev · Bob Smith replied in thread · 2025-05-14 09:20
    latest reply text...
    → slackcli read C045DEF6:1718197900.000200

[3] #general · Carlos mentioned you · 2025-05-14 09:15
    hey @you can you look at this?
    → slackcli read C078GHI9:1718197850.000300

[4] @Eve · Eve · DM · 2025-05-14 09:10
    hey, got a minute?
    → slackcli read D091JKL2:1718197800.000400

--- 20 items | next: slackcli activity --cursor dXNlcjpVMDYx ---
```

Each item shows:
- **Header:** `[N] channel · description · timestamp`
- **Body:** message text preview (truncated to ~200 runes), indented 4 spaces
- **Ref:** `→ slackcli read <channel_id:ts>` for immediate follow-up

Use `slackcli read <channel_id:ts>` to fetch the full thread for any item.

## JSON output (`--json`)

One JSON object per line (NDJSON). No presenter footer.

```json
{"type":"message_reaction","feed_ts":"1718197925.001234","is_unread":true,"channel_id":"C012ABC3","channel_name":"#ops","ts":"1718197800.000100","thread_ts":"","user_id":"U123","username":"alice","display_name":"Alice","text":"your message...","reaction":"thumbsup","reactor_id":"U456","reactor_name":"Bob"}
```

### Fields

| Field | Description |
|---|---|
| `type` | Activity type (raw API name) |
| `feed_ts` | When the activity happened (Slack timestamp) |
| `is_unread` | Whether you have seen it |
| `channel_id` | Channel or DM ID — use with `ts` for `slackcli read` |
| `channel_name` | Resolved `#name` or `@user` |
| `ts` | Message timestamp — use with `channel_id` for `slackcli read` |
| `thread_ts` | Parent thread ts (empty if top-level message) |
| `user_id` | Actor who triggered the activity |
| `username` / `display_name` | Resolved actor name |
| `text` | Message body preview (truncated to ~200 runes) |
| `reaction` | Emoji name without colons (only for `message_reaction`) |
| `reactor_id` / `reactor_name` | Who reacted (only for `message_reaction`) |

When more items are available a pagination trailer is emitted as the final line:

```json
{"_pagination":{"has_more":true,"next_cursor":"dXNlcjpVMDYx"}}
```

## Pagination

Pass the cursor from a previous response to fetch the next page:

```
slackcli activity --cursor dXNlcjpVMDYx
slackcli activity --cursor dXNlcjpVMDYx --json
```

The plain-text footer includes the full next-page command with all flags preserved:

```
--- 20 items | next: slackcli activity --type reaction --cursor dXNlcjpVMDYx ---
```

## Examples

```
# All recent activity
slackcli activity

# Unread only
slackcli activity --unread

# Reactions and mentions only
slackcli activity --type reaction,mention

# Thread replies only, JSON output
slackcli activity --type thread --json

# First 5 items as JSON
slackcli activity --json --count 5

# Paginate
slackcli activity --json --cursor dXNlcjpVMDYx

# Raw API type name
slackcli activity --type list_record_edited
```
