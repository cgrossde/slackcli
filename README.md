# slackcli

A Unix CLI tool that gives an AI agent programmatic access to Slack — reading messages, searching, posting, and watching the real-time feed — using browser-extracted credentials. No Slack app or bot token required.

## How it works

`slackcli auth login` opens a browser window, intercepts the session token (`xoxc`) and cookie (`xoxd`) from the running Slack web app, and saves them to the macOS Keychain. Every subsequent command loads those credentials and makes API calls exactly as the browser would.

## Requirements

- macOS (Keychain storage)
- Go 1.21+
- Chromium or Firefox (for `auth login`)

## Install

```sh
go install github.com/cgrossde/slackcli@latest
```

Or build from source:

```sh
git clone git@github.com:cgrossde/slackcli.git
cd slackcli

# Configure your write allowlist before building (see below)
cp internal/slack/allowlist.txt.example internal/slack/allowlist.txt
# edit internal/slack/allowlist.txt and add your channel IDs

go build -o slackcli .
```

### Write allowlist

`send`, `react`, `delete`, `forward`, and `snippet` only write to channels you explicitly permit. The allowlist is a plain-text file embedded into the binary at build time — it never ships in the repository.

```sh
cp internal/slack/allowlist.txt.example internal/slack/allowlist.txt
```

Edit `internal/slack/allowlist.txt` and add one channel ID per line:

```
C0123456789  # #general
C9876543210  # #team-alerts
```

Find a channel's ID in Slack: open the channel → click the channel name → scroll to the bottom of the About panel.

An empty or absent `allowlist.txt` (the default in a clean checkout) causes all write operations to be denied — the binary is safe to build and share without it. Rebuild after any change to the file.

**Why the allowlist exists.** Two reasons, both deliberate:

First, replies and messages to colleagues should come from a human. An agent that silently posts into a shared channel or a 1:1 DM on your behalf — even with good intentions — erodes the trust that makes those conversations work. The allowlist forces a conscious decision: you choose which conversations an agent is permitted to write into.

Second, it prevents a runaway agent from causing damage at scale. Without a hard gate, a misbehaving or prompt-injected agent could spam channels, impersonate you in DMs, or flood a thread before anyone notices. The allowlist is the simplest possible circuit breaker: if the target is not on the list, nothing happens.

The intended pattern is a dedicated **agent notification channel** or a **test channel** added to the allowlist — a contained surface where automated output is expected and accepted by everyone in it.

## First run

```sh
slackcli setup
```

Walks through two steps: authenticates with Slack (skipped if credentials are already valid), then optionally installs the Claude/OpenCode skill (`~/.claude/skills/slackcli/SKILL.md`) so an LLM agent can use `slackcli` without reading any docs.

Run `slackcli setup --install-skill` at any time to update the skill without re-authenticating.

## Quick start

```sh
# Log in
slackcli auth login --workspace myorg.slack.com

# Read a message or thread
slackcli read https://myorg.slack.com/archives/C012ABC/p1718197925001234
slackcli read C012ABC:1718197925.001234

# Search
slackcli search "deployment" --channel ops --after 7d
slackcli search --users alice

# Activity feed (mentions, reactions, thread replies)
slackcli activity --unread
slackcli activity --type reaction,mention

# Stream real-time events
slackcli live --workspace myorg.slack.com
slackcli live --mention   # only events that mention you

# Post a message
slackcli send "hello team" --channel CXXXXXXXXXX
echo "report" | slackcli send --channel CXXXXXXXXXX --md
```

## Commands

| Command | Description |
|---------|-------------|
| `auth login` | Open browser, extract credentials, save to Keychain |
| `auth reauth` | Delete existing credentials then re-login |
| `auth status` | Verify saved tokens |
| `auth logout` | Remove Keychain entry |
| `auth default` | Get or set the default workspace |
| `read <url\|channelID:ts>` | Print a message or full thread; download a file attachment |
| `search <query>` | Search messages, channels (`--channels`), or users (`--users`) |
| `activity` | Show your Activity feed (@mentions, reactions, thread replies, DMs) |
| `live` | Stream real-time WebSocket events |
| `send` | Post a message to a whitelisted channel |
| `react <emoji>` | Add or remove an emoji reaction |
| `delete` | Delete one of your own messages |

All commands accept `--json` for NDJSON output (where applicable) and `-w/--workspace` to target a specific workspace.

## Output contract

Designed for **progressive disclosure** — an LLM agent can start with any command and always know what to do next:

- Every result includes a `→ slackcli read <channel:ts>` reference so the agent can drill into any message without prior knowledge of IDs.
- Pagination footers emit a ready-to-run next-page command with all flags reconstructed (e.g. `slackcli activity --cursor dXNlcjpVMDYx`).
- On bad input or missing flags, the command prints its own `--help` output before the error — no separate lookup needed.
- Every command exits with `[exit:0 | Xms]` on success. On failure, stderr is always attached: `[stderr] reason` then `[exit:1 | Xms]`. The agent never receives a silent failure.
- Output larger than ~200 lines is truncated; the full content lands in `/tmp/slackcli-output-N.txt` with `grep`/`tail` hints appended.

