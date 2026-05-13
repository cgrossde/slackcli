// Package slack — snippet.go implements file upload (snippet) and delete
// operations. Upload uses the modern 3-step flow via slack-go's UploadFileContext.
package slack

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	slackgo "github.com/slack-go/slack"
)

// CreateSnippetParams holds all parameters for uploading a snippet.
type CreateSnippetParams struct {
	Channel  string // required; must be in AllowedWriteChannels
	Content  string // required
	Title    string // optional; defaults to "Untitled"
	Filetype string // optional; e.g. "go", "python", "text"
	ThreadTs string // optional; post as reply in this thread
	Comment  string // optional; initial_comment accompanying the snippet
}

// CreateSnippet uploads text content as a code snippet to a channel.
// Returns (fileID, resolvedTitle, error).
//
// Returns an error if params.Channel is not in AllowedWriteChannels.
func (c *Client) CreateSnippet(params CreateSnippetParams) (string, string, error) {
	if !IsWriteAllowed(params.Channel) {
		return "", "", fmt.Errorf("snippet: channel %q is not in the write allowlist", params.Channel)
	}

	title := params.Title
	if title == "" {
		title = "Untitled"
	}

	// Derive a filename: title + extension based on filetype.
	filename := deriveSnippetFilename(title, params.Filetype)

	uploadParams := slackgo.UploadFileParameters{
		Content:         params.Content,
		FileSize:        len(params.Content),
		Filename:        filename,
		Title:           title,
		SnippetType:     params.Filetype,
		Channel:         params.Channel,
		ThreadTimestamp: params.ThreadTs,
		InitialComment:  params.Comment,
	}

	file, err := c.api.UploadFileContext(context.Background(), uploadParams)
	if err != nil {
		return "", "", fmt.Errorf("snippet: files.upload: %w", err)
	}

	return file.ID, title, nil
}

// DeleteSnippet deletes a file (snippet) by ID via files.delete.
// The Slack API enforces ownership — only the file creator can delete.
func (c *Client) DeleteSnippet(fileID string) error {
	if err := c.api.DeleteFileContext(context.Background(), fileID); err != nil {
		return fmt.Errorf("snippet: files.delete: %w", err)
	}
	return nil
}

// deriveSnippetFilename returns a safe filename for the snippet.
// If filetype is a known extension alias, it is appended; otherwise
// the filename is just the sanitised title.
func deriveSnippetFilename(title, filetype string) string {
	// Remove path separators that would confuse the multipart upload.
	safe := strings.NewReplacer("/", "-", "\\", "-").Replace(title)
	if safe == "" {
		safe = "snippet"
	}

	ext := filetypeExtension(filetype)
	if ext != "" {
		// Avoid double extension if the title already ends with it.
		if filepath.Ext(safe) != "."+ext {
			return safe + "." + ext
		}
	}
	return safe
}

// filetypeExtension maps a Slack snippet_type value to a file extension.
// Returns empty string for unknown or empty types.
func filetypeExtension(ft string) string {
	switch ft {
	case "c":
		return "c"
	case "cpp":
		return "cpp"
	case "csharp":
		return "cs"
	case "css":
		return "css"
	case "go":
		return "go"
	case "html":
		return "html"
	case "java":
		return "java"
	case "javascript":
		return "js"
	case "json":
		return "json"
	case "kotlin":
		return "kt"
	case "markdown":
		return "md"
	case "python":
		return "py"
	case "ruby":
		return "rb"
	case "rust":
		return "rs"
	case "shell":
		return "sh"
	case "sql":
		return "sql"
	case "swift":
		return "swift"
	case "typescript":
		return "ts"
	case "xml":
		return "xml"
	case "yaml":
		return "yaml"
	case "text", "auto", "":
		return ""
	default:
		return ""
	}
}
