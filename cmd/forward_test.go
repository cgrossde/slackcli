// Package cmd — forward_test.go tests the forward command logic without
// network or keychain access.
package cmd

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// parseForwardSource
// ---------------------------------------------------------------------------

func TestParseForwardSource_urlArg(t *testing.T) {
	url := "https://myorg.slack.com/archives/C0B3PCPL0CF/p1718197925001234"
	flags := ForwardFlags{}
	ch, ts, _, err := parseForwardSource([]string{url}, flags)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ch != "C0B3PCPL0CF" {
		t.Errorf("channel = %q, want C0B3PCPL0CF", ch)
	}
	if !strings.Contains(ts, "1718197925") {
		t.Errorf("ts = %q, expected ts from URL", ts)
	}
}

func TestParseForwardSource_channelTs(t *testing.T) {
	flags := ForwardFlags{}
	ch, ts, _, err := parseForwardSource([]string{"C0B3PCPL0CF:1718197925.001234"}, flags)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ch != "C0B3PCPL0CF" {
		t.Errorf("channel = %q, want C0B3PCPL0CF", ch)
	}
	if ts != "1718197925.001234" {
		t.Errorf("ts = %q, want 1718197925.001234", ts)
	}
}

func TestParseForwardSource_flags(t *testing.T) {
	flags := ForwardFlags{Channel: "C0B3PCPL0CF", Ts: "1718197925.001234"}
	ch, ts, _, err := parseForwardSource(nil, flags)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ch != "C0B3PCPL0CF" {
		t.Errorf("channel = %q, want C0B3PCPL0CF", ch)
	}
	if ts != "1718197925.001234" {
		t.Errorf("ts = %q, want 1718197925.001234", ts)
	}
}

func TestParseForwardSource_missingBoth(t *testing.T) {
	flags := ForwardFlags{}
	_, _, _, err := parseForwardSource(nil, flags)
	if err == nil {
		t.Fatal("expected error when neither URL nor flags provided, got nil")
	}
}

func TestParseForwardSource_missingTs(t *testing.T) {
	flags := ForwardFlags{Channel: "C0B3PCPL0CF"} // no Ts
	_, _, _, err := parseForwardSource(nil, flags)
	if err == nil {
		t.Fatal("expected error when --ts missing, got nil")
	}
}

func TestParseForwardSource_missingChannel(t *testing.T) {
	flags := ForwardFlags{Ts: "1718197925.001234"} // no Channel
	_, _, _, err := parseForwardSource(nil, flags)
	if err == nil {
		t.Fatal("expected error when --channel missing, got nil")
	}
}

func TestParseForwardSource_channelConflict(t *testing.T) {
	url := "https://myorg.slack.com/archives/C0B3PCPL0CF/p1718197925001234"
	flags := ForwardFlags{Channel: "C999999999"}
	_, _, _, err := parseForwardSource([]string{url}, flags)
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
}

func TestParseForwardSource_tsConflict(t *testing.T) {
	url := "https://myorg.slack.com/archives/C0B3PCPL0CF/p1718197925001234"
	flags := ForwardFlags{Ts: "9999999999.000000"}
	_, _, _, err := parseForwardSource([]string{url}, flags)
	if err == nil {
		t.Fatal("expected ts conflict error, got nil")
	}
}

func TestParseForwardSource_nonURLArg(t *testing.T) {
	flags := ForwardFlags{}
	_, _, _, err := parseForwardSource([]string{"not-a-url"}, flags)
	if err == nil {
		t.Fatal("expected error for non-URL/non-channelts arg, got nil")
	}
}

// ---------------------------------------------------------------------------
// channel name pass-through
// ---------------------------------------------------------------------------

func TestParseForwardSource_channelNamePassthrough(t *testing.T) {
	// parseForwardSource passes channel names through as-is; resolution to an ID
	// is deferred to Forward() via resolveChannelName.
	flags := ForwardFlags{Channel: "general", Ts: "1718197925.001234"}
	ch, ts, _, err := parseForwardSource(nil, flags)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ch != "general" {
		t.Errorf("expected channel pass-through %q, got %q", "general", ch)
	}
	if ts != "1718197925.001234" {
		t.Errorf("unexpected ts %q", ts)
	}
}

// ---------------------------------------------------------------------------
// Forward — workspace / credentials path
// ---------------------------------------------------------------------------

func TestForward_reachesWorkspaceCheck(t *testing.T) {
	// Forward should fail at the write-allowlist check on the destination,
	// not at a parsing error, proving that --to validation and source parsing
	// succeeded and execution reached the ForwardMessage call.
	_, err := Forward(nil, ForwardFlags{
		Channel: "C0B3PCPL0CF",
		Ts:      "1234567890.123456",
		To:      "CNOTALLOWD", // not in write allowlist → deterministic error
	})
	if err == nil {
		t.Fatal("expected allowlist error, got nil")
	}
	// Must be an allowlist/forward error, not a parsing or --to error.
	errStr := err.Error()
	if strings.Contains(errStr, "provide a Slack URL") || strings.Contains(errStr, "argument must be") {
		t.Errorf("unexpected parse error (expected allowlist error): %v", err)
	}
	if strings.Contains(errStr, "--to is required") {
		t.Errorf("unexpected --to error (expected allowlist error): %v", err)
	}
	if !strings.Contains(errStr, "write allowlist") {
		t.Errorf("expected write allowlist error, got: %v", err)
	}
}

func TestForward_missingTo(t *testing.T) {
	// Forward with no --to must return an error before any parsing.
	_, err := Forward(nil, ForwardFlags{
		Channel: "C0B3PCPL0CF",
		Ts:      "1234567890.123456",
		// To deliberately omitted
	})
	if err == nil {
		t.Fatal("expected error for missing --to, got nil")
	}
	if !strings.Contains(err.Error(), "--to is required") {
		t.Errorf("expected --to error, got: %v", err)
	}
}

func TestForward_parseErrorPropagated(t *testing.T) {
	// Forward with --to set but no source must return the source parsing error.
	_, err := Forward(nil, ForwardFlags{To: "C0B3Z1KT80K"})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "forward:") {
		t.Errorf("expected error prefixed with 'forward:', got: %v", err)
	}
}
