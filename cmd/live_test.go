package cmd

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/cgrossde/slackcli/internal/slack"
)

// ---------------------------------------------------------------------------
// FormatEvent tests
// ---------------------------------------------------------------------------

func TestFormatEvent_Message(t *testing.T) {
	e := slack.Event{
		Type:    "message",
		Channel: "C012ABC",
		User:    "U111",
		Text:    "We rolled back the deployment.",
		Ts:      "1718197925.001234",
	}
	got := FormatEvent(e, nil, nil)
	assertContains(t, got, "[message]")
	assertContains(t, got, "C012ABC")
	assertContains(t, got, "U111")
	assertContains(t, got, "We rolled back the deployment.")
	assertContains(t, got, "→ slackcli read C012ABC:1718197925.001234")
	assertContains(t, got, "2024-06-12") // 1718197925 is 2024-06-12
}

func TestFormatEvent_ThreadReply(t *testing.T) {
	e := slack.Event{
		Type:     "message",
		Channel:  "C012ABC",
		User:     "U222",
		Text:     "What was the root cause?",
		Ts:       "1718197926.000001",
		ThreadTs: "1718197925.001234",
	}
	got := FormatEvent(e, nil, nil)
	assertContains(t, got, "[message:thread_reply]")
	assertContains(t, got, "→ slackcli read C012ABC:1718197926.000001")
}

func TestFormatEvent_MessageChanged(t *testing.T) {
	e := slack.Event{
		Type:    "message",
		SubType: "message_changed",
		Channel: "C012ABC",
		User:    "U111",
		Ts:      "1718197925.001234",
	}
	got := FormatEvent(e, nil, nil)
	assertContains(t, got, "[message:message_changed]")
}

func TestFormatEvent_ReactionAdded(t *testing.T) {
	e := slack.Event{
		Type:     "reaction_added",
		Channel:  "C012ABC",
		User:     "U333",
		Reaction: "thumbsup",
		ItemUser: "U111",
		ItemTs:   "1718197925.001234",
		Ts:       "1718197933.000001",
	}
	got := FormatEvent(e, nil, nil)
	assertContains(t, got, "[reaction_added]")
	assertContains(t, got, ":thumbsup:")
	assertContains(t, got, "on message by U111")
	assertContains(t, got, "→ slackcli read C012ABC:1718197925.001234")
}

func TestFormatEvent_ReactionRemoved(t *testing.T) {
	e := slack.Event{
		Type:     "reaction_removed",
		Channel:  "C012ABC",
		User:     "U333",
		Reaction: "thumbsup",
		ItemUser: "U111",
		ItemTs:   "1718197925.001234",
		Ts:       "1718197935.000001",
	}
	got := FormatEvent(e, nil, nil)
	assertContains(t, got, "[reaction_removed]")
}

func TestFormatEvent_MemberJoined(t *testing.T) {
	e := slack.Event{
		Type:    "member_joined_channel",
		Channel: "C012ABC",
		User:    "U444",
		Ts:      "1718197940.000001",
	}
	got := FormatEvent(e, nil, nil)
	assertContains(t, got, "[member_joined_channel]")
	assertContains(t, got, "joined")
}

func TestFormatEvent_MemberLeft(t *testing.T) {
	e := slack.Event{
		Type:    "member_left_channel",
		Channel: "C012ABC",
		User:    "U444",
		Ts:      "1718197945.000001",
	}
	got := FormatEvent(e, nil, nil)
	assertContains(t, got, "[member_left_channel]")
	assertContains(t, got, "left")
}

func TestFormatEvent_TeamJoin(t *testing.T) {
	e := slack.Event{
		Type: "team_join",
		User: "U555",
		Ts:   "1718197950.000001",
	}
	got := FormatEvent(e, nil, nil)
	assertContains(t, got, "[team_join]")
	assertContains(t, got, "New workspace member")
}

