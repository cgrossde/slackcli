# send

Post a message to a whitelisted Slack channel.

## Usage

```
slackcli send [message | channelID:ts | url] [flags]
```

The positional argument is interpreted as:
- A **Slack permalink URL** — channel ID and thread timestamp are extracted; used as the post target
- A **`channelID:ts`** string (e.g. `CXXXXXXXXXX:1718197925.001234`) — channel ID and thread timestamp are extracted; used as the post target
- Any other string — treated as the **inline message body**

## Description

Posts a message to a Slack channel. Only channels in the write allowlist (configured in `internal/slack/allowlist.txt` at build time) may receive messages. Attempts to post to any other channel are rejected before any API call is made.

## Write allowlist

`send` (and all other write commands: `react`, `delete`, `forward`, `snippet create`) enforce an allowlist before making any API call. The allowlist is defined in `internal/slack/allowlist.txt` — a plain-text file embedded into the binary at build time. It is gitignored and never ships in the repository.

Add one channel ID per line before building:

```sh
cp internal/slack/allowlist.txt.example internal/slack/allowlist.txt
# then edit it and add your channel IDs
```

If `allowlist.txt` is absent or empty, all write operations are denied immediately — the binary is safe to distribute without it.

Attempting to send to an unlisted channel exits with:

```
channel "CXXXXXXXXXX" is not in the write allowlist
[exit:1 | 2ms]
```

**Why the allowlist exists.** Two reasons, both deliberate:

First, replies and messages to colleagues should come from a human. An agent that silently posts into a shared channel or a 1:1 DM on your behalf — even with good intentions — erodes the trust that makes those conversations work. The allowlist forces a conscious decision: you choose which conversations an agent is permitted to write into.

Second, it prevents a runaway agent from causing damage at scale. Without a hard gate, a misbehaving or prompt-injected agent could spam channels, impersonate you in DMs, or flood a thread before anyone notices. The allowlist is the simplest possible circuit breaker: if the target is not on the list, nothing happens.

The intended pattern is a dedicated **agent notification channel** or a **test channel** added to the allowlist — a contained surface where automated output is expected and accepted by everyone in it.

## Message Body

Exactly one source is required. Sources are evaluated in this priority order:

1. **Positional argument** that is not a Slack URL and not a `channelID:ts` string — used as-is as the message text
2. **`--file <path>`** — the entire file contents are read and used as the message body
3. **Piped stdin** — when stdin is non-interactive (i.e. a pipe or redirect), the entire stdin stream is read and used as the message body

Providing more than one source (e.g. both `--file` and piped stdin) is an error.

## Target Channel Resolution

The channel to post to is resolved in this order:

### Slack permalink URL (positional)

```
https://myorg.slack.com/archives/CXXXXXXXXXX/p1718197925001234
```

Channel ID and thread timestamp are extracted from the URL. `--channel` is ignored. The message is posted as a reply to that thread unless `--thread` is also supplied (which would override the extracted timestamp).

### `channelID:ts` (positional)

```
CXXXXXXXXXX:1718197925.001234
```

Channel ID and thread timestamp are extracted from the string. Behaviour is identical to the URL form above.

### `--channel` flag

When neither a URL nor `channelID:ts` positional argument is provided, `--channel` is required and specifies the target channel ID. Pass `--thread` separately to reply to an existing thread.

## Flags

- `--channel <id>`: Channel ID to post to. Required unless a Slack URL or `channelID:ts` positional argument is provided.
- `--thread <ts>`: Reply in this thread. Supply the root message timestamp (e.g. `1718197925.001234`). Overrides any thread timestamp extracted from a URL or `channelID:ts` positional.
- `--file <path>`: Read message body from this file instead of inline text or stdin.
- `--md`: Convert the message body from Markdown to Slack mrkdwn before sending. See [Markdown Conversion](#markdown-conversion---md) below.
- `--react <emoji>`: Add an emoji reaction to the sent message. The emoji name must be given without colons (e.g. `white_check_mark`); surrounding colons are stripped automatically.
- `--no-preview`: Suppress Slack's automatic link preview for URLs in the message body (`unfurl_links: false`). By default Slack unfurls links.
- `-w, --workspace <name>`: Workspace to use. Defaults to the stored default workspace (`slackcli auth default`), or the single saved workspace when only one exists.

## Output

```
Sent: CXXXXXXXXXX ts=1718197925.001234
Reacted: :white_check_mark: on CXXXXXXXXXX ts=1718197925.001234
[exit:0 | 504ms]
```

The first block shows the channel and timestamp of the posted message. When `--react` is supplied a second line confirms the reaction. There is no `--json` mode for this command.

## Examples

```bash
# Inline text
slackcli send "hello team" --channel CXXXXXXXXXX

# Piped stdin
echo "deployment complete" | slackcli send --channel CXXXXXXXXXX

# File
slackcli send --file report.txt --channel CXXXXXXXXXX

# Reply in thread via Slack URL (positional)
slackcli send "looks good" https://myorg.slack.com/archives/CXXXXXXXXXX/p1718197925001234

# Reply in thread via channelID:ts (positional)
slackcli send "looks good" CXXXXXXXXXX:1718197925.001234

# Markdown conversion from piped stdin
cat release-notes.md | slackcli send --channel CXXXXXXXXXX --md

# Reply in thread using --thread flag
slackcli send "follow-up" --channel CXXXXXXXXXX --thread 1718197925.001234

# React to the sent message with a checkmark
slackcli send "done" --channel CXXXXXXXXXX --react white_check_mark

# Colons in the emoji name are stripped automatically
slackcli send "done" --channel CXXXXXXXXXX --react :white_check_mark:

# Suppress link preview
slackcli send "check this out https://example.com" --channel CXXXXXXXXXX --no-preview
```
## Markdown Conversion (`--md`)

When `--md` is passed the message body is converted from standard Markdown to Slack mrkdwn before it is sent. The converter uses [goldmark](https://github.com/yuin/goldmark) (GFM-compliant AST parser) and applies these rules:

| Markdown | Slack mrkdwn |
|---|---|
| `**bold**` or `__bold__` | `*bold*` |
| `*italic*` or `_italic_` | `_italic_` |
| `` `code` `` | `` `code` `` (unchanged) |
| `~~strikethrough~~` | `~strikethrough~` |
| `[text](url)` | `<url\|text>` |
| `# Heading` | `*Heading*` |
| `- item` / `* item` | `• item` |
| Fenced code block | language label stripped; content preserved |
| Table | Rendered as box-drawing ASCII |
| `---` (thematic break) | `———` |

Pass `--md` together with `--file` or piped stdin to convert longer documents before posting.

## Authentication

Credentials must be saved beforehand via `slackcli auth login`. The command reads these credentials to authenticate API requests.

## Implementation

- `cmd/send.go`: `Send(args, flags, stdin) (string, error)` — argument parsing, source/target resolution, orchestration
- `internal/slack/send.go`: `(*Client).SendMessage(channelID, text, threadTs)` — calls `chat.postMessage`
- `internal/slack/send.go`: `(*Client).AddReaction(channelID, ts, emoji)` — calls `reactions.add` after a successful send when `--react` is set
- `internal/slack/whitelist.go`: `IsWriteAllowed(channelID)` — enforces the channel allowlist before any API call
- `internal/slack/mrkdwn.go`: `MarkdownToMrkdwn(text)` — goldmark-based Markdown→mrkdwn conversion
