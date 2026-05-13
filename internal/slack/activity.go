// Package slack — activity.go implements the activity.feed API endpoint.
//
// activity.feed is an internal Slack API that returns the Activity panel feed
// (mentions, reactions, thread replies, DMs, keyword alerts, etc.).
// It uses form-encoded POST with an xoxc token, exactly like client.userBoot.
package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// ActivityFeedParams controls the activity.feed request.
type ActivityFeedParams struct {
	Limit  int      // items per request; clamped to [1,50] by the caller
	Cursor string   // opaque pagination cursor from a prior response
	Unread bool     // true → mode=priority_unreads_v1; false → chrono_reads_and_unreads
	Types  []string // empty → all types sent (Slack default)
}

// ActivityItem is one entry from the activity.feed response, fully parsed.
type ActivityItem struct {
	FeedTs   string // when the activity happened (Slack ts format)
	IsUnread bool

	// Discriminator — raw API name: message_reaction, thread_v2, at_user, dm, …
	Type string

	// Location — always populated when we can extract coordinates.
	ChannelID string
	ThreadTs  string
	MessageTs string

	// Actor / content.
	AuthorUserID string // who wrote the original message (populated for reactions)
	ActorUserID  string // who performed the action (reactor, replier, mentioner)
	Reaction     string // emoji name without colons; only for message_reaction
}

// ActivityFeedResult holds the parsed response from activity.feed.
type ActivityFeedResult struct {
	Items      []ActivityItem
	NextCursor string
	HasMore    bool
}

// ──────────────────────────────────────────────────────────────────────────────
// Wire shapes — unexported; used only for JSON unmarshalling.
// ──────────────────────────────────────────────────────────────────────────────

type activityFeedResponse struct {
	OK               bool               `json:"ok"`
	Error            string             `json:"error"`
	Items            []activityRawItem  `json:"items"`
	ResponseMetadata activityRespMeta   `json:"response_metadata"`
}

type activityRespMeta struct {
	NextCursor string `json:"next_cursor"`
}

type activityRawItem struct {
	FeedTs   string          `json:"feed_ts"`
	IsUnread bool            `json:"is_unread"`
	Item     json.RawMessage `json:"item"`
}

// activityItemCore is the top-level discriminator inside each item.
type activityItemCore struct {
	Type string `json:"type"`

	// message_reaction / at_user / at_channel / at_everyone / at_user_group /
	// keyword / dm / generic_system_alert — all carry a "message" sub-object.
	Message *activityMessage `json:"message"`

	// Reaction actor — message_reaction only.
	Reaction *activityReaction `json:"reaction"`

	// thread_v2 — carries bundle_info.
	BundleInfo *activityBundleInfo `json:"bundle_info"`
}

type activityMessage struct {
	Ts           string `json:"ts"`
	Channel      string `json:"channel"`
	ThreadTs     string `json:"thread_ts"`
	AuthorUserID string `json:"author_user_id"`
	User         string `json:"user"` // actor who sent/triggered (mentions, DMs, keywords, etc.)
}

type activityReaction struct {
	User string `json:"user"`
	Name string `json:"name"`
}

type activityBundleInfo struct {
	Payload *activityBundlePayload `json:"payload"`
}

type activityBundlePayload struct {
	ThreadEntry *activityThreadEntry `json:"thread_entry"`
}

type activityThreadEntry struct {
	ChannelID             string `json:"channel_id"`
	ThreadTs              string `json:"thread_ts"`
	LatestTs              string `json:"latest_ts"`
	LatestReplyActorUserID string `json:"latest_reply_actor_user_id"`
}

// ──────────────────────────────────────────────────────────────────────────────
// GetActivityFeed fetches one page of the activity feed.
// ──────────────────────────────────────────────────────────────────────────────