func TestFormatEvent_ChannelStr(t *testing.T) {
	chanNames := map[string]string{"C012ABC": "general"}
	e := slack.Event{
		Type:    "message",
		Channel: "C012ABC",
		User:    "U111",
		Text:    "hi",
		Ts:      "1718197925.001234",
	}
	got := FormatEvent(e, nil, chanNames)
	assertContains(t, got, "#general")
}

func TestFormatEvent_TextTruncation(t *testing.T) {
	// Build a string of exactly 210 runes.
	long := strings.Repeat("a", 210)
	e := slack.Event{
		Type:    "message",
		Channel: "C012ABC",
		User:    "U111",
		Text:    long,
		Ts:      "1718197925.001234",
	}
	got := FormatEvent(e, nil, nil)
	// The formatted text line should contain "…" and be 200 runes + "…".
	assertContains(t, got, "…")
}

func TestFormatEvent_TextNotTruncated(t *testing.T) {
	short := strings.Repeat("a", 50)
	e := slack.Event{
		Type:    "message",
		Channel: "C012ABC",
		User:    "U111",
		Text:    short,
		Ts:      "1718197925.001234",
	}
	got := FormatEvent(e, nil, nil)
	if strings.Contains(got, "…") {
		t.Errorf("expected no truncation for short text, got: %s", got)
	}
}

// ---------------------------------------------------------------------------
// LiveFilter.Accept tests
// ---------------------------------------------------------------------------

func TestLiveFilter_NoFilter(t *testing.T) {
	f := LiveFilter{}
	e := slack.Event{Type: "message", Channel: "C012ABC", User: "U111"}
	if !f.Accept(e, nil, nil) {
		t.Error("zero filter should accept everything")
	}
}

func TestLiveFilter_TypeMatch(t *testing.T) {
	f := LiveFilter{Types: []string{"message"}}
	e := slack.Event{Type: "message", Channel: "C012ABC", User: "U111"}
	if !f.Accept(e, nil, nil) {
		t.Error("expected type match to pass")
	}
}

func TestLiveFilter_TypeNoMatch(t *testing.T) {
	f := LiveFilter{Types: []string{"reaction_added"}}
	e := slack.Event{Type: "message", Channel: "C012ABC", User: "U111"}
	if f.Accept(e, nil, nil) {
		t.Error("expected type mismatch to fail")
	}
}

func TestLiveFilter_MultipleTypes(t *testing.T) {
	f := LiveFilter{Types: []string{"message", "reaction_added"}}

	e1 := slack.Event{Type: "message"}
	if !f.Accept(e1, nil, nil) {
		t.Error("expected message to pass")
	}
	e2 := slack.Event{Type: "reaction_added"}
	if !f.Accept(e2, nil, nil) {
		t.Error("expected reaction_added to pass")
	}
	e3 := slack.Event{Type: "team_join"}
	if f.Accept(e3, nil, nil) {
		t.Error("expected team_join to fail")
	}
}

func TestLiveFilter_ChannelByID(t *testing.T) {
	f := LiveFilter{Channels: []string{"C012ABC"}}
	e := slack.Event{Type: "message", Channel: "C012ABC"}
	if !f.Accept(e, nil, nil) {
		t.Error("expected channel ID match to pass")
	}
	e2 := slack.Event{Type: "message", Channel: "C999"}
	if f.Accept(e2, nil, nil) {
		t.Error("expected different channel to fail")
	}
}

func TestLiveFilter_ChannelByName(t *testing.T) {
	chanNames := map[string]string{"C012ABC": "general"}
	f := LiveFilter{Channels: []string{"general"}}
	e := slack.Event{Type: "message", Channel: "C012ABC"}
	if !f.Accept(e, nil, chanNames) {
		t.Error("expected channel name match to pass")
	}
}

