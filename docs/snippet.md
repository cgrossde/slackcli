# snippet

Create and delete Slack code snippets (collapsible code previews with syntax highlighting).

## Subcommands

| Subcommand | Description |
|---|---|
| `snippet create` | Upload text as a code snippet to a whitelisted channel |
| `snippet delete` | Delete a snippet by file ID |
| `snippet types` | List supported `--type` values |

---

## snippet create

### Usage

```
slackcli snippet create [content] [flags]
```

### Description

Uploads text content as a code snippet to a whitelisted Slack channel. Snippets appear
in Slack as collapsible code blocks with optional syntax highlighting.

Only channels in the write allowlist (configured in `internal/slack/allowlist.txt` at build time) may receive snippets. Attempts to post to any other channel are rejected
before any API call is made.

### Content Body

Exactly one source is required. Sources are evaluated in this priority order:

1. **Positional argument** — inline content string
2. **`--file <path>`** — entire file contents are read and used as the snippet body
3. **Piped stdin** — when stdin is non-interactive (pipe or redirect), the entire stdin stream is read

### Filetype Inference

When `--type` is omitted but `--file` is provided, the filetype is inferred from the
file extension:

| Extension(s) | `--type` value |
|---|---|
| `.go` | `go` |
| `.py` | `python` |
| `.ts`, `.tsx` | `typescript` |
| `.js`, `.mjs` | `javascript` |
| `.rs` | `rust` |
| `.rb` | `ruby` |
| `.java` | `java` |
| `.kt`, `.kts` | `kotlin` |
| `.cs` | `csharp` |
| `.c` | `c` |
| `.cpp`, `.cc`, `.cxx` | `cpp` |
| `.sh`, `.bash` | `shell` |
| `.sql` | `sql` |
| `.json` | `json` |
| `.yaml`, `.yml` | `yaml` |
| `.xml` | `xml` |
| `.css` | `css` |
| `.html`, `.htm` | `html` |
| `.md`, `.markdown` | `markdown` |
| `.txt` | `text` |
| (unknown) | _(no type set)_ |

Run `slackcli snippet types` to see all valid `--type` values.

### Title Inference

When `--title` is omitted but `--file` is provided, the title defaults to the base
filename (e.g. `--file path/to/main.go` → title `main.go`). When neither is provided,
the title defaults to `Untitled`.

### Flags

- `--channel <id>`: Channel ID to post to. **Required.**
- `--title <text>`: Snippet title. Defaults to the filename or `"Untitled"`.
- `--type <filetype>`: Syntax highlighting type (run `slackcli snippet types` for valid values).
- `--thread <ts>`: Post as a reply in this thread timestamp.
- `--file <path>`: Read content from a local file instead of inline text or stdin.
- `--comment <text>`: Initial comment text accompanying the snippet.
- `-w, --workspace <name>`: Workspace to use. Defaults to the stored default.

### Output

```
Snippet created: F0123456789 (My Title) in C0B3PCPL0CF
[exit:0 | 450ms]
```

### Examples

```
# Pipe a file
cat main.go | slackcli snippet create --channel C0B3PCPL0CF --type go --title "main.go"

# Inline content
slackcli snippet create 'SELECT count(*) FROM users' --channel C0B3PCPL0CF --type sql

# From a file (type and title inferred from filename)
slackcli snippet create --file main.go --channel C0B3PCPL0CF

# Post as a thread reply
slackcli snippet create --file query.sql --channel C0B3PCPL0CF --thread 1718197925.001234

# With an initial comment
slackcli snippet create --file report.py --channel C0B3PCPL0CF --comment "Latest version"
```

---

## snippet delete

### Usage

```
slackcli snippet delete <file_id> [flags]
```

### Description

Deletes a Slack snippet (file) by its file ID via `files.delete`. Only the file creator
can delete a snippet; the Slack API enforces ownership. No channel allowlist check is
performed — ownership is the sole constraint.

File IDs look like `F0123456789`. They are returned by `snippet create` and visible in
Slack file permalinks.

### Flags

- `-w, --workspace <name>`: Workspace to use. Defaults to the stored default.

### Output

```
Snippet deleted: F0123456789
[exit:0 | 210ms]
```

### Examples

```
slackcli snippet delete F0123456789
slackcli snippet delete F0123456789 --workspace myorg.slack.com
```

---

## snippet types

### Usage

```
slackcli snippet types
```

### Description

Lists all supported `--type` values for `snippet create`. Output is a two-column table
(type → description). Hardcoded; no API call required.

### Output

```
auto           Auto Detect Type
text           Plain Text
c              C
cpp            C++
csharp         C#
css            CSS
go             Go
html           HTML
java           Java
javascript     JavaScript
json           JSON
kotlin         Kotlin
markdown       Markdown
python         Python
ruby           Ruby
rust           Rust
shell          Shell
sql            SQL
swift          Swift
typescript     TypeScript
xml            XML
yaml           YAML
[exit:0 | 1ms]
```
