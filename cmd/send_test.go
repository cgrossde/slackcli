// Package cmd — send_test.go tests the send command logic without network or
// keychain access.
package cmd

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// parseSendArgs
// ---------------------------------------------------------------------------

func TestParseSendArgs_channelFlag(t *testing.T) {
	flags := SendFlags{Channel: "C0B3PCPL0CF"}
	ch, thread, body, err := parseSendArgs([]string{}, flags)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ch != "C0B3PCPL0CF" {
		t.Errorf("channel = %q, want C0B3PCPL0CF", ch)
	}
	if thread != "" {
		t.Errorf("thread = %q, want empty", thread)
	}
	if len(body) != 0 {
		t.Errorf("bodyArgs = %v, want empty", body)
	}
}

func TestParseSendArgs_inlineText(t *testing.T) {
	flags := SendFlags{Channel: "C0B3PCPL0CF"}
	ch, _, body, err := parseSendArgs([]string{"hello world"}, flags)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ch != "C0B3PCPL0CF" {
		t.Errorf("channel = %q, want C0B3PCPL0CF", ch)
	}
	if len(body) != 1 || body[0] != "hello world" {
		t.Errorf("bodyArgs = %v, want [hello world]", body)
	}
}

func TestParseSendArgs_slackURL(t *testing.T) {
	url := "https://myorg.slack.com/archives/C0B3PCPL0CF/p1718197925001234"
	flags := SendFlags{}
	ch, thread, body, err := parseSendArgs([]string{url}, flags)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ch != "C0B3PCPL0CF" {
		t.Errorf("channel = %q, want C0B3PCPL0CF", ch)
	}
	if !strings.Contains(thread, "1718197925") {
		t.Errorf("thread = %q, expected ts from URL", thread)
	}
	if len(body) != 0 {
		t.Errorf("bodyArgs = %v, want empty (body from URL form must come from file/stdin)", body)
	}
}

func TestParseSendArgs_slackURLWithTextArg(t *testing.T) {
	url := "https://myorg.slack.com/archives/C0B3PCPL0CF/p1718197925001234"
	flags := SendFlags{}
	// Text arg alongside URL: text is the message body.
	_, _, body, err := parseSendArgs([]string{"my message", url}, flags)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(body) != 1 || body[0] != "my message" {
		t.Errorf("bodyArgs = %v, want [my message]", body)
	}
}

func TestParseSendArgs_missingChannel(t *testing.T) {
	flags := SendFlags{}
	_, _, _, err := parseSendArgs([]string{"hello"}, flags)
	if err == nil {
		t.Fatal("expected error when --channel missing, got nil")
	}
}

func TestParseSendArgs_threadFlag(t *testing.T) {
	flags := SendFlags{Channel: "C0B3PCPL0CF", Thread: "1111111111.000000"}
	_, thread, _, err := parseSendArgs([]string{}, flags)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if thread != "1111111111.000000" {
		t.Errorf("thread = %q, want 1111111111.000000", thread)
	}
}

func TestParseSendArgs_channelConflict(t *testing.T) {
	url := "https://myorg.slack.com/archives/C0B3PCPL0CF/p1718197925001234"
	flags := SendFlags{Channel: "C999999999"}
	_, _, _, err := parseSendArgs([]string{url}, flags)
	if err == nil {
		t.Fatal("expected conflict error, got nil")
	}
}

func TestParseSendArgs_channelTs(t *testing.T) {
	flags := SendFlags{}
	ch, thread, body, err := parseSendArgs([]string{"C0B3PCPL0CF:1718197925.001234"}, flags)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ch != "C0B3PCPL0CF" {
		t.Errorf("channel = %q, want C0B3PCPL0CF", ch)
	}
	if thread != "1718197925.001234" {
		t.Errorf("thread = %q, want 1718197925.001234", thread)
	}
	if len(body) != 0 {
		t.Errorf("bodyArgs = %v, want empty", body)
	}
}

func TestParseSendArgs_channelTsWithText(t *testing.T) {
	flags := SendFlags{}
	ch, thread, body, err := parseSendArgs([]string{"reply text", "C0B3PCPL0CF:1718197925.001234"}, flags)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ch != "C0B3PCPL0CF" {
		t.Errorf("channel = %q, want C0B3PCPL0CF", ch)
	}
	if thread != "1718197925.001234" {
		t.Errorf("thread = %q, want 1718197925.001234", thread)
	}
	if len(body) != 1 || body[0] != "reply text" {
		t.Errorf("bodyArgs = %v, want [reply text]", body)
	}
}