func TestLiveFilter_ChannelByNameWithHash(t *testing.T) {
	chanNames := map[string]string{"C012ABC": "general"}
	f := LiveFilter{Channels: []string{"#general"}}
	e := slack.Event{Type: "message", Channel: "C012ABC"}
	if !f.Accept(e, nil, chanNames) {
		t.Error("expected #channel name match to pass")
	}
}

func TestLiveFilter_FromUserByID(t *testing.T) {
	f := LiveFilter{FromUser: "U111"}
	e := slack.Event{Type: "message", User: "U111"}
	if !f.Accept(e, nil, nil) {
		t.Error("expected user ID match to pass")
	}
	e2 := slack.Event{Type: "message", User: "U222"}
	if f.Accept(e2, nil, nil) {
		t.Error("expected different user to fail")
	}
}

func TestLiveFilter_FromUserCacheMatch(t *testing.T) {
	cache := slack.NewUserCacheFromMap("test.slack.com", map[string]slack.CachedUser{
		"U111": {ID: "U111", Name: "alice", DisplayName: "alice"},
	})
	f := LiveFilter{FromUser: "alice"}
	e := slack.Event{Type: "message", User: "U111"}
	if !f.Accept(e, cache, nil) {
		t.Error("expected cache name match to pass")
	}
}

// ---------------------------------------------------------------------------
// LiveEventTypes test
// ---------------------------------------------------------------------------

func TestLiveEventTypes(t *testing.T) {
	out := LiveEventTypes()
	assertContains(t, out, "message")
	assertContains(t, out, "reaction_added")
	assertContains(t, out, "reaction_removed")
	assertContains(t, out, "member_joined_channel")
	assertContains(t, out, "member_left_channel")
	assertContains(t, out, "channel_created")
	assertContains(t, out, "channel_deleted")
	assertContains(t, out, "channel_rename")
	assertContains(t, out, "team_join")
	assertContains(t, out, "desktop_notification")
}

// ---------------------------------------------------------------------------
// truncateRunes
// ---------------------------------------------------------------------------

func TestTruncateRunes(t *testing.T) {
	tests := []struct {
		in   string
		n    int
		want string
	}{
		{"hello", 10, "hello"},
		{"hello", 3, "hel…"},
		{"hello", 5, "hello"},
		{"", 5, ""},
		{"αβγδε", 3, "αβγ…"},   // multibyte runes
		{"αβγδε", 10, "αβγδε"}, // no truncation needed
	}
	for _, tt := range tests {
		got := truncateRunes(tt.in, tt.n)
		if got != tt.want {
			t.Errorf("truncateRunes(%q, %d) = %q, want %q", tt.in, tt.n, got, tt.want)
		}
	}
}

// ---------------------------------------------------------------------------
// formatEventTs
// ---------------------------------------------------------------------------

func TestFormatEventTs(t *testing.T) {
	// 1718197925 = 2024-06-12 13:32:05 UTC
	got := formatEventTs("1718197925.001234")
	if !strings.HasPrefix(got, "2024-06-12") {
		t.Errorf("unexpected ts format: %q", got)
	}
}

func TestFormatEventTs_Empty(t *testing.T) {
	if formatEventTs("") != "" {
		t.Error("empty ts should return empty string")
	}
}

func TestFormatEventTs_NoDot(t *testing.T) {
	// Unusual but we must not crash.
	got := formatEventTs("1718197925")
	if got == "" {
		t.Error("non-empty ts without dot should return something")
	}
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

func assertContains(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("expected output to contain %q\ngot: %s", substr, s)
	}
}

// ---------------------------------------------------------------------------
// FormatEventJSON
// ---------------------------------------------------------------------------

