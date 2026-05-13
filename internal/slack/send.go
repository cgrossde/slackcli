// Package slack — send.go implements write operations: post a message and add
// a reaction. Both operations are gated by AllowedWriteChannels.
package slack

import (
	"fmt"
	"strings"

	slackgo "github.com/slack-go/slack"
)

// SendMessage posts text to channelID. If threadTs is non-empty the message
// is posted as a reply in that thread. Returns (channel, timestamp, error).
//
// Returns an error if channelID is not in AllowedWriteChannels.
func (c *Client) SendMessage(channelID, text, threadTs string, noPreview bool) (string, string, error) {
	if !IsWriteAllowed(channelID) {
		return "", "", fmt.Errorf("send: channel %q is not in the write allowlist", channelID)
	}

	opts := []slackgo.MsgOption{
		slackgo.MsgOptionText(text, false),
	}
	if threadTs != "" {
		opts = append(opts, slackgo.MsgOptionTS(threadTs))
	}
	if noPreview {
		opts = append(opts, slackgo.MsgOptionDisableLinkUnfurl())
	}

	ch, ts, err := c.api.PostMessage(channelID, opts...)
	if err != nil {
		return "", "", fmt.Errorf("send: chat.postMessage: %w", err)
	}
	return ch, ts, nil
}

// AddReaction adds the named emoji reaction to the message at (channelID, ts).
// emoji must be an emoji name without surrounding colons (e.g. "thumbsup").
//
// Returns an error if channelID is not in AllowedWriteChannels.
func (c *Client) AddReaction(channelID, ts, emoji string) error {
	if !IsWriteAllowed(channelID) {
		return fmt.Errorf("react: channel %q is not in the write allowlist", channelID)
	}

	ref := slackgo.NewRefToMessage(channelID, ts)
	if err := c.api.AddReaction(emoji, ref); err != nil {
		return fmt.Errorf("react: reactions.add: %w", err)
	}
	return nil
}

// RemoveReaction removes the named emoji reaction from the message at (channelID, ts).
// emoji must be an emoji name without surrounding colons (e.g. "thumbsup").
//
// Returns an error if channelID is not in AllowedWriteChannels.
func (c *Client) RemoveReaction(channelID, ts, emoji string) error {
	if !IsWriteAllowed(channelID) {
		return fmt.Errorf("react: channel %q is not in the write allowlist", channelID)
	}

	ref := slackgo.NewRefToMessage(channelID, ts)
	if err := c.api.RemoveReaction(emoji, ref); err != nil {
		return fmt.Errorf("react: reactions.remove: %w", err)
	}
	return nil
}

// DeleteMessage deletes the message at (channelID, ts) via chat.delete.
// Returns (channel, timestamp, error).
//
// Returns an error if channelID is not in AllowedWriteChannels.
func (c *Client) DeleteMessage(channelID, ts string) (string, string, error) {
	if !IsWriteAllowed(channelID) {
		return "", "", fmt.Errorf("delete: channel %q is not in the write allowlist", channelID)
	}
	ch, respTs, err := c.api.DeleteMessage(channelID, ts)
	if err != nil {
		return "", "", fmt.Errorf("delete: chat.delete: %w", err)
	}
	return ch, respTs, nil
}

// BuildPermalink constructs a Slack message permalink from its components
// without making an API call.
//
// workspace is the bare domain, e.g. "myorg.slack.com".
// ts is in API format, e.g. "1718197925.001234".
// The result matches the canonical permalink form:
//   https://myorg.slack.com/archives/CHANNEL/p1718197925001234
func BuildPermalink(workspace, channelID, ts string) string {
	// Convert API ts ("1718197925.001234") to the URL segment ("p1718197925001234")
	// by stripping the '.' and prepending 'p'.
	pTs := "p" + strings.ReplaceAll(ts, ".", "")
	return "https://" + workspace + "/archives/" + channelID + "/" + pTs
}

// ForwardMessage forwards a message by posting its permalink to dstChannelID.
// By default unfurl_links is enabled so the preview card renders; pass
// noPreview=true to suppress it. If noteText is non-empty, it is prepended
// before the permalink on its own line. Returns (channel, timestamp, error).
//
// srcWorkspace is the bare domain of the source workspace, e.g. "myorg.slack.com".
// Returns an error if dstChannelID is not in AllowedWriteChannels.
func (c *Client) ForwardMessage(srcWorkspace, srcChannelID, srcTs, dstChannelID, noteText string, noPreview bool) (string, string, error) {
	if !IsWriteAllowed(dstChannelID) {
		return "", "", fmt.Errorf("forward: channel %q is not in the write allowlist", dstChannelID)
	}

	permalink := BuildPermalink(srcWorkspace, srcChannelID, srcTs)

	// Compose the message text.
	text := permalink
	if noteText != "" {
		text = noteText + "\n" + permalink
	}

	opts := []slackgo.MsgOption{
		slackgo.MsgOptionText(text, false),
	}
	if noPreview {
		opts = append(opts, slackgo.MsgOptionDisableLinkUnfurl())
	} else {
		opts = append(opts, slackgo.MsgOptionEnableLinkUnfurl())
	}

	ch, ts, err := c.api.PostMessage(dstChannelID, opts...)
	if err != nil {
		return "", "", fmt.Errorf("forward: chat.postMessage: %w", err)
	}
	return ch, ts, nil
}
