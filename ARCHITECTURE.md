# slackcli Architecture

## Overview

`slackcli` is built around two strictly separated layers. The boundary between them is a logical necessity, not a style choice.

```
┌─────────────────────────────────────────────┐
│  Layer 2: Presentation Layer                │  ← Serves LLM constraints
│  Overflow | Metadata footer | stderr attach  │
├─────────────────────────────────────────────┤
│  Layer 1: Execution Layer                   │  ← Pure Slack API semantics
│  Command routing | API calls | raw output   │
└─────────────────────────────────────────────┘
```

---

## Why Two Layers

Two hard constraints of LLM callers drive the need for a presentation layer:

**Constraint A: Finite context window.** Every token costs inference budget. Large outputs push earlier conversation out of the context window — the agent forgets. A 10MB API response doesn't just waste budget; it degrades reasoning quality on everything else in the window.

**Constraint B: LLMs process only text.** Structured but oversized text produces the same degradation. An agent receiving 5,000 lines of log output cannot effectively attend to the first 200 lines of the same conversation.

If you address these constraints inside the execution layer, you corrupt the output. Truncating a channel history response before returning it is fine. Truncating it _mid-processing_ in a composed pipeline breaks composition. The only correct position for presentation transforms is **after** execution completes.

---

## Layer 1: Execution

**Responsibility:** Talk to Slack. Return raw results.

- Routes subcommands to Slack Web API calls
- Handles authentication (xoxc token + xoxd cookie injection)
- Captures full API responses — no truncation, no annotation
- Captures errors and exit status
- Returns raw output upward to Layer 2 as `(string, error)`

Layer 1 has no knowledge of LLM constraints. It does not truncate. It does not annotate. It does not format for readability. It executes and returns.

**Files:**
- `internal/slack/` — Slack API client (auth, conversations, search, users)
- `cmd/` — subcommand routing

---

## Layer 2: Presentation

**Responsibility:** Transform Layer 1 output for safe, efficient LLM consumption.

Applied after execution completes. Never touches execution logic.

### Mechanism A: Overflow Mode

If output exceeds 200 lines or 50KB:

1. Truncate to first 200 lines (rune-safe — no broken UTF-8)
2. Write full output to `/tmp/slackcli-output-{n}.txt`
3. Append overflow notice with `grep`/`tail` hints

```
[first 200 lines]

--- output truncated (1420 lines, 89.4KB) ---
Full output: /tmp/slackcli-output-3.txt
Explore: cat /tmp/slackcli-output-3.txt | grep <pattern>
         cat /tmp/slackcli-output-3.txt | tail 100
```

The agent already knows `grep`, `head`, `tail`. Overflow mode converts a context problem into a navigation skill the agent already has.

### Mechanism B: Metadata Footer

After execution, append to every response:

```
[exit:0 | 1.2s]
```

- Exit code using Unix convention (0 = success, non-zero = failure)
- Duration in human-readable form

The footer is **always present**, including on success. The agent internalises these signals over a conversation. Inconsistent output format means every call feels like the first.

The footer is appended to final output only — never inside a composed pipeline where it would appear as data.

### Mechanism C: stderr Attachment

On any non-zero exit:

```
[stdout content if any]
[stderr] reason for failure here
[exit:1 | 3ms]
```

**Never drop stderr.** The most common mistake is discarding stderr when stdout has content. This is catastrophically wrong for agents: the agent receives "it failed" with no information about why, and retries blindly.

---

## Authentication Flow

```
internal/browser/   ← CDP-based credential extraction (auth subcommand)
internal/slack/     ← HTTP client with xoxc + xoxd injection
```

The `auth login` subcommand runs a visible browser, intercepts the xoxc token from API network traffic and localStorage, reads the xoxd cookie from browser storage state, and saves credentials to the macOS Keychain (one generic-password item per workspace).

All subsequent commands load credentials from the Keychain and inject them as:
- `Authorization: Bearer xoxc-...` header
- `Cookie: d=xoxd-...` header

This is the same credential pair the Slack web app uses. No Slack app or bot token required.

---

## Package Structure

