// Package cmd — delete.go implements the "delete" command.
//
// Layer 1: Delete removes the authenticated user's own message from a
// whitelisted channel. Layer 2 wiring (presenter) is applied in main.go.
package cmd

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/cgrossde/slackcli/internal/keychain"
	"github.com/cgrossde/slackcli/internal/slack"
)

// DeleteFlags holds the parsed flag values for the delete command.
type DeleteFlags struct {
	Workspace string
	Channel   string
	Ts        string
	ThreadTs  string
}

// NewDeleteCmd builds the "delete" Cobra command.
func NewDeleteCmd() *cobra.Command {
	var flags DeleteFlags

	cmd := &cobra.Command{
		Use:   "delete [url | channelID:ts]",
		Short: "Delete one of your own messages from a whitelisted channel",
		Long: `Delete a message you sent in a whitelisted Slack channel.

The target message is specified by either:
  - A Slack message URL as the first positional argument
  - A channelID:ts token as the first positional argument
  - --channel <id> and --ts <timestamp> flags
  - --channel <id>, --ts <timestamp>, and --thread-ts <parent-ts> for thread replies

Only your own messages can be deleted. The command verifies ownership via
auth.test before calling chat.delete; messages sent by other users are
rejected locally with a clear error.

Only channels in the write allowlist (allowlist.txt, embedded at build time) are accepted.`,
		Example: `  slackcli delete https://myorg.slack.com/archives/C0B3PCPL0CF/p1718197925001234
  slackcli delete C0B3PCPL0CF:1718197925.001234
  slackcli delete --channel C0B3PCPL0CF --ts 1718197925.001234
  slackcli delete --channel C0B3PCPL0CF --ts 1779023515.154839 --thread-ts 1779023514.528229`,
		Args: cobra.RangeArgs(0, 1),
		RunE: func(c *cobra.Command, args []string) error {
			out, err := Delete(args, flags)
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
	f.StringVar(&flags.ThreadTs, "thread-ts", "", "Parent thread timestamp (required for thread replies)")

	return cmd
}

// Delete is the Layer 1 implementation for the delete command. It resolves the
// target message, verifies ownership, then deletes it via the Slack API.
func Delete(args []string, flags DeleteFlags) (string, error) {
	channelID, ts, threadTs, err := parseDeleteTarget(args, flags)
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

	// Verify the message exists and was posted by the authenticated user.
	authResult, err := client.AuthTest()
	if err != nil {
		return "", fmt.Errorf("delete: auth.test: %w", err)
	}
	if !authResult.OK {
		return "", fmt.Errorf("delete: auth.test failed: %s", authResult.Error)
	}

	var msg slack.Message
	if threadTs != "" {
		// Thread reply: conversations.history does not include replies; use
		// conversations.replies scoped to the parent thread.
		msg, err = client.GetReply(channelID, threadTs, ts)
	} else {
		msg, err = client.GetMessage(channelID, ts)
	}
	if err != nil {
		return "", fmt.Errorf("delete: %w", err)
	}

	if msg.User != authResult.UserID {
		return "", fmt.Errorf(
			"delete: message at ts=%s was not sent by you (sent by %s)",
			ts, msg.User,
		)
	}

	ch, respTs, err := client.DeleteMessage(channelID, ts)
	if err != nil {
		return "", err
	}

	return fmt.Sprintf("Deleted: %s ts=%s\n", ch, respTs), nil
}

// parseDeleteTarget resolves the channel ID and timestamp for the delete command
// from a positional Slack URL, a compact channelID:ts token, or --channel/--ts flags.
// threadTs is non-empty when the URL identifies a thread reply.
func parseDeleteTarget(extraArgs []string, flags DeleteFlags) (channelID, ts, threadTs string, err error) {
	if len(extraArgs) > 0 {
		raw := extraArgs[0]
		var ref slack.MessageRef
		switch {
		case isSlackArchivesURL(raw):
			ref, err = slack.ParseSlackURL(raw)
			if err != nil {
				return "", "", "", fmt.Errorf("delete: %w", err)
			}
		case slack.IsChannelTs(raw):
			ref, err = slack.ParseChannelTs(raw)
			if err != nil {
				return "", "", "", fmt.Errorf("delete: %w", err)
			}
		default:
			return "", "", "", fmt.Errorf("delete: argument must be a Slack URL or channelID:ts; got %q", raw)
		}
		channelID = ref.ChannelID
		ts = ref.Ts
		threadTs = ref.ThreadTs

		// Allow flags to override but error on conflict.
		if flags.Channel != "" && flags.Channel != channelID {
			return "", "", "", fmt.Errorf("delete: --channel %q conflicts with channel %q from positional arg", flags.Channel, channelID)
		}
		if flags.Ts != "" && flags.Ts != ts {
			return "", "", "", fmt.Errorf("delete: --ts %q conflicts with timestamp %q from positional arg", flags.Ts, ts)
		}
		return channelID, ts, threadTs, nil
	}

	// Flag form.
	channelID = flags.Channel
	ts = flags.Ts
	if channelID == "" || ts == "" {
		return "", "", "", fmt.Errorf("delete: provide a Slack URL, channelID:ts, or both --channel and --ts")
	}
	return channelID, ts, flags.ThreadTs, nil
}
