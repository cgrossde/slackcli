# read

Fetch and print a Slack message or full thread, or download a file attachment.

## Usage

The `read` command accepts four forms of message reference:

```
slackcli read <permalink-url>
```

A Slack permalink URL. Workspace is extracted from the URL.

```
slackcli read <channelID>:<ts>
```

Channel ID and message timestamp. Workspace is resolved via `--workspace` flag, stored default (`slackcli auth default`), or the single saved workspace when only one exists.

```
slackcli read <channelID>:<threadTs>:<replyTs>
```

Three-part form: fetches the thread rooted at `threadTs` and carries `replyTs` as the specific reply that triggered the read. Equivalent to `slackcli read <channelID>:<replyTs> --thread-ts <threadTs>`. Produced automatically by `slackcli activity` for `thread_v2` items.

```
slackcli read <file-permalink-url>
```

A Slack file permalink URL. Downloads the file to disk. Workspace is inferred from the URL.
## Flags

- `-h, --help`: Show help for the read command
- `--json`: Output messages as NDJSON (one object per line)
- `--pretty`: Render output with ANSI colours and markdown formatting
- `-w, --workspace string`: Workspace for `<channelID>:<ts>` references. Defaults to stored default or sole saved workspace.
- `-o, --output string`: Output path for file downloads. Default: `./<filename>` from the file's name.
- `--thread-ts string`: Thread root timestamp. When set alongside a `<channelID>:<replyTs>` positional arg, fetches the thread rooted at `threadTs` without an extra API round-trip. Overrides any `threadTs` already embedded in a three-part ref.
## Arguments
`<url|channelID:ts|channelID:threadTs:replyTs|file-permalink>` (required) accepts four forms:

### Permalink URL

A full Slack message permalink:

```
https://myorg.slack.com/archives/C012ABC/p1718197925001234
```

The URL must contain the channel ID (e.g., `C012ABC`) and a timestamp segment starting with `p` followed by 13 digits (e.g., `p1718197925001234`). The last 6 digits are the fractional part. For example, `p1718197925001234` decodes to timestamp `1718197925.001234`.

Workspace is extracted from the URL; `--workspace` is ignored.

### Channel ID and Timestamp

A channel ID and message timestamp separated by a colon:

```
C012ABC3456:1718197925.001234
```

Workspace resolution for `<channelID>:<ts>` form proceeds in this order:
1. If `--workspace` flag is set, use that workspace
2. If a default workspace is stored (`slackcli auth default --workspace <name>`), use it
3. If exactly one workspace is saved, use it
4. Otherwise, fail with an error

### Channel ID, Thread Root, and Reply Timestamp

Three-part compact ref for reading a specific reply in context:

```
C012ABC3456:1718197000.000001:1718197925.001234
```

`threadTs` is the thread root; `replyTs` is the specific reply. The full thread is fetched in one call (no prefetch round-trip). This form is emitted by `slackcli activity` for `thread_v2` items. You can also express this as:

```
slackcli read C012ABC3456:1718197925.001234 --thread-ts 1718197000.000001
```
### File Permalink URL

A Slack file permalink:

```
https://myorg.slack.com/files/WH1K7QTFU/F0B3HRU6ZA7/image.png
```

Downloads the file using authenticated `files.info` + `url_private`. Saved to `--output` path or `./<filename>` by default. Workspace is extracted from the URL; falls back to the default stored workspace for Slack Enterprise Grid orgs where the file URL domain differs from the login workspace domain.

## Output

Messages are printed as plain text. Each message in the thread has a 120-character separator line, followed by the message body, optional file and reaction lines, and a blank line.

Format for each message in the thread:

```
== Alice Example (alice) 2026-05-12 14:32 ══════════════════════[ message ]==
We rolled back the deployment after the latency spike.

== Bob (bob) 2026-05-12 14:35 ════════════════════════════════[ reply 1 ]==
Thanks for the quick fix.

```

Header format: `== <author> <timestamp> ═══…═══[ message ]==` (exactly 120 chars wide, padded with `═` between the timestamp and the right-aligned label). The label is `[ message ]==` for the thread root and `[ reply N ]==` for each reply.

Author format:
- Regular user: `DisplayName (handle)` — e.g. `Alice Example (alice)`
- Bot or unknown display name: `handle (bot)` or raw user ID
- Unknown: `(unknown)`

Timestamp: UTC, formatted as `YYYY-MM-DD HH:MM` (no seconds, no timezone label).

If `--pretty` is set, output includes ANSI colours and markdown formatting.

### File and reaction output

When a message has file attachments, each file appears as:

```
  [file] image.png (PNG)
  → slackcli read https://myorg.slack.com/files/WH1K7QTFU/F0B3HRU6ZA7/image.png
```

In `--pretty` mode, image files (MIME type `image/*`) are rendered inline when running in iTerm2 (`LC_TERMINAL=iTerm2`). The image is sized to its natural width as a percentage of terminal width, capped at 100%.

When a message has emoji reactions, they appear as:

