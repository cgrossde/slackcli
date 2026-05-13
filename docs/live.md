# slackcli live

Stream real-time Slack events to stdout via the Slack WebSocket gateway.

## Synopsis

```
slackcli live --workspace <domain> [flags]
slackcli live types
```

## Description

`slackcli live` connects to the Slack real-time messaging gateway and writes
events to stdout as they arrive. Each event is one formatted block, separated
by a blank line. The format is designed for LLM consumption: grep-friendly
type tags in brackets, copy-pasteable `slackcli read` references on every
message event.

The command runs until you press `Ctrl+C` (SIGINT) or it receives SIGTERM.
On exit, the standard presenter footer is written: `[exit:0 | 4m23s]`.

On WebSocket disconnect, the command automatically retries up to 3 times with
exponential backoff (1 s, 3 s, 9 s). If all retries fail, the command exits
non-zero.

Credentials must already be saved:

```
slackcli auth login --workspace myorg.slack.com
```

## Flags

| Flag | Short | Default | Description |
|---|---|---|---|
| `--workspace` | `-w` | stored default | Workspace domain (e.g. `myorg` or `myorg.slack.com`); uses stored default when omitted |
| `--channel` | `-c` | (all) | Filter to channel(s) by name (e.g. general). Repeatable. |
| `--from` | `-f` | (all) | Filter to events from a specific user (display name or ID). |
| `--type` | `-t` | (all) | Filter to event type(s). Repeatable. See `slackcli live types`. |
| `--json` | | off | Output events as NDJSON (one object per line); suppresses presenter footer. |
| `--mention` | | off | Only show events relevant to you: messages that mention `<@YOURUID>`, or replies in threads you have participated in. Calls `auth.test` at startup; fetches the thread root on every reply event to check `reply_users`. |

## Output Format

```
[message] #general · alice (u123456) · 2024-06-12 14:32:05
  We rolled back the deployment after the latency spike.
  → slackcli read C012ABC:1718197925.001234

[reaction_added] #general · bob · 2024-06-12 14:33:01
  :thumbsup: on message by alice (u123456)
  → slackcli read C012ABC:1718197925.001234

[message:thread_reply] #general · carol · 2024-06-12 14:35:00
  What was the root cause?
  → slackcli read C012ABC:1718197926.000001
```

### Attachments

When a message contains attachments (link unfurls, forwarded DM previews), each attachment is rendered as an indented block after the message text:

```
[message] #general · Alice Smith · 2026-05-19 10:16:15
  Review
  [attachment] U314838
  Hi, can you pls review https://github.example.com/org/mobile-sdk/pull/3218
  Thanks
  → https://myorg.slack.com/archives/D0123ABC/p1779178003000100
  → slackcli read C0B3Z1KT80K:1779179004.666099
```

### Type tags

| Tag | Meaning |
|---|---|
| `[message]` | New top-level message |
| `[message:thread_reply]` | Reply inside a thread |
| `[message:message_changed]` | Message edited |
| `[message:message_deleted]` | Message deleted |
| `[reaction_added]` | Emoji reaction added |
| `[reaction_removed]` | Emoji reaction removed |
| `[member_joined_channel]` | User joined a channel |
| `[member_left_channel]` | User left a channel |
| `[channel_created]` | Channel created |
| `[channel_deleted]` | Channel deleted |
| `[channel_rename]` | Channel renamed |
| `[team_join]` | New workspace member |
| `[desktop_notification]` | Unread mention / push notification |

Message text longer than 200 characters is truncated with `…`.

## Examples

```sh
# Stream everything in a workspace
slackcli live --workspace myorg.slack.com

# Watch a single channel
slackcli live --workspace myorg.slack.com --channel general

# Watch multiple channels
slackcli live --workspace myorg.slack.com -c general -c engineering

# Watch a specific user across all channels
slackcli live --workspace myorg.slack.com --from alice

# Only messages (no reactions, membership changes, etc.)
slackcli live --workspace myorg.slack.com --type message

# Multiple event types
slackcli live --workspace myorg.slack.com -t message -t reaction_added

# Pipe through grep for keyword monitoring
slackcli live --workspace myorg.slack.com | grep '\[message\]' | grep -i 'deploy'

# Read a message that appeared in the live stream
slackcli read C012ABC3456:1718197925.001234

# Only events that directly mention you
slackcli live --workspace myorg.slack.com --mention

# Mentions in a specific channel
slackcli live --workspace myorg.slack.com --channel incidents --mention
```

