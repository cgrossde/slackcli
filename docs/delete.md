# delete

Delete one of your own messages from a whitelisted Slack channel.

## Usage

```
slackcli delete [url | channelID:ts] [flags]
slackcli delete --channel <id> --ts <timestamp> [flags]
```

The target message is the first positional argument (a Slack URL or `channelID:ts` token) **or** supplied via `--channel` + `--ts` flags.

## Flags

- `--channel <id>`: Channel ID containing the message
- `--ts <timestamp>`: Message timestamp (e.g. `1718197925.001234`)
- `--thread-ts <timestamp>`: Parent thread timestamp — required when targeting a thread reply via flags (omit for top-level messages)
- `-w, --workspace <name>`: Workspace (defaults to stored default or sole saved workspace)

## Arguments

### Target message (exactly one form)

#### Slack URL (positional arg)

A full Slack message permalink:

```
https://myorg.slack.com/archives/C0B3PCPL0CF/p1718197925001234
```

Thread reply URLs include `?thread_ts=<parent_ts>` — this is extracted automatically:

```
https://myorg.slack.com/archives/C0B3PCPL0CF/p1779023515154839?thread_ts=1779023514.528229&cid=C0B3PCPL0CF
```

The URL must contain the channel ID and a timestamp segment starting with `p` followed by 13 digits.

#### Channel ID and Timestamp (positional arg)

A channel ID and message timestamp separated by a colon:

```
C0B3PCPL0CF:1718197925.001234
```

#### Flags form

```
--channel C0B3PCPL0CF --ts 1718197925.001234
```

For a thread reply, add `--thread-ts`:

```
--channel C0B3PCPL0CF --ts 1779023515.154839 --thread-ts 1779023514.528229
```

Both `--channel` and `--ts` must be provided together. If a positional arg is also present, the flag values must match the resolved channel and timestamp or the command fails with a conflict error.

Workspace resolution proceeds in this order:
1. If `--workspace` flag is set, use that workspace
2. If a default workspace is stored (`slackcli auth default --workspace <name>`), use it
3. If exactly one workspace is saved, use it
4. Otherwise, fail with an error

## Ownership check

Before calling `chat.delete`, the command calls `auth.test` to obtain the authenticated user's ID, then fetches the message — via `conversations.replies` for thread replies, `conversations.history` for top-level messages — and compares the sender's user ID against the authenticated user. If they do not match, the command fails locally with:

```
delete: message at ts=<ts> was not sent by you (sent by <user_id>)
```

This prevents confusing API errors and makes the failure reason immediately clear.

## Channel allowlist

Only channels in the write allowlist (configured in `internal/slack/allowlist.txt` at build time) are accepted. Targeting any other channel fails immediately before making any API call.

## Output

```
Deleted: C0B3PCPL0CF ts=1718197925.001234
[exit:0 | 423ms]
```

There is no `--json` mode. Output is always plain text.

## Examples

```bash
# Delete via Slack URL
slackcli delete https://myorg.slack.com/archives/C0B3PCPL0CF/p1718197925001234

# Delete a thread reply via URL (thread_ts extracted automatically)
slackcli delete "https://myorg.slack.com/archives/C0B3PCPL0CF/p1779023515154839?thread_ts=1779023514.528229&cid=C0B3PCPL0CF"

# Delete via channelID:ts
slackcli delete C0B3PCPL0CF:1718197925.001234

# Delete via flags
slackcli delete --channel C0B3PCPL0CF --ts 1718197925.001234

# Delete a thread reply via flags
slackcli delete --channel C0B3PCPL0CF --ts 1779023515.154839 --thread-ts 1779023514.528229

# Explicit workspace
slackcli delete C0B3PCPL0CF:1718197925.001234 --workspace myorg
```

## Authentication

Credentials for the workspace must be saved beforehand via `slackcli auth login`. The command reads these credentials to authenticate API requests.

## Implementation

- `cmd/delete.go`: `Delete(args, flags)` — Layer 1 handler; `parseDeleteTarget` resolves channel ID, timestamp, and thread timestamp from positional arg or flags; ownership check via `auth.test` + `GetMessage` (top-level) or `GetReply` (thread replies)
- `internal/slack/send.go`: `(*Client).DeleteMessage(channelID, ts)` — Slack `chat.delete` API call, whitelist-gated
- `internal/slack/whitelist.go`: `IsWriteAllowed(channelID)` — enforces channel allowlist before any write