**JSON mode** (`--json`) suppresses the footer entirely and streams NDJSON — one object per line. Designed for scripts and programs: pipe into `jq`, feed another command, or accumulate records without parsing human-readable text.

## Examples

### Help is self-documenting

```
$ slackcli search --help
Usage:
  slackcli search [keywords] [flags]

Examples:
  slackcli search "deployment" --channel ops --after 7d
  slackcli search "from:me" --in-dm --after monday
  slackcli search "has:link" --after 2026-05-11
  slackcli search "incident" --after 2024-06-01 --before 2024-06-30
  slackcli search "with:@alice -bot" --sort timestamp --asc
  slackcli search --channels general
  slackcli search --channels ops --json
  slackcli search --users alice

Flags:
      --after string       Messages after date (YYYY-MM-DD, Nd/Nw/Nm/Ny, or day name e.g. monday)
      --asc                Sort ascending (default is descending)
      --before string      Messages before date (YYYY-MM-DD, Nd/Nw/Nm/Ny, or day name e.g. friday)
  -c, --channel string     Restrict to channel (e.g. general)
      --channels           Search channels by name (mutually exclusive with --users)
  -n, --count int          Results per page (1–100) (default 20)
  -f, --from string        Restrict to messages from user (display name or U.../W... ID)
  -h, --help               help for search
      --in-channel         Restrict to public/private channels (is:channel)
      --in-dm              Restrict to direct messages (is:dm)
      --json               Output results as NDJSON (one object per line)
  -p, --page int           Page number, 1-indexed (default 1)
      --sort string        Sort field: "score" or "timestamp" (default "score")
      --users              Search users by name, employee ID, or email (mutually exclusive with --channels)
      --with string        Restrict to conversations with user (e.g. @alice or U012ABC)
  -w, --workspace string   Workspace to search (required if >1 saved)

Search Slack messages (default), channels (--channels), or users (--users).

MODE FLAGS (mutually exclusive):
  --channels   Search channels by name via search.modules.channels
  --users      Search users by display name, employee ID, or email
  (default)    Search messages using standard Slack modifiers

MESSAGE SEARCH — flags build a Slack search query:
Channel can be a name (e.g. "ops").
From accepts a display name or a Slack user ID (U.../W...).

Date flags accept YYYY-MM-DD, relative values, or day names:
  Nd/Nw/Nm/Ny  — N days/weeks/months/years ago (e.g. 7d, 2w)
  today / yesterday
  monday … sunday  — most recent past occurrence

Slack modifiers can be passed directly in the keyword argument:
  has:link        messages with a URL
  has:reaction    messages with any emoji reaction
  is:dm           direct messages only  (same as --in-dm)
  is:channel      channel messages only (same as --in-channel)
  with:@alice     conversations where alice participated
  -word           exclude messages containing word
  "exact phrase"  exact phrase match
```

### Search result with overflow truncation

```
$ slackcli search "deployment" --channel ops --after 7d
search: "deployment in:#ops after:2026-05-09"
total: 84  page: 1/5  (20 per page)

[1] #ops · Alice Johnson (alice) · 2026-05-15 14:32
    We rolled back the deployment after the latency spike. The root cause was a
    misconfigured rate limit on the payment service — details in the post-mortem…
    → slackcli read C012ABC3456:1718200320.123456

[2] #ops · Bob Smith (bob) · 2026-05-14 09:17
    Deployment window is 22:00 UTC tonight. All teams please have rollback plans ready.
    → slackcli read C012ABC3456:1718113037.654321

[3] #ops · Carol White (carol) · 2026-05-13 11:05
    Staging deployment passed smoke tests. Promoting to prod in 30 min.
    → slackcli read C012ABC3456:1718027105.000400

...

--- output truncated (312 lines, 21.4KB) ---
Full output: /tmp/slackcli-output-1.txt
Explore: cat /tmp/slackcli-output-1.txt | grep <pattern>
         cat /tmp/slackcli-output-1.txt | tail 100
Narrow: slackcli search --channel <channel> --from <user> --after <date> --before <date> "<keywords>"
        (see: slackcli search --help)
[exit:0 | 1.3s]
```

## Docs

- [`docs/auth.md`](docs/auth.md) — login, keychain behaviour
- [`docs/read.md`](docs/read.md) — message refs, file download, JSON schema
- [`docs/search.md`](docs/search.md) — query syntax, filters, pagination
- [`docs/activity.md`](docs/activity.md) — activity feed, type aliases
- [`docs/live.md`](docs/live.md) — real-time streaming, filters, `--mention`
- [`docs/send.md`](docs/send.md) — posting, Markdown conversion
- [`docs/react.md`](docs/react.md) — reactions
- [`docs/delete.md`](docs/delete.md) — message deletion
- [`ARCHITECTURE.md`](ARCHITECTURE.md) — two-layer design, JSON schemas
