# Go CLI — Coding Instructions

Minimum rules for this codebase. Read before writing any code.

---

## Language & toolchain

- Go 1.21+. Use stdlib-first; add a dependency only when it earns its keep.
- `go.mod` module path matches the repo. Run `go mod tidy` after every dependency change.
- Format with `gofmt` (or `goimports`). No exceptions; CI rejects unformatted code.
- Vet with `go vet ./...` before committing.

---

## Project layout

```
cmd/<name>/main.go   // entry point — parse flags, call run(), exit on error
internal/            // all application logic; not importable from outside
  <feature>/
    <feature>.go
    <feature>_test.go
```

`main.go` is a thin shell:

```go
func main() {
    if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
        fmt.Fprintln(os.Stderr, err)
        os.Exit(1)
    }
}
```

`run` takes explicit I/O writers. This makes it testable without subprocess overhead.

---

## Errors

Wrap with context at every layer boundary. Never swallow.

```go
// add context, preserve type for callers who need errors.Is / errors.As
return fmt.Errorf("load config: %w", err)

// intentionally opaque — use %v when callers must NOT inspect the type
return fmt.Errorf("load config: %v", err)
```

Sentinel errors for outcomes callers must branch on:

```go
var ErrNotFound = errors.New("not found")
// caller: errors.Is(err, ErrNotFound)
```

Never compare errors with `==` through a wrapped chain. Always use `errors.Is` / `errors.As`.

Return early. Don't nest happy-path logic inside `if err == nil` blocks.

Do **not** add `github.com/pkg/errors`. Stdlib wrapping covers the common case.

---

## Logging

Use `log/slog`. Do not use `log.Printf`, `logrus`, or `zap`.

```go
// setup in main — text for terminals, JSON for piped/production output
logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{
    Level: slog.LevelInfo,
}))
slog.SetDefault(logger)

// usage
slog.Info("starting", "version", version)
slog.Error("command failed", "err", err)
```

Key-value pairs only — no `fmt.Sprintf` inside log calls.

Log to **stderr**. Stdout is for program output that other tools may pipe.

---

## CLI flags

Use `flag` from stdlib for simple tools. Use `github.com/spf13/cobra` when the tool has subcommands.

- With Cobra: define flags on the local `*cobra.Command`, not the global `flag.CommandLine`.
- With Cobra: set `SilenceUsage: true` and `SilenceErrors: true` on the root; handle error printing through the presenter, not Cobra's default.
- Print usage to stderr on bad input; exit code 2 for usage errors, 1 for runtime errors.
- `--version` and `--help` are always supported.

---

## Naming

- Packages: short, lowercase, no underscores. `config`, `fetch`, `report` — not `configManager`, `utils`, `common`.
- Unexported by default. Export only what callers outside the package genuinely need.
- Acronyms: `userID`, `httpClient`, `parseURL` — not `userId`, `HttpClient`, `parseUrl`.
- Error variables: `ErrFoo`; error types: `FooError`.

---

## Testing

Every non-trivial function has a `_test.go` file alongside it.

```go
func TestRun_missingFile(t *testing.T) {
    var stdout, stderr bytes.Buffer
    err := run([]string{"--file", "/no/such/file"}, &stdout, &stderr)
    if !errors.Is(err, os.ErrNotExist) {
        t.Fatalf("expected ErrNotExist, got %v", err)
    }
}
```

- Use `testing` + `testify/assert` if needed, nothing else.
- No mocks for filesystem or subprocess — use real temp dirs (`t.TempDir()`) and real binaries.
- Table-driven tests for functions with multiple input variants.
- `t.Fatal` / `t.Errorf` — not `panic`.

---

## I/O and stdlib hygiene

- `io.ReadAll`, `os.ReadFile`, `os.WriteFile` — never `ioutil.*` (deprecated since Go 1.16).
- Accept `io.Reader` / `io.Writer` in functions that do I/O; do not hardcode `os.Stdin` / `os.Stdout` below `main`.
- Close resources with `defer` immediately after the open succeeds, not at the end of a long function.
- Check errors from `Close()` on writable resources (files, network connections).

---

## What to avoid

| Don't | Do instead |
|---|---|
| `panic` for recoverable errors | return `error` |
| `init()` with side effects | explicit initialisation in `run()` |
| Global mutable state | pass dependencies explicitly |
| `interface{}` / `any` without cause | typed parameters |
| `ioutil.*` | `io.*` / `os.*` |
| `log.Printf` | `slog.Info` / `slog.Error` |
| `pkg/errors` | `fmt.Errorf("%w", err)` |
| Exported symbol in `internal/` "just in case" | export only at the point of need |

---

## slackcli project exceptions

These rules override or supplement the defaults above for this specific project.

**Entry point:** `main.go` at the repo root (not `cmd/slackcli/main.go`). This is a
single-binary tool; the extra nesting adds no value.

**Cobra is used** because the tool has subcommands (`auth login`, `auth status`, …).
`flag.FlagSet` is not used for any command.

**`run` signature:**
```go
func run(args []string, stdout, stderr io.Writer) error
```
All subcommand logic writes to the injected writers. `os.Stdout`/`os.Stderr` are
only referenced in `main()` itself.

**Two-layer architecture:** See `ARCHITECTURE.md`. Layer 1 (`cmd/`) returns raw
strings. Layer 2 (`internal/output`) applies overflow truncation, the `[exit:N | Xms]`
footer, and stderr attachment. The presenter is applied in `main.go`, never inside
`cmd/` or `internal/`.

**`log/slog` scope:** Use `slog` for progress messages to stderr (e.g. "Opening
browser…"). Do not use `slog` for Layer 1 command output — that goes through the
presenter as a plain string.

**Interface injection for testability:** When a `cmd/` function orchestrates calls to `*slack.Client` but must be unit-testable without keychain or network access, define a minimal unexported interface covering only the methods called, and accept that interface instead of the concrete type. The real `*slack.Client` satisfies it implicitly; tests provide a hand-written struct literal stub. No mock framework is used — a stub is a struct with fields, not generated code.

```go
type fileClient interface {
    GetFileInfo(fileID string) (slack.File, error)
    FetchFileBytes(url string) ([]byte, string, error)
}

func downloadFile(client fileClient, ref slack.FileRef, outputPath string) (string, error) { … }
```

This is not a violation of the
