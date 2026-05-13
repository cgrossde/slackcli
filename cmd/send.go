// Package cmd — send.go implements the "send" command.
//
// Layer 1: Send posts a message to a whitelisted Slack channel.
// Layer 2 wiring (presenter) is applied in main.go.
package cmd

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cgrossde/slackcli/internal/keychain"
	"github.com/cgrossde/slackcli/internal/slack"
)

// SendFlags holds the parsed flag values for the send command.
type SendFlags struct {
	Workspace string
	Channel   string
	Thread    string
	File      string
	Markdown  bool
	React     string // emoji name to react with after sending (empty = no reaction)
	NoPreview bool
}

// NewSendCmd builds the "send" Cobra command.
func NewSendCmd() *cobra.Command {
	var flags SendFlags

	cmd := &cobra.Command{
		Use:   "send [message | url | channelID:ts]",
		Short: "Post a message to a Slack channel",
		Long: `Post a message to a whitelisted Slack channel.

The message body is taken from exactly one source (in priority order):
  1. Positional argument (if it is not a Slack URL or channelID:ts) — inline text
  2. --file <path>                                                   — read body from file
  3. Piped stdin (non-interactive)                                   — read entire stdin

When the positional argument is a Slack URL (https://*.slack.com/archives/...) or a
channelID:ts token (e.g. C0B3PCPL0CF:1718197925.001234), the channel ID and thread
timestamp are extracted from it and used as the post target (reply in that thread).
In that case the message body must come from --file or stdin.

Both a ref (URL or channelID:ts) and an inline text body may be supplied together:
  slackcli send "my reply" C0B3PCPL0CF:1718197925.001234

Only channels in the write allowlist (allowlist.txt, embedded at build time) are accepted.

--no-preview suppresses Slack's automatic link preview (unfurl_links: false).`,
		Example: `  echo "hello" | slackcli send --channel C0B3PCPL0CF
  slackcli send "quick note" --channel C0B3PCPL0CF
  slackcli send --file msg.txt --channel C0B3PCPL0CF
  slackcli send --file msg.txt --channel C0B3PCPL0CF --thread 1718197925.001234
  slackcli send --file report.md --channel C0B3PCPL0CF --md
  slackcli send "quick note" --channel C0B3PCPL0CF --react white_check_mark
  slackcli send "reply" https://myorg.slack.com/archives/C0B3PCPL0CF/p1718197925001234
  slackcli send "reply" C0B3PCPL0CF:1718197925.001234
  slackcli send "check this" --channel C0B3PCPL0CF --no-preview`,
		Args: cobra.RangeArgs(0, 2),
		RunE: func(c *cobra.Command, args []string) error {
			out, err := Send(args, flags, os.Stdin)
			if err != nil {
				return err
			}
			_, werr := fmt.Fprint(c.OutOrStdout(), out)
			return werr
		},
	}

	f := cmd.Flags()
	f.StringVarP(&flags.Workspace, "workspace", "w", "", "Workspace (defaults to saved default)")
	f.StringVar(&flags.Channel, "channel", "", "Channel ID to post to")
	f.StringVar(&flags.Thread, "thread", "", "Thread timestamp to reply in")
	f.StringVar(&flags.File, "file", "", "Read message body from file")
	f.BoolVar(&flags.Markdown, "md", false, "Convert Markdown to Slack mrkdwn before sending")
	f.StringVar(&flags.React, "react", "", "Emoji name to react with after sending (without colons)")
	f.BoolVar(&flags.NoPreview, "no-preview", false, "Suppress Slack link preview (unfurl_links: false)")

	return cmd
}