```
  Reactions: 👍 3  👌 1
```

(plain text uses `:thumbsup: ×3` form)

### Attachment output

When a message has attachments (forwarded DM previews, link unfurls), each attachment is rendered as an indented block:

```
  [attachment] Alice Smith
  Hi, can you pls review https://github.example.com/org/mobile-sdk/pull/3218
  Thanks
  → https://github.example.com/org/mobile-sdk/pull/3218
```

The header line combines `author_name` and `title` (separated by ` — `) when both are present. If only one is present, it appears alone. The URL shown is `title_link` if set, otherwise `from_url`.


### File download output

When downloading a file:

```
Saved: image.png (142938 bytes)
[exit:0 | 843ms]
```


## JSON Output (`--json`)

```
slackcli read C012ABC3456:1718197925.001234 --json
slackcli read https://myorg.slack.com/archives/C012ABC/p1718197925001234 --json
```

Each message in the thread is emitted as one JSON object per line. No presenter footer is written to stdout; errors go to stderr only. The full thread is always returned — there is no pagination.

### Record fields

| Field | Type | Notes |
|---|---|---|
| `user_id` | string | Author's Slack user ID |
| `username` | string | Slack handle or bot name |
| `display_name` | string | Human name from cache (empty if not cached) |
| `ts` | string | Message timestamp (e.g. `"1718200320.123456"`) |
| `thread_ts` | string | Thread root timestamp (same as `ts` for root message) |
| `text` | string | Full message text |
| `is_root` | bool | `true` for the first message (the thread root) |
| `reply_count` | int | Reply count; omitted when 0 |
| `channel_id` | string | Channel or DM ID |
| `channel_type` | string | `"channel"`, `"dm"`, or `"group"` (inferred from channel ID prefix) |
| `files` | array | File attachments; omitted when empty. Each object: `id`, `name`, `pretty_type`?, `mimetype`?, `permalink`?, `url_private`? |
| `reactions` | array | Emoji reactions; omitted when empty. Each object: `name`, `count`, `users`? (user ID list) |
| `attachments` | array | Link unfurls and forwarded message previews; omitted when empty. Each object: `author_name`?, `author_link`?, `title`?, `title_link`?, `pretext`?, `text`?, `from_url`?, `service_name`?, `image_url`?, `thumb_url`?, `footer`? |

## Thread Behavior

The command always prints the full thread:

- **Standalone messages**: If the target message is not part of a thread, only that message is printed.
- **Thread roots**: If the message is a thread root (`thread_ts == ts` and `reply_count > 0`), all replies are fetched and printed.
- **Replies**: If the message is a reply to a thread (`thread_ts != ts`), the full thread starting from the root is fetched and printed.

Thread pagination is handled automatically; up to 200 messages are fetched per page.

## Channel URL redirect

If you pass a channel-only URL (no message timestamp), `read` exits with a helpful error instead of failing silently:

```
[stderr] this is a channel link, not a message permalink. To read recent channel messages:
  slackcli history C0B3Z1KT80K
To read a specific message, right-click it in Slack → Copy link.
[exit:1 | 21µs]
```

The channel ID is extracted from the URL automatically. Use `slackcli history` to fetch recent messages from a channel.

## Authentication

Credentials for the workspace must be saved beforehand via `slackcli auth login`. The command reads these credentials to authenticate API requests.

### Enterprise Grid

On Enterprise Grid, a single xoxc/xoxd credential works across all member workspaces. When a bare channel ID is given (not a full URL), the channel may belong to a sibling workspace. `read` handles this automatically:

1. **Channel cache** — `~/.cache/slackcli/channels.json` maps channel IDs to the workspace that last served them. On a cache hit the correct workspace is used directly.
2. **Grid retry** — On `ErrMessageNotFound`, the sibling workspaces stored in the keychain entry (`grid_workspaces`, populated at login or via `auth workspaces`) are tried in order.
3. **Fallback** — If no grid metadata is stored, all saved workspaces are tried (legacy behaviour).
4. **Workspace header** — When the channel was resolved via a sibling workspace, a `[workspace: X]` header is prepended to the output; in `--json` mode a `"workspace"` field is added to each record.

## Implementation

- `cmd/read.go`: `ReadMessage`, `ReadMessagePretty`, `ReadMessageJSON`, `ReadFile` (command handlers); `downloadFile` performs the actual download via the `fileClient` interface
- `cmd/pretty.go`: `PrettyThread` — ANSI rendering, iTerm2 inline image protocol
- `cmd/iterm2.go`: iTerm2 OSC 1337 image sequence, terminal size detection
- `internal/slack/conversations.go`: `GetMessage`, `GetThread`, `Message.Files`, `Message.Reactions`, `Message.Attachments`
- `internal/slack/client.go`: `FetchFileBytes`, `GetFileInfo`
- `internal/slack/url.go`: `ParseMessageRef`, `ParseFileRef`, `IsFileURL`; `ParseChannelURL`, `IsChannelURL` (channel URL detection for redirect)