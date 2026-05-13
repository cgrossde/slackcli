// Package cmd — snippet.go implements the "snippet" command group.
//
// Layer 1: SnippetCreate uploads a text snippet to a whitelisted channel.
//          SnippetDelete deletes a snippet by file ID.
//          SnippetTypes returns a formatted list of supported snippet types.
// Layer 2 wiring (presenter) is applied in main.go.
package cmd

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cgrossde/slackcli/internal/keychain"
	"github.com/cgrossde/slackcli/internal/slack"
)

// snippetType maps Slack snippet_type values to human descriptions.
// Source: https://docs.slack.dev/reference/objects/file-object#types
var snippetTypes = []struct{ name, description string }{
	{"auto", "Auto Detect Type"},
	{"text", "Plain Text"},
	{"c", "C"},
	{"cpp", "C++"},
	{"csharp", "C#"},
	{"css", "CSS"},
	{"go", "Go"},
	{"html", "HTML"},
	{"java", "Java"},
	{"javascript", "JavaScript"},
	{"json", "JSON"},
	{"kotlin", "Kotlin"},
	{"markdown", "Markdown"},
	{"python", "Python"},
	{"ruby", "Ruby"},
	{"rust", "Rust"},
	{"shell", "Shell"},
	{"sql", "SQL"},
	{"swift", "Swift"},
	{"typescript", "TypeScript"},
	{"xml", "XML"},
	{"yaml", "YAML"},
}

// SnippetCreateFlags holds parsed flag values for the snippet create subcommand.
type SnippetCreateFlags struct {
	Workspace string
	Channel   string
	Title     string
	Filetype  string
	Thread    string
	File      string
	Comment   string
}

// SnippetDeleteFlags holds parsed flag values for the snippet delete subcommand.
type SnippetDeleteFlags struct {
	Workspace string
}

// NewSnippetCmd builds the "snippet" Cobra command group.
func NewSnippetCmd() *cobra.Command {
	snippetCmd := &cobra.Command{
		Use:   "snippet",
		Short: "Create or delete Slack code snippets",
		Long: `Create and delete Slack code snippets (collapsible code previews).

Run "slackcli snippet types" to list supported --type values.`,
	}

	// --- create subcommand ---
	var createFlags SnippetCreateFlags
	createCmd := &cobra.Command{
		Use:   "create [content]",
		Short: "Upload a code snippet to a whitelisted channel",
		Long: `Upload text content as a code snippet to a whitelisted Slack channel.

Snippets appear in Slack as collapsible code previews with optional syntax
highlighting. The content body is taken from exactly one source (in priority order):
  1. Positional argument — inline content
  2. --file <path>       — read content from a local file
  3. Piped stdin (non-interactive)

Only channels in the write allowlist (allowlist.txt, embedded at build time) are accepted.
Run "slackcli snippet types" to list valid --type values.`,
		Example: `  echo 'SELECT 1' | slackcli snippet create --channel C0B3PCPL0CF --type sql
  slackcli snippet create 'fmt.Println("hi")' --channel C0B3PCPL0CF --type go --title "Hello"
  slackcli snippet create --file main.go --channel C0B3PCPL0CF
  slackcli snippet create --file query.sql --channel C0B3PCPL0CF --thread 1718197925.001234`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			out, err := SnippetCreate(args, createFlags, os.Stdin)
			if err != nil {
				return err
			}
			_, werr := fmt.Fprint(c.OutOrStdout(), out)
			return werr
		},
	}
	cf := createCmd.Flags()
	cf.StringVarP(&createFlags.Workspace, "workspace", "w", "", "Workspace (defaults to saved default)")
	cf.StringVar(&createFlags.Channel, "channel", "", "Channel ID to post to")
	cf.StringVar(&createFlags.Title, "title", "", "Snippet title (defaults to filename or \"Untitled\")")
	cf.StringVar(&createFlags.Filetype, "type", "", "Filetype for syntax highlighting (run: slackcli snippet types)")
	cf.StringVar(&createFlags.Thread, "thread", "", "Thread timestamp to post as a reply")
	cf.StringVar(&createFlags.File, "file", "", "Read content from a local file")
	cf.StringVar(&createFlags.Comment, "comment", "", "Initial comment accompanying the snippet")

	// --- delete subcommand ---
	var deleteFlags SnippetDeleteFlags
	deleteCmd := &cobra.Command{
		Use:   "delete <file_id>",
		Short: "Delete a snippet by file ID",
		Long: `Delete a Slack snippet (file) by its file ID via files.delete.

Only the file creator can delete a snippet; the Slack API enforces this.
File IDs look like "F0123456789".`,
		Example: `  slackcli snippet delete F0123456789
  slackcli snippet delete F0123456789 --workspace myorg.slack.com`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			out, err := SnippetDelete(args[0], deleteFlags)
			if err != nil {
				return err
			}
			_, werr := fmt.Fprint(c.OutOrStdout(), out)
			return werr
		},
	}
	df := deleteCmd.Flags()
	df.StringVarP(&deleteFlags.Workspace, "workspace", "w", "", "Workspace (defaults to saved default)")

	// --- types subcommand ---
	typesCmd := &cobra.Command{
		Use:   "types",
		Short: "List supported --type values for snippet create",
		RunE: func(c *cobra.Command, _ []string) error {
			fmt.Fprint(c.OutOrStdout(), SnippetTypes())
			return nil
		},
	}

	snippetCmd.AddCommand(createCmd, deleteCmd, typesCmd)
	return snippetCmd
}

