// Package slack — conversations.go implements conversations.history and
// conversations.replies via slack-go.
package slack

import (
	"context"
	"errors"
	"fmt"

	slackgo "github.com/slack-go/slack"
)

// ErrMessageNotFound is returned by GetMessage when the API call succeeds but
// the message does not exist in the channel (empty result set). This is
// distinct from API errors (channel_not_found, not_in_channel, etc.) and is
// used by callers that want to retry with different credentials.
var ErrMessageNotFound = errors.New("message not found")

// ErrChannelNotFound is returned by GetHistory when the channel is not
// accessible by the authenticated token (channel_not_found, not_in_channel).
// Callers that want to retry with different workspace credentials check for
// this error.
var ErrChannelNotFound = errors.New("channel not found")

// isChannelNotFoundSlackErr reports whether the Slack API error code indicates
// the channel is unknown to the authenticated workspace.
func isChannelNotFoundSlackErr(code string) bool {
	return code == "channel_not_found" || code == "not_in_channel"
}

// Message is a Slack message returned by conversations.history or
// conversations.replies.
type Message struct {
	Type         string
	User         string
	BotID        string
	Username     string
	Text         string
	Ts           string
	ThreadTs     string
	ReplyCount   int
	ReplyUsers   []string   // user IDs of everyone who has replied in this thread
	ParentUserID string
	Files        []File       // attached files (file_share, file_mention)
	Reactions    []Reaction   // emoji reactions on this message
	Attachments  []Attachment // link unfurls, forwarded messages, etc.
}

// File is a Slack file attachment carried on a message.
type File struct {
	ID         string
	Name       string
	Title      string
	PrettyType string // human-readable type, e.g. "PNG"
	Mimetype   string
	Permalink  string
	URLPrivate string
}

// Attachment is a Slack message attachment (link unfurl, forwarded message, etc.).
// Only the fields useful for LLM consumption are retained.
type Attachment struct {
	AuthorName  string // e.g. the sender of a forwarded DM
	AuthorLink  string
	Title       string
	TitleLink   string
	Pretext     string
	Text        string
	FromURL     string // original URL for link unfurls
	ServiceName string // e.g. "github"
	ImageURL    string
	ThumbURL    string
	Footer      string
}

// Reaction is a single emoji reaction and its voter count.
type Reaction struct {
	Name  string   // emoji name without colons, e.g. "thumbsup"
	Count int
	Users []string // user IDs who reacted
}

// fromSlackMsg converts a slack-go Message to our Message type.
func fromSlackMsg(m slackgo.Message) Message {
	files := make([]File, 0, len(m.Files))
	for _, f := range m.Files {
		files = append(files, File{
			ID:         f.ID,
			Name:       f.Name,
			Title:      f.Title,
			PrettyType: f.PrettyType,
			Mimetype:   f.Mimetype,
			Permalink:  f.Permalink,
			URLPrivate: f.URLPrivate,
		})
	}
	reactions := make([]Reaction, 0, len(m.Reactions))
	for _, r := range m.Reactions {
		users := make([]string, len(r.Users))
		copy(users, r.Users)
		reactions = append(reactions, Reaction{
			Name:  r.Name,
			Count: r.Count,
			Users: users,
		})
	}
	attachments := make([]Attachment, 0, len(m.Attachments))
	for _, a := range m.Attachments {
		attachments = append(attachments, Attachment{
			AuthorName:  a.AuthorName,
			AuthorLink:  a.AuthorLink,
			Title:       a.Title,
			TitleLink:   a.TitleLink,
			Pretext:     a.Pretext,
			Text:        a.Text,
			FromURL:     a.FromURL,
			ServiceName: a.ServiceName,
			ImageURL:    a.ImageURL,
			ThumbURL:    a.ThumbURL,
			Footer:      a.Footer,
		})
	}
	return Message{
		Type:         m.Type,
		User:         m.User,
		BotID:        m.BotID,
		Username:     m.Username,
		Text:         m.Text,
		Ts:           m.Timestamp,
		ThreadTs:     m.ThreadTimestamp,
		ReplyCount:   m.ReplyCount,
		ReplyUsers:   m.ReplyUsers,
		ParentUserID: m.ParentUserId,
		Files:        files,
		Reactions:    reactions,
		Attachments:  attachments,
	}
}

// GetMessage fetches a single message by channel ID and timestamp.
// Uses conversations.history with oldest=ts, latest=ts, inclusive=true, limit=1.
// Returns an error if the message does not exist.
func (c *Client) GetMessage(channelID, ts string) (Message, error) {
	resp, err := c.api.GetConversationHistory(&slackgo.GetConversationHistoryParameters{
		ChannelID: channelID,
		Oldest:    ts,
		Latest:    ts,
		Inclusive: true,
		Limit:     1,
	})
	if err != nil {
		// slack-go returns API errors (ok=false) via err, not resp.Ok.
		// Detect access-denial codes and surface guidance.
		code := err.Error()
		if code == "channel_not_found" || code == "not_in_channel" || code == "missing_scope" {
			return Message{}, fmt.Errorf("conversations.history: %s (channel=%s — wrong workspace? use --workspace)", code, channelID)
		}
		return Message{}, fmt.Errorf("conversations.history: %w", err)
	}
	if !resp.Ok {
		return Message{}, fmt.Errorf("conversations.history: %s", resp.Error)
	}
	if len(resp.Messages) == 0 {
		return Message{}, fmt.Errorf("%w: channel=%s ts=%s", ErrMessageNotFound, channelID, ts)
	}
	return fromSlackMsg(resp.Messages[0]), nil
}

