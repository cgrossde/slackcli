---
name: slackcli
description: >
  Read, search, post, and monitor Slack using browser-extracted credentials.
  Use whenever the user asks to read a Slack message or thread, search Slack,
  post to Slack, watch the activity feed, stream live events, or open a
  message in the Slack desktop app, or provides a *.slack.com URL.
allowed-tools:
  - Bash
  - Read
---

# /slackcli

You have `slackcli` on PATH. **The CLI is self-documenting — run `slackcli --help` or `slackcli <command> --help` whenever you are unsure of flags or syntax.**

**Never use `--json`.** Plain-text output is richer: it contains `→ slackcli read <channel:ts>` hints and pagination footers that tell you the exact next command to run. `--json` suppresses all of that. Reserve it only when piping output to an external script.

> **NEVER fetch a `*.slack.com` URL with the agent `read` or `browser` tools.** They have no Slack credentials and will return an auth error or raw HTML. Every Slack URL MUST be handled with `slackcli read <url>` or `slackcli history <url>`.

---

## High-value workflows

### First run / install skill
```sh
slackcli setup                     # authenticate + install this skill
slackcli setup --install-skill     # update the skill without re-authenticating
slackcli setup --uninstall-skill   # remove the installed skill
```

### Auth
```sh
slackcli auth status                     # verify saved credentials
slackcli auth login --workspace myorg    # browser-based login; saves to Keychain
slackcli auth default --workspace myorg  # set the default workspace
```

### Don't have a channel ID? Start here.
```sh
slackcli chats                           # recent DMs and group DMs, with IDs
slackcli chats --type unread             # unread conversations only
slackcli chats --type all-with-channels  # include joined channels
slackcli search --channels ops           # find a channel by name → get its ID
```
Copy the ID from the output, then use it in `history`, `send`, `open`, etc.

### Read a message or thread
```sh
slackcli read <permalink-url>            # paste from Slack "Copy link"
slackcli read C012ABC:1718197925.001234  # channel:ts compact ref
slackcli read C012ABC:1718197000.000001:1718197925.001234  # three-part: thread root + reply
```
Output always includes the full thread. Every message shows a `→ slackcli read ...` hint for drilling deeper.

### Channel history
```sh
slackcli history C012ABC -n 50           # newest-first; bare channel ID or name
slackcli history https://myorg.slack.com/archives/C012ABC
slackcli history C012ABC --after 3d      # messages from the last 3 days
```
When more messages exist the footer shows the exact `--cursor` command to continue.
`history` gives channel-level messages with reply counts — thread replies are **not** shown. `read` always returns the full thread. When in doubt, `read`.

### Activity feed
```sh
slackcli activity                        # all recent activity (@mentions, reactions, DMs, threads)
slackcli activity --unread              # unread only
slackcli activity --type reaction,mention
slackcli activity --type thread         # thread replies only
```
Each item carries a `→ slackcli read ...` hint. Pagination cursor is in the footer.

### Search
```sh
slackcli search "deployment" --channel ops --after 7d
slackcli search "decision" --with @alice --in-dm
slackcli search --users alice            # find users by name or email
```
The constructed Slack query is echoed in the header. Pagination: `--page N`.

### Open in desktop app
```sh
slackcli open <permalink-url>            # jump to a specific message
slackcli open C012ABC:1718197925.001234  # channel:ts ref
slackcli open '#general'                 # channel by name — quotes required; # is a shell comment
slackcli open @alice                     # open or resume a DM
slackcli open --print C012ABC           # print the slack:// URL instead of launching
```

### Post and react
```sh
slackcli send "hello team" --channel CXXXXXXXXXX
echo "deployment done" | slackcli send --channel CXXXXXXXXXX
slackcli send "lgtm" https://myorg.slack.com/archives/C012ABC/p1718197925001234  # reply in thread
cat release-notes.md | slackcli send --channel CXXXXXXXXXX --md   # Markdown → mrkdwn

slackcli react thumbsup <permalink-url>
slackcli react --remove thumbsup C012ABC:1718197925.001234
```
`send`, `react`, `delete`, `forward`, and `snippet create` only write to channels in the allowlist. Non-allowlisted targets are rejected immediately — no API call is made.

### Forward and delete
```sh
slackcli forward <permalink-url> --to CXXXXXXXXXX        # forward with link preview
slackcli forward <permalink-url> --to CXXXXXXXXXX --note "FYI"

slackcli delete <permalink-url>          # delete your own message
slackcli delete C012ABC:1718197925.001234
```

### Stream live events (blocking — use deliberately)
```sh
slackcli live --channel incidents        # stream a channel; Ctrl+C to stop
slackcli live --mention                  # only events that mention you
slackcli live types                      # list supported event types
```
`live` blocks until interrupted. Do not invoke it unless the user explicitly wants a live monitor.

---

## Rules that `--help` won't tell you

**Follow the `→` hints.** Every result and every message shows `→ slackcli read <ref>` or `next: slackcli ... --cursor ...`. These are ready-to-run — copy and execute them rather than constructing references by hand.

**Write allowlist.** `send`, `react`, `delete`, `forward`, and `snippet create` only work on allowlisted channels. The CLI rejects non-allowed channels immediately with a clear error — no need to guess.

**`open` needs the desktop app.** Launches the Slack app via `slack://`. Use `--print` to get the URL without launching (useful for sharing or when the app is not running).

**Truncated output.** Outputs over ~200 lines are written to `/tmp/slackcli-output-N.txt` with a hint appended. Use `grep`/`head`/`tail` on that file rather than re-running.

**Workspace resolution.** All commands accept `--workspace`; when omitted the stored default is used. Set it once with `slackcli auth default --workspace <name>`.