func TestFormatEventJSON_message(t *testing.T) {
	e := slack.Event{
		Type:    "message",
		Channel: "C012ABC",
		User:    "U111",
		Text:    "We rolled back the deployment.",
		Ts:      "1718197925.001234",
	}
	got := FormatEventJSON(e, nil, nil)

	assertContains(t, got, `"type":"message"`)
	assertContains(t, got, `"channel_id":"C012ABC"`)
	assertContains(t, got, `"user_id":"U111"`)
	assertContains(t, got, `"ts":"1718197925.001234"`)
	assertContains(t, got, `"text":"We rolled back the deployment."`)
	assertContains(t, got, `"subtype":""`)
	assertContains(t, got, `"thread_ts":""`)

	// Must be a single JSON object (no newline from this function).
	if strings.Contains(got, "\n") {
		t.Error("FormatEventJSON should not contain a newline")
	}
}

func TestFormatEventJSON_threadReply(t *testing.T) {
	e := slack.Event{
		Type:     "message",
		Channel:  "C012ABC",
		User:     "U222",
		Text:     "What was the root cause?",
		Ts:       "1718197926.000001",
		ThreadTs: "1718197925.001234",
	}
	got := FormatEventJSON(e, nil, nil)
	assertContains(t, got, `"thread_ts":"1718197925.001234"`)
	assertContains(t, got, `"ts":"1718197926.000001"`)
}

func TestFormatEventJSON_reactionAdded(t *testing.T) {
	e := slack.Event{
		Type:     "reaction_added",
		Channel:  "C012ABC",
		User:     "U333",
		Ts:       "1718197933.000001",
		Reaction: "thumbsup",
		ItemTs:   "1718197925.001234",
	}
	got := FormatEventJSON(e, nil, nil)
	assertContains(t, got, `"type":"reaction_added"`)
	assertContains(t, got, `"user_id":"U333"`)
	assertContains(t, got, `"reaction":"thumbsup"`)
	assertContains(t, got, `"item_ts":"1718197925.001234"`)
}

func TestFormatEventJSON_withCache(t *testing.T) {
	cache := slack.NewUserCacheFromMap("ws", map[string]slack.CachedUser{
		"U111": {ID: "U111", Name: "alice", DisplayName: "Alice Example"},
	})
	e := slack.Event{
		Type:    "message",
		Channel: "C012ABC",
		User:    "U111",
		Text:    "hello",
		Ts:      "1.0",
	}
	got := FormatEventJSON(e, cache, nil)
	assertContains(t, got, `"username":"alice"`)
	assertContains(t, got, `"display_name":"Alice Example (alice)"`)
}

func TestFormatEventJSON_withChanNames(t *testing.T) {
	chanNames := map[string]string{"C012ABC": "general"}
	e := slack.Event{
		Type:    "message",
		Channel: "C012ABC",
		User:    "U999",
		Ts:      "1.0",
	}
	got := FormatEventJSON(e, nil, chanNames)
	assertContains(t, got, `"channel_name":"general"`)
}

func TestFormatEventJSON_validJSON(t *testing.T) {
	e := slack.Event{
		Type:    "message",
		Channel: "C012ABC",
		User:    "U111",
		Text:    "hello",
		Ts:      "1718197925.001234",
	}
	got := FormatEventJSON(e, nil, nil)
	var m map[string]interface{}
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Errorf("FormatEventJSON produced invalid JSON: %v\ngot: %s", err, got)
	}
}

// ---------------------------------------------------------------------------
// LiveFilter — SelfUserID / --mention
// ---------------------------------------------------------------------------

func TestLiveFilter_MentionAccepted(t *testing.T) {
	f := LiveFilter{SelfUserID: "U999"}
	e := slack.Event{
		Type:     "message",
		Channel:  "C012ABC",
		User:     "U111",
		Text:     "hey <@U999>",
		Ts:       "1.0",
		Mentions: []string{"U999"},
	}
	if !f.Accept(e, nil, nil) {
		t.Error("expected event mentioning SelfUserID to be accepted")
	}
}