## JSON Output (`--json`)

```sh
slackcli live --workspace myorg.slack.com --json
slackcli live --workspace myorg.slack.com --channel general --json | jq -r '.text'
```

Each event is emitted as one JSON object per line immediately when received. No presenter footer is written; the stream runs until Ctrl+C (exit 0) or a fatal WebSocket error (stderr + exit non-zero).

### Record fields

| Field | Type | Notes |
|---|---|---|
| `type` | string | Event type (e.g. `"message"`, `"reaction_added"`) |
| `subtype` | string | Message subtype (e.g. `"message_changed"`); empty string when absent |
| `channel_id` | string | Channel or DM ID |
| `channel_name` | string | Channel name from `chanNames` map; empty string when not available |
| `user_id` | string | User who triggered the event |
| `username` | string | Slack handle from cache; empty string when not cached |
| `display_name` | string | Human name from cache (e.g. `"Alice Example (alice)"`); empty when not cached |
| `ts` | string | Event timestamp |
| `thread_ts` | string | Thread root timestamp (empty string for top-level messages) |
| `text` | string | Message body (full text, not truncated) |
| `reaction` | string | Emoji name without colons (e.g. `"thumbsup"`); omitted for non-reaction events |
| `item_ts` | string | Timestamp of the message that was reacted to; omitted for non-reaction events |
| `attachments` | array | Link unfurls and forwarded message previews; omitted when empty. Each object: `author_name`?, `author_link`?, `title`?, `title_link`?, `pretext`?, `text`?, `from_url`?, `service_name`?, `image_url`?, `thumb_url`?, `footer`? |

All fields except `reaction`, `item_ts`, and `attachments` are always present; fields with no value are the zero string `""`. `reaction`, `item_ts`, and `attachments` are omitted entirely when not applicable.

## Subcommands

### `slackcli live types`

List all supported real-time event types with descriptions.

```
slackcli live types
```

## Architecture notes

**Gateway URL**: `client.userBoot` is called once at startup with the xoxc
token and xoxd cookie. The response contains a `workspaces` array; the team ID
for the requested workspace is extracted and used to build the WebSocket URL:

```
wss://wss-primary.slack.com/?token=<xoxc>&gateway_server=<team_id>
```

The gateway URL is reused across reconnects — no re-fetch needed.

**Event filtering**: the WebSocket layer uses an allowlist
(`slack.AllowedEventTypes`). Any event type not in the list — including
`user_typing`, `presence_change`, `file_deleted`, and all other noise — is
silently dropped before reaching the formatter. To see all allowed types:
`slackcli live types`.

**User display names** are resolved from a local filesystem cache
(`~/.cache/slackcli/users-<workspace>.json`). Cache misses trigger a
`users.info` API call. If the cache cannot be loaded, raw user IDs are shown.

**Channel resolution for reaction events**: the Slack RTM gateway does not
include a top-level `channel` field on `reaction_added` / `reaction_removed`
events. Instead, the channel is nested inside `item.channel` (the channel of
the message that was reacted to). `slackcli live` transparently promotes
`item.channel` to the top-level channel field so that `--channel` filtering and
output display work identically for reactions and messages.

**`--mention` filter**: calls `auth.test` once at startup to resolve the
authenticated user's Slack user ID. An event is accepted if either:

1. The resolved self-ID appears in `Event.Mentions` (direct `<@UXXXX>` mention in the message text), or
2. The event is a thread reply (`thread_ts` ≠ `ts`) and `GetMessage(channel, thread_ts)` returns a root whose `reply_users` slice contains the self-ID.

The thread root fetch happens on every reply event that passes other filters; there is no local caching. Fetch errors are logged as warnings and fail open (the event is not shown). The `reply_users` field is populated by Slack on any thread root that has at least one reply.

**Channel names**: displayed as-is from the event payload where present;
falls back to the raw channel ID otherwise.

**TLS**: the WebSocket connection uses the same uTLS Chrome fingerprint as the
HTTP client, with the xoxd cookie injected on the upgrade request.