```
slackcli/
├── main.go                      Entry point: run(args, stdout, stderr); Layer 1→2 bridge
├── main_test.go                 Tests for run() and top-level routing
├── cmd/
│   ├── auth.go                  Layer 1: auth subcommands (Cobra tree + pure functions)
│   ├── auth_test.go
│   ├── chats.go                 list recent DMs, MPDMs, channels
│   ├── chats_test.go
│   ├── iterm2.go                iTerm2 inline image protocol, terminal size detection
│   ├── iterm2_test.go
│   ├── live.go                  stream real-time WebSocket events
│   ├── live_test.go
│   ├── open.go                  open Slack targets in the desktop app via slack:// deep links
│   ├── open_test.go
│   ├── pretty.go                --pretty ANSI rendering (PrettyThread)
│   ├── pretty_test.go
│   ├── read.go                  read a message, thread, or download a file
│   ├── read_test.go
│   ├── search.go                search messages, channels, users (dispatch)
│   ├── search_channels.go       --channels mode: channel search + name→ID resolution
│   ├── search_users.go          --users mode: user cache + edge API search
│   ├── search_test.go
│   ├── search_channels_test.go
│   ├── search_users_test.go
│   ├── react.go                 add/remove emoji reactions
│   ├── react_test.go
│   ├── send.go                  post messages to whitelisted channels
│   ├── send_test.go
│   ├── delete.go                delete the authenticated user's own messages
│   ├── delete_test.go
│   ├── activity.go              show the Slack Activity feed
│   ├── activity_test.go
│   ├── forward.go               forward a message to another channel via permalink post
│   ├── forward_test.go
│   ├── history.go               fetch recent channel messages (conversations.history)
│   ├── history_test.go
│   ├── snippet.go               create/delete code snippets via files upload API
│   └── snippet_test.go
├── internal/
│   ├── browser/
│   │   └── extractor.go         CDP-based credential extraction (Chrome DevTools Protocol; no Playwright)
│   ├── slack/
│   │   ├── client.go            HTTP client with cookie injection; FetchFileBytes, GetFileInfo
│   │   ├── auth.go              auth.test
│   │   ├── conversations.go     conversations.history, conversations.replies; GetHistory; Message.Files, Message.Reactions, Message.Attachments; HistoryParams, HistoryResult; `(*Client).OpenIM(ctx, userID)` — open/resume 1:1 DM
│   │   ├── deeplink.go          DeepLinkChannel, DeepLinkMessage, DeepLinkFile, DeepLinkWorkspace — pure slack:// URL builders
│   │   ├── deeplink_test.go
│   │   ├── grid.go              GridWorkspaces (client.userBoot enumeration)
│   │   ├── search.go            search.messages
│   │   ├── channels_search.go   search.modules.channels (channel search + name resolution)
│   │   ├── url.go               ParseMessageRef, ParseFileRef, IsFileURL; ParseChannelURL, IsChannelURL, ChannelRef
│   │   ├── users.go             users.info; in-memory cache with filesystem backing
│   │   ├── users_search.go      Flannel edge API (edgeapi.slack.com) — user search
│   │   ├── mrkdwn.go            Markdown → Slack mrkdwn conversion (goldmark AST)
│   │   ├── send.go              SendMessage, AddReaction, RemoveReaction, DeleteMessage, ForwardMessage, BuildPermalink; write-allowlist gating
│   │   ├── whitelist.go         AllowedWriteChannels map; IsWriteAllowed
│   │   ├── activity.go          activity.feed API — GetActivityFeed; ActivityItem, ActivityFeedResult
│   │   ├── websocket.go         WebSocket connection for live events; Event.Attachments; `(*Client).TeamID(ctx, ws)` — per-workspace team ID lookup via client.userBoot
│   │   ├── snippet.go           CreateSnippet, DeleteSnippet; files upload/delete
│   │   └── snippet_test.go
│   ├── keychain/
│   │   ├── keychain.go          macOS Keychain: save/load/delete/list credentials
│   │   └── default.go           SetDefault, GetDefault, ResolveDefault
│   └── output/
│       └── presenter.go         Layer 2: overflow, footer, stderr attachment
└── ARCHITECTURE.md
```

### Entry point contract

```go
func main() {
    if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
        slog.Error("fatal", "err", err)
        os.Exit(1)
    }
}

func run(args []string, stdout, stderr io.Writer) error { … }
```

`run` takes explicit I/O writers. Tests pass `bytes.Buffer`; production passes
`os.Stdout`/`os.Stderr`. No I/O is hardcoded below `main()`.

### Layer 1 → Layer 2 bridge

`cmd/` functions return `(string, error)` — raw output string and execution error.
`main.go`'s `WrapWithPresenter` captures the output, measures elapsed time, and
calls `output.Format` before writing to `stdout`. When `--json` is set on the
command, `WrapWithPresenter` bypasses `output.Format` entirely and writes the
buffer verbatim — no footer, no overflow. Browser-dependent commands
(login/reauth) apply the presenter inline because timing wraps the browser session.

---

## Design Constraints

**Layer 1 must be raw and lossless.** Do not truncate, annotate, or transform output inside execution code. Pass it up.

**Layer 2 must not call Slack.** Presentation logic has no business making API calls. If you find yourself needing to fetch additional data in the presenter, it belongs in a Layer 1 command.

**Output must be pipeable.** Every command's stdout must survive `| grep`, `| jq`, `| head`. The metadata footer uses bracket syntax (`[exit:0]`) that is unlikely to appear as data content and can be stripped with `grep -v '^\[exit:'` if needed.

**Commands are not interactive.** No `readline`, no spinners on stdout, no "press enter to continue." The caller is an LLM running in a loop.

---

## JSON Output Mode (`--json`)

Selected commands expose a `--json` flag that switches the output format from human-readable plain text to **NDJSON** (newline-delimited JSON). This is an opt-in for scripts and programs consuming the CLI programmatically — the default plain-text output is the primary format for LLM agents.

### Rules

**Layer 2 is bypassed entirely.** When `--json` is set, `WrapWithPresenter` writes the raw NDJSON buffer directly to stdout and returns without emitting the `[exit:N | Xms]` footer, overflow notices, or stderr attachment. The footer would corrupt the NDJSON stream.

**One JSON object per line.** No top-level array, no envelope. Each logical record is emitted as a single compact JSON object followed by a newline — the NDJSON convention. `wc -l` counts records; `grep` filters them; `jq -c '.'` validates them.

**Errors go to stderr only, exit non-zero.** In JSON mode, error messages are written to stderr as plain text. stdout may be empty or contain partial NDJSON if an error occurs mid-stream. No JSON error object is written to stdout — the stream must remain parseable.

**Pagination trailers (`search`, `activity`, `history`, `chats`).** When more results exist, the final line of output is a trailer object. For `search`:
```
{"_pagination": {"next_page": 2, "has_more": true, "total": 47, "page": 1, "pages": 3}}
```
For `activity`, `history`, and `chats`:
```
{"_pagination": {"has_more": true, "next_cursor": "dXNlcjpVMDYx"}}
```
The leading underscore makes `_pagination` unambiguously not a data record. Pass `--page <next_page>` (`search`) or `--cursor <next_cursor>` (`activity`, `history`, `chats`) to fetch the next page. No trailer is emitted on the last page.

**No auto-pagination.** `search` results can be very large. Callers must page explicitly. There is no `--all` flag.

**Output stability is a contract.** Within a version series, `--json` field names and types are stable. Adding new fields to an object is allowed (callers must tolerate unknown keys). Removing or renaming fields, or changing a field's type, is a breaking change.

### Streaming commands (`live`)

`live --json` bypasses the presenter footer on both clean exit and fatal error. Each event is written as a single JSON object followed by a newline, exactly as it is received. On Ctrl+C, the stream ends and the process exits 0. On fatal WebSocket failure, the error is written to stderr and the process exits non-zero.

### Per-command schemas

Full field documentation is in each command's doc file. Quick reference:

| Command | Record fields | Trailer |
|---|---|---|
| `search --json` | `channel_id`, `channel_name`, `channel_type`, `user_id`, `username`, `display_name`, `ts`, `thread_ts?`, `text`, `permalink?`, `dm_peer_id?`, `participant_ids?` | `_pagination` when more pages exist |
| `search --channels --json` | `id`, `name`, `topic?`, `purpose?`, `member_count`, `is_archived` | none |
| `search --users --json` | `id`, `name`, `display_name`, `email`, `source` | none |
| `read --json` | `user_id`, `username`, `display_name`, `ts`, `thread_ts`, `text`, `is_root`, `reply_count?`, `channel_id`, `channel_type`, `files?`, `reactions?`, `attachments?` | none |
| `live --json` | `type`, `subtype`, `channel_id`, `channel_name`, `user_id`, `username`, `display_name`, `ts`, `thread_ts`, `text`, `reaction?`, `item_ts?`, `attachments?` | none |
| `activity --json` | `type`, `feed_ts`, `is_unread`, `channel_id`, `channel_name`, `ts`, `thread_ts?`, `read_ref`, `user_id`, `username`, `display_name`, `text`, `reaction?`, `reactor_id?`, `reactor_name?` | `_pagination` when more items exist |
| `history --json` | `user_id`, `username`, `display_name`, `ts`, `thread_ts`, `text`, `is_root`, `reply_count?`, `channel_id`, `channel_type`, `files?`, `reactions?`, `attachments?` | `_pagination` (`next_cursor`) when more messages exist |
| `chats --json` | `id`, `type`, `name`, `raw_name?`, `peer_id?`, `member_ids?`, `latest_ts?`, `is_starred?`, `has_unreads?`, `mention_count?` | `_pagination` (`next_cursor`) when more items exist |

---

## Reference

The two-layer model and the Unix-as-agent-interface pattern originate from production experience at Manus (2024–2025). Full rationale in the knowledge base:

- `Knowledgebase/wiki/notes/manus-unix-agent-tool-interface.md`
- `Knowledgebase/wiki/concepts/agent-tool-two-layer-architecture.md`

Reference Go implementation: [github.com/epiral/agent-clip](https://github.com/epiral/agent-clip)
