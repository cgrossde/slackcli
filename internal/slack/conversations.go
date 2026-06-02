// Package slack — conversations.go implements conversations.history and
// conversations.replies via slack-go.
package slack

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

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

// Conversation represents a non-channel conversation (DM or MPDM) returned by
// conversations.list.
type Conversation struct {
	ID       string   // Slack channel/conversation ID
	Name     string   // raw name (e.g. "mpdm-alice--bob-1") or "" for DMs
	IsIM     bool     // true for 1:1 direct messages
	IsMpIM   bool     // true for multi-party DMs
	User     string   // for DMs: the peer user ID
	Members  []string // for MPDMs: participant user IDs (may be empty if not fetched)
	LastRead string   // epoch ts of last-read message
	LatestTs string   // epoch ts of the most recent message (from latest.ts)
	Priority float64  // Slack's internal sort priority (higher = more important)
}

// ConversationListParams holds parameters for ListConversations.
type ConversationListParams struct {
	// Types filters by conversation type. Valid values: "im", "mpim".
	// Defaults to both when empty.
	Types  []string
	Limit  int    // 1–200; 0 means use API default (100)
	Cursor string // pagination cursor from a previous call
	// Workspace is the workspace domain (e.g. "myorg.slack.com").
	// Required for Enterprise Grid workspaces; uses slack.com when empty.
	Workspace string
}

// ConversationListResult holds the response from conversations.list.
type ConversationListResult struct {
	Conversations []Conversation
	HasMore       bool
	Cursor        string // next_cursor for the next page; empty when HasMore is false
}

