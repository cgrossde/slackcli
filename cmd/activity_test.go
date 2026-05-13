package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cgrossde/slackcli/internal/slack"
)

// ──────────────────────────────────────────────────────────────────────────────
// expandTypeAliases
// ──────────────────────────────────────────────────────────────────────────────

func TestExpandTypeAliases_empty(t *testing.T) {
	got := expandTypeAliases("")
	if got != nil {
		t.Errorf("expected nil for empty input, got %v", got)
	}
}

func TestExpandTypeAliases_whitespaceOnly(t *testing.T) {
	got := expandTypeAliases("   ")
	if got != nil {
		t.Errorf("expected nil for whitespace-only input, got %v", got)
	}
}

func TestExpandTypeAliases_singleAlias(t *testing.T) {
	got := expandTypeAliases("reaction")
	want := []string{"message_reaction"}
	if len(got) != len(want) || got[0] != want[0] {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestExpandTypeAliases_multiExpansion(t *testing.T) {
	// channel_mention expands to two types.
	got := expandTypeAliases("channel_mention")
	want := []string{"at_channel", "at_everyone"}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestExpandTypeAliases_multipleAliases(t *testing.T) {
	got := expandTypeAliases("reaction,mention")
	if len(got) != 2 {
		t.Fatalf("expected 2 types, got %d: %v", len(got), got)
	}
	if got[0] != "message_reaction" {
		t.Errorf("first type: got %q, want %q", got[0], "message_reaction")
	}
	if got[1] != "at_user" {
		t.Errorf("second type: got %q, want %q", got[1], "at_user")
	}
}

func TestExpandTypeAliases_rawAPINamePassthrough(t *testing.T) {
	// Raw API names that aren't in the alias map pass through verbatim.
	got := expandTypeAliases("list_record_edited")
	if len(got) != 1 || got[0] != "list_record_edited" {
		t.Errorf("got %v, want [list_record_edited]", got)
	}
}

func TestExpandTypeAliases_mixedAliasAndRaw(t *testing.T) {
	got := expandTypeAliases("thread,list_record_edited")
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d: %v", len(got), got)
	}
	if got[0] != "thread_v2" {
		t.Errorf("got[0] = %q, want thread_v2", got[0])
	}
	if got[1] != "list_record_edited" {
		t.Errorf("got[1] = %q, want list_record_edited", got[1])
	}
}

func TestExpandTypeAliases_deduplication(t *testing.T) {
	// Same alias twice should produce deduplicated output.
	got := expandTypeAliases("mention,mention")
	if len(got) != 1 || got[0] != "at_user" {
		t.Errorf("expected single at_user, got %v", got)
	}
}

func TestExpandTypeAliases_inviteExpands(t *testing.T) {
	got := expandTypeAliases("invite")
	if len(got) != 2 {
		t.Fatalf("expected 2, got %d: %v", len(got), got)
	}
	if got[0] != "internal_channel_invite" || got[1] != "external_channel_invite" {
		t.Errorf("got %v", got)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// activityDescription
// ──────────────────────────────────────────────────────────────────────────────

func TestActivityDescription_reaction(t *testing.T) {
	item := slack.ActivityItem{
		Type:        "message_reaction",
		ActorUserID: "U123",
		Reaction:    "thumbsup",
	}
	// No cache — actor falls back to raw ID.
	got := activityDescription(item, nil)
	if !strings.Contains(got, "U123") {
		t.Errorf("expected actor ID in description, got %q", got)
	}
	if !strings.Contains(got, ":thumbsup:") {
		t.Errorf("expected emoji name in description, got %q", got)
	}
}

func TestActivityDescription_reactionWithCache(t *testing.T) {
	cache := slack.NewUserCacheFromMap("test.slack.com", map[string]slack.CachedUser{
		"U456": {ID: "U456", Name: "alice", DisplayName: "Alice"},
	})
	item := slack.ActivityItem{
		Type:        "message_reaction",
		ActorUserID: "U456",
		Reaction:    "wave",
	}
	got := activityDescription(item, cache)
	if !strings.Contains(got, "Alice") {
		t.Errorf("expected display name in description, got %q", got)
	}
	if !strings.Contains(got, ":wave:") {
		t.Errorf("expected emoji in description, got %q", got)
	}
}

func TestActivityDescription_thread(t *testing.T) {
	item := slack.ActivityItem{Type: "thread_v2", ActorUserID: "U789"}
	got := activityDescription(item, nil)
	if !strings.Contains(got, "replied in thread") {
		t.Errorf("expected 'replied in thread', got %q", got)
	}
}

func TestActivityDescription_mention(t *testing.T) {
	item := slack.ActivityItem{Type: "at_user", ActorUserID: "U000"}
	got := activityDescription(item, nil)
	if !strings.Contains(got, "mentioned you") {
		t.Errorf("expected 'mentioned you', got %q", got)
	}
}

func TestActivityDescription_dm(t *testing.T) {
	item := slack.ActivityItem{Type: "dm", ActorUserID: "U111"}
	got := activityDescription(item, nil)
	if !strings.Contains(got, "DM") {
		t.Errorf("expected DM in description, got %q", got)
	}
}

func TestActivityDescription_unknownType(t *testing.T) {
	item := slack.ActivityItem{Type: "list_record_edited", ActorUserID: "U999"}
	got := activityDescription(item, nil)
	if !strings.Contains(got, "list_record_edited") {
		t.Errorf("expected type name in description for unknown type, got %q", got)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// formatActivityPlain
// ──────────────────────────────────────────────────────────────────────────────

func makeSampleResult() slack.ActivityFeedResult {
	return slack.ActivityFeedResult{
		Items: []slack.ActivityItem{
			{
				Type:        "message_reaction",
				FeedTs:      "1718197925.001234",
				IsUnread:    true,
				ChannelID:   "C012ABC3",
				MessageTs:   "1718197800.000100",
				ActorUserID: "U456",
				Reaction:    "thumbsup",
			},
			{
				Type:      "at_user",
				FeedTs:    "1718197800.000200",
				IsUnread:  true,
				ChannelID: "C012ABC3",
				MessageTs: "1718197700.000300",
			},
		},
		NextCursor: "",
		HasMore:    false,
	}
}

func TestFormatActivityPlain_numberedItems(t *testing.T) {
	result := makeSampleResult()
	chanNames := map[string]string{"C012ABC3": "#ops"}
	texts := map[string]string{
		"C012ABC3:1718197800.000100": "great work everyone",
		"C012ABC3:1718197700.000300": "hey @you check this",
	}
	got := formatActivityPlain(result, texts, chanNames, nil, ActivityFlags{Count: 20})

	assertContains(t, got, "[1]")
	assertContains(t, got, "[2]")
	assertContains(t, got, "#ops")
	assertContains(t, got, ":thumbsup:")
	assertContains(t, got, "great work everyone")
	assertContains(t, got, "hey @you check this")
	assertContains(t, got, "→ slackcli read C012ABC3:1718197800.000100")
	assertContains(t, got, "→ slackcli read C012ABC3:1718197700.000300")
}

func TestFormatActivityPlain_noCursor_noNextHint(t *testing.T) {
	result := makeSampleResult() // HasMore=false
	got := formatActivityPlain(result, nil, nil, nil, ActivityFlags{Count: 20})
	if strings.Contains(got, "--cursor") {
		t.Errorf("expected no --cursor hint when HasMore is false, got: %s", got)
	}
	assertContains(t, got, "--- 2 items ---")
}

func TestFormatActivityPlain_withCursor(t *testing.T) {
	result := makeSampleResult()
	result.HasMore = true
	result.NextCursor = "abc123"
	got := formatActivityPlain(result, nil, nil, nil, ActivityFlags{Count: 20})
	assertContains(t, got, "--cursor abc123")
	assertContains(t, got, "slackcli activity")
}

func TestFormatActivityPlain_cursorIncludesFlags(t *testing.T) {
	result := makeSampleResult()
	result.HasMore = true
	result.NextCursor = "xyz"
	flags := ActivityFlags{
		Count:     5,
		Unread:    true,
		Type:      "reaction",
		Workspace: "myorg.slack.com",
	}
	got := formatActivityPlain(result, nil, nil, nil, flags)
	assertContains(t, got, "--workspace myorg.slack.com")
	assertContains(t, got, "--unread")
	assertContains(t, got, "--count 5")
	assertContains(t, got, "--type reaction")
	assertContains(t, got, "--cursor xyz")
}

func TestFormatActivityPlain_empty(t *testing.T) {
	result := slack.ActivityFeedResult{}
	got := formatActivityPlain(result, nil, nil, nil, ActivityFlags{})
	assertContains(t, got, "No activity items")
}

func TestFormatActivityPlain_textTruncation(t *testing.T) {
	// Build a string longer than maxTextRunes (200 runes).
	longText := strings.Repeat("a", 250)
	result := slack.ActivityFeedResult{
		Items: []slack.ActivityItem{{
			Type:      "at_user",
			FeedTs:    "1718197800.000000",
			ChannelID: "C1",
			MessageTs: "1718197700.000000",
		}},
	}
	texts := map[string]string{"C1:1718197700.000000": longText}
	got := formatActivityPlain(result, texts, nil, nil, ActivityFlags{})
	// The truncated text should end with the truncation marker "…"
	if !strings.Contains(got, "…") {
		t.Errorf("expected truncation marker '…' in output, got: %s", got)
	}
	// Should not contain the full 250-char string.
	if strings.Contains(got, longText) {
		t.Errorf("expected text to be truncated, but full string present")
	}
}

func TestFormatActivityPlain_thread_v2_readRefUsesThreadTs(t *testing.T) {
	// For thread_v2, the read hint must be the three-part form
	// channelID:threadTs:replyTs so the reader gets both the thread root
	// (for fetching) and the specific reply ts (for context).
	result := slack.ActivityFeedResult{
		Items: []slack.ActivityItem{{
			Type:      "thread_v2",
			FeedTs:    "1718197800.000000",
			ChannelID: "C9",
			ThreadTs:  "1718197000.000000",
			MessageTs: "1718197500.000000",
		}},
	}
	got := formatActivityPlain(result, nil, nil, nil, ActivityFlags{})
	assertContains(t, got, "→ slackcli read C9:1718197000.000000:1718197500.000000")
	// Must not emit the old two-part form pointing only at the thread root.
	if strings.Contains(got, "→ slackcli read C9:1718197000.000000\n") {
		t.Errorf("must not emit two-part root-only hint, got: %s", got)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// formatActivityJSON
// ──────────────────────────────────────────────────────────────────────────────

func TestFormatActivityJSON_fields(t *testing.T) {
	cache := slack.NewUserCacheFromMap("test.slack.com", map[string]slack.CachedUser{
		"U456": {ID: "U456", Name: "alice", DisplayName: "Alice"},
	})
	result := makeSampleResult()
	chanNames := map[string]string{"C012ABC3": "#ops"}
	texts := map[string]string{
		"C012ABC3:1718197800.000100": "looks good",
		"C012ABC3:1718197700.000300": "ping",
	}

	out := formatActivityJSON(result, texts, chanNames, cache)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 NDJSON lines, got %d:\n%s", len(lines), out)
	}

	var rec activityItemJSON
	if err := json.Unmarshal([]byte(lines[0]), &rec); err != nil {
		t.Fatalf("parse line 0: %v", err)
	}
	if rec.Type != "message_reaction" {
		t.Errorf("type: got %q, want message_reaction", rec.Type)
	}
	if rec.ChannelID != "C012ABC3" {
		t.Errorf("channel_id: got %q, want C012ABC3", rec.ChannelID)
	}
	if rec.ChannelName != "#ops" {
		t.Errorf("channel_name: got %q, want #ops", rec.ChannelName)
	}
	if rec.Reaction != "thumbsup" {
		t.Errorf("reaction: got %q, want thumbsup", rec.Reaction)
	}
	if rec.ReactorID != "U456" {
		t.Errorf("reactor_id: got %q, want U456", rec.ReactorID)
	}
	if rec.ReactorName != "Alice" {
		t.Errorf("reactor_name: got %q, want Alice", rec.ReactorName)
	}
	if rec.Text != "looks good" {
		t.Errorf("text: got %q, want 'looks good'", rec.Text)
	}
	if !rec.IsUnread {
		t.Error("is_unread: expected true")
	}
}

func TestFormatActivityJSON_paginationTrailer(t *testing.T) {
	result := makeSampleResult()
	result.HasMore = true
	result.NextCursor = "cursor99"

	out := formatActivityJSON(result, nil, nil, nil)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	// 2 items + 1 pagination trailer
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %d", len(lines))
	}

	lastLine := lines[len(lines)-1]
	var trailer activityPaginationJSON
	if err := json.Unmarshal([]byte(lastLine), &trailer); err != nil {
		t.Fatalf("parse trailer: %v", err)
	}
	if !trailer.Pagination.HasMore {
		t.Error("has_more: expected true")
	}
	if trailer.Pagination.NextCursor != "cursor99" {
		t.Errorf("next_cursor: got %q, want cursor99", trailer.Pagination.NextCursor)
	}
}

func TestFormatActivityJSON_noPaginationWhenNoMore(t *testing.T) {
	result := makeSampleResult() // HasMore=false
	out := formatActivityJSON(result, nil, nil, nil)
	lines := strings.Split(strings.TrimRight(out, "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (no trailer), got %d", len(lines))
	}
	if strings.Contains(out, "_pagination") {
		t.Error("expected no _pagination trailer when HasMore is false")
	}
}

func TestFormatActivityJSON_reactionActorVsAuthor(t *testing.T) {
	// For message_reaction: UserID/Username/DisplayName should reflect the
	// message author (AuthorUserID), while ReactorID/ReactorName show the actor.
	cache := slack.NewUserCacheFromMap("test.slack.com", map[string]slack.CachedUser{
		"U_AUTHOR":  {ID: "U_AUTHOR", Name: "bob", DisplayName: "Bob"},
		"U_REACTOR": {ID: "U_REACTOR", Name: "carol", DisplayName: "Carol"},
	})
	result := slack.ActivityFeedResult{
		Items: []slack.ActivityItem{{
			Type:         "message_reaction",
			FeedTs:       "1.0",
			ChannelID:    "C1",
			MessageTs:    "0.1",
			AuthorUserID: "U_AUTHOR",
			ActorUserID:  "U_REACTOR",
			Reaction:     "heart",
		}},
	}
	out := formatActivityJSON(result, nil, nil, cache)
	var rec activityItemJSON
	if err := json.Unmarshal([]byte(strings.TrimRight(out, "\n")), &rec); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if rec.UserID != "U_AUTHOR" {
		t.Errorf("user_id should be author, got %q", rec.UserID)
	}
	if rec.DisplayName != "Bob" {
		t.Errorf("display_name should be Bob (author), got %q", rec.DisplayName)
	}
	if rec.ReactorID != "U_REACTOR" {
		t.Errorf("reactor_id: got %q, want U_REACTOR", rec.ReactorID)
	}
	if rec.ReactorName != "Carol" {
		t.Errorf("reactor_name: got %q, want Carol", rec.ReactorName)
	}
}

func TestFormatActivityJSON_thread_v2_fields(t *testing.T) {
	// For thread_v2: ts is the latest-reply ts (item.MessageTs, as received from
	// the API); thread_ts is the root. JSON consumers navigate to the thread via
	// thread_ts. The plain-text read hint uses thread_ts separately.
	result := slack.ActivityFeedResult{
		Items: []slack.ActivityItem{{
			Type:      "thread_v2",
			FeedTs:    "1718197800.000000",
			ChannelID: "C9",
			ThreadTs:  "1718197000.000000",
			MessageTs: "1718197500.000000", // latest reply
		}},
	}
	out := formatActivityJSON(result, nil, nil, nil)
	var rec activityItemJSON
	if err := json.Unmarshal([]byte(strings.TrimRight(out, "\n")), &rec); err != nil {
		t.Fatalf("parse: %v", err)
	}
	if rec.Ts != "1718197500.000000" {
		t.Errorf("ts: got %q, want latest-reply ts 1718197500.000000", rec.Ts)
	}
	if rec.ThreadTs != "1718197000.000000" {
		t.Errorf("thread_ts: got %q, want thread root 1718197000.000000", rec.ThreadTs)
	}
}

// ──────────────────────────────────────────────────────────────────────────────
// activityChannelLabel
// ──────────────────────────────────────────────────────────────────────────────

func TestActivityChannelLabel_resolved(t *testing.T) {
	item := slack.ActivityItem{ChannelID: "C1"}
	got := activityChannelLabel(item, map[string]string{"C1": "#general"})
	if got != "#general" {
		t.Errorf("got %q, want #general", got)
	}
}

func TestActivityChannelLabel_fallbackToID(t *testing.T) {
	item := slack.ActivityItem{ChannelID: "C999"}
	got := activityChannelLabel(item, map[string]string{})
	if got != "C999" {
		t.Errorf("got %q, want C999", got)
	}
}

func TestActivityChannelLabel_unknown(t *testing.T) {
	item := slack.ActivityItem{}
	got := activityChannelLabel(item, nil)
	if got != "(unknown)" {
		t.Errorf("got %q, want (unknown)", got)
	}
}