// GetThread fetches the full thread rooted at threadTs in the given channel.
// The first message in the returned slice is the root; subsequent messages are
// replies in chronological order. If the message has no replies, a slice of
// one element is returned.
//
// Pagination is handled automatically; all pages are returned.
func (c *Client) GetThread(channelID, threadTs string) ([]Message, error) {
	var all []Message
	cursor := ""
	for {
		msgs, hasMore, nextCursor, err := c.api.GetConversationReplies(&slackgo.GetConversationRepliesParameters{
			ChannelID: channelID,
			Timestamp: threadTs,
			Cursor:    cursor,
			Limit:     200,
		})
		if err != nil {
			return nil, fmt.Errorf("conversations.replies: %w", err)
		}
		for _, m := range msgs {
			all = append(all, fromSlackMsg(m))
		}
		if !hasMore || nextCursor == "" {
			break
		}
		cursor = nextCursor
	}
	return all, nil
}

// GetReply fetches a single reply from a thread by (channelID, threadTs, replyTs).
// Uses conversations.replies with oldest/latest pinned to replyTs so only that
// one message is returned. The root message (index 0) is skipped; only replies
// are scanned for a matching ts.
// Returns an error if the reply is not found.
func (c *Client) GetReply(channelID, threadTs, replyTs string) (Message, error) {
	msgs, _, _, err := c.api.GetConversationReplies(&slackgo.GetConversationRepliesParameters{
		ChannelID: channelID,
		Timestamp: threadTs,
		Oldest:    replyTs,
		Latest:    replyTs,
		Inclusive: true,
		Limit:     5,
	})
	if err != nil {
		return Message{}, fmt.Errorf("conversations.replies: %w", err)
	}
	for _, m := range msgs {
		if m.Timestamp == replyTs {
			return fromSlackMsg(m), nil
		}
	}
	return Message{}, fmt.Errorf("reply not found: channel=%s thread=%s ts=%s", channelID, threadTs, replyTs)
}

// GetChannelName returns the name (without #) of the channel identified by
// channelID. It calls conversations.info; the result is not cached.
// For DM channels (IsIM=true), the peer's user ID is returned instead of
// the empty name field.
// Returns an error when the channel is not found or the API call fails.
func (c *Client) GetChannelName(ctx context.Context, channelID string) (string, error) {
	ch, err := c.api.GetConversationInfoContext(ctx, &slackgo.GetConversationInfoInput{
		ChannelID: channelID,
	})
	if err != nil {
		return "", fmt.Errorf("conversations.info %s: %w", channelID, err)
	}
	if ch.IsIM && ch.User != "" {
		return ch.User, nil
	}
	return ch.Name, nil
}

// HistoryParams holds parameters for GetHistory.
type HistoryParams struct {
	Limit  int    // 1–200; clamped internally to [1,200]
	Oldest string // epoch ts string, e.g. "1718197925.000000"; empty = no lower bound
	Latest string // epoch ts string; empty = no upper bound
	Cursor string // opaque pagination cursor from a previous HistoryResult
}

// HistoryResult holds the response from conversations.history.
type HistoryResult struct {
	Messages []Message
	HasMore  bool
	Cursor   string // next_cursor for the next page; empty when HasMore is false
}

// GetHistory fetches recent messages from channelID.
// Messages are returned newest-first (API order).
func (c *Client) GetHistory(channelID string, params HistoryParams) (HistoryResult, error) {
	limit := params.Limit
	if limit < 1 {
		limit = 1
	} else if limit > 200 {
		limit = 200
	}

	p := &slackgo.GetConversationHistoryParameters{
		ChannelID: channelID,
		Limit:     limit,
	}
	if params.Oldest != "" {
		p.Oldest = params.Oldest
	}
	if params.Latest != "" {
		p.Latest = params.Latest
	}
	if params.Cursor != "" {
		p.Cursor = params.Cursor
	}

	resp, err := c.api.GetConversationHistory(p)
	if err != nil {
		return HistoryResult{}, fmt.Errorf("conversations.history: %w", err)
	}
	if !resp.Ok {
		if isChannelNotFoundSlackErr(resp.Error) {
			return HistoryResult{}, fmt.Errorf("conversations.history: %w", ErrChannelNotFound)
		}
		return HistoryResult{}, fmt.Errorf("conversations.history: %s", resp.Error)
	}

	msgs := make([]Message, 0, len(resp.Messages))
	for _, m := range resp.Messages {
		msgs = append(msgs, fromSlackMsg(m))
	}

	cursor := ""
	if resp.HasMore {
		cursor = resp.ResponseMetaData.NextCursor
	}

	return HistoryResult{
		Messages: msgs,
		HasMore:  resp.HasMore,
		Cursor:   cursor,
	}, nil
}
