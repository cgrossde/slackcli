// Package cmd — delete_test.go tests the delete command logic without network
// or keychain access.
package cmd

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// parseDeleteTarget
// ---------------------------------------------------------------------------

func TestParseDeleteTarget_urlArg(t *testing.T) {
	url := "https://myorg.slack.com/archives/C0B3PCPL0CF/p1718197925001234"
	flags := DeleteFlags{}
	ch, ts, threadTs, err := parseDeleteTarget([]string{url}, flags)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ch != "C0B3PCPL0CF" {
		t.Errorf("channel = %q, want C0B3PCPL0CF", ch)
	}
	if !strings.Contains(ts, "1718197925") {
		t.Errorf("ts = %q, expected ts from URL", ts)
	}
	if threadTs != "" {
		t.Errorf("threadTs = %q, want empty for non-thread URL", threadTs)
	}
}

func TestParseDeleteTarget_channelTs(t *testing.T) {
	flags := DeleteFlags{}
	ch, ts, threadTs, err := parseDeleteTarget([]string{"C0B3PCPL0CF:1718197925.001234"}, flags)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ch != "C0B3PCPL0CF" {
		t.Errorf("channel = %q, want C0B3PCPL0CF", ch)
	}
	if ts != "1718197925.001234" {
		t.Errorf("ts = %q, want 1718197925.001234", ts)
	}
	if threadTs != "" {
		t.Errorf("threadTs = %q, want empty for channelTs form", threadTs)
	}
}

func TestParseDeleteTarget_flags(t *testing.T) {
	flags := DeleteFlags{Channel: "C0B3PCPL0CF", Ts: "1718197925.001234"}
	ch, ts, threadTs, err := parseDeleteTarget(nil, flags)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ch != "C0B3PCPL0CF" {
		t.Errorf("channel = %q, want C0B3PCPL0CF", ch)
	}
	if ts != "1718197925.001234" {
		t.Errorf("ts = %q, want 1718197925.001234", ts)
	}
	if threadTs != "" {
		t.Errorf("threadTs = %q, want empty for flag form", threadTs)
	}
}

func TestParseDeleteTarget_missingBoth(t *testing.T) {
	flags := DeleteFlags{}
	_, _, _, err := parseDeleteTarget(nil, flags)
	if err == nil {
		t.Fatal("expected error when neither URL nor flags provided, got nil")
	}
}

func TestParseDeleteTarget_missingTs(t *testing.T) {
	flags := DeleteFlags{Channel: "C0B3PCPL0CF"} // no Ts
	_, _, _, err := parseDeleteTarget(nil, flags)
	if err == nil {
		t.Fatal("expected error when --ts missing, got nil")
	}
}

func TestParseDeleteTarget_missingChannel(t *testing.T) {
	flags := DeleteFlags{Ts: "1718197925.001234"} // no Channel
	_, _, _, err := parseDeleteTarget(nil, flags)
	if err == nil {
		t.Fatal("expected error when --channel missing, got nil")
	}
}

func TestParseDeleteTarget_channelConflict(t *testing.T) {
	url := "https://myorg.slack.com/archives/C0B3PCPL0CF/p1718197925001234"
	flags := DeleteFlags{Channel: "C999999999"}
	_, _, _, err := parseDeleteTarget([]string{url}, flags)
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
}

func TestParseDeleteTarget_tsConflict(t *testing.T) {
	url := "https://myorg.slack.com/archives/C0B3PCPL0CF/p1718197925001234"
	flags := DeleteFlags{Ts: "9999999999.000000"}
	_, _, _, err := parseDeleteTarget([]string{url}, flags)
	if err == nil {
		t.Fatal("expected ts conflict error, got nil")
	}
}

func TestParseDeleteTarget_nonURLArg(t *testing.T) {
	flags := DeleteFlags{}
	_, _, _, err := parseDeleteTarget([]string{"not-a-url"}, flags)
	if err == nil {
		t.Fatal("expected error for non-URL/non-channelts arg, got nil")
	}
}

// ---------------------------------------------------------------------------
// channel name pass-through
// ---------------------------------------------------------------------------

func TestParseDeleteTarget_channelNamePassthrough(t *testing.T) {
	// parseDeleteTarget passes channel names through as-is; resolution to an ID
	// is deferred to Delete() via resolveChannelName.
	flags := DeleteFlags{Channel: "general", Ts: "1718197925.001234"}
	ch, ts, threadTs, err := parseDeleteTarget(nil, flags)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ch != "general" {
		t.Errorf("expected channel pass-through %q, got %q", "general", ch)
	}
	if ts != "1718197925.001234" {
		t.Errorf("unexpected ts %q", ts)
	}
	if threadTs != "" {
		t.Errorf("threadTs = %q, want empty for channel name pass-through", threadTs)
	}
}

func TestParseDeleteTarget_threadReplyURL(t *testing.T) {
	threadURL := "https://myorg.slack.com/archives/C0B3PCPL0CF/p1779023515154839?thread_ts=1779023514.528229&cid=C0B3PCPL0CF"
	flags := DeleteFlags{}
	ch, ts, threadTs, err := parseDeleteTarget([]string{threadURL}, flags)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ch != "C0B3PCPL0CF" {
		t.Errorf("channel = %q, want C0B3PCPL0CF", ch)
	}
	if ts != "1779023515.154839" {
		t.Errorf("ts = %q, want 1779023515.154839", ts)
	}
	if threadTs != "1779023514.528229" {
		t.Errorf("threadTs = %q, want 1779023514.528229", threadTs)
	}
}

// ---------------------------------------------------------------------------
// Delete — workspace / credentials path
// ---------------------------------------------------------------------------

func TestDelete_reachesWorkspaceCheck(t *testing.T) {
	// Verify that Delete advances past target parsing when valid flags are
	// supplied, by checking the error is not a parse error.
	// Uses a non-allowlisted channel so DeleteMessage rejects before chat.delete,
	// but after keychain/credential resolution. On a populated keychain this test
	// will call auth.test; on an empty one it will fail at workspace resolution.
	// Either way, the error must not be a parse error.
	_, err := Delete(nil, DeleteFlags{Channel: "C0B3PCPL0CF", Ts: "1234567890.123456"})
	if err == nil {
		t.Fatal("expected error (no real message at that ts), got nil")
	}
	errStr := err.Error()
	if strings.Contains(errStr, "provide a Slack URL") || strings.Contains(errStr, "argument must be") {
		t.Errorf("unexpected parse error: %v", err)
	}
}

func TestDelete_parseErrorPropagated(t *testing.T) {
	// Delete with no args and no flags must return the target parsing error.
	_, err := Delete(nil, DeleteFlags{})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "delete:") {
		t.Errorf("expected error prefixed with 'delete:', got: %v", err)
	}
}
