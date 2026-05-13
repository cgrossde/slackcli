# Implementation Internals

This document covers implementation details that span multiple commands. For an overview of the project architecture, see `ARCHITECTURE.md`.

## HTTP client and TLS fingerprinting

Slack revokes sessions that do not present a browser-like TLS fingerprint. The `internal/slack/client.go` module addresses this by using `github.com/rusq/chttp/v2`, which wraps `refraction-networking/utls` to emulate Chrome's TLS ClientHello and HTTP/2 SETTINGS frames.

A `transport.FuncTransport` wrapper injects browser headers on every request: `sec-ch-ua`, `sec-fetch-dest`, `sec-fetch-mode`, `sec-fetch-site`, and `Origin`.

The `chromeUA` constant is pinned to `Chrome/143.0.7499.4`, which is the Chromium version bundled with `playwright-go v0.5700.1`. This ensures the User-Agent is consistent between the login browser session and API calls. When upgrading `playwright-go`, update both the constant and the version together.

Cookie injection seeds a `cookiejar` with `d=<xoxd>` for `.slack.com`. If uTLS initialisation fails, a plain `cookieTransport` fallback is used.

Tests use `NewClientWithHTTP(token, cookie, httpClient)` to inject a plain `*http.Client` pointing at the test server. This bypasses chttp entirely while preserving cookie and token fields on the `Client` struct. The older pattern of passing `slackgo.OptionHTTPClient(newPlainCookieClient(...))` is equivalent but only affects the slack-go layer; `NewClientWithHTTP` also sets `httpClient` on the struct, which is needed for methods that call Slack's edge APIs directly (e.g. `GetActivityFeed`, `GatewayServer`).

## Two-layer architecture

The codebase uses a two-layer design for command execution and output formatting.

Layer 1 consists of `cmd/` and `internal/slack/` packages. These implement pure command execution, always returning `(string, error)` with no truncation or annotation. This layer is unaware of output limits or presentation concerns.

Layer 2 is `internal/output/presenter.go`, wired into `main.go`. This layer handles overflow truncation, appends an `[exit:N | Xms]` footer, and attaches stderr when needed.

When overflow fires, the truncation notice includes three hints:
- `Explore: cat <file> | grep <pattern>` / `cat <file> | tail 100` — navigate the dump file
- `Narrow: slackcli search --channel <channel> --from <user> --after <date> --before <date> "<keywords>"` — refine the query; `(see: slackcli search --help)` links to the full flag reference

The `WrapWithPresenter` function in `main.go` captures a command's stdout into a buffer, runs the command, then passes `(stdout, stderr, exitCode, elapsed)` to `output.Format`. This decoupling allows Layer 1 to remain simple and testable.

For login and reauth, the presenter is applied inline in `makeLoginRunE` because timing must wrap the browser session.

## errAlreadyPresented sentinel

`errAlreadyPresented` is a sentinel error defined in `main.go`. It is returned by `RunE` implementations that have already written a formatted `output.Format` block and need `run()` to exit non-zero without emitting a second presenter block.

The `run()` function recognises this sentinel: it skips the presenter, returns the error so `main()` exits non-zero, and produces no additional output.

Never call `os.Exit` inside a `RunE` implementation. Doing so makes the code path untestable and violates the `run()` contract.

## Keychain storage

Workspace credentials are stored in the macOS Keychain via `internal/keychain/keychain.go`. One generic-password item is maintained per workspace.

Service name is `slackcli`; account name is the workspace domain (for example, `myorg.slack.com`). The value is a JSON-encoded `Entry` struct containing `Workspace`, `Token`, `Cookie`, and `SavedAt` fields.

Missing entries are signalled by the `ErrNotFound` sentinel. Callers use `errors.Is` to check for this condition.


### Default workspace item

A second item tracks the default workspace:
  service  = "slackcli"
  account  = "__default__"
  password = workspace domain string (plain text, not JSON)

Managed by internal/keychain/default.go: SetDefault, GetDefault,
DeleteDefault (no-op when absent), ResolveDefault.

ResolveDefault order:
  1. Stored default (GetDefault)
  2. Single saved workspace (implicit)
  3. Error — ambiguous or empty
## Cobra wiring rules

Several conventions apply when using the Cobra command framework:

- Never set `RunE` on a command group. Bare group invocation must show help.
- `MarkFlagRequired` must be called on the exact command that owns the flag, not on parent groups.
- Flag errors and RunE errors are caught by `run()` after `root.Execute()` returns and formatted through `output.Format`. RunE errors additionally trigger help output before the error block (see Help and error UX below).
- A `RunE` that writes its own presenter output must return `errAlreadyPresented`.

## Help and error UX

Help layout: Usage and flags render first; the Long description appears below.
This is set via root.SetHelpTemplate in buildRoot — the template emits
UsageString first, then Long. All commands inherit this automatically.

--help bypass: WrapWithPresenter redirects cmd.OutOrStdout() to a capture
buffer. Cobra's --help handler writes to OutOrStdout(), which would swallow
help into the buffer and never flush it. WrapWithPresenter saves the default
HelpFunc before the redirect, then sets a replacement that temporarily swaps
OutOrStdout() to finalOut, calls the saved HelpFunc, then swaps back.

Error path: When a wrapped command's RunE returns a non-nil error,
WrapWithPresenter calls cmd.HelpFunc()(cmd, args) and writes a blank line to
finalOut before emitting the presenter block. The caller sees:
  [help output]

  [stderr] error message
  [exit:1 | Xms]

This means no-arg or bad-arg invocations are always self-documenting.

## Write operations and channel allowlist

The `send`, `react`, `delete`, `forward`, and `snippet` commands write to Slack. To prevent accidental writes to arbitrary channels, all write operations are gated by an allowlist embedded into the binary at build time from `internal/slack/allowlist.txt` (gitignored).

```
# internal/slack/allowlist.txt — one channel ID per line
C0123456789  # #general
C9876543210  # #team-alerts
```

`(*Client).SendMessage`, `(*Client).AddReaction`, `(*Client).RemoveReaction`, `(*Client).DeleteMessage`, `(*Client).ForwardMessage`, and `(*Client).CreateSnippet` in `internal/slack/send.go` each call `IsWriteAllowed(channelID)` before touching the Slack API and return an error immediately if the channel is not listed. This enforcement is at the API client layer — no cmd-layer caller can bypass it.

To permit writing to a new channel, add its ID to `internal/slack/allowlist.txt` and rebuild the binary. See `allowlist.txt.example` in the same directory for format details.

## Markdown → mrkdwn conversion

`internal/slack/mrkdwn.go` implements `MarkdownToMrkdwn(text string) string`, a pure conversion function used by `send --md`. It uses `github.com/yuin/goldmark` (GFM-compliant AST parser, already a project dependency) to parse the input and walks the AST to emit Slack mrkdwn. This avoids the double-transformation hazards of regex-based approaches.

Conversion highlights:
- Headings → `*heading*` (bold)
- `**bold**` / `__bold__` → `*bold*`; `*italic*` → `_italic_`; `~~strike~~` → `~strike~`
- Links: `[text](url)` → `<url|text>`
- Fenced code blocks: language label stripped, content passed through verbatim
- Tables: rendered as box-drawing ASCII (fenced with ` ``` `)
- Thematic breaks (`---`) → `———`
- Bullet lists: `- item` / `* item` → `• item`
