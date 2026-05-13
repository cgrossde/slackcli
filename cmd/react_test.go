// Package cmd — react_test.go tests the react command logic without network or
// keychain access.
package cmd

import (
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// parseReactTarget
// ---------------------------------------------------------------------------

func TestParseReactTarget_urlArg(t *testing.T) {
	url := "https://myorg.slack.com/archives/C0B3PCPL0CF/p1718197925001234"
	flags := ReactFlags{}
	ch, ts, err := parseReactTarget([]string{url}, flags)
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

func TestParseReactTarget_channelTs(t *testing.T) {
	flags := ReactFlags{}
	ch, ts, err := parseReactTarget([]string{"C0B3PCPL0CF:1718197925.001234"}, flags)
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

func TestParseReactTarget_flags(t *testing.T) {
	flags := ReactFlags{Channel: "C0B3PCPL0CF", Ts: "1718197925.001234"}
	ch, ts, err := parseReactTarget(nil, flags)
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

func TestParseReactTarget_missingBoth(t *testing.T) {
	flags := ReactFlags{}
	_, _, err := parseReactTarget(nil, flags)
	if err == nil {
		t.Fatal("expected error when neither URL nor flags provided, got nil")
	}
}

func TestParseReactTarget_missingTs(t *testing.T) {
	flags := ReactFlags{Channel: "C0B3PCPL0CF"} // no Ts
	_, _, err := parseReactTarget(nil, flags)
	if err == nil {
		t.Fatal("expected error when --ts missing, got nil")
	}
}

func TestParseReactTarget_missingChannel(t *testing.T) {
	flags := ReactFlags{Ts: "1718197925.001234"} // no Channel
	_, _, err := parseReactTarget(nil, flags)
	if err == nil {
		t.Fatal("expected error when --channel missing, got nil")
	}
}

func TestParseReactTarget_channelConflict(t *testing.T) {
	url := "https://myorg.slack.com/archives/C0B3PCPL0CF/p1718197925001234"
	flags := ReactFlags{Channel: "C999999999"}
	_, _, err := parseReactTarget([]string{url}, flags)
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
}

func TestParseReactTarget_tsConflict(t *testing.T) {
	url := "https://myorg.slack.com/archives/C0B3PCPL0CF/p1718197925001234"
	flags := ReactFlags{Ts: "9999999999.000000"}
	_, _, err := parseReactTarget([]string{url}, flags)
	if err == nil {
		t.Fatal("expected ts conflict error, got nil")
	}
}

func TestParseReactTarget_nonURLSecondArg(t *testing.T) {
	flags := ReactFlags{}
	_, _, err := parseReactTarget([]string{"not-a-url"}, flags)
	if err == nil {
		t.Fatal("expected error for non-URL second arg, got nil")
	}
}

// ---------------------------------------------------------------------------
// React emoji parsing
// ---------------------------------------------------------------------------

func TestReact_emojiStripsColons(t *testing.T) {
	// Test that ":thumbsup:" and "thumbsup" are treated identically — the
	// colon-stripping happens before any channel or API check.
	// Use a non-allowlisted channel so the test terminates at the write-allowlist
	// check inside AddReaction, before any keychain or network access.
	for _, emoji := range []string{":thumbsup:", "thumbsup"} {
		_, err := React([]string{emoji}, ReactFlags{Channel: "CNOTALLOWD", Ts: "1234.5678"})
		if err == nil {
			t.Errorf("React(%q) expected allowlist error, got nil", emoji)
			continue
		}
		if strings.Contains(err.Error(), "emoji") {
			t.Errorf("React(%q) error mentions emoji unexpectedly: %v", emoji, err)
		}
		if !strings.Contains(err.Error(), "write allowlist") {
			t.Errorf("React(%q) expected allowlist error, got: %v", emoji, err)
		}
	}
}

func TestReact_removeFlag(t *testing.T) {
	// With --remove set, React should reach the write-allowlist check without
	// any flag-parsing or emoji-validation error. Use a non-allowlisted channel
	// so the test terminates there, before any keychain or network access.
	_, err := React([]string{"thumbsup"}, ReactFlags{
		Channel: "CNOTALLOWD",
		Ts:      "1234567890.123456",
		Remove:  true,
	})
	if err == nil {
		t.Fatal("expected allowlist error, got nil")
	}
	if !strings.Contains(err.Error(), "write allowlist") {
		t.Errorf("expected allowlist error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// channel name pass-through (name→ID resolution happens in React(), not here)
// ---------------------------------------------------------------------------

func TestParseReactTarget_channelNamePassthrough(t *testing.T) {
	// parseReactTarget passes channel names through as-is; resolution to an ID
	// is deferred to React() via resolveChannelName (tested in search_channels_test.go).
	flags := ReactFlags{Channel: "general", Ts: "1718197925.001234"}
	ch, ts, err := parseReactTarget(nil, flags)
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