// SnippetCreate is the Layer 1 implementation for the snippet create subcommand.
// It resolves the content body, infers filetype and title when possible, then
// uploads to the Slack API.
//
// stdin is used only when no positional text arg or --file is provided and
// stdin is a pipe (non-tty). Pass os.Stdin from the command layer; tests may
// inject a bytes.Reader.
func SnippetCreate(args []string, flags SnippetCreateFlags, stdin io.Reader) (string, error) {
	if flags.Channel == "" {
		return "", fmt.Errorf("snippet create: --channel is required")
	}

	var err error

	// Resolve workspace.
	workspace := flags.Workspace
	if workspace == "" {
		workspace, err = keychain.ResolveDefault()
		if err != nil {
			return "", fmt.Errorf("resolving workspace: %w\nRun: slackcli auth login --workspace <name>", err)
		}
	} else {
		workspace = CanonicalDomain(workspace)
	}

	// Resolve content body.
	content, err := resolveMessageBody(args, flags.File, stdin)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(content) == "" {
		return "", fmt.Errorf("snippet create: content is empty")
	}

	// Resolve filetype: explicit flag > inferred from file extension > empty.
	filetype := flags.Filetype
	if filetype == "" && flags.File != "" {
		ext := strings.TrimPrefix(filepath.Ext(flags.File), ".")
		filetype = inferFiletype(ext)
	}

	// Resolve title: explicit flag > base filename > "Untitled" (handled in CreateSnippet).
	title := flags.Title
	if title == "" && flags.File != "" {
		title = filepath.Base(flags.File)
	}

	// Load credentials.
	workspace, entry, err := loadCredentials(workspace)
	if err != nil {
		return "", err
	}

	client := slack.NewClient(entry.Token, entry.Cookie)

	channelID := flags.Channel
	if !looksLikeChannelID(channelID) {
		resolved, rerr := resolveChannelName(client, workspace, channelID)
		if rerr != nil {
			return "", rerr
		}
		channelID = resolved
	}

	fileID, resolvedTitle, err := client.CreateSnippet(slack.CreateSnippetParams{
		Channel:  channelID,
		Content:  content,
		Title:    title,
		Filetype: filetype,
		ThreadTs: flags.Thread,
		Comment:  flags.Comment,
	})
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("Snippet created: %s (%s) in %s\n", fileID, resolvedTitle, channelID), nil
}

// SnippetDelete is the Layer 1 implementation for the snippet delete subcommand.
func SnippetDelete(fileID string, flags SnippetDeleteFlags) (string, error) {
	var err error

	// Resolve workspace.
	workspace := flags.Workspace
	if workspace == "" {
		workspace, err = keychain.ResolveDefault()
		if err != nil {
			return "", fmt.Errorf("resolving workspace: %w\nRun: slackcli auth login --workspace <name>", err)
		}
	} else {
		workspace = CanonicalDomain(workspace)
	}

	workspace, entry, err := loadCredentials(workspace)
	if err != nil {
		return "", err
	}

	client := slack.NewClient(entry.Token, entry.Cookie)
	if err := client.DeleteSnippet(fileID); err != nil {
		return "", err
	}

	return fmt.Sprintf("Snippet deleted: %s\n", fileID), nil
}

// SnippetTypes returns a formatted table of supported snippet type values.
func SnippetTypes() string {
	var b strings.Builder
	for _, st := range snippetTypes {
		fmt.Fprintf(&b, "%-14s %s\n", st.name, st.description)
	}
	return b.String()
}

// inferFiletype maps common file extensions to Slack snippet_type values.
// Returns empty string when the extension is unknown.
func inferFiletype(ext string) string {
	switch strings.ToLower(ext) {
	case "c":
		return "c"
	case "cpp", "cc", "cxx":
		return "cpp"
	case "cs":
		return "csharp"
	case "css":
		return "css"
	case "go":
		return "go"
	case "html", "htm":
		return "html"
	case "java":
		return "java"
	case "js", "mjs":
		return "javascript"
	case "json":
		return "json"
	case "kt", "kts":
		return "kotlin"
	case "md", "markdown":
		return "markdown"
	case "py":
		return "python"
	case "rb":
		return "ruby"
	case "rs":
		return "rust"
	case "sh", "bash":
		return "shell"
	case "sql":
		return "sql"
	case "swift":
		return "swift"
	case "ts", "tsx":
		return "typescript"
	case "xml":
		return "xml"
	case "yaml", "yml":
		return "yaml"
	case "txt":
		return "text"
	default:
		return ""
	}
}
