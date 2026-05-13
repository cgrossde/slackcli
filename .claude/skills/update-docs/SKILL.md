---
name: update-docs
description: Update the slackcli docs/ directory, CLAUDE.md, CODING-INSTRUCTIONS.md, and ARCHITECTURE.md to match the current source code. Use when the user asks to update docs, sync documentation, or invokes /update-docs.
allowed-tools:
  - Bash
  - Read
  - Edit
  - Write
  - Search
---

# Update slackcli Docs

Keep all documentation in sync with the current source. The targets are:

| File | Covers |
|------|--------|
| `CLAUDE.md` | Command table, flags, output contract, architecture summary — agent-facing quick reference |
| `CODING-INSTRUCTIONS.md` | Go style, error handling, testing rules — developer conventions |
| `ARCHITECTURE.md` | Two-layer model, JSON output mode, per-command field schemas, design constraints |
| `docs/read.md` | `read` command: all accepted forms, flags, output format, JSON schema |
| `docs/search.md` | `search` command: flags, filters, output format, JSON schema, pagination |
| `docs/users.md` | `users` command: flags, output format, JSON schema |
| `docs/auth.md` | `auth` subcommands: flags, keychain behaviour |
| `docs/live.md` | `live` command: flags, event types, streaming behaviour |
| `docs/websocket.md` | WebSocket gateway internals, event struct, reconnection |
| `docs/internals.md` | HTTP client, TLS fingerprinting, Cobra wiring, keychain, two-layer bridge |

---

## Step 1: Identify what changed

```bash
git diff HEAD~10..HEAD --name-only
```

Adjust the depth if the branch is newer. Map changed source files to affected docs:

| Changed source | Docs to check |
|---------------|---------------|
| `cmd/read.go`, `cmd/pretty.go`, `cmd/iterm2.go` | `docs/read.md`, `CLAUDE.md`, `ARCHITECTURE.md` |
| `cmd/search.go`, `internal/slack/search.go` | `docs/search.md`, `CLAUDE.md`, `ARCHITECTURE.md` |
| `cmd/users.go`, `internal/slack/users*.go` | `docs/users.md`, `CLAUDE.md` |
| `cmd/auth.go`, `internal/slack/auth.go` | `docs/auth.md`, `CLAUDE.md` |
| `cmd/live.go`, `internal/slack/websocket.go` | `docs/live.md`, `docs/websocket.md`, `CLAUDE.md` |
| `internal/slack/client.go` | `docs/internals.md` |
| `internal/slack/conversations.go`, `url.go` | `docs/read.md`, `ARCHITECTURE.md` |
| `internal/keychain/` | `docs/internals.md`, `docs/auth.md` |
| `internal/output/presenter.go` | `docs/internals.md`, `ARCHITECTURE.md` |
| `main.go` | `docs/internals.md`, `CLAUDE.md` |

Read only the source files relevant to the changed docs. Do not read unchanged packages.

---

## Step 2: For each stale doc, identify the gaps

Compare source to doc. Look for:

- **New commands or subcommands** not in `CLAUDE.md`'s command table
- **New flags** not listed under `## Flags`
- **New accepted input forms** (e.g. file permalink for `read`)
- **Changed output format** — new fields, new lines, renamed fields
- **New JSON fields** missing from the schema table in `ARCHITECTURE.md` or the command doc
- **Removed or renamed flags/fields**
- **New behaviour** worth documenting (e.g. inline image rendering, file download, Enterprise Grid fallback, reactions display)

Do **not** update without user approval:
- **`ARCHITECTURE.md`** structural changes — anything that modifies the two-layer model, design constraints, or rationale prose. Additive factual updates (new files in the package tree, new fields in the JSON schema table) are fine without asking.
- **`CODING-INSTRUCTIONS.md`** — any change, because every rule here affects all future code written for this project.
- **`CLAUDE.md`** command table and output contract — additions are fine; removing or changing an existing entry requires approval.

For these, **stop before editing** and present the proposed change to the user:

```
Proposed change to ARCHITECTURE.md:
  Section "Package structure" — add cmd/iterm2.go entry
  Reason: new file added in recent commits

Proposed change to CODING-INSTRUCTIONS.md:
  Add note under "slackcli project exceptions": fileClient interface pattern
  for injecting test doubles without mocks
  Reason: established by cmd/read.go downloadFile refactor

Approve? (yes / no / edit)
```

Apply only on explicit approval. If the user says no or edits, adjust and re-present before applying.

Silently update (no approval needed):
- `docs/*.md` — command docs are always kept current without asking
- `CLAUDE.md` — adding a new command row or flag that didn't exist before
- `ARCHITECTURE.md` — additive factual edits: new file in package tree, new field in JSON schema table
---

## Step 3: Update each stale doc

### `CLAUDE.md`

- **Command table**: one row per command, flags column lists all flags with brief descriptions. Keep existing format.
- **Output contract**: reflect any new output fields or changed footer behaviour.
- **Reference section**: add links to any new doc files.

### `CODING-INSTRUCTIONS.md`

Update only when:
- A new project-wide pattern was established (e.g. a new interface introduced for testability)
- An existing rule was changed or a new exception added
- A new external dependency was added with rationale

### `ARCHITECTURE.md`

- **Package structure** tree: add new files, remove deleted ones.
- **JSON output schema table**: add new fields for any command that gained them. Columns: `Command | Record fields | Trailer`.
- **Design constraints**: only update if a constraint changed.

### `docs/*.md`

**Flags section** — one bullet per flag, format:
```
- `--flag-name value`: Description. Default: `value` (omit if empty/none).
```

**Output format** — show a realistic rendered example. Use real-looking IDs, not `<placeholder>`.

**JSON schema table** — columns: `Field | Type | Notes`. Notes explain semantics, not Go types. Fields marked `omitempty` in source: "omitted when zero/empty".

**Reference forms** — list every accepted input syntax. For `read`, this includes:
1. Message permalink URL: `https://org.slack.com/archives/CHANNELID/pTIMESTAMP`
2. Compact form: `CHANNELID:ts`
3. File permalink URL: `https://org.slack.com/files/USERID/FILEID/name.ext`

**Keep examples concrete** — realistic IDs and timestamps, not angle-bracket placeholders.

---

## Step 4: Verify

After editing, re-read each changed doc:
- No section references a flag or field that no longer exists
- JSON schema table matches actual `json:"..."` struct tags in source
- Every accepted input form is listed
- The `## Implementation` section (if present) names the correct files

---

## Step 5: Report

Print a one-line summary per file touched:
```
CLAUDE.md            — updated: added read --output flag, file download form
ARCHITECTURE.md      — updated: read --json schema (files, reactions fields)
docs/read.md         — updated: file permalink form, --output flag, files/reactions output, image rendering note
docs/internals.md    — no changes needed
```

Do not commit. The user decides when to commit.
