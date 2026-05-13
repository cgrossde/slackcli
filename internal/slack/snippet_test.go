// Package slack — snippet_test.go tests CreateSnippet and DeleteSnippet at the
// internal/slack layer using a fake HTTP server.
package slack

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	slackgo "github.com/slack-go/slack"
)

// ---------------------------------------------------------------------------
// CreateSnippet tests
// ---------------------------------------------------------------------------

func TestCreateSnippet_notAllowed(t *testing.T) {
	c := NewClient("xoxc-test", "xoxd-test")
	_, _, err := c.CreateSnippet(CreateSnippetParams{
		Channel: "CNOTALLOWD",
		Content: "hello",
	})
	if err == nil {
		t.Fatal("expected error for non-whitelisted channel, got nil")
	}
}

func TestCreateSnippet_ok(t *testing.T) {
	var capturedSnippetType string
	var capturedFilename string

	srv := snippetTestServer(t, map[string]http.HandlerFunc{
		"/files.getUploadURLExternal": func(w http.ResponseWriter, r *http.Request) {
			if err := r.ParseForm(); err != nil {
				t.Errorf("ParseForm: %v", err)
			}
			capturedSnippetType = r.FormValue("snippet_type")
			capturedFilename = r.FormValue("filename")
			// Return upload URL pointing back at our server.
			sendJSONReply(w, map[string]any{
				"ok":         true,
				"upload_url": "http://" + r.Host + "/upload",
				"file_id":    "F123",
			})
		},
		"/upload": func(w http.ResponseWriter, r *http.Request) {
			// Drain the body so the client doesn't get a connection reset.
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusOK)
		},
		"/files.completeUploadExternal": func(w http.ResponseWriter, r *http.Request) {
			sendJSONReply(w, map[string]any{
				"ok": true,
				"files": []map[string]any{
					{"id": "F123", "title": "My Snippet"},
				},
			})
		},
	})
	defer srv.Close()

	c := NewClient("xoxc-test", "xoxd-test",
		slackgo.OptionAPIURL(srv.URL+"/"),
	)
	fileID, title, err := c.CreateSnippet(CreateSnippetParams{
		Channel:  "C0B3PCPL0CF",
		Content:  "package main",
		Title:    "My Snippet",
		Filetype: "go",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fileID != "F123" {
		t.Errorf("fileID = %q, want F123", fileID)
	}
	if title != "My Snippet" {
		t.Errorf("title = %q, want \"My Snippet\"", title)
	}
	if capturedSnippetType != "go" {
		t.Errorf("snippet_type = %q, want \"go\"", capturedSnippetType)
	}
	if capturedFilename != "My Snippet.go" {
		t.Errorf("filename = %q, want \"My Snippet.go\"", capturedFilename)
	}
}

func TestCreateSnippet_defaultTitle(t *testing.T) {
	srv := snippetTestServer(t, map[string]http.HandlerFunc{
		"/files.getUploadURLExternal": func(w http.ResponseWriter, r *http.Request) {
			sendJSONReply(w, map[string]any{
				"ok":         true,
				"upload_url": "http://" + r.Host + "/upload",
				"file_id":    "F456",
			})
		},
		"/upload": func(w http.ResponseWriter, r *http.Request) {
			_, _ = io.Copy(io.Discard, r.Body)
			w.WriteHeader(http.StatusOK)
		},
		"/files.completeUploadExternal": func(w http.ResponseWriter, r *http.Request) {
			sendJSONReply(w, map[string]any{
				"ok": true,
				"files": []map[string]any{
					{"id": "F456", "title": "Untitled"},
				},
			})
		},
	})
	defer srv.Close()

	c := NewClient("xoxc-test", "xoxd-test",
		slackgo.OptionAPIURL(srv.URL+"/"),
	)
	_, title, err := c.CreateSnippet(CreateSnippetParams{
		Channel: "C0B3PCPL0CF",
		Content: "hello",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if title != "Untitled" {
		t.Errorf("title = %q, want \"Untitled\"", title)
	}
}

// ---------------------------------------------------------------------------
// DeleteSnippet tests
// ---------------------------------------------------------------------------

func TestDeleteSnippet_ok(t *testing.T) {
	srv := snippetTestServer(t, map[string]http.HandlerFunc{
		"/files.delete": func(w http.ResponseWriter, r *http.Request) {
			if err := r.ParseForm(); err != nil {
				t.Errorf("ParseForm: %v", err)
			}
			if got := r.FormValue("file"); got != "F999" {
				t.Errorf("file param = %q, want F999", got)
			}
			sendJSONReply(w, map[string]any{"ok": true})
		},
	})
	defer srv.Close()

	c := NewClient("xoxc-test", "xoxd-test",
		slackgo.OptionAPIURL(srv.URL+"/"),
	)
	if err := c.DeleteSnippet("F999"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestDeleteSnippet_apiError(t *testing.T) {
	srv := snippetTestServer(t, map[string]http.HandlerFunc{
		"/files.delete": func(w http.ResponseWriter, r *http.Request) {
			sendJSONReply(w, map[string]any{
				"ok":    false,
				"error": "cant_delete_file",
			})
		},
	})
	defer srv.Close()

	c := NewClient("xoxc-test", "xoxd-test",
		slackgo.OptionAPIURL(srv.URL+"/"),
	)
	err := c.DeleteSnippet("F999")
	if err == nil {
		t.Fatal("expected error for API failure, got nil")
	}
}

// ---------------------------------------------------------------------------
// deriveSnippetFilename tests
// ---------------------------------------------------------------------------

func TestDeriveSnippetFilename(t *testing.T) {
	cases := []struct {
		title, filetype, want string
	}{
		{"my snippet", "go", "my snippet.go"},
		{"my snippet.go", "go", "my snippet.go"}, // already has extension
		{"query", "sql", "query.sql"},
		{"readme", "markdown", "readme.md"},
		{"notes", "text", "notes"},
		{"notes", "", "notes"},
		{"notes", "auto", "notes"},
		{"", "go", "snippet.go"},
		{"path/sep", "go", "path-sep.go"},
	}
	for _, tc := range cases {
		got := deriveSnippetFilename(tc.title, tc.filetype)
		if got != tc.want {
			t.Errorf("deriveSnippetFilename(%q, %q) = %q, want %q",
				tc.title, tc.filetype, got, tc.want)
		}
	}
}

// snippetTestServer is a dedicated test server helper for snippet tests,
// distinct from sendTestServer to avoid conflicts when both run in the same package.
func snippetTestServer(t *testing.T, handlers map[string]http.HandlerFunc) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	for path, h := range handlers {
		mux.HandleFunc(path, h)
	}
	return httptest.NewServer(mux)
}

// Ensure encoding/json import is used (used by TestDeleteSnippet_apiError indirectly).
var _ = json.Marshal
