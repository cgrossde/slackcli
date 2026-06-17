// Package cmd — open_test.go covers the open command's parsing and dispatch
// paths that do not require credentials or the network. Tests that would
// otherwise reach keychain.ResolveDefault() are constrained to inputs that
// fail validation in slack.* parsers BEFORE any credential lookup.
package cmd

import (
	"strings"
	"testing"

	"github.com/cgrossde/slackcli/internal/slack"
)

// ---------------------------------------------------------------------------
// Form detection — buildDeepLink dispatches on input shape; these tests
// exercise the failure branch in each form so we never reach the keychain.
// ---------------------------------------------------------------------------

func TestOpen_emptyTarget(t *testing.T) {
	_, err := Open("", OpenFlags{})
	if err == nil {
		t.Fatal("want error for empty target, got nil")
	}
	if !strings.Contains(err.Error(), "must not be empty") {
		t.Errorf("error %q lacks expected hint", err)
	}
}

func TestOpen_unrecognisedTarget(t *testing.T) {
	// "ABCDE" is not a Slack URL, channelID, channelID:ts, #name, @user, or
	// a plain lower-case name. Must fail with a clear hint and never reach
	// the keychain.
	_, err := Open("ABCDE", OpenFlags{})
	if err == nil {
		t.Fatal("want error for unrecognised target, got nil")
	}
	if !strings.Contains(err.Error(), "cannot interpret target") {
		t.Errorf("error %q lacks expected hint", err)
	}
}

func TestOpen_malformedSlackURL(t *testing.T) {
	// https://… that is not a valid Slack permalink — fails inside
	// slack.ParseSlackURL before any credential touch.
	_, err := Open("https://example.com/not/slack", OpenFlags{})
	if err == nil {
		t.Fatal("want error for non-Slack URL, got nil")
	}
}

func TestOpen_malformedChannelTs(t *testing.T) {
	// Bad channel-ID prefix — fails inside slack.ParseChannelTs (channel ID
	// must start with C/D/G/W).
	_, err := Open("X123:1718197925.001234", OpenFlags{})
	if err == nil {
		t.Fatal("want error for bad channel:ts, got nil")
	}
}

// ---------------------------------------------------------------------------
// pickWorkspace — pure logic, no keychain reads when ref or flag is set.
// ---------------------------------------------------------------------------

func TestPickWorkspace_refTakesPriority(t *testing.T) {
	got, err := pickWorkspace("acme.slack.com", "other")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "acme.slack.com" {
		t.Errorf("got %q, want %q", got, "acme.slack.com")
	}
}

func TestPickWorkspace_flagFallback(t *testing.T) {
	got, err := pickWorkspace("", "myorg")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "myorg.slack.com" {
		t.Errorf("got %q, want %q", got, "myorg.slack.com")
	}
}

// ---------------------------------------------------------------------------
// pickUserExact — pure logic.
// ---------------------------------------------------------------------------

func TestPickUserExact(t *testing.T) {
	users := mustImportCachedUsers(t)
	if got := pickUserExact(users, "alice"); got != "U001" {
		t.Errorf("alice → %q, want U001", got)
	}
	if got := pickUserExact(users, "Alice Anderson"); got != "U001" {
		t.Errorf("display match → %q, want U001", got)
	}
	if got := pickUserExact(users, "Charlie"); got != "" {
		t.Errorf("missing → %q, want empty", got)
	}
}

// ---------------------------------------------------------------------------
// isPlainName — name detection used to disambiguate channel names from IDs.
// ---------------------------------------------------------------------------

func TestIsPlainName(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"general", true},
		{"ops-team", true},
		{"ops_team_v2", true},
		{"alice.bob", true},
		{"", false},
		{"Caps", false},      // upper-case → looks like an ID prefix, not a name
		{"#hash", false},     // leading # is stripped before this check
		{"with space", false},
		{"slash/path", false},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := isPlainName(tc.in); got != tc.want {
				t.Errorf("isPlainName(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// launch — Print path is fully testable; non-Print path reaches the OS, so
// only the Print branch is asserted here.
// ---------------------------------------------------------------------------

func TestLaunch_printReturnsURL(t *testing.T) {
	got, err := launch("slack://channel?team=T1&id=C1", OpenFlags{Print: true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "slack://channel?team=T1&id=C1\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// mustImportCachedUsers builds a tiny [CachedUser] slice via the real type so
// we don't depend on package-internal helpers here.
func mustImportCachedUsers(t *testing.T) []slack.CachedUser {
	t.Helper()
	return []slack.CachedUser{
		{ID: "U001", Name: "alice", DisplayName: "Alice Anderson"},
		{ID: "U002", Name: "bob", DisplayName: "Bob Brown"},
	}
}
