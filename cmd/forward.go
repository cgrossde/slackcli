// Package cmd — forward.go implements the "forward" command.
//
// Layer 1: Forward posts a source message's permalink to a destination channel
// with unfurl_links enabled, optionally prepending a note. Layer 2 wiring
// (presenter) is applied in main.go.
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/cgrossde/slackcli/internal/keychain"
	"github.com/cgrossde/slackcli/internal/slack"
)

// ForwardFlags holds the parsed flag values for the forward command.
type ForwardFlags struct {
	Workspace string
	Channel   string // source --channel
	Ts        string // source --ts
	To        string // destination channel (required)
	Note      string // optional note prepended before the permalink
	NoPreview bool   // suppress link unfurling (unfurl_links: false)
}

// NewForwardCmd builds the "forward" Cobra command.
func NewForwardCmd() *cobra.Command {
	var flags ForwardFlags

	cmd := &cobra.Command{
		Use:   "forward [url | channelID:ts]",
		Short: "Forward a Slack message to another channel or DM",
		Long: `Forward a message to a whitelisted destination channel by posting its
permalink with link unfurling enabled by default. Pass --no-preview to suppress
the link preview. Slack renders a rich preview of the original message in the
destination channel.

The source message is specified by exactly one of:
  - A Slack message URL as the first positional argument
  - A channelID:ts token as the first positional argument
  - --channel <id> and --ts <timestamp> flags

--to is required and specifies the destination channel ID or name.

An optional --note prepends text before the permalink (mirrors Slack's
"add a note" field in the native forward UI).

Only the destination channel is checked against the write allowlist.
The source channel is read-only (permalink lookup only) and is not gated.`,
		Example: `  slackcli forward https://myorg.slack.com/archives/C0B3PCPL0CF/p1718197925001234 --to C0B3Z1KT80K
  slackcli forward C0B3PCPL0CF:1718197925.001234 --to C0B3Z1KT80K --note "FYI"
  slackcli forward --channel C0B3PCPL0CF --ts 1718197925.001234 --to C0B3Z1KT80K`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(c *cobra.Command, args []string) error {
			out, err := Forward(args, flags)
			if err != nil {
				return err
			}
			_, werr := fmt.Fprint(c.OutOrStdout(), out)
			return werr
		},
	}

	f := cmd.Flags()
	f.StringVarP(&flags.Workspace, "workspace", "w", "", "Workspace (defaults to saved default)")
	f.StringVar(&flags.Channel, "channel", "", "Channel ID containing the source message")
	f.StringVar(&flags.Ts, "ts", "", "Source message timestamp")
	f.StringVar(&flags.To, "to", "", "Destination channel ID or name (required)")
	f.StringVar(&flags.Note, "note", "", "Optional note prepended before the permalink")
	f.BoolVar(&flags.NoPreview, "no-preview", false, "Suppress link preview on the forwarded permalink (unfurl_links: false)")

	return cmd
}

// Forward is the Layer 1 implementation for the forward command. It resolves
// the source message, then posts its permalink to the destination channel.
func Forward(args []string, flags ForwardFlags) (string, error) {
	if flags.To == "" {
		return "", fmt.Errorf("forward: --to is required (destination channel)")
	}

	srcChannelID, srcTs, srcWorkspace, err := parseForwardSource(args, flags)
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

	workspace, entry, err := loadCredentials(workspace)
	if err != nil {
		return "", err
	}

	client := slack.NewClient(entry.Token, entry.Cookie)

	// Resolve destination channel name to ID if needed.
	dstChannelID := flags.To
	if !looksLikeChannelID(dstChannelID) {
		dstChannelID, err = resolveChannelName(client, workspace, dstChannelID)
		if err != nil {
			return "", err
		}
	}

	// Use the workspace extracted from the URL when available; it's already the
	// correct host (e.g. "myorg.slack.com"). Fall back to the resolved
	// keychain domain for the channelID:ts and --channel/--ts forms.
	if srcWorkspace == "" {
		srcWorkspace = workspace
	}

	ch, respTs, err := client.ForwardMessage(srcWorkspace, srcChannelID, srcTs, dstChannelID, flags.Note, flags.NoPreview)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("Forwarded: %s ts=%s\n", ch, respTs), nil
}

// parseForwardSource resolves the channel ID and timestamp for the forward command
// from a positional Slack URL, a compact channelID:ts token, or --channel/--ts flags.
func parseForwardSource(extraArgs []string, flags ForwardFlags) (channelID, ts, workspace string, err error) {
	if len(extraArgs) > 0 {
		raw := extraArgs[0]
		var ref slack.MessageRef
		switch {
		case isSlackArchivesURL(raw):
			ref, err = slack.ParseSlackURL(raw)
			if err != nil {
				return "", "", "", fmt.Errorf("forward: %w", err)
			}
		case slack.IsChannelTs(raw):
			ref, err = slack.ParseChannelTs(raw)
			if err != nil {
				return "", "", "", fmt.Errorf("forward: %w", err)
			}
		default:
			return "", "", "", fmt.Errorf("forward: argument must be a Slack URL or channelID:ts; got %q", raw)
		}
		channelID = ref.ChannelID
		ts = ref.Ts
		workspace = ref.Workspace // non-empty only for URL form

		// Allow flags to override but error on conflict.
		if flags.Channel != "" && flags.Channel != channelID {
			return "", "", "", fmt.Errorf("forward: --channel %q conflicts with channel %q from positional arg", flags.Channel, channelID)
		}
		if flags.Ts != "" && flags.Ts != ts {
			return "", "", "", fmt.Errorf("forward: --ts %q conflicts with timestamp %q from positional arg", flags.Ts, ts)
		}
		return channelID, ts, workspace, nil
	}

	// Flag form — workspace left empty; Forward fills it from the keychain.
	channelID = flags.Channel
	ts = flags.Ts
	if channelID == "" || ts == "" {
		return "", "", "", fmt.Errorf("forward: provide a Slack URL, channelID:ts, or both --channel and --ts")
	}
	return channelID, ts, "", nil
}