// Send is the Layer 1 implementation for the send command. It resolves the
// target channel and message body, then posts via the Slack API.
//
// stdin is used only when no positional text arg or --file is provided and
// stdin is a pipe (non-tty). Pass os.Stdin from the command layer; tests may
// inject a bytes.Reader.
func Send(args []string, flags SendFlags, stdin io.Reader) (string, error) {
	channelID, threadTs, bodyArgs, err := parseSendArgs(args, flags)
	if err != nil {
		return "", err
	}

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

	// Resolve message body.
	text, err := resolveMessageBody(bodyArgs, flags.File, stdin)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(text) == "" {
		return "", fmt.Errorf("message body is empty")
	}

	// Apply Markdown conversion if requested.
	if flags.Markdown {
		text = slack.MarkdownToMrkdwn(text)
	}

	// Load credentials and post.
	workspace, entry, err := loadCredentials(workspace)
	if err != nil {
		return "", err
	}

	client := slack.NewClient(entry.Token, entry.Cookie)

	// Resolve channel name to ID if the caller passed a name (e.g. "general").
	if !looksLikeChannelID(channelID) {
		resolved, rerr := resolveChannelName(client, workspace, channelID)
		if rerr != nil {
			return "", rerr
		}
		channelID = resolved
	}

	ch, ts, err := client.SendMessage(channelID, text, threadTs, flags.NoPreview)
	if err != nil {
		return "", err
	}

	out := fmt.Sprintf("Sent: %s ts=%s\n", ch, ts)

	if flags.React != "" {
		emoji := strings.Trim(flags.React, ":")
		if err := client.AddReaction(ch, ts, emoji); err != nil {
			return out, fmt.Errorf("send: adding reaction :%s:: %w", emoji, err)
		}
		out += fmt.Sprintf("Reacted: :%s: on %s ts=%s\n", emoji, ch, ts)
	}

	return out, nil
}

// parseSendArgs resolves channelID, threadTs, and any remaining positional
// body text from the command args and flags. bodyArgs holds text positional
// arguments (i.e. non-URL args); may be empty if body comes from file/stdin.
func parseSendArgs(args []string, flags SendFlags) (channelID, threadTs string, bodyArgs []string, err error) {
	threadTs = flags.Thread

	// Classify positional args: at most one ref arg (URL or channelID:ts) and
	// one inline text arg.
	var refArg, textArg string
	for _, a := range args {
		if isSlackArchivesURL(a) || slack.IsChannelTs(a) {
			if refArg != "" {
				return "", "", nil, fmt.Errorf("send: at most one message reference argument is accepted")
			}
			refArg = a
		} else {
			if textArg != "" {
				return "", "", nil, fmt.Errorf("send: at most one inline text argument is accepted")
			}
			textArg = a
		}
	}

	if refArg != "" {
		// Ref form: extract channel + thread_ts from URL or channelID:ts.
		var ref slack.MessageRef
		if isSlackArchivesURL(refArg) {
			ref, err = slack.ParseSlackURL(refArg)
		} else {
			ref, err = slack.ParseChannelTs(refArg)
		}
		if err != nil {
			return "", "", nil, fmt.Errorf("send: %w", err)
		}
		channelID = ref.ChannelID
		// Use the ref's ts as the thread to reply in, unless --thread overrides.
		if threadTs == "" {
			threadTs = ref.Ts
		}
		if flags.Channel != "" && flags.Channel != channelID {
			return "", "", nil, fmt.Errorf("send: --channel %q conflicts with channel %q from positional arg", flags.Channel, channelID)
		}
	} else {
		// No ref: --channel is required.
		channelID = flags.Channel
		if channelID == "" {
			return "", "", nil, fmt.Errorf("send: --channel is required when no Slack URL or channelID:ts is provided")
		}
	}

	if textArg != "" {
		bodyArgs = []string{textArg}
	}
	return channelID, threadTs, bodyArgs, nil
}

// resolveMessageBody returns the message text from the first available source:
//  1. bodyArgs (inline text positional arg)
//  2. --file path
//  3. stdin (only when stdin is a pipe)
func resolveMessageBody(bodyArgs []string, filePath string, stdin io.Reader) (string, error) {
	if len(bodyArgs) > 0 {
		return bodyArgs[0], nil
	}

	if filePath != "" {
		data, err := os.ReadFile(filePath)
		if err != nil {
			return "", fmt.Errorf("send: reading --file %q: %w", filePath, err)
		}
		return string(data), nil
	}

	// Try stdin — but only if it is a pipe (not a terminal).
	if f, ok := stdin.(*os.File); ok {
		stat, statErr := f.Stat()
		if statErr != nil || (stat.Mode()&os.ModeCharDevice) != 0 {
			// Terminal or stat failed — no stdin available.
			return "", fmt.Errorf("send: message body required (pass text arg, --file, or pipe via stdin)")
		}
	}

	data, err := io.ReadAll(stdin)
	if err != nil {
		return "", fmt.Errorf("send: reading stdin: %w", err)
	}
	return string(data), nil
}

// isSlackArchivesURL reports whether s looks like a Slack message permalink.
func isSlackArchivesURL(s string) bool {
	return strings.HasPrefix(s, "https://") &&
		strings.Contains(s, ".slack.com/archives/")
}
