# history

Fetch and print recent messages from a Slack channel.

Messages are returned newest-first (most recent at the top), matching the `conversations.history` API order.

## Usage

```
slackcli history [<channel-url> | <channelID> | <channel-name>] [flags]
```

## Flags

```
      --channel string     Channel ID, name, or URL
  -n, --count int          Number of messages to fetch (1–200, default 25)
      --before string      Only messages before this date
      --after string       Only messages after this date
      --cursor string      Pagination cursor from a previous response
      --pretty             Render with ANSI colours and markdown formatting
      --json               Output messages as NDJSON (one object per line)
  -w, --workspace string   Workspace (default: stored default)
  -h, --help               help for history
```

## Accepted channel forms

```
slackcli history https://myorg.slack.com/archives/C0B3Z1KT80K
slackcli history C0B3Z1KT80K
slackcli history general
slackcli history --channel C0B3Z1KT80K
```

When a channel URL is provided, the workspace is extracted from it and `--workspace` is ignored. For bare channel IDs or names, the workspace is resolved from `--workspace` or the stored default.

## Date flags (`--before` / `--after`)

Both flags accept the same date formats as `search`:

| Input | Meaning |
|---|---|
| `YYYY-MM-DD` | Absolute date |
| `Nd` | N days ago |
| `Nw` | N weeks ago |
| `Nm` | N months ago |
| `Ny` | N years ago |
| `today` | Start of today |
| `yesterday` | Start of yesterday |
| `monday`–`sunday` | Most recent past occurrence of that weekday |

Dates are resolved to epoch timestamps at midnight UTC and passed to the `conversations.history` API as `oldest` (`--after`) and `latest` (`--before`).

## Output format

### Plain text (default)

Each message is rendered with the standard 120-character header line used by `read`:

```
== Alice (alice) 2026-05-16 09:00 ════════════════════════════════[ message ]==
Hey team, standup starting now.
  → slackcli read C0B3Z1KT80K:1716847200.000001

== Bob (bob) 2026-05-16 08:45 ══════════════════════════════════[ message ]==
PR is ready for review.
  → slackcli read C0B3Z1KT80K:1716846300.000002
  [3 replies]
  [file] screenshot.png (PNG)
  → slackcli read https://myorg.slack.com/files/W123/F456/screenshot.png

--- 2 messages | has_more: false
    Tip: slackcli read <channel>:<ts> for full thread
```
 When a message has attachments, each is rendered as an indented block after reactions:
 
 ```
   [attachment] U314838
   CR Please: https://github.example.com/org/mobile-sdk/pull/3223 [Branchfix]
   → https://github.example.com/org/mobile-sdk/pull/3223
 ```


Thread roots show a reply count indicator: `[N replies]` after the body.

When more messages exist:

```
--- 25 messages | has_more: true
    next: slackcli history C0B3Z1KT80K --cursor dXNlcjpVMDYx
    Tip: slackcli read <channel>:<ts> for full thread
```

### Pretty (`--pretty`)

Renders with ANSI colours, markdown formatting, and inline images (iTerm2).

### JSON (`--json`)

Emits one JSON object per message, using the same schema as `read --json`:

```json
{"user_id":"U123","username":"alice","display_name":"Alice","ts":"1718197925.001234","thread_ts":"","text":"Hey team","is_root":false,"reply_count":0,"channel_id":"C0B3Z1KT80K","channel_type":"channel"}
```
 When a message has attachments (forwarded DM previews, link unfurls), an `"attachments"` array is included. Each attachment object contains any of: `author_name`, `author_link`, `title`, `title_link`, `pretext`, `text`, `from_url`, `service_name`, `image_url`, `thumb_url`, `footer` (all omitted when empty).

When more messages exist, a pagination trailer is appended as the final line:

```json
{"_pagination":{"has_more":true,"cursor":"dXNlcjpVMDYx"}}
```

In JSON mode the presenter footer (`[exit:N | Xms]`) is suppressed.

## Pagination

```sh
# First page
slackcli history C0B3Z1KT80K -n 50

# Next page using cursor from footer
slackcli history C0B3Z1KT80K -n 50 --cursor dXNlcjpVMDYx
```

## Reading a thread

`history` shows channel-level messages only. To expand a thread:

```sh
slackcli read C0B3Z1KT80K:1718197925.001234
```

## Redirect from `read`

If you accidentally pass a channel URL (no message timestamp) to `read`, it prints a helpful redirect:

```
This is a channel link, not a message permalink. To read recent channel messages:
  slackcli history C0B3Z1KT80K
To read a specific message, right-click it in Slack → Copy link.
```

## Enterprise Grid

When a bare channel ID is used (not a URL), the channel may belong to a sibling workspace on Enterprise Grid. `history` handles this transparently:

1. **Channel cache** — `~/.cache/slackcli/channels.json` is checked first; on a hit the correct workspace is used directly without a retry.
2. **Grid retry** — On `channel_not_found`, sibling workspaces from `grid_workspaces` in the keychain entry are tried in order.
3. **Fallback** — If no grid metadata is stored, all saved workspaces are tried (legacy behaviour).

The `"workspace"` field in `--json` output reflects the workspace that actually served the request.
