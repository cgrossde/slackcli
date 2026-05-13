# forward

Forward a Slack message to another channel or DM by posting its permalink with
link unfurling enabled. Slack renders a rich preview of the original message in
the destination channel.

## Usage

```
slackcli forward [url | channelID:ts] --to <channel> [flags]
slackcli forward --channel <id> --ts <timestamp> --to <channel> [flags]
```

The source message is the first positional argument (a Slack URL or
`channelID:ts` token) **or** supplied via `--channel` + `--ts` flags.

`--to` is always required.

## Flags

- `--to <channel>`: Destination channel ID or name (required)
- `--note <text>`: Optional note prepended before the permalink
- `--no-preview`: Suppress link unfurling on the forwarded permalink (`unfurl_links: false`). By default forwarding enables the preview so the full message card renders in the destination channel.
- `--channel <id>`: Channel ID containing the source message
- `--ts <timestamp>`: Source message timestamp (e.g. `1718197925.001234`)
- `-w, --workspace <name>`: Workspace (defaults to stored default or sole saved workspace)

## Arguments

### Source message (exactly one form)

#### Slack URL (positional arg)

A full Slack message permalink:

```
https://myorg.slack.com/archives/C0B3PCPL0CF/p1718197925001234
```

#### Channel ID and Timestamp (positional arg)

A channel ID and message timestamp separated by a colon:

```
C0B3PCPL0CF:1718197925.001234
```

#### Flags form

```
--channel C0B3PCPL0CF --ts 1718197925.001234
```

Both `--channel` and `--ts` must be provided together. If a positional arg is
also present, the flag values must match the resolved channel and timestamp or
the command fails with a conflict error.

### Destination channel (`--to`)

Accepts a channel ID (`C0B3Z1KT80K`) or a channel name (`general`). Channel
names are resolved to IDs via the Slack API before the write allowlist is
checked.

### Note (`--note`)

When `--note` is supplied, its text is prepended before the permalink on its
own line. This mirrors Slack's "add a note" field in the native forward UI:

```
FYI, see the original discussion below:
https://myorg.slack.com/archives/C0B3PCPL0CF/p1718197925001234
```

Workspace resolution proceeds in this order:
1. If `--workspace` flag is set, use that workspace
2. If a default workspace is stored (`slackcli auth default --workspace <name>`), use it
3. If exactly one workspace is saved, use it
4. Otherwise, fail with an error

## Mechanism

There is no dedicated `chat.shareMessage` API endpoint. The recommended
approach (as confirmed by Slack) is to call `chat.postMessage` with the
source message's permalink as text and `unfurl_links: true`. Slack renders a
full rich preview of the forwarded message at the destination.

The source channel is not write-gated — only a permalink lookup
(`chat.getPermalink`) is performed against it. The destination channel is
checked against the write allowlist before `chat.postMessage` is called.

## Channel allowlist

Only the **destination** channel is checked against the write allowlist.
Populate `internal/slack/allowlist.txt` with your destination channel IDs before building.

## Output

```
Forwarded: C0B3Z1KT80K ts=1718197925.001234
[exit:0 | 340ms]
```

There is no `--json` mode. Output is always plain text.

## Examples

```bash
# Forward via Slack URL
slackcli forward https://myorg.slack.com/archives/C0B3PCPL0CF/p1718197925001234 --to C0B3Z1KT80K

# Forward with a note
slackcli forward C0B3PCPL0CF:1718197925.001234 --to C0B3Z1KT80K --note "FYI"

# Forward via flags
slackcli forward --channel C0B3PCPL0CF --ts 1718197925.001234 --to C0B3Z1KT80K

# Explicit workspace
slackcli forward C0B3PCPL0CF:1718197925.001234 --to C0B3Z1KT80K --workspace myorg

# Forward without expanding the link preview
slackcli forward C0B3PCPL0CF:1718197925.001234 --to C0B3Z1KT80K --no-preview
```
## Authentication

Credentials for the workspace must be saved beforehand via `slackcli auth login`.

## Implementation

- `cmd/forward.go`: `Forward(args, flags)` — Layer 1 handler; `parseForwardSource` resolves source channel ID and timestamp; `--to` validation and channel name resolution
- `internal/slack/send.go`: `(*Client).ForwardMessage(srcChannelID, srcTs, dstChannelID, noteText)` — calls `chat.getPermalink` then `chat.postMessage` with `unfurl_links: true`; write-allowlist-gated on the destination channel
- `internal/slack/whitelist.go`: `IsWriteAllowed(channelID)` — enforces channel allowlist before any write