// GetActivityFeed calls POST https://slack.com/api/activity.feed and returns
// the parsed result. It follows the same form-encoded request pattern as
// GatewayServer (client.userBoot).
func (c *Client) GetActivityFeed(ctx context.Context, params ActivityFeedParams) (ActivityFeedResult, error) {
	form := url.Values{}
	form.Set("token", c.token)
	form.Set("limit", strconv.Itoa(params.Limit))
	if params.Unread {
		form.Set("mode", "priority_unreads_v1")
	} else {
		form.Set("mode", "chrono_reads_and_unreads")
	}
	if len(params.Types) > 0 {
		form.Set("types", strings.Join(params.Types, ","))
	} else {
		// Send all known types, matching Slack's default UI behaviour.
		form.Set("types", strings.Join(allActivityTypes, ","))
	}
	if params.Cursor != "" {
		form.Set("cursor", params.Cursor)
	}
	// Internal metadata expected by Slack's edge API.
	form.Set("_x_reason", "fetchActivityFeed")
	form.Set("_x_mode", "online")
	form.Set("_x_sonic", "true")
	form.Set("_x_app_name", "client")

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"https://slack.com/api/activity.feed",
		strings.NewReader(form.Encode()))
	if err != nil {
		return ActivityFeedResult{}, fmt.Errorf("activity.feed: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return ActivityFeedResult{}, fmt.Errorf("activity.feed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ActivityFeedResult{}, fmt.Errorf("activity.feed: read body: %w", err)
	}

	var raw activityFeedResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return ActivityFeedResult{}, fmt.Errorf("activity.feed: parse response: %w", err)
	}
	if !raw.OK {
		return ActivityFeedResult{}, fmt.Errorf("activity.feed: %s", raw.Error)
	}

	items := make([]ActivityItem, 0, len(raw.Items))
	for _, ri := range raw.Items {
		item, ok := parseActivityItem(ri)
		if !ok {
			continue // unrecognised shape — skip silently
		}
		items = append(items, item)
	}

	return ActivityFeedResult{
		Items:      items,
		NextCursor: raw.ResponseMetadata.NextCursor,
		HasMore:    raw.ResponseMetadata.NextCursor != "",
	}, nil
}

// parseActivityItem converts a raw feed entry into an ActivityItem.
// Returns (item, false) when the item shape is unrecognised or lacks location.
func parseActivityItem(ri activityRawItem) (ActivityItem, bool) {
	var core activityItemCore
	if err := json.Unmarshal(ri.Item, &core); err != nil {
		return ActivityItem{}, false
	}

	out := ActivityItem{
		FeedTs:   ri.FeedTs,
		IsUnread: ri.IsUnread,
		Type:     core.Type,
	}

	switch core.Type {
	case "thread_v2":
		// Location is nested in bundle_info.payload.thread_entry.
		if core.BundleInfo == nil || core.BundleInfo.Payload == nil || core.BundleInfo.Payload.ThreadEntry == nil {
			return ActivityItem{}, false
		}
		te := core.BundleInfo.Payload.ThreadEntry
		out.ChannelID = te.ChannelID
		out.ThreadTs = te.ThreadTs
		out.MessageTs = te.LatestTs
		if out.MessageTs == "" {
			out.MessageTs = te.ThreadTs
		}
		out.ActorUserID = te.LatestReplyActorUserID

	default:
		// All other types carry a top-level "message" field.
		if core.Message == nil {
			return ActivityItem{}, false
		}
		out.ChannelID = core.Message.Channel
		out.ThreadTs = core.Message.ThreadTs
		out.MessageTs = core.Message.Ts
		out.AuthorUserID = core.Message.AuthorUserID

		if core.Type == "message_reaction" && core.Reaction != nil {
			// Reaction: actor is the reactor; author is who wrote the message.
			out.ActorUserID = core.Reaction.User
			out.Reaction = core.Reaction.Name
		} else {
			// For all other message-based types (dm, at_user, keyword, etc.),
			// the actor is the sender of the message, carried in message.user.
			out.ActorUserID = core.Message.User
		}

	}
	if out.ChannelID == "" || out.MessageTs == "" {
		return ActivityItem{}, false
	}
	return out, true
}

// allActivityTypes is the complete set of types Slack's web client requests.
var allActivityTypes = []string{
	"thread_v2",
	"dm",
	"generic_system_alert",
	"message_reaction",
	"internal_channel_invite",
	"list_record_edited",
	"bot_dm_bundle",
	"at_user",
	"at_user_group",
	"at_channel",
	"at_everyone",
	"keyword",
	"list_record_assigned",
	"list_user_mentioned",
	"external_channel_invite",
	"shared_workspace_invite",
	"external_dm_invite",
}