// ---------------------------------------------------------------------------
// resolveMessageBody
// ---------------------------------------------------------------------------

func TestResolveMessageBody_fromArgs(t *testing.T) {
	text, err := resolveMessageBody([]string{"inline text"}, "", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "inline text" {
		t.Errorf("text = %q, want inline text", text)
	}
}

func TestResolveMessageBody_fromFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/msg.txt"
	if werr := os.WriteFile(path, []byte("file content\n"), 0o600); werr != nil {
		t.Fatal(werr)
	}
	text, err := resolveMessageBody(nil, path, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "file content\n" {
		t.Errorf("text = %q, want %q", text, "file content\n")
	}
}

func TestResolveMessageBody_fromFileMissing(t *testing.T) {
	_, err := resolveMessageBody(nil, "/no/such/file.txt", nil)
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

func TestResolveMessageBody_fromStdin(t *testing.T) {
	r := bytes.NewBufferString("stdin content")
	text, err := resolveMessageBody(nil, "", r)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "stdin content" {
		t.Errorf("text = %q, want stdin content", text)
	}
}

func TestResolveMessageBody_argTakesPriorityOverFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/msg.txt"
	if werr := os.WriteFile(path, []byte("file content"), 0o600); werr != nil {
		t.Fatal(werr)
	}
	text, err := resolveMessageBody([]string{"inline takes priority"}, path, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if text != "inline takes priority" {
		t.Errorf("text = %q, want inline takes priority", text)
	}
}

// ---------------------------------------------------------------------------
// isSlackArchivesURL
// ---------------------------------------------------------------------------

func TestIsSlackArchivesURL(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"https://myorg.slack.com/archives/C0B3PCPL0CF/p123", true},
		{"https://acme.slack.com/archives/C123/p456", true},
		{"http://myorg.slack.com/archives/C123/p456", false}, // http not https
		{"hello world", false},
		{"C0B3PCPL0CF:1234.5678", false},
		{"https://myorg.slack.com/messages/C123", false},
	}
	for _, tc := range cases {
		if got := isSlackArchivesURL(tc.in); got != tc.want {
			t.Errorf("isSlackArchivesURL(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// looksLikeChannelID — gate predicate for channel name resolution
// ---------------------------------------------------------------------------

func TestLooksLikeChannelID(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"C0B3PCPL0CF", true},  // normal channel ID
		{"D0B3PCPL0CF", true},  // DM
		{"G0B3PCPL0CF", true},  // group DM
		{"W0B3PCPL0CF", true},  // workspace-level
		{"C123", true},
		{"general", false},     // channel name, not ID
		{"#general", false},    // prefixed name
		{"ops-team", false},    // hyphenated name
		{"c0b3pcpl0cf", false}, // lowercase not a valid ID
		{"", false},
		{"C", false},           // too short
	}
	for _, tc := range cases {
		if got := looksLikeChannelID(tc.in); got != tc.want {
			t.Errorf("looksLikeChannelID(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Send -- react flag
// ---------------------------------------------------------------------------

func TestSend_reactFlagReachesAllowlist(t *testing.T) {
	// Supply a non-allowlisted channel so Send fails before touching the
	// keychain or network, while still proving that the --react flag is wired
	// and that colon stripping does not panic or error at the parse stage.
	_, err := Send(
		[]string{},
		SendFlags{
			Channel: "CNOTALLOWD",
			React:   ":white_check_mark:", // colons stripped in Send body
		},
		strings.NewReader("hello"),
	)
	if err == nil {
		t.Fatal("expected allowlist error, got nil")
	}
	if !strings.Contains(err.Error(), "write allowlist") {
		t.Errorf("expected write allowlist error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Send -- no-preview flag
// ---------------------------------------------------------------------------

func TestSend_noPreviewFlagReachesAllowlist(t *testing.T) {
	// Supply a non-allowlisted channel so Send fails at the allowlist gate
	// before touching the keychain or network, proving that --no-preview is
	// wired through SendFlags without panicking.
	_, err := Send(
		[]string{},
		SendFlags{
			Channel:   "CNOTALLOWD",
			NoPreview: true,
		},
		strings.NewReader("hello"),
	)
	if err == nil {
		t.Fatal("expected allowlist error, got nil")
	}
	if !strings.Contains(err.Error(), "write allowlist") {
		t.Errorf("expected write allowlist error, got: %v", err)
	}
}
