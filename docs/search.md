# search

Search Slack messages (default), channels by name (`--channels`), or users (`--users`).

## Usage

```
slackcli search [keywords] [flags]
slackcli search --channels <query> [flags]
slackcli search --users <query> [flags]
```

`[keywords]` is one or more search terms for message search. `--channels` and `--users` are mutually exclusive mode flags; when neither is set, message search runs.

## Flags

### Mode flags (mutually exclusive)

```
      --channels           Search channels by name (mutually exclusive with --users)
      --users              Search users by name, employee ID, or email (mutually exclusive with --channels)
```

### Shared flags

```
  -n, --count int          Results per page (1–100, default 20)
      --json               Output results as NDJSON (one object per line)
  -w, --workspace string   Workspace to search (required if >1 saved)
  -h, --help               help for search
```

### Message search flags (ignored in --channels / --users mode)

```
  -c, --channel string     Restrict to channel (e.g. general, ops)
  -f, --from string        Restrict to messages from user (@alice, U012AB3CD)
      --after string       Messages after date (YYYY-MM-DD, Nd/Nw/Nm/Ny, or day name)
      --before string      Messages before date (YYYY-MM-DD, Nd/Nw/Nm/Ny, or day name)
  -p, --page int           Page number, 1-indexed (default 1)
      --sort string        Sort field: "score" (default) or "timestamp"
      --asc                Sort ascending (default is descending)
      --in-dm              Restrict to direct messages (is:dm)
      --in-channel         Restrict to public/private channels (is:channel)
      --with string        Restrict to conversations with user (e.g. @alice or U012ABC)
```

## Date Format

`--after` and `--before` accept either an absolute date or a relative duration:

| Input | Meaning |
|---|---|
| `2024-01-15` | Absolute date (YYYY-MM-DD) |
| `2d` | 2 days ago |
| `1w` | 1 week ago |
| `3m` | 3 months ago (calendar months) |
| `1y` | 1 year ago |
| `today` | Today's date |
| `yesterday` | Yesterday's date |
| `monday`–`sunday` | Most recent past occurrence (case-insensitive) |

Relative durations and named days are resolved to a date at command execution time, then passed to Slack as `after:YYYY-MM-DD` / `before:YYYY-MM-DD` modifiers. The resolved dates are shown in the output header so results are reproducible.

## Query Construction

Flags are translated to Slack search modifiers and prepended to the keyword query in this order: keywords, `in:`, `from:`, `with:`, `is:`, `after:`, `before:`.

| Flag | Slack modifier |
|---|---|
| `--channel ops` | `in:#ops` |
| `--from alice` | `from:alice` |
| `--from U012AB3CD` | `from:<U012AB3CD>` |
| `--with alice` | `with:alice` |
| `--with @alice` | `with:alice` |
| `--with U012AB3CD` | `with:<U012AB3CD>` |
| `--in-dm` | `is:dm` |
| `--in-channel` | `is:channel` |
| `--after 2d` → `2024-06-11` | `after:2024-06-11` |
| `--before 1w` → `2024-06-06` | `before:2024-06-06` |

The full constructed query is printed in the output header so you can inspect and reproduce it manually.

## Slack Modifiers

You can pass Slack search modifiers directly in the keyword string for advanced queries:

| Modifier | Meaning |
|---|---|
| `has:link` | Messages with a URL |
| `has:reaction` | Messages with any emoji reaction |
| `is:dm` | Direct messages only |
| `is:channel` | Channel messages only |
| `with:@alice` | Conversations where alice participated |
| `-word` | Exclude messages containing word |
| `"exact phrase"` | Exact phrase match |

These are passed through as-is to Slack and can be combined in the keyword argument.

## Examples

### Message search

```
# Messages containing "deployment" in #ops from the last 2 days
slackcli search "deployment" --channel ops --after 2d

# Messages from Alice mentioning the API in the last week
slackcli search "API" --from alice --after 1w

# All messages matching "incident" between two absolute dates
slackcli search "incident" --after 2024-06-01 --before 2024-06-30

# Page 2 of results for "on-call", sorted by timestamp ascending
slackcli search "on-call" --sort timestamp --asc --page 2

# DM search: messages to/from bob in the last month
slackcli search "handover" --from bob --after 1m

# Messages with links in the last week
slackcli search "has:link" --after 7d

# Conversations with alice containing "decision"
slackcli search "decision" --with @alice

# Combined: exclude bots, find reactions in #incidents
slackcli search "-bot has:reaction" --channel incidents --after 2d
```

### Channel search

```
# Find channels whose name contains "general"
slackcli search --channels general

# Find ops-related channels, JSON output
slackcli search --channels ops --json

# Use a different workspace
slackcli search --channels infra --workspace myorg.slack.com
```

### User search

```
# Find users named Alice
slackcli search --users alice

# Find by employee ID (exact match)
slackcli search --users u123456

# Find by partial email
slackcli search --users @example.com

# JSON output
slackcli search --users alice --json
```

## Output Format

