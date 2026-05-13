package slack

import (
	"encoding/json"
	"testing"
)

// buildRawItem wraps a JSON-encoded item object into an activityRawItem.
func buildRawItem(feedTs string, isUnread bool, item any) activityRawItem {
	b, _ := json.Marshal(item)
	return activityRawItem{FeedTs: feedTs, IsUnread: isUnread, Item: json.RawMessage(b)}
}

// ──────────────────────────────────────────────────────────────────────────────
// parseActivityItem — actor extraction
// ──────────────────────────────────────────────────────────────────────────────

func TestParseActivityItem_dmActorFromMessageUser(t *testing.T) {
	// For dm/at_user/keyword types, the actor is message.user, not author_user_id.
	ri := buildRawItem("1.0", true, map[string]any{
		"type": "dm",
		"message": map[string]any{
			"ts":             "1718197800.000100",
			"channel":        "D012ABC",
			"author_user_id": "U_ME",   // the recipient (you)
			"user":           "U_THEM", // the sender (actor)
		},
	})
	item, ok := parseActivityItem(ri)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if item.ActorUserID != "U_THEM" {
		t.Errorf("ActorUserID: got %q, want U_THEM", item.ActorUserID)
	}
	if item.AuthorUserID != "U_ME" {
		t.Errorf("AuthorUserID: got %q, want U_ME", item.AuthorUserID)
	}
}

func TestParseActivityItem_atUserActorFromMessageUser(t *testing.T) {
	ri := buildRawItem("1.0", true, map[string]any{
		"type": "at_user",
		"message": map[string]any{
			"ts":             "1718197800.000200",
			"channel":        "C012ABC",
			"author_user_id": "U_ME",
			"user":           "U_MENTIONER",
		},
	})
	item, ok := parseActivityItem(ri)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if item.ActorUserID != "U_MENTIONER" {
		t.Errorf("ActorUserID: got %q, want U_MENTIONER", item.ActorUserID)
	}
}

func TestParseActivityItem_reactionActorFromReactionUser(t *testing.T) {
	// For message_reaction, actor is reaction.user (not message.user).
	ri := buildRawItem("1.0", true, map[string]any{
		"type": "message_reaction",
		"message": map[string]any{
			"ts":             "1718197800.000300",
			"channel":        "C012ABC",
			"author_user_id": "U_AUTHOR",
			"user":           "U_SHOULD_NOT_USE",
		},
		"reaction": map[string]any{
			"user": "U_REACTOR",
			"name": "thumbsup",
		},
	})
	item, ok := parseActivityItem(ri)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if item.ActorUserID != "U_REACTOR" {
		t.Errorf("ActorUserID: got %q, want U_REACTOR", item.ActorUserID)
	}
	if item.AuthorUserID != "U_AUTHOR" {
		t.Errorf("AuthorUserID: got %q, want U_AUTHOR", item.AuthorUserID)
	}
	if item.Reaction != "thumbsup" {
		t.Errorf("Reaction: got %q, want thumbsup", item.Reaction)
	}
}

func TestParseActivityItem_threadV2ActorFromLatestReply(t *testing.T) {
	ri := buildRawItem("1.0", true, map[string]any{
		"type": "thread_v2",
		"bundle_info": map[string]any{
			"payload": map[string]any{
				"thread_entry": map[string]any{
					"channel_id":                   "C012ABC",
					"thread_ts":                    "1718197700.000100",
					"latest_ts":                    "1718197800.000100",
					"latest_reply_actor_user_id":   "U_REPLIER",
				},
			},
		},
	})
	item, ok := parseActivityItem(ri)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if item.ActorUserID != "U_REPLIER" {
		t.Errorf("ActorUserID: got %q, want U_REPLIER", item.ActorUserID)
	}
	if item.ChannelID != "C012ABC" {
		t.Errorf("ChannelID: got %q, want C012ABC", item.ChannelID)
	}
	if item.MessageTs != "1718197800.000100" {
		t.Errorf("MessageTs: got %q, want 1718197800.000100", item.MessageTs)
	}
}

func TestParseActivityItem_missingMessage_skipped(t *testing.T) {
	ri := buildRawItem("1.0", true, map[string]any{
		"type": "at_user",
		// no "message" field — should be skipped
	})
	_, ok := parseActivityItem(ri)
	if ok {
		t.Error("expected ok=false for missing message field")
	}
}

func TestParseActivityItem_missingChannelID_skipped(t *testing.T) {
	ri := buildRawItem("1.0", true, map[string]any{
		"type": "thread_v2",
		"bundle_info": map[string]any{
			"payload": map[string]any{
				"thread_entry": map[string]any{
					// channel_id missing
					"thread_ts": "1718197700.000100",
					"latest_ts": "1718197800.000100",
				},
			},
		},
	})
	_, ok := parseActivityItem(ri)
	if ok {
		t.Error("expected ok=false when channel_id is missing")
	}
}
