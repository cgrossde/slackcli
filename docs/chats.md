# chats

List recent DMs, group chats, and channels sorted by last activity.

Conversations are returned most-recently-active first. By default only DMs and group DMs are shown (`--type all`); pass `--type all-with-channels` or `--type channel` to include joined channels.

## Usage

```
slackcli chats [flags]
```

## Flags

```
  -t, --type string        Filter type: all (DMs+MPDMs, default), dm, mpdm, channel, all-with-channels, unread
  -n, --count int          Number of conversations (1–200, default 20)
      --cursor string      Pagination cursor from a previous response
      --json               Output as NDJSON (one object per line)
  -w, --workspace string   Workspace (default: stored default)
  -h, --help               help for chats
```

## Type modes

| Value | What is returned |
|---|---|
| `all` | DMs + MPDMs (default; fast path via `users.conversations`) |
| `dm` | 1:1 direct messages only |
| `mpdm` | Multi-party DMs only |
| `channel` | Joined channels only (via `client.counts`) |
| `all-with-channels` | DMs + MPDMs + joined channels |
| `unread` | All conversations with unread messages |

`channel`, `all-with-channels`, and `unread` use the heavier `client.counts` / `userBoot` path and may be slightly slower than `all`, `dm`, or `mpdm`.

## Output format

### Plain text (default)

Each conversation is printed on one line with its Slack ID, type, display name, most-recent-message timestamp, and optional unread/starred indicators:

```
D09SD70E1HU  dm      @Alice Johnson                            2026-06-12 09:00  [2 mention(s)]
G0B3PCPL0CF  mpdm    alice, bob, carol                         2026-06-11 18:30
D012BOTCHAN  dm      @Bot Name                                 2026-06-10 14:00  [unread]
C0B3PCPL0CF  channel #general                                  2026-06-09 17:15  *

--- 4 chats | has_more: false
    Tip: slackcli history <id> to read messages
```

Unread indicators:

- `[N mention(s)]` — one or more unread @mentions
- `[unread]` — unread messages, no direct mentions
- `*` — conversation is starred

When more results exist, a cursor hint is shown:

```
--- 20 chats | has_more: true
    next: slackcli chats --cursor dXNlcjpVMDYx
    Tip: slackcli history <id> to read messages
```

### JSON (`--json`)

Emits one JSON object per conversation:

```json
{"id":"D09SD70E1HU","type":"dm","name":"@Alice Johnson","peer_id":"U09SD70PEER","latest_ts":"1749722400.001234","has_unreads":true,"mention_count":2}
{"id":"G0B3PCPL0CF","type":"mpdm","name":"alice, bob, carol","raw_name":"mpdm-alice--bob--carol-1","member_ids":["U09SD70E1HU","U09BOBXXXX","U09CAROLXX"],"latest_ts":"1749657000.005678"}
{"id":"D012BOTCHAN","type":"dm","name":"@Bot Name","peer_id":"U012BOTUSER","latest_ts":"1749564000.009012","has_unreads":true}
```

#### JSON field schema

| Field | Type | Notes |
|---|---|---|
| `id` | string | Slack channel/conversation ID |
| `type` | string | `dm`, `mpdm`, or `channel` |
| `name` | string | Display name (e.g. `@Alice`, `alice, bob, carol`, `#general`) |
| `raw_name` | string | Raw internal name; omitted when empty |
| `peer_id` | string | For DMs: the peer user ID; omitted for others |
| `member_ids` | []string | For MPDMs: participant user IDs; omitted when empty |
| `latest_ts` | string | Timestamp of most recent message; omitted when empty |
| `is_starred` | bool | `true` when starred; omitted when `false` |
| `has_unreads` | bool | `true` when unread messages exist; omitted when `false` |
| `mention_count` | int | Unread @mention count; omitted when zero |

When more results exist, a pagination trailer is appended as the final line:

```json
{"_pagination":{"has_more":true,"next_cursor":"dXNlcjpVMDYx"}}
```

In JSON mode the presenter footer (`[exit:N | Xms]`) is suppressed.

## Pagination

```sh
# First page (default 20)
slackcli chats -n 50

# Next page using cursor from footer
slackcli chats -n 50 --cursor dXNlcjpVMDYx

# JSON pagination
slackcli chats --json -n 50
slackcli chats --json -n 50 --cursor dXNlcjpVMDYx
```

## Reading messages from a chat

`chats` returns conversation IDs. Pass the ID to `history` to read messages:

```sh
# List recent chats
slackcli chats

# Read messages from one of them
slackcli history D09SD70E1HU
slackcli history G0B3PCPL0CF -n 50
```

## Examples

```sh
# DMs and group DMs (default)
slackcli chats

# Only unread conversations
slackcli chats --type unread

# All conversations including channels, top 50
slackcli chats --type all-with-channels -n 50

# 1:1 DMs only, JSON output
slackcli chats --type dm --json

# Joined channels, different workspace
slackcli chats --type channel -w myorg
```

## Implementation notes

- **`all` / `dm` / `mpdm`** — resolved via `users.conversations` (`ListConversations` in `internal/slack/conversations.go`). Faster; does not require the full workspace boot.
- **`channel` / `all-with-channels` / `unread`** — resolved via `client.counts` / `userBoot` (`GetChannelCounts`, `GetChannelDirectory` in `internal/slack/channels.go`). Includes joined channel metadata.
- Display names for DMs are resolved from the workspace user cache; MPDMs use member user IDs resolved to display names. Channels whose name is not in the cache are resolved via `conversations.info` (`ChannelInfo` in `internal/slack/channels.go`).
- Results are sorted by `latest_ts` descending before formatting; entries with no timestamp sort to the bottom.