### Message search output

```
search: "deployment in:#ops after:2024-06-11"
total: 47  page: 1/3  (20 per page)

[1] #ops · Alice Example (alice) · 2024-06-12 14:32
    We rolled back the deployment after the latency spike.
    → slackcli read C012AB3CD:1718200320.123456

[2] #ops · bob · 2024-06-11 09:17
    Deployment window is 22:00 UTC tonight.
    → slackcli read C012AB3CD:1718113037.654321

...

--- page 1 of 3 | next: slackcli search --page 2 --channel ops --after 2024-06-11 "deployment" ---
Tip: slackcli read <channel>:<ts> fetches the full thread
Tip: pass Slack modifiers in the query — e.g. has:link  has:reaction  is:dm  with:@alice  -word
```

Fields per result:

- `[N]` — 1-indexed position within the current page
- Channel label, author display name (with Slack handle when known), and local timestamp
- Message text as returned by the API (Slack may truncate long messages server-side)
- `→ slackcli read <channelID>:<ts>` — the exact command to fetch the full thread

When total results exceed one page, the pagination line includes a ready-to-run next-page command with all active flags reconstructed. When there is no next page the line is just `--- page N of N ---`.

Two tip lines always follow the pagination line: the first reminding callers that `slackcli read` fetches the full thread for any result, and the second documenting the Slack modifiers available in the keyword argument.

When invoked with no keywords and no filter flags, `search` prints help to stdout followed by an error and exits with status 1:
```
[error] provide keywords or at least one filter flag (--channel, --from, --after, --before)
```

### Channel search output (`--channels`)

```
3 channel(s) matching "general":

[1] #general — 412 members — ID: C0B3PCPL0CF
    Topic: Company-wide announcements
    Purpose: The general channel
[2] #general-ops — 18 members — ID: C1234567890
    Topic: Ops discussions
[3] #general-archive [archived] — 0 members — ID: C9876543210
```

Fields per result:

- `[N]` — 1-indexed position
- Channel name, `[archived]` when applicable, member count, and Slack channel ID
- Topic and Purpose lines when present

When no channels match: `no channels matching "query"` (exit 0).

### User search output (`--users`)

```
[1] Alice Johnson (u123456) · alice.johnson@example.com · WH1K7QTFU

--- also found via Slack ---

[2] Carol White (u234567) · carol.white@example.com · W85KFV98Q
[3] Eve Turner (u567890) · eve.turner@example.com · U02ESS5LDB4
```

Users from the local cache appear first (no separator). Users found only via the Slack API appear after the `--- also found via Slack ---` separator.

When there are no results: `no users matching "query"` (exit 0).


## JSON Output (`--json`)

### Message search JSON

```
slackcli search "deployment" --channel ops --after 7d --json
slackcli search "deployment" --json --page 2
```

Each result is emitted as one JSON object per line (NDJSON). No presenter footer is written to stdout; errors go to stderr only.

#### Record fields

| Field | Type | Notes |
|---|---|---|
| `channel_id` | string | Slack channel ID |
| `channel_name` | string | Channel name or DM peer ID |
| `channel_type` | string | `"channel"`, `"dm"`, or `"mpim"` |
| `user_id` | string | Author's Slack user ID |
| `username` | string | Author's Slack handle |
| `display_name` | string | Human name from cache (empty if not cached) |
| `ts` | string | Raw Slack timestamp (e.g. `"1718200320.123456"`) |
| `thread_ts` | string | Thread root timestamp; omitted for top-level messages |
| `text` | string | Message text as returned by the API |
| `permalink` | string | Full permalink URL; omitted when the API does not return one |
| `dm_peer_id` | string | DM records only: peer's user ID |
| `participant_ids` | array | MPIM records only: participant handles |

#### Pagination trailer

When the current page is not the last, the final line is a trailer object:

```json
{"_pagination": {"next_page": 2, "has_more": true, "total": 47, "page": 1, "pages": 3}}
```

Pass `--page <next_page>` with the same query and filters to fetch subsequent pages. No trailer is emitted on the last page.

### Channel search JSON (`--channels --json`)

```
slackcli search --channels general --json
```

Each channel is emitted as one JSON object per line.

| Field | Type | Notes |
|---|---|---|
| `id` | string | Slack channel ID |
| `name` | string | Channel name (without `#`) |
| `topic` | string | Channel topic; omitted when empty |
| `purpose` | string | Channel purpose; omitted when empty |
| `member_count` | int | Current member count |
| `is_archived` | bool | True for archived channels |

### User search JSON (`--users --json`)

```
slackcli search --users alice --json
slackcli search --users u123456 --json
```

Each matching user is emitted as one JSON object per line. Cache results come first, then edge API results.

| Field | Type | Notes |
|---|---|---|
| `id` | string | Slack user ID (`W...` or `U...`) |
|| `name` | string | Slack handle / employee ID (e.g. `u123456`) |
|| `display_name` | string | Human name (e.g. `Alice Johnson`) |
| `email` | string | Email address (may be empty for lazily-populated cache entries) |
| `source` | string | `"cache"` (local cache) or `"edge"` (Flannel edge API) |
## Authentication

