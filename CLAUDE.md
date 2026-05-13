Before planning any feature, read `ARCHITECTURE.md`.
Before writing any code, read `CODING-INSTRUCTIONS.md`.

# slackcli — Agent Coding Instructions

## Project purpose

`slackcli` is a Unix CLI tool that gives an AI agent programmatic access to Slack — reading messages, searching, posting — using browser-extracted credentials (xoxc token + xoxd cookie). No Slack app or bot token required.

The intended caller is an LLM agent. Every design decision must serve that caller first.

---

## Commands

| Command | Flags | Description |
|---|---|---|
| `auth login` | `--workspace` (required), `--firefox` | Open browser, extract credentials, save to Keychain |
| `auth reauth` | `--workspace` (required), `--firefox` | Delete existing credentials then re-login |
| `auth status` | `--workspace` (optional) | Verify saved tokens via `auth.test` |
| `auth logout` | `--workspace` (required) | Remove Keychain entry |
| `auth default` | `--workspace` (optional) | Get or set the default workspace |
| `auth workspaces` | `--workspace` (optional) | List Enterprise Grid sibling workspaces; backfills grid metadata in Keychain |
| `read <url>` | `--json`, `--pretty`, `-w/--workspace`, `-o/--output` | Print a Slack message or full thread, or download a file attachment |
| `search <query>` | `-c/--channel`, `-f/--from`, `--after`, `--before` (YYYY-MM-DD, Nd/Nw/Nm/Ny, named days: today/yesterday/monday–sunday), `-n/--count`, `-p/--page`, `--sort`, `--asc`, `--in-dm`, `--in-channel`, `--with`, `--json`, `-w/--workspace` | Search Slack messages (default), channels (`--channels`), or users (`--users`) |
| `live` | `-w/--workspace` (optional), `-c/--channel`, `-f/--from`, `-t/--type`, `--json`, `--mention` | Stream real-time events from the WebSocket gateway |
| `activity` | `--unread`, `-n/--count`, `--cursor`, `-t/--type`, `--json`, `-w/--workspace` | Show the Slack Activity feed (@mentions, reactions, thread replies, DMs, keyword alerts, etc.) |
| `history [channel-url\|channelID\|name]` | `--channel`, `-n/--count`, `--before`, `--after`, `--cursor`, `--pretty`, `--json`, `-w/--workspace` | Fetch recent messages from a channel (newest-first). `--before`/`--after` accept YYYY-MM-DD, Nd/Nw/Nm/Ny, named days. `read` redirects to this command when given a channel-only URL. |
| `live types` | — | List supported real-time event types |
| `send [message\|url\|channelID:ts]` | `--channel`, `--thread`, `--file`, `--md`, `--react`, `--no-preview`, `-w/--workspace` | Post a message to a whitelisted channel (allowlist: `C0B3PCPL0CF`). Body from inline arg, `--file`, or piped stdin. Target via `--channel`, URL, or `channelID:ts`. `--md` converts Markdown to mrkdwn. `--react <emoji>` adds a reaction to the sent message. `--no-preview` suppresses link unfurling. |
| `react <emoji> [url\|channelID:ts]` | `--channel`, `--ts`, `--remove`, `-w/--workspace` | Add or remove an emoji reaction on a message in a whitelisted channel. Target via URL, `channelID:ts`, or `--channel`+`--ts`. |
| `delete [url\|channelID:ts]` | `--channel`, `--ts`, `--thread-ts`, `-w/--workspace` | Delete one of your own messages from a whitelisted channel. Target via URL, `channelID:ts`, or `--channel`+`--ts`. Thread replies require `--thread-ts` (or use a URL with `?thread_ts=`). Ownership is verified via `auth.test` before deletion. |
| `forward [url\|channelID:ts]` | `--to` (required), `--channel`, `--ts`, `--note`, `--no-preview`, `-w/--workspace` | Forward a message to a whitelisted destination channel by posting its permalink with link unfurling. Target source via URL, `channelID:ts`, or `--channel`+`--ts`. Optional `--note` prepends text before the permalink. `--no-preview` suppresses the link preview card. |
| `snippet create [content]` | `--channel` (required), `--title`, `--type`, `--thread`, `--file`, `--comment`, `-w/--workspace` | Upload text as a code snippet to a whitelisted channel. Body from inline arg, `--file`, or piped stdin. Filetype inferred from file extension when `--type` is omitted. |
| `snippet delete <file_id>` | `-w/--workspace` | Delete a snippet by file ID. Slack enforces ownership. |
| `snippet types` | — | List supported `--type` values for snippet create. |
Full usage details: `docs/auth.md`, `docs/read.md`, `docs/search.md`, `docs/live.md`, `docs/activity.md`, `docs/history.md`, `docs/send.md`, `docs/react.md`, `docs/delete.md`, `docs/forward.md`, `docs/snippet.md`.

---

## Output contract

### Default (plain text)

Every command writes to stdout with a trailing footer:

```
[output]
[exit:0 | 12ms]
```

On failure, stderr is always included:

```
[stdout if any]
[stderr] reason here
[exit:1 | 3ms]
```

Output exceeding ~200 lines or ~50KB is truncated; the full content is written to `/tmp/slackcli-output-N.txt` with a grep/tail hint appended.

### JSON mode (`--json`)

`read`, `search`, `live`, `activity`, and `history` support `--json`. When set:

- Output is **NDJSON** — one JSON object per line, no envelope, no top-level array.
- **The presenter footer is suppressed entirely.** No `[exit:N | Xms]` line, no overflow file, no stderr attachment block.
- Errors are written to **stderr only** as plain text; stdout may be empty or partial.
- `search` emits a `{"_pagination": {...}}` trailer as the final line when more pages exist. Pass `--page <next_page>` to continue.
- `activity` and `history` emit a `{"_pagination": {...}}` trailer when more items/messages exist. Pass `--cursor <cursor>` to continue.
- `live --json` streams events until Ctrl+C (exit 0) or fatal error (stderr + exit non-zero); no footer is emitted either way.

See `ARCHITECTURE.md` §"JSON Output Mode" for the full design rationale and per-command field schemas.
---

## Architecture

Two-layer model — see `ARCHITECTURE.md` for the full design rationale.

- **Layer 1** (`cmd/`, `internal/slack/`): executes, returns `(string, error)`, no truncation or annotation
- **Layer 2** (`internal/output/`, wired in `main.go`): overflow, footer, stderr attachment

Implementation internals (HTTP client, TLS fingerprinting, keychain storage, Cobra wiring, `errAlreadyPresented` sentinel): `docs/internals.md`.

---

## Adding a new command

### Testing rules — keychain and network isolation

**NEVER call Layer 1 functions (`cmd.Forward`, `cmd.Delete`, `cmd.Send`, etc.) directly in tests when those functions will reach `keychain.ResolveDefault()` or `keychain.Load()`.** The developer's real keychain may have live credentials; a test that stumbles through to `PostMessage` or `chat.delete` will silently send or delete real messages.

The correct boundary for Layer 1 tests is the **first deterministic failure that does not involve the keychain or network**:
- Argument/flag parsing errors — always safe; fired before any keychain call.
- Write-allowlist rejection — safe; fired inside `ForwardMessage`/`SendMessage`/etc. before any API call, and the channel ID can be chosen to guarantee rejection (e.g. `CNOTALLOWD`).
- Do NOT use a real allowlisted channel as `--to`/`--channel` in a test and rely on workspace resolution failing; a populated keychain will silently pass that step and make a real API call.

When you need to test behaviour that requires credentials or a Slack API response, use a local `httptest.Server` and `slack.NewClient("xoxc-test", "xoxd-test", slackgo.OptionAPIURL(srv.URL+"/"))` — see `internal/slack/send_test.go` for the pattern. Never test against the real keychain or the real Slack API.

Checklist before a command is considered complete:

- [ ] Exits 0 on success, non-zero on all failure paths
- [ ] Writes only parseable text to stdout
- [ ] All errors include corrective guidance (`run: slackcli auth login ...`)
- [ ] `[exit:N | Xms]` footer on every response
- [ ] `--help` implemented with complete flag documentation
- [ ] No-arg invocation prints help then the error (WrapWithPresenter handles this automatically for RunE errors)
- [ ] Overflow mode applied if output can be large
- [ ] Long description content placed in Long field; use Example field for concrete examples — help layout renders Usage+Flags first, then Long
- [ ] Tests using the real Keychain (SetDefault, Save, etc.) save and restore prior state in t.Cleanup
- [ ] Layer 1 tests never reach `keychain.ResolveDefault()` or `keychain.Load()` — use parsing errors or allowlist rejection as the test boundary, or use an `httptest.Server` stub
- [ ] Layer 1 function (`cmd/`) returns `(string, error)` — no direct I/O
- [ ] `WrapWithPresenter` called in `main.go`'s `buildRoot`
- [ ] `--workspace` flag is **optional** on every command; resolve via `keychain.ResolveDefault()` when empty — never use `MarkFlagRequired("workspace")`
If the command has structured output (records, not prose), also add `--json`:
- [ ] `--json` flag registered on the command (not globally)
- [ ] Layer 1 returns NDJSON string when `flags.JSON` is true — one object per line, no trailing footer
- [ ] Errors in JSON mode: written to `cmd.ErrOrStderr()`, nothing to stdout, return `errAlreadyPresented`
- [ ] `search`-like commands: emit `{"_pagination": {...}}` trailer when `page < pages`
- [ ] `WrapWithPresenter` bypass is automatic when the `--json` flag is registered — no extra wiring needed
- [ ] Tests for the JSON formatter (struct fields, pagination trailer, source labelling)


### Presenter patterns

Two patterns exist; choose based on whether the command streams output:

**`WrapWithPresenter`** (most commands): command writes to `c.OutOrStdout()`, presenter captures it and emits the footer. Gives help-on-error automatically. Wire in `buildRoot`.

**Inline presenter** (streaming commands: `live`, `login`, `reauth`): RunE is built in `main.go`, writes directly to `stdout`, emits footer once at exit via `output.Format`. No automatic help-on-error.

Rules:
- Leaf subcommands of an inline-presenter parent (e.g. `live types`) are **not** streaming — wrap them with `WrapWithPresenter` individually by iterating `cmd.Commands()` in `buildRoot`.
- An inline-presenter command that exits due to a missing flag or bad input must emit help + error manually; `WrapWithPresenter` will not do it.
---

## Reference

- `ARCHITECTURE.md` — two-layer model, design constraints
- `CODING-INSTRUCTIONS.md` — Go style, error handling, testing rules
- `docs/auth.md` — auth command group
- `docs/read.md` — read command
- `docs/search.md` — search command
- `docs/internals.md` — HTTP client, TLS fingerprinting, Cobra wiring, keychain
- `docs/live.md` — live command
- `docs/activity.md` — activity command
- `docs/send.md` — send command
- `docs/react.md` — react command
- `docs/delete.md` — delete command
- `docs/forward.md` — forward command
- `docs/history.md` — history command
- `docs/snippet.md` — snippet command