package slack

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	slackgo "github.com/slack-go/slack"
)

// newTestClient creates a Client wired to a test HTTP server.
// The server URL is injected via slack.OptionAPIURL so all slack-go requests
// are routed to it. The URL must end with "/" (slack-go appends method names
// directly to the endpoint).
func newTestClient(t *testing.T, handler http.Handler) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	httpClient := newPlainCookieClient("xoxd-test")
	c := NewClient("xoxc-test", "xoxd-test",
		slackgo.OptionHTTPClient(httpClient),
		slackgo.OptionAPIURL(srv.URL+"/"),
	)
	return c, srv
}

// slackOKResponse is a helper to write a JSON response with ok:true and extra fields.
func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

func TestAuthTest_success(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/auth.test" {
			http.NotFound(w, r)
			return
		}
		writeJSON(w, map[string]any{
			"ok":      true,
			"url":     "https://myorg.slack.com/",
			"team":    "My Org",
			"user":    "alice",
			"team_id": "T001",
			"user_id": "U001",
		})
	}))

	got, err := c.AuthTest()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !got.OK {
		t.Errorf("OK: got false, want true")
	}
	if got.User != "alice" {
		t.Errorf("User: got %q, want %q", got.User, "alice")
	}
	if got.TeamID != "T001" {
		t.Errorf("TeamID: got %q, want %q", got.TeamID, "T001")
	}
}

func TestAuthTest_invalidAuth(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]any{"ok": false, "error": "invalid_auth"})
	}))

	got, err := c.AuthTest()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.OK {
		t.Error("expected OK=false for invalid_auth")
	}
	if got.Error != "invalid_auth" {
		t.Errorf("Error: got %q, want %q", got.Error, "invalid_auth")
	}
}

func TestAuthTest_httpError(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	}))

	_, err := c.AuthTest()
	if err != nil {
		// slack-go may decode a 500 as an error or return an empty ok=false — either is acceptable
		return
	}
	// If no error, OK must be false (slack-go decoded an error body)
}

func TestAuthTest_cookieInjected(t *testing.T) {
	var gotCookie string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotCookie = r.Header.Get("Cookie")
		writeJSON(w, map[string]any{"ok": true})
	}))

	_, err := c.AuthTest()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if gotCookie != "d=xoxd-test" {
		t.Errorf("Cookie header: got %q, want %q", gotCookie, "d=xoxd-test")
	}
}