Requires a user token (xoxc). Bot tokens cannot use `search.messages` or `search.modules.channels` — they lack the required scopes. Credentials must be saved via `slackcli auth login` beforehand.

## Implementation

### Layer 1

`cmd/search.go` implements `Search(query string, flags SearchFlags) (string, error)`.

Dispatch:
1. If `--channels && --users` → error (mutually exclusive).
2. If `--channels` → `searchChannels(query, flags)` in `cmd/search_channels.go`.
3. If `--users` → `SearchUsers(query, workspace, jsonMode)` in `cmd/search_users.go`.
4. Default → message search.

Message search steps:
1. Parse flags and positional args.
2. Resolve relative dates to absolute `YYYY-MM-DD` strings via `resolveDate(input string, now time.Time) (string, error)`.
3. Build the Slack query string via `buildSearchQuery(keywords string, flags SearchFlags) string`, combining modifiers in deterministic order: keywords, `in:`, `from:`, `with:`, `is:`, `after:`, `before:`.
4. Call `internal/slack/Client.SearchMessages(query, params)`.
5. Format: `formatSearchResults` (plain text) or `formatSearchResultsJSON` (NDJSON).

Channel search (`cmd/search_channels.go`) calls `internal/slack/Client.SearchChannels` which POSTs to `search.modules.channels` — the undocumented endpoint used by Slack's Cmd+K Quick Switcher. Results are formatted by `formatChannelResults` / `formatChannelResultsJSON`.

User search (`cmd/search_users.go`) searches the local `UserCache` first, then the Flannel edge API (`edgeapi.slack.com/cache/<enterpriseID>/users/search`). See the user search behaviour section below.

`resolveDate` is pure (takes `now time.Time`) and is the primary unit-test target: cover absolute dates, each suffix, zero values (e.g. `0d`), named days (today, yesterday, each weekday), invalid suffixes, and empty input.

### Layer 2

Standard overflow and footer from the presenter. When output exceeds ~200 lines or ~50KB, the overflow notice includes a `Narrow:` hint with the full set of search filter flags and placeholders, prompting the caller to refine the query rather than navigate the dump file.
### Internal API

`internal/slack/search.go` calls `search.messages` directly via HTTP (not slack-go) so it can capture `thread_ts`, which slack-go's `SearchMessage` struct omits. It returns a typed result:

```go
type SearchResult struct {
    Query   string
    Total   int
    Page    int
    Pages   int
    Count   int
    Matches []SearchMatch
}

type SearchMatch struct {
    ChannelID      string
    ChannelName    string
    IsMPIM         bool     // true for multi-party DMs
    DMPeerID       string   // for 1:1 DMs: peer's user ID
    ParticipantIDs []string // for MPDMs: handles from the mpdm-...-1 name
    UserID         string
    Username       string   // legacy handle field
    Ts             string   // raw Slack timestamp
    ThreadTs       string   // thread root ts; extracted from permalink URL when absent as a field
    Permalink      string   // full permalink URL
    Text           string
}
```

The wrapper does not handle pagination itself — the caller passes `Page` and `Count` via `slack.SearchParameters`. Fetching multiple pages is the caller's responsibility.

`internal/slack/channels_search.go` — channel search API:

```go
// SearchChannels queries the undocumented search.modules.channels endpoint.
// workspace is the full Slack hostname (e.g. "myorg.slack.com").
// count=0 uses the default of 20.
func (c *Client) SearchChannels(ctx context.Context, workspace, query string, count int) ([]ChannelResult, error)

type ChannelResult struct {
    ID          string
    Name        string
    Topic       string
    Purpose     string
    MemberCount int
    IsPrivate   bool
    IsArchived  bool
}
```

### User search behaviour

Two sources are queried in order:

**1. Local user cache** (`~/.cache/slackcli/users-<workspace>.json`)

An instant, offline scan. The cache contains users encountered in messages and threads fetched by previous `read` and `search` commands. Results are sorted by display name. These users appear first because they are people you interact with regularly.

The cache has no TTL — entries are written once when a user ID is first encountered and are never evicted. Email addresses are populated when a `users.info` call is made; existing entries may have `email: ""` until then.

**2. Flannel edge API** (`edgeapi.slack.com/cache/<enterpriseID>/users/search`)

A single HTTP request to Slack's internal edge search layer — the same endpoint the Slack client uses for its DM recipient picker and Cmd+K switcher. This is not a public API; it requires browser credentials (`xoxc`/`xoxd`) which slackcli already holds.

For queries that look like an employee ID (one letter followed by 4+ digits, e.g. `u123456`), `fuzz=0` is used for exact handle matching. All other queries use `fuzz=1` (prefix/fuzzy matching). The server returns up to 25 results.

Results already present in the local cache are deduplicated and not shown again. **Edge API results are never written to the local cache.**
