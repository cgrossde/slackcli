// Package cmd — history_test.go tests the history command logic without
// network or keychain access.
package cmd

import (
	"fmt"
	"strings"
	"testing"

	"github.com/cgrossde/slackcli/internal/slack"
)

// ---------------------------------------------------------------------------
// parseHistoryChannel
// ---------------------------------------------------------------------------

func TestParseHistoryChannel_channelURL(t *testing.T) {
	args := []string{"https://myorg.slack.com/archives/C0B3Z1KT80K"}
	id, ws, err := parseHistoryChannel(args, HistoryFlags{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "C0B3Z1KT80K" {
		t.Errorf("channelID = %q, want C0B3Z1KT80K", id)
	}
	if ws != "myorg.slack.com" {
		t.Errorf("workspace = %q, want myorg.slack.com", ws)
	}
}

func TestParseHistoryChannel_bareID(t *testing.T) {
	args := []string{"C0B3Z1KT80K"}
	id, ws, err := parseHistoryChannel(args, HistoryFlags{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "C0B3Z1KT80K" {
		t.Errorf("channelID = %q, want C0B3Z1KT80K", id)
	}
	if ws != "" {
		t.Errorf("workspace = %q, want empty for bare ID", ws)
	}
}

func TestParseHistoryChannel_name(t *testing.T) {
	// Names are passed through as-is; resolution happens in resolveHistoryChannelID.
	args := []string{"general"}
	id, ws, err := parseHistoryChannel(args, HistoryFlags{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "general" {
		t.Errorf("channelID = %q, want general", id)
	}
	if ws != "" {
		t.Errorf("workspace = %q, want empty for name", ws)
	}
}

func TestParseHistoryChannel_flag(t *testing.T) {
	flags := HistoryFlags{Channel: "C0B3Z1KT80K"}
	id, _, err := parseHistoryChannel(nil, flags)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "C0B3Z1KT80K" {
		t.Errorf("channelID = %q, want C0B3Z1KT80K", id)
	}
}

func TestParseHistoryChannel_conflict(t *testing.T) {
	args := []string{"C0B3Z1KT80K"}
	flags := HistoryFlags{Channel: "C999999999"}
	_, _, err := parseHistoryChannel(args, flags)
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
	if !strings.Contains(err.Error(), "twice") {
		t.Errorf("expected 'twice' in error, got: %v", err)
	}
}

func TestParseHistoryChannel_missing(t *testing.T) {
	_, _, err := parseHistoryChannel(nil, HistoryFlags{})
	if err == nil {
		t.Fatal("expected error when no channel provided, got nil")
	}
	if !strings.Contains(err.Error(), "channel required") {
		t.Errorf("expected 'channel required' in error, got: %v", err)
	}
}

func TestParseHistoryChannel_sameArgAndFlag(t *testing.T) {
	// Providing the same value in both positional arg and --channel is not an error.
	args := []string{"C0B3Z1KT80K"}
	flags := HistoryFlags{Channel: "C0B3Z1KT80K"}
	id, _, err := parseHistoryChannel(args, flags)
	if err != nil {
		t.Fatalf("unexpected error when positional == flag: %v", err)
	}
	if id != "C0B3Z1KT80K" {
		t.Errorf("channelID = %q, want C0B3Z1KT80K", id)
	}
}

// ---------------------------------------------------------------------------
// buildHistoryParams
// ---------------------------------------------------------------------------

func TestBuildHistoryParams_defaults(t *testing.T) {
	flags := HistoryFlags{Count: 25}
	p, err := buildHistoryParams(flags)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Limit != 25 {
		t.Errorf("Limit = %d, want 25", p.Limit)
	}
	if p.Oldest != "" || p.Latest != "" || p.Cursor != "" {
		t.Errorf("expected empty Oldest/Latest/Cursor, got %+v", p)
	}
}

func TestBuildHistoryParams_clampToMax(t *testing.T) {
	flags := HistoryFlags{Count: 999}
	p, err := buildHistoryParams(flags)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Limit != 200 {
		t.Errorf("Limit = %d, want 200 (clamped)", p.Limit)
	}
}

func TestBuildHistoryParams_clampToMin(t *testing.T) {
	flags := HistoryFlags{Count: 0}
	p, err := buildHistoryParams(flags)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Limit != 1 {
		t.Errorf("Limit = %d, want 1 (clamped)", p.Limit)
	}
}

func TestBuildHistoryParams_cursor(t *testing.T) {
	flags := HistoryFlags{Count: 10, Cursor: "dXNlcjpVMDYx"}
	p, err := buildHistoryParams(flags)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Cursor != "dXNlcjpVMDYx" {
		t.Errorf("Cursor = %q, want dXNlcjpVMDYx", p.Cursor)
	}
}

func TestBuildHistoryParams_afterDate(t *testing.T) {
	flags := HistoryFlags{Count: 10, After: "2026-01-01"}
	p, err := buildHistoryParams(flags)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Oldest == "" {
		t.Error("Oldest: expected non-empty epoch for --after date")
	}
	// 2026-01-01 00:00:00 UTC = 1767225600
	if p.Oldest != "1767225600" {
		t.Errorf("Oldest = %q, want 1767225600", p.Oldest)
	}
}

func TestBuildHistoryParams_invalidAfter(t *testing.T) {
	flags := HistoryFlags{Count: 10, After: "not-a-date"}
	_, err := buildHistoryParams(flags)
	if err == nil {
		t.Fatal("expected error for invalid --after, got nil")
	}
	if !strings.Contains(err.Error(), "--after") {
		t.Errorf("expected '--after' in error, got: %v", err)
	}
}

func TestBuildHistoryParams_invalidBefore(t *testing.T) {
	flags := HistoryFlags{Count: 10, Before: "baddate"}
	_, err := buildHistoryParams(flags)
	if err == nil {
		t.Fatal("expected error for invalid --before, got nil")
	}
	if !strings.Contains(err.Error(), "--before") {
		t.Errorf("expected '--before' in error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// dateToEpoch
// ---------------------------------------------------------------------------

func TestDateToEpoch_empty(t *testing.T) {
	s, err := dateToEpoch("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s != "" {
		t.Errorf("expected empty string, got %q", s)
	}
}

func TestDateToEpoch_absoluteDate(t *testing.T) {
	s, err := dateToEpoch("2026-01-01")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if s != "1767225600" {
		t.Errorf("epoch = %q, want 1767225600", s)
	}
}

func TestDateToEpoch_relativeDate(t *testing.T) {
	// resolveDate resolves "0d" to today; just check it converts without error.
	s, err := dateToEpoch("0d")
	if err != nil {
		t.Fatalf("unexpected error for 0d: %v", err)
	}
	if s == "" {
		t.Error("expected non-empty epoch for relative date")
	}
}

// ---------------------------------------------------------------------------
// formatHistoryPlain
// ---------------------------------------------------------------------------

func TestFormatHistoryPlain_empty(t *testing.T) {
	result := slack.HistoryResult{Messages: nil, HasMore: false}
	out := formatHistoryPlain(result, "C0B3Z1KT80K", nil, "", "")
	if !strings.Contains(out, "0 messages") {
		t.Errorf("expected '0 messages' in output, got: %q", out)
	}
}

func TestFormatHistoryPlain_singleMessage(t *testing.T) {
	result := slack.HistoryResult{
		Messages: []slack.Message{{Text: "hello world", Ts: "1000000000.000001"}},
		HasMore:  false,
	}
	out := formatHistoryPlain(result, "C0B3Z1KT80K", nil, "", "")
	if !strings.Contains(out, "hello world") {
		t.Errorf("expected message body in output, got: %q", out)
	}
	if !strings.Contains(out, "1 message") {
		t.Errorf("expected '1 message' footer in output, got: %q", out)
	}
}

func TestFormatHistoryPlain_pluralMessages(t *testing.T) {
	msgs := make([]slack.Message, 3)
	for i := range msgs {
		msgs[i] = slack.Message{Text: fmt.Sprintf("msg %d", i), Ts: "1000000000.000001"}
	}
	result := slack.HistoryResult{Messages: msgs, HasMore: false}
	out := formatHistoryPlain(result, "C0B3Z1KT80K", nil, "", "")
	if !strings.Contains(out, "3 messages") {
		t.Errorf("expected '3 messages' footer, got: %q", out)
	}
}

func TestFormatHistoryPlain_hasMoreFooter(t *testing.T) {
	msgs := make([]slack.Message, 5)
	for i := range msgs {
		msgs[i] = slack.Message{Text: fmt.Sprintf("msg %d", i), Ts: "1000000000.000001"}
	}
	result := slack.HistoryResult{Messages: msgs, HasMore: true, Cursor: "nextcursor123"}
	out := formatHistoryPlain(result, "C0B3Z1KT80K", nil, "", "")
	if !strings.Contains(out, "has_more: true") {
		t.Errorf("expected has_more:true in output, got: %q", out)
	}
	if !strings.Contains(out, "--cursor nextcursor123") {
		t.Errorf("expected cursor hint in output, got: %q", out)
	}
}

func TestFormatHistoryMessage_replyCount(t *testing.T) {
	m := slack.Message{Text: "thread root", Ts: "1000000000.000001", ReplyCount: 3}
	out := formatHistoryMessage(m, "C0B3Z1KT80K", nil, "", "")
	if !strings.Contains(out, "[3 replies]") {
		t.Errorf("expected '[3 replies]' in output, got: %q", out)
	}
}

func TestFormatHistoryMessage_singleReply(t *testing.T) {
	m := slack.Message{Text: "thread root", Ts: "1000000000.000001", ReplyCount: 1}
	out := formatHistoryMessage(m, "C0B3Z1KT80K", nil, "", "")
	if !strings.Contains(out, "[1 reply]") {
		t.Errorf("expected singular '[1 reply]' label, got: %q", out)
	}
}

func TestFormatHistoryMessage_noReplyCount(t *testing.T) {
	m := slack.Message{Text: "simple message", Ts: "1000000000.000001", ReplyCount: 0}
	out := formatHistoryMessage(m, "C0B3Z1KT80K", nil, "", "")
	if strings.Contains(out, "repl") {
		t.Errorf("expected no reply indicator for 0 replies, got: %q", out)
	}
}

func TestFormatHistoryMessage_headerWidth(t *testing.T) {
	m := slack.Message{Text: "body", Ts: "1718197925.000000", User: "U123"}
	out := formatHistoryMessage(m, "C0B3Z1KT80K", nil, "", "")
	// The first line is the header; it must be exactly 120 chars + newline.
	lines := strings.SplitN(out, "\n", 2)
	if len(lines[0]) != 120 {
		t.Errorf("header line width = %d, want 120; header: %q", len(lines[0]), lines[0])
	}
}

func TestFormatHistoryMessage_readRef(t *testing.T) {
	m := slack.Message{Text: "hello", Ts: "1718197925.001234"}
	out := formatHistoryMessage(m, "C0B3Z1KT80K", nil, "", "")
	want := "  → slackcli read C0B3Z1KT80K:1718197925.001234"
	if !strings.Contains(out, want) {
		t.Errorf("expected read ref %q in output, got: %q", want, out)
	}
}

func TestFormatHistoryMessage_dmPeer_senderIsPeer(t *testing.T) {
	cache := slack.NewUserCacheFromMap("example.slack.com", map[string]slack.CachedUser{
		"UOTHER": {ID: "UOTHER", Name: "alice", DisplayName: "Alice"},
	})
	m := slack.Message{Text: "hey", Ts: "1718197925.000000", User: "UOTHER"}
	out := formatHistoryMessage(m, "D0B3PCPL0CF", cache, "USELF", "Alice")
	header := strings.SplitN(out, "\n", 2)[0]
	if !strings.Contains(header, "DM: Alice → You") {
		t.Errorf("expected 'DM: Alice → You' in header: %q", header)
	}
}

func TestFormatHistoryMessage_dmPeer_senderIsSelf(t *testing.T) {
	m := slack.Message{Text: "sent by me", Ts: "1718197925.000000", User: "USELF"}
	out := formatHistoryMessage(m, "D0B3PCPL0CF", nil, "USELF", "Alice")
	header := strings.SplitN(out, "\n", 2)[0]
	if !strings.Contains(header, "DM: You → Alice") {
		t.Errorf("expected 'DM: You → Alice' in header: %q", header)
	}
}

// ---------------------------------------------------------------------------
// History — parse errors propagated without hitting keychain
// ---------------------------------------------------------------------------

func TestHistory_missingChannel(t *testing.T) {
	_, err := History(nil, HistoryFlags{})
	if err == nil {
		t.Fatal("expected error when no channel provided, got nil")
	}
	if !strings.Contains(err.Error(), "channel required") {
		t.Errorf("expected 'channel required' in error, got: %v", err)
	}
}

func TestHistory_invalidBefore(t *testing.T) {
	_, err := History([]string{"C0B3Z1KT80K"}, HistoryFlags{Before: "not-a-date"})
	if err == nil {
		t.Fatal("expected error for invalid --before, got nil")
	}
}

func TestHistoryJSON_missingChannel(t *testing.T) {
	_, err := HistoryJSON(nil, HistoryFlags{})
	if err == nil {
		t.Fatal("expected error when no channel provided, got nil")
	}
	if !strings.Contains(err.Error(), "channel required") {
		t.Errorf("expected 'channel required' in error, got: %v", err)
	}
}

func TestHistoryPretty_missingChannel(t *testing.T) {
	_, err := HistoryPretty(nil, HistoryFlags{})
	if err == nil {
		t.Fatal("expected error when no channel provided, got nil")
	}
}