func TestLiveFilter_MentionRejectedWhenNotMentioned(t *testing.T) {
	f := LiveFilter{SelfUserID: "U999"}
	e := slack.Event{
		Type:     "message",
		Channel:  "C012ABC",
		User:     "U111",
		Text:     "nothing relevant here",
		Ts:       "1.0",
		Mentions: []string{"U111"},
	}
	if f.Accept(e, nil, nil) {
		t.Error("expected event not mentioning SelfUserID to be rejected")
	}
}

func TestLiveFilter_MentionRejectedWhenNoMentions(t *testing.T) {
	f := LiveFilter{SelfUserID: "U999"}
	e := slack.Event{
		Type:    "message",
		Channel: "C012ABC",
		User:    "U111",
		Text:    "no mentions at all",
		Ts:      "1.0",
	}
	if f.Accept(e, nil, nil) {
		t.Error("expected event with no mentions to be rejected when --mention is active")
	}
}

func TestLiveFilter_MentionDoesNotAffectWithoutSelfUserID(t *testing.T) {
	// SelfUserID empty → mention filter inactive, all events pass
	f := LiveFilter{}
	e := slack.Event{
		Type:     "message",
		Channel:  "C012ABC",
		User:     "U111",
		Text:     "no mentions",
		Ts:       "1.0",
	}
	if !f.Accept(e, nil, nil) {
		t.Error("expected event to be accepted when SelfUserID is empty")
	}
}

func TestLiveFilter_ThreadParticipant_Accepted(t *testing.T) {
	f := LiveFilter{
		SelfUserID: "U999",
		IsThreadParticipant: func(channelID, threadTs string) bool {
			return channelID == "C012ABC" && threadTs == "1.0"
		},
	}
	// Reply in a thread where we are a participant — no direct mention.
	e := slack.Event{
		Type:     "message",
		Channel:  "C012ABC",
		User:     "U111",
		Text:     "another reply",
		Ts:       "2.0",
		ThreadTs: "1.0", // Ts != ThreadTs → this is a reply
	}
	if !f.Accept(e, nil, nil) {
		t.Error("expected thread-participant event to be accepted")
	}
}

func TestLiveFilter_ThreadParticipant_Rejected(t *testing.T) {
	f := LiveFilter{
		SelfUserID: "U999",
		IsThreadParticipant: func(_, _ string) bool {
			return false // not a participant
		},
	}
	e := slack.Event{
		Type:     "message",
		Channel:  "C012ABC",
		User:     "U111",
		Text:     "some reply",
		Ts:       "2.0",
		ThreadTs: "1.0",
	}
	if f.Accept(e, nil, nil) {
		t.Error("expected non-participant thread reply to be rejected")
	}
}

func TestLiveFilter_ThreadParticipant_SkippedForTopLevel(t *testing.T) {
	called := false
	f := LiveFilter{
		SelfUserID: "U999",
		IsThreadParticipant: func(_, _ string) bool {
			called = true
			return true
		},
	}
	// Top-level message (no ThreadTs) — participant check must not be called.
	e := slack.Event{
		Type:    "message",
		Channel: "C012ABC",
		User:    "U111",
		Text:    "top-level, no mention",
		Ts:      "1.0",
	}
	f.Accept(e, nil, nil)
	if called {
		t.Error("IsThreadParticipant must not be called for top-level messages")
	}
}

func TestLiveFilter_ThreadParticipant_NilFuncIgnored(t *testing.T) {
	// SelfUserID set but IsThreadParticipant nil — must not panic.
	f := LiveFilter{SelfUserID: "U999"}
	e := slack.Event{
		Type:     "message",
		Channel:  "C012ABC",
		User:     "U111",
		Text:     "no mention",
		Ts:       "2.0",
		ThreadTs: "1.0",
	}
	// Should not panic; should reject because not mentioned and no checker.
	if f.Accept(e, nil, nil) {
		t.Error("expected rejection when IsThreadParticipant is nil and no mention")
	}
}
