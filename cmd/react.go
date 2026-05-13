// Package cmd — react.go implements the "react" command.
//
// Layer 1: React adds or removes an emoji reaction on a message in a
// whitelisted channel. Layer 2 wiring (presenter) is applied in main.go.
package cmd

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cgrossde/slackcli/internal/keychain"
	"github.com/cgrossde/slackcli/internal/slack"
)

// ReactFlags holds the parsed flag values for the react command.
type ReactFlags struct {
	Workspace string
	Channel   string
	Ts        string
	Remove    bool
}

// NewReactCmd builds the "react" Cobra command.
func NewReactCmd() *cobra.Command {
	var flags ReactFlags

	cmd := &cobra.Command{
		Use:   "react <emoji> [url | channelID:ts]",
		Short: "Add (or remove) an emoji reaction on a Slack message",
		Long: `Add an emoji reaction to a message in a whitelisted Slack channel.
Pass --remove to remove an existing reaction instead.

The target message is specified by exactly one of:
  - A Slack message URL as the second positional argument
  - A channelID:ts token as the second positional argument
  - --channel <id> and --ts <timestamp> flags

The emoji name must be provided without surrounding colons.

Only channels in the write allowlist (allowlist.txt, embedded at build time) are accepted.`,
		Example: `  slackcli react thumbsup https://myorg.slack.com/archives/C0B3PCPL0CF/p1718197925001234
  slackcli react white_check_mark --channel C0B3PCPL0CF --ts 1718197925.001234
  slackcli react thumbsup --remove --channel C0B3PCPL0CF --ts 1718197925.001234`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(c *cobra.Command, args []string) error {
			out, err := React(args, flags)
			if err != nil {
				return err
			}
			_, werr := fmt.Fprint(c.OutOrStdout(), out)
			return werr
		},
	}

	f := cmd.Flags()
	f.StringVarP(&flags.Workspace, "workspace", "w", "", "Workspace (defaults to saved default)")
	f.StringVar(&flags.Channel, "channel", "", "Channel ID containing the message")
	f.StringVar(&flags.Ts, "ts", "", "Message timestamp")
	f.BoolVar(&flags.Remove, "remove", false, "Remove the reaction instead of adding it")

	return cmd
}

// React is the Layer 1 implementation for the react command. It resolves the
// target message and emoji, then adds the reaction via the Slack API.
func React(args []string, flags ReactFlags) (string, error) {
	if len(args) < 1 {
		return "", fmt.Errorf("react: emoji name is required as the first argument")
	}

	emoji := strings.Trim(args[0], ":")
	if emoji == "" {
		return "", fmt.Errorf("react: emoji name must not be empty")
	}

	channelID, ts, err := parseReactTarget(args[1:], flags)
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

	// Resolve channel name to ID if the caller passed a name (e.g. "general").
	if !looksLikeChannelID(channelID) {
		channelID, err = resolveChannelName(client, workspace, channelID)
		if err != nil {
			return "", err
		}
	}

	if flags.Remove {
		if err := client.RemoveReaction(channelID, ts, emoji); err != nil {
			return "", err
		}
		return fmt.Sprintf("Removed: :%s: from %s ts=%s\n", emoji, channelID, ts), nil
	}
	if err := client.AddReaction(channelID, ts, emoji); err != nil {
		return "", err
	}
	return fmt.Sprintf("Reacted: :%s: on %s ts=%s\n", emoji, channelID, ts), nil
}

// parseReactTarget resolves the channel ID and timestamp for the react command
// from a positional Slack URL, a compact channelID:ts token, or --channel/--ts flags.
func parseReactTarget(extraArgs []string, flags ReactFlags) (channelID, ts string, err error) {
	if len(extraArgs) > 0 {
		raw := extraArgs[0]
		var ref slack.MessageRef
		switch {
		case isSlackArchivesURL(raw):
			ref, err = slack.ParseSlackURL(raw)
			if err != nil {
				return "", "", fmt.Errorf("react: %w", err)
			}
		case slack.IsChannelTs(raw):
			ref, err = slack.ParseChannelTs(raw)
			if err != nil {
				return "", "", fmt.Errorf("react: %w", err)
			}
		default:
			return "", "", fmt.Errorf("react: second argument must be a Slack URL or channelID:ts; got %q", raw)
		}
		channelID = ref.ChannelID
		ts = ref.Ts

		// Allow flags to override but error on conflict.
		if flags.Channel != "" && flags.Channel != channelID {
			return "", "", fmt.Errorf("react: --channel %q conflicts with channel %q from positional arg", flags.Channel, channelID)
		}
		if flags.Ts != "" && flags.Ts != ts {
			return "", "", fmt.Errorf("react: --ts %q conflicts with timestamp %q from positional arg", flags.Ts, ts)
		}
		return channelID, ts, nil
	}

	// Flag form.
	channelID = flags.Channel
	ts = flags.Ts
	if channelID == "" || ts == "" {
		return "", "", fmt.Errorf("react: provide a Slack URL, channelID:ts, or both --channel and --ts")
	}
	return channelID, ts, nil
}