// ListConversations calls users.conversations filtered to non-channel conversation
// types (im and/or mpim). It posts directly via the shared http.Client (same
// pattern as activity.feed) to bypass the enterprise_is_restricted error that
// slack-go's GetConversations raises on Enterprise Grid workspaces.
//
// Results are returned in API order (most recently active first when the
// Slack token belongs to the authenticated user).
func (c *Client) ListConversations(params ConversationListParams) (ConversationListResult, error) {
	types := params.Types
	if len(types) == 0 {
		types = []string{"im", "mpim"}
	}
	limit := params.Limit
	if limit < 1 {
		limit = 100
	} else if limit > 200 {
		limit = 200
	}

	form := url.Values{}
	form.Set("token", c.token)
	form.Set("types", strings.Join(types, ","))
	form.Set("limit", strconv.Itoa(limit))
	form.Set("exclude_archived", "false")
	if params.Cursor != "" {
		form.Set("cursor", params.Cursor)
	}
	// Internal metadata sent by the Slack web client.
	form.Set("_x_reason", "users.conversations")
	form.Set("_x_mode", "online")
	form.Set("_x_sonic", "true")
	form.Set("_x_app_name", "client")

	// Build the base URL using the workspace domain when provided, so that
	// Enterprise Grid workspaces route to the correct shard.
	apiBase := "https://slack.com"
	if params.Workspace != "" {
		apiBase = "https://" + strings.TrimPrefix(params.Workspace, "https://")
		apiBase = strings.TrimSuffix(apiBase, "/")
	}

	// Try users.conversations first; fall back to client.counts on Enterprise
	// Grid workspaces where the former is restricted.
	req, err := http.NewRequest(http.MethodPost,
		apiBase+"/api/users.conversations",
		strings.NewReader(form.Encode()))
	if err != nil {
		return ConversationListResult{}, fmt.Errorf("users.conversations: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return ConversationListResult{}, fmt.Errorf("users.conversations: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ConversationListResult{}, fmt.Errorf("users.conversations: read body: %w", err)
	}

	var raw struct {
		OK               bool   `json:"ok"`
		Error            string `json:"error"`
		Channels         []struct {
			ID       string  `json:"id"`
			Name     string  `json:"name"`
			IsIM     bool    `json:"is_im"`
			IsMpIM   bool    `json:"is_mpim"`
			User     string  `json:"user"`
			Members  []string `json:"members"`
			LastRead string  `json:"last_read"`
			Priority float64 `json:"priority"`
			Latest   *struct {
				Ts string `json:"ts"`
			} `json:"latest"`
		} `json:"channels"`
		ResponseMetadata struct {
			NextCursor string `json:"next_cursor"`
		} `json:"response_metadata"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return ConversationListResult{}, fmt.Errorf("users.conversations: parse response: %w", err)
	}
	if !raw.OK {
		if raw.Error == "enterprise_is_restricted" {
			// Enterprise Grid admins have disabled users.conversations.
			// Fall back to client.counts which the Slack web client uses for
			// its sidebar DM list and is not subject to the same restriction.
			return c.listConversationsViaClientCounts(params)
		}
		return ConversationListResult{}, fmt.Errorf("users.conversations: %s", raw.Error)
	}

	convs := make([]Conversation, 0, len(raw.Channels))
	for _, ch := range raw.Channels {
		conv := Conversation{
			ID:       ch.ID,
			Name:     ch.Name,
			IsIM:     ch.IsIM,
			IsMpIM:   ch.IsMpIM,
			User:     ch.User,
			Members:  ch.Members,
			LastRead: ch.LastRead,
			Priority: ch.Priority,
		}
		if ch.Latest != nil {
			conv.LatestTs = ch.Latest.Ts
		}
		convs = append(convs, conv)
	}

	nextCursor := raw.ResponseMetadata.NextCursor
	return ConversationListResult{
		Conversations: convs,
		HasMore:       nextCursor != "",
		Cursor:        nextCursor,
	}, nil
}

// listConversationsViaClientCounts is the fallback for Enterprise Grid
// workspaces where users.conversations is restricted. It calls client.counts
// which the Slack web client uses to populate the sidebar DM list.
//
// client.counts returns IM and MPIM entries with last_read and latest ts.
// It does not support type-filtering or pagination — we filter locally.
func (c *Client) listConversationsViaClientCounts(params ConversationListParams) (ConversationListResult, error) {
	apiBase := "https://slack.com"
	if params.Workspace != "" {
		apiBase = "https://" + strings.TrimPrefix(params.Workspace, "https://")
		apiBase = strings.TrimSuffix(apiBase, "/")
	}

	form := url.Values{}
	form.Set("token", c.token)
	form.Set("_x_reason", "client.counts")
	form.Set("_x_mode", "online")
	form.Set("_x_sonic", "true")
	form.Set("_x_app_name", "client")

	req, err := http.NewRequest(http.MethodPost,
		apiBase+"/api/client.counts",
		strings.NewReader(form.Encode()))
	if err != nil {
		return ConversationListResult{}, fmt.Errorf("client.counts: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return ConversationListResult{}, fmt.Errorf("client.counts: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ConversationListResult{}, fmt.Errorf("client.counts: read body: %w", err)
	}

	// client.counts response shape — only fields we need.
	var raw struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
		IMs   []struct {
			ID       string  `json:"id"`
			UserID   string  `json:"user_id"`
			User     string  `json:"user"`     // alternative field name used in some responses
			LastRead string  `json:"last_read"`
			Latest   string  `json:"latest"`
			Priority float64 `json:"priority"`
		} `json:"ims"`
		MPIMs []struct {
			ID       string  `json:"id"`
			Name     string  `json:"name"`
			LastRead string  `json:"last_read"`
			Latest   string  `json:"latest"`
			Priority float64 `json:"priority"`
		} `json:"mpims"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return ConversationListResult{}, fmt.Errorf("client.counts: parse response: %w", err)
	}
	if !raw.OK {
		return ConversationListResult{}, fmt.Errorf("client.counts: %s", raw.Error)
	}

	// Build the want-set from the requested types.
	wantIM, wantMPIM := false, false
	for _, t := range params.Types {
		switch t {
		case "im":
			wantIM = true
		case "mpim":
			wantMPIM = true
		}
	}
	if !wantIM && !wantMPIM {
		wantIM, wantMPIM = true, true
	}

	limit := params.Limit
	if limit < 1 {
		limit = 100
	}

	convs := make([]Conversation, 0, len(raw.IMs)+len(raw.MPIMs))
	if wantIM {
		for _, im := range raw.IMs {
			peerID := im.UserID
			if peerID == "" {
				peerID = im.User
			}
			convs = append(convs, Conversation{
				ID:       im.ID,
				IsIM:     true,
				User:     peerID,
				LastRead: im.LastRead,
				LatestTs: im.Latest,
				Priority: im.Priority,
			})
		}
	}
	if wantMPIM {
		for _, mp := range raw.MPIMs {
			convs = append(convs, Conversation{
				ID:       mp.ID,
				Name:     mp.Name,
				IsMpIM:   true,
				LastRead: mp.LastRead,
				LatestTs: mp.Latest,
				Priority: mp.Priority,
			})
		}
	}

	// Return all entries unsorted and unresolved. The caller (cmd layer)
	// sorts by LatestTs, trims to the requested count, then calls
	// ResolveConversationNames to fill in missing User/Name fields with
	// minimal conversations.info round-trips.
	return ConversationListResult{
		Conversations: convs,
		HasMore:       false, // client.counts returns all in one shot
		Cursor:        "",
	}, nil
}

// ResolveConversationNames fills in missing User (for DMs) and Name (for
// MPDMs) fields by calling conversations.info for each entry that lacks them.
// Call this after sorting and trimming to minimise API round-trips.
func (c *Client) ResolveConversationNames(convs []Conversation) {
	for i := range convs {
		if convs[i].IsIM && convs[i].User == "" {
			if peerID, err := c.GetChannelName(context.Background(), convs[i].ID); err == nil {
				convs[i].User = peerID
			}
		}
		if convs[i].IsMpIM && convs[i].Name == "" {
			if name, err := c.GetChannelName(context.Background(), convs[i].ID); err == nil {
				convs[i].Name = name
			}
		}
	}
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
