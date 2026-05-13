// Package slack — send_test.go tests SendMessage and AddReaction at the
// internal/slack layer. The tests use a fake HTTP server matching slack-go's
// test patterns so no real Keychain or network access is required.
package slack

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	slackgo "github.com/slack-go/slack"
)

// testAllowedChannels are the channel IDs seeded into AllowedWriteChannels for
// all internal/slack tests. They match the IDs used in httptest stubs below.
var testAllowedChannels = []string{"C0B3PCPL0CF", "C0B3Z1KT80K"}

// TestMain seeds AllowedWriteChannels before any test runs so that tests using
// httptest stubs are not blocked by an empty embed (allowlist.txt is gitignored
// and absent in CI / clean checkouts).
func TestMain(m *testing.M) {
	for _, id := range testAllowedChannels {
		AllowedWriteChannels[id] = true
	}
	os.Exit(m.Run())
}

// ---------------------------------------------------------------------------
// Whitelist tests
// ---------------------------------------------------------------------------

func TestIsWriteAllowed(t *testing.T) {
	cases := []struct {
		channelID string
		want      bool
	}{
		{"C0B3PCPL0CF", true},
		{"C000000000", false},
		{"", false},
		{"c0b3pcpl0cf", false}, // case-sensitive
	}
	for _, tc := range cases {
		if got := IsWriteAllowed(tc.channelID); got != tc.want {
			t.Errorf("IsWriteAllowed(%q) = %v, want %v", tc.channelID, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Send/react test server helpers
//
// The package already has newTestClient(t, handler) in auth_test.go.
// We use different helper names to avoid redeclaration.
// ---------------------------------------------------------------------------

// sendTestServer starts an httptest.Server that routes requests by path using
// the provided handler map.
func sendTestServer(handlers map[string]http.HandlerFunc) *httptest.Server {
	mux := http.NewServeMux()
	for path, h := range handlers {
		mux.HandleFunc(path, h)
	}
	return httptest.NewServer(mux)
}

// sendJSONReply writes v as JSON to w.
func sendJSONReply(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// newSendClient creates a *Client pointed at srv.
func newSendClient(srv *httptest.Server) *Client {
	return NewClient("xoxc-test", "xoxd-test",
		slackgo.OptionAPIURL(srv.URL+"/"),
	)
}

// ---------------------------------------------------------------------------
// SendMessage tests
// ---------------------------------------------------------------------------

func TestSendMessage_notAllowed(t *testing.T) {
	c := NewClient("xoxc-test", "xoxd-test")
	_, _, err := c.SendMessage("CNOTALLOWD", "hello", "", false)
	if err == nil {
		t.Fatal("expected error for non-whitelisted channel, got nil")
	}
}

func TestSendMessage_ok(t *testing.T) {
	srv := sendTestServer(map[string]http.HandlerFunc{
		"/chat.postMessage": func(w http.ResponseWriter, r *http.Request) {
			sendJSONReply(w, map[string]any{
				"ok":      true,
				"channel": "C0B3PCPL0CF",
				"ts":      "1234567890.123456",
			})
		},
	})
	defer srv.Close()

	c := newSendClient(srv)
	ch, ts, err := c.SendMessage("C0B3PCPL0CF", "hello", "", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ch != "C0B3PCPL0CF" {
		t.Errorf("channel = %q, want C0B3PCPL0CF", ch)
	}
	if ts != "1234567890.123456" {
		t.Errorf("ts = %q, want 1234567890.123456", ts)
	}
}

func TestSendMessage_withThread(t *testing.T) {
	var gotThreadTs string
	srv := sendTestServer(map[string]http.HandlerFunc{
		"/chat.postMessage": func(w http.ResponseWriter, r *http.Request) {
			if err := r.ParseForm(); err == nil {
				gotThreadTs = r.FormValue("thread_ts")
			}
			sendJSONReply(w, map[string]any{
				"ok":      true,
				"channel": "C0B3PCPL0CF",
				"ts":      "9999999999.000001",
			})
		},
	})
	defer srv.Close()

	c := newSendClient(srv)
	_, _, err := c.SendMessage("C0B3PCPL0CF", "reply", "1111111111.000000", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotThreadTs != "1111111111.000000" {
		t.Errorf("thread_ts sent = %q, want 1111111111.000000", gotThreadTs)
	}
}

func TestSendMessage_apiError(t *testing.T) {
	srv := sendTestServer(map[string]http.HandlerFunc{
		"/chat.postMessage": func(w http.ResponseWriter, r *http.Request) {
			sendJSONReply(w, map[string]any{
				"ok":    false,
				"error": "channel_not_found",
			})
		},
	})
	defer srv.Close()

	c := newSendClient(srv)
	_, _, err := c.SendMessage("C0B3PCPL0CF", "hello", "", false)
	if err == nil {
		t.Fatal("expected error from API, got nil")
	}
}

// ---------------------------------------------------------------------------
// AddReaction tests
// ---------------------------------------------------------------------------

func TestAddReaction_notAllowed(t *testing.T) {
	c := NewClient("xoxc-test", "xoxd-test")
	err := c.AddReaction("CNOTALLOWD", "1234.567890", "thumbsup")
	if err == nil {
		t.Fatal("expected error for non-whitelisted channel, got nil")
	}
}

func TestAddReaction_ok(t *testing.T) {
	var gotName, gotChannel, gotTs string
	srv := sendTestServer(map[string]http.HandlerFunc{
		"/reactions.add": func(w http.ResponseWriter, r *http.Request) {
			if err := r.ParseForm(); err == nil {
				gotName = r.FormValue("name")
				gotChannel = r.FormValue("channel")
				gotTs = r.FormValue("timestamp")
			}
			sendJSONReply(w, map[string]any{"ok": true})
		},
	})
	defer srv.Close()

	c := newSendClient(srv)
	err := c.AddReaction("C0B3PCPL0CF", "1234567890.123456", "thumbsup")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotName != "thumbsup" {
		t.Errorf("name = %q, want thumbsup", gotName)
	}
	if gotChannel != "C0B3PCPL0CF" {
		t.Errorf("channel = %q, want C0B3PCPL0CF", gotChannel)
	}
	if gotTs != "1234567890.123456" {
		t.Errorf("timestamp = %q, want 1234567890.123456", gotTs)
	}
}

func TestAddReaction_apiError(t *testing.T) {
	srv := sendTestServer(map[string]http.HandlerFunc{
		"/reactions.add": func(w http.ResponseWriter, r *http.Request) {
			sendJSONReply(w, map[string]any{
				"ok":    false,
				"error": "already_reacted",
			})
		},
	})
	defer srv.Close()

	c := newSendClient(srv)
	err := c.AddReaction("C0B3PCPL0CF", "1234567890.123456", "thumbsup")
	if err == nil {
		t.Fatal("expected error from API, got nil")
	}
	if !containsSub(err.Error(), "already_reacted") {
		t.Errorf("error %q does not contain %q", err.Error(), "already_reacted")
	}
}

// ---------------------------------------------------------------------------
// RemoveReaction tests
// ---------------------------------------------------------------------------

func TestRemoveReaction_notAllowed(t *testing.T) {
	c := NewClient("xoxc-test", "xoxd-test")
	err := c.RemoveReaction("CNOTALLOWD", "1234.567890", "thumbsup")
	if err == nil {
		t.Fatal("expected error for non-whitelisted channel, got nil")
	}
}

func TestRemoveReaction_ok(t *testing.T) {
	var gotName, gotChannel, gotTs string
	srv := sendTestServer(map[string]http.HandlerFunc{
		"/reactions.remove": func(w http.ResponseWriter, r *http.Request) {
			if err := r.ParseForm(); err == nil {
				gotName = r.FormValue("name")
				gotChannel = r.FormValue("channel")
				gotTs = r.FormValue("timestamp")
			}
			sendJSONReply(w, map[string]any{"ok": true})
		},
	})
	defer srv.Close()

	c := newSendClient(srv)
	err := c.RemoveReaction("C0B3PCPL0CF", "1234567890.123456", "thumbsup")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotName != "thumbsup" {
		t.Errorf("name = %q, want thumbsup", gotName)
	}
	if gotChannel != "C0B3PCPL0CF" {
		t.Errorf("channel = %q, want C0B3PCPL0CF", gotChannel)
	}
	if gotTs != "1234567890.123456" {
		t.Errorf("timestamp = %q, want 1234567890.123456", gotTs)
	}
}

func TestRemoveReaction_apiError(t *testing.T) {
	srv := sendTestServer(map[string]http.HandlerFunc{
		"/reactions.remove": func(w http.ResponseWriter, r *http.Request) {
			sendJSONReply(w, map[string]any{
				"ok":    false,
				"error": "no_reaction",
			})
		},
	})
	defer srv.Close()

	c := newSendClient(srv)
	err := c.RemoveReaction("C0B3PCPL0CF", "1234567890.123456", "thumbsup")
	if err == nil {
		t.Fatal("expected error from API, got nil")
	}
	if !containsSub(err.Error(), "no_reaction") {
		t.Errorf("error %q does not contain %q", err.Error(), "no_reaction")
	}
}

// ---------------------------------------------------------------------------
// DeleteMessage tests
// ---------------------------------------------------------------------------

func TestDeleteMessage_notAllowed(t *testing.T) {
	c := NewClient("xoxc-test", "xoxd-test")
	_, _, err := c.DeleteMessage("CNOTALLOWD", "1234567890.123456")
	if err == nil {
		t.Fatal("expected error for non-whitelisted channel, got nil")
	}
	if !containsSub(err.Error(), "write allowlist") {
		t.Errorf("error %q does not contain 'write allowlist'", err.Error())
	}
}

func TestDeleteMessage_ok(t *testing.T) {
	var gotChannel, gotTs string
	srv := sendTestServer(map[string]http.HandlerFunc{
		"/chat.delete": func(w http.ResponseWriter, r *http.Request) {
			if err := r.ParseForm(); err == nil {
				gotChannel = r.FormValue("channel")
				gotTs = r.FormValue("ts")
			}
			sendJSONReply(w, map[string]any{
				"ok":      true,
				"channel": "C0B3PCPL0CF",
				"ts":      "1234567890.123456",
			})
		},
	})
	defer srv.Close()

	c := newSendClient(srv)
	ch, ts, err := c.DeleteMessage("C0B3PCPL0CF", "1234567890.123456")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ch != "C0B3PCPL0CF" {
		t.Errorf("channel = %q, want C0B3PCPL0CF", ch)
	}
	if ts != "1234567890.123456" {
		t.Errorf("ts = %q, want 1234567890.123456", ts)
	}
	if gotChannel != "C0B3PCPL0CF" {
		t.Errorf("sent channel = %q, want C0B3PCPL0CF", gotChannel)
	}
	if gotTs != "1234567890.123456" {
		t.Errorf("sent ts = %q, want 1234567890.123456", gotTs)
	}
}

func TestDeleteMessage_apiError(t *testing.T) {
	srv := sendTestServer(map[string]http.HandlerFunc{
		"/chat.delete": func(w http.ResponseWriter, r *http.Request) {
			sendJSONReply(w, map[string]any{
				"ok":    false,
				"error": "cant_delete_message",
			})
		},
	})
	defer srv.Close()

	c := newSendClient(srv)
	_, _, err := c.DeleteMessage("C0B3PCPL0CF", "1234567890.123456")
	if err == nil {
		t.Fatal("expected error from API, got nil")
	}
	if !containsSub(err.Error(), "cant_delete_message") {
		t.Errorf("error %q does not contain 'cant_delete_message'", err.Error())
	}
}

// containsSub is a substring check used in test assertions.
func containsSub(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ---------------------------------------------------------------------------
// SendMessage noPreview tests
// ---------------------------------------------------------------------------

func TestSendMessage_noPreview_sendsDisableUnfurl(t *testing.T) {
	var gotUnfurlLinks string
	srv := sendTestServer(map[string]http.HandlerFunc{
		"/chat.postMessage": func(w http.ResponseWriter, r *http.Request) {
			if err := r.ParseForm(); err == nil {
				gotUnfurlLinks = r.FormValue("unfurl_links")
			}
			sendJSONReply(w, map[string]any{
				"ok":      true,
				"channel": "C0B3PCPL0CF",
				"ts":      "1234567890.123456",
			})
		},
	})
	defer srv.Close()

	c := newSendClient(srv)
	_, _, err := c.SendMessage("C0B3PCPL0CF", "hello https://example.com", "", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotUnfurlLinks != "false" {
		t.Errorf("unfurl_links = %q, want \"false\"", gotUnfurlLinks)
	}
}

func TestSendMessage_defaultDoesNotDisableUnfurl(t *testing.T) {
	var gotUnfurlLinks string
	srv := sendTestServer(map[string]http.HandlerFunc{
		"/chat.postMessage": func(w http.ResponseWriter, r *http.Request) {
			if err := r.ParseForm(); err == nil {
				gotUnfurlLinks = r.FormValue("unfurl_links")
			}
			sendJSONReply(w, map[string]any{
				"ok":      true,
				"channel": "C0B3PCPL0CF",
				"ts":      "1234567890.123456",
			})
		},
	})
	defer srv.Close()

	c := newSendClient(srv)
	_, _, err := c.SendMessage("C0B3PCPL0CF", "hello https://example.com", "", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// When noPreview is false, no unfurl_links param should be sent.
	if gotUnfurlLinks == "false" {
		t.Errorf("unfurl_links should not be forced false when noPreview=false, got %q", gotUnfurlLinks)
	}
}

// ---------------------------------------------------------------------------
// ForwardMessage tests
// ---------------------------------------------------------------------------

func TestForwardMessage_notAllowed(t *testing.T) {
	c := NewClient("xoxc-test", "xoxd-test")
	_, _, err := c.ForwardMessage("myorg.slack.com", "C0B3PCPL0CF", "1234567890.123456", "CNOTALLOWD", "", false)
	if err == nil {
		t.Fatal("expected error for non-whitelisted destination, got nil")
	}
	if !containsSub(err.Error(), "write allowlist") {
		t.Errorf("error %q does not contain 'write allowlist'", err.Error())
	}
}

func TestForwardMessage_defaultEnablesUnfurl(t *testing.T) {
	var gotUnfurlLinks string
	srv := sendTestServer(map[string]http.HandlerFunc{
		"/chat.postMessage": func(w http.ResponseWriter, r *http.Request) {
			if err := r.ParseForm(); err == nil {
				gotUnfurlLinks = r.FormValue("unfurl_links")
			}
			sendJSONReply(w, map[string]any{
				"ok":      true,
				"channel": "C0B3PCPL0CF",
				"ts":      "1234567890.123456",
			})
		},
	})
	defer srv.Close()

	c := newSendClient(srv)
	_, _, err := c.ForwardMessage("myorg.slack.com", "C0B3PCPL0CF", "1234567890.123456", "C0B3PCPL0CF", "", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotUnfurlLinks != "true" {
		t.Errorf("unfurl_links = %q, want \"true\" (default forward enables unfurl)", gotUnfurlLinks)
	}
}

func TestForwardMessage_noPreviewDisablesUnfurl(t *testing.T) {
	var gotUnfurlLinks string
	srv := sendTestServer(map[string]http.HandlerFunc{
		"/chat.postMessage": func(w http.ResponseWriter, r *http.Request) {
			if err := r.ParseForm(); err == nil {
				gotUnfurlLinks = r.FormValue("unfurl_links")
			}
			sendJSONReply(w, map[string]any{
				"ok":      true,
				"channel": "C0B3PCPL0CF",
				"ts":      "1234567890.123456",
			})
		},
	})
	defer srv.Close()

	c := newSendClient(srv)
	_, _, err := c.ForwardMessage("myorg.slack.com", "C0B3PCPL0CF", "1234567890.123456", "C0B3PCPL0CF", "", true)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotUnfurlLinks != "false" {
		t.Errorf("unfurl_links = %q, want \"false\" when noPreview=true", gotUnfurlLinks)
	}
}

func TestForwardMessage_withNote(t *testing.T) {
	var gotText string
	srv := sendTestServer(map[string]http.HandlerFunc{
		"/chat.postMessage": func(w http.ResponseWriter, r *http.Request) {
			if err := r.ParseForm(); err == nil {
				gotText = r.FormValue("text")
			}
			sendJSONReply(w, map[string]any{
				"ok":      true,
				"channel": "C0B3PCPL0CF",
				"ts":      "1234567890.123456",
			})
		},
	})
	defer srv.Close()

	c := newSendClient(srv)
	_, _, err := c.ForwardMessage("myorg.slack.com", "C0B3PCPL0CF", "1234567890.123456", "C0B3PCPL0CF", "FYI", false)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	expectedPermalink := "https://myorg.slack.com/archives/C0B3PCPL0CF/p1234567890123456"
	expectedText := "FYI\n" + expectedPermalink
	if gotText != expectedText {
		t.Errorf("text = %q, want %q", gotText, expectedText)
	}
}
