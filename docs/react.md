# react

Add (or remove) an emoji reaction on a message in a whitelisted Slack channel.

## Usage

```
slackcli react <emoji> [url | channelID:ts] [flags]
```

The emoji is always the first positional argument. The target message is the second positional argument (a Slack URL or `channelID:ts` token) **or** supplied via `--channel` + `--ts` flags.

## Flags

- `--channel <id>`: Channel ID containing the message
- `--ts <timestamp>`: Message timestamp (e.g. `1718197925.001234`)
- `--remove`: Remove the reaction instead of adding it
- `-w, --workspace <name>`: Workspace (defaults to stored default or sole saved workspace)

## Arguments

### Emoji name

The first positional argument is the emoji name. Surrounding colons are optional — both `thumbsup` and `:thumbsup:` are accepted.

### Target message (exactly one form)

#### Slack URL (second positional arg)

A full Slack message permalink:

```
https://myorg.slack.com/archives/C0B3PCPL0CF/p1718197925001234
```

The URL must contain the channel ID and a timestamp segment starting with `p` followed by 13 digits. Workspace is extracted from the URL; `--workspace` is ignored.

#### Channel ID and Timestamp (second positional arg)

A channel ID and message timestamp separated by a colon:

```
C0B3PCPL0CF:1718197925.001234
```

Workspace resolution proceeds in this order:
1. If `--workspace` flag is set, use that workspace
2. If a default workspace is stored (`slackcli auth default --workspace <name>`), use it
3. If exactly one workspace is saved, use it
4. Otherwise, fail with an error

#### Flags form

```
--channel C0B3PCPL0CF --ts 1718197925.001234
```

Both `--channel` and `--ts` must be provided together. If a positional arg is also present, the flag values must match the resolved channel and timestamp or the command fails with a conflict error.

## Channel allowlist

Only channels in the write allowlist (configured in `internal/slack/allowlist.txt` at build time) are accepted. Targeting any other channel fails immediately before making any API call.

## Output

### Add reaction

```
Reacted: :thumbsup: on C0B3PCPL0CF ts=1718197925.001234
[exit:0 | 504ms]
```

### Remove reaction

```
Removed: :thumbsup: from C0B3PCPL0CF ts=1718197925.001234
[exit:0 | 312ms]
```

There is no `--json` mode. Output is always plain text.

## Examples

```bash
# React via Slack URL
slackcli react thumbsup https://myorg.slack.com/archives/C0B3PCPL0CF/p1718197925001234

# React via channelID:ts
slackcli react thumbsup C0B3PCPL0CF:1718197925.001234

# React via flags
slackcli react white_check_mark --channel C0B3PCPL0CF --ts 1718197925.001234

# Remove a reaction
slackcli react thumbsup --remove C0B3PCPL0CF:1718197925.001234

# Emoji with or without colons — both accepted
slackcli react :thumbsup: C0B3PCPL0CF:1718197925.001234

# Explicit workspace
slackcli react thumbsup C0B3PCPL0CF:1718197925.001234 --workspace myorg
```

## Authentication

Credentials for the workspace must be saved beforehand via `slackcli auth login`. The command reads these credentials to authenticate API requests.

## Implementation

- `cmd/react.go`: `React(args, flags)` — Layer 1 handler; `parseReactTarget` resolves channel ID and timestamp from positional arg or flags
- `internal/slack/send.go`: `(*Client).AddReaction(channelID, ts, emoji)`, `(*Client).RemoveReaction(channelID, ts, emoji)` — Slack `reactions.add` / `reactions.remove` API calls
- `internal/slack/whitelist.go`: `IsWriteAllowed(channelID)` — enforces channel allowlist before any write
