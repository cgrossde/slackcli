# open

Open a Slack channel, DM, message, thread, or file in the macOS Slack desktop app via the `slack://` URL scheme.

## Usage

```
slackcli open <target> [flags]
```

The target is a single positional argument; the form is auto-detected.

## Flags

- `--print`: Emit the resolved `slack://` URL to stdout instead of launching it. Useful for piping or testing.
- `-w, --workspace <name>`: Workspace (defaults to stored default or sole saved workspace). Used only for forms that don't already encode a workspace (i.e. not for full Slack URLs).

## Target forms

### Message permalink

```
https://myorg.slack.com/archives/C012ABC/p1718197925001234
https://myorg.slack.com/archives/C012ABC/p1718197925001234?thread_ts=1718197000.000001
```

Switches to the channel and scrolls/highlights the message. Thread reply URLs (with `?thread_ts=`) open the thread side-pane anchored on the reply.

### Channel permalink

```
https://myorg.slack.com/archives/C012ABC
```

Switches to the channel; no message scroll.

### Compact `channelID:ts` ref

```
C012ABC3456:1718197925.001234                          # message in channel
C012ABC3456:1718197000.000001:1718197925.001234        # thread reply (root + reply)
```

The two-part form jumps to a top-level message. The three-part form opens the thread side-pane on the reply.

### Bare channel/DM/MPDM ID

```
C012ABC3456    public/private channel
D012ABC3456    1:1 DM
G012ABC3456    multi-party DM
```

Opens the conversation; no message scroll.

### Channel name

```
#general
general          # leading # optional
```

Resolved via `search.modules.channels`. Adds one round-trip on first use.

### User mention

```
@alice
@"Alice Anderson"   # use shell quoting for names with spaces
```

Resolved through the local user cache, falling back to the edge user-search API. The 1:1 DM channel is opened (or resumed) via `conversations.open`, and the resulting `D…` ID is deep-linked. Already-formed user IDs (`U…`/`W…`) pass through unchanged.

### File permalink

```
https://myorg.slack.com/files/UUSER/F012ABC/filename.ext
```

Opens the file viewer in the desktop client.

## Behaviour notes

- **macOS only.** The command shells out to `/usr/bin/open`. On other operating systems it returns an error; use `--print` to obtain the URL for use elsewhere.
- **Team-ID lookup.** The `slack://` scheme requires the per-workspace team ID (`T…`). On first use for a workspace, the command calls `client.userBoot` once and caches the team ID in the keychain entry (`team_id` field). Subsequent opens are zero round-trip. The team ID is also backfilled at `slackcli auth login` time.
- **Enterprise Grid.** On Grid the enterprise ID (`E…`) is *not* used, even when present; channel IDs route correctly only through the member workspace's `T…`.
- **Cross-channel jumps.** The `&message=<dotted-ts>` form switches the active channel and scrolls in one shot — no need to be on the destination channel first.
- **Permalink form vs deep-link form.** Slack's HTTPS permalinks (`https://*.slack.com/archives/...`) open in the default browser on macOS rather than the Slack app; that's why this command translates them to `slack://` URLs before launching.

## Output

Plain text only — no `--json` mode. Successful runs print the resolved `slack://` URL, which is exactly what was passed to `open(1)`:

```
slack://channel?team=T012ABC&id=C0B3PCPL0CF&message=1718197925.001234
[exit:0 | 28ms]
```

With `--print`, the URL is emitted but no process is launched.

## Examples

```bash
# Message permalink — jumps to the exact message
slackcli open https://myorg.slack.com/archives/C012ABC/p1718197925001234

# Thread reply — opens the side-pane on the reply
slackcli open "https://myorg.slack.com/archives/C012ABC/p1718197925001234?thread_ts=1718197000.000001"

# Compact channel:ts (single message)
slackcli open C012ABC3456:1718197925.001234

# Three-part: thread root + reply
slackcli open C012ABC3456:1718197000.000001:1718197925.001234

# Bare channel
slackcli open C012ABC3456

# Channel name
slackcli open '#general'

# User mention
slackcli open '@alice'

# File
slackcli open https://myorg.slack.com/files/UUSER/F012ABC/screenshot.png

# Just print the URL — no launch
slackcli open --print '#general'
```

## Authentication

Credentials for the workspace must be saved beforehand via `slackcli auth login`. The command reads these credentials only when a target form requires resolution (channel name, user handle, team-ID lookup); URL and `channelID[:ts]` forms with the team ID already cached touch only the keychain.

## Implementation

- `cmd/open.go`: `Open(target, flags)` — Layer 1 handler; `buildDeepLink` dispatches on target shape; `pickWorkspace`, `teamIDFor`, `resolveUserHandle`, `pickUserExact` are local helpers.
- `internal/slack/deeplink.go`: `DeepLinkChannel`, `DeepLinkMessage`, `DeepLinkFile`, `DeepLinkWorkspace` — pure URL builders.
- `internal/slack/websocket.go`: `(*Client).TeamID(ctx, workspace)` — `client.userBoot`-backed lookup, shared with `GatewayServer`.
- `internal/slack/conversations.go`: `(*Client).OpenIM(ctx, userID)` — wraps `conversations.open` for `@user` resolution.
- `internal/keychain/keychain.go`: `Entry.TeamID` — cached per-workspace team ID, populated lazily at first use and at login.
