// Package slack — channels_search_test.go tests SearchChannels using an
// httptest TLS server so no real credentials or network access is needed.
package slack

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// channelsTestServer starts an httptest TLS Server. The returned workspace
// string is the host:port suitable for passing to SearchChannels; the TLS
// client from srv.Client() is wired into the test Client so certificate
// verification succeeds.
func channelsTestServer(t *testing.T, handler http.Handler) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewTLSServer(handler)
	t.Cleanup(srv.Close)
	// workspace = host:port (without scheme); SearchChannels prepends "https://".
	workspace := strings.TrimPrefix(srv.URL, "https://")
	return srv, workspace
}

// newChannelsClient creates a Client whose httpClient is the TLS-configured
// client from the test server so certificate verification passes.
func newChannelsClient(srv *httptest.Server) *Client {
	return &Client{
		token:      "xoxc-test",
		cookie:     "xoxd-test",
		httpClient: srv.Client(),
	}
}

func TestSearchChannels_ok(t *testing.T) {
	want := channelsSearchResponse{
		OK: true,
		Items: []channelsSearchItem{
			{
				ID:         "C001",
				Name:       "general",
				Topic:      channelsSearchText{Value: "General discussion"},
				Purpose:    channelsSearchText{Value: "Everything"},
				NumMembers: 120,
				IsPrivate:  false,
				IsArchived: false,
			},
			{
				ID:         "C002",
				Name:       "general-ops",
				Topic:      channelsSearchText{Value: "Ops stuff"},
				Purpose:    channelsSearchText{Value: "Operations"},
				NumMembers: 30,
				IsPrivate:  true,
				IsArchived: false,
			},
		},
	}

	srv, workspace := channelsTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/api/search.modules.channels" {
			t.Errorf("unexpected path: %s", r.URL.Path)
		}
		if ct := r.Header.Get("Content-Type"); ct != "application/x-www-form-urlencoded" {
			t.Errorf("expected Content-Type application/x-www-form-urlencoded, got %q", ct)
		}
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if got := r.FormValue("module"); got != "channels" {
			t.Errorf("expected module=channels, got %q", got)
		}
		if got := r.FormValue("query"); got != "general" {
			t.Errorf("expected query=general, got %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(want)
	}))

	c := newChannelsClient(srv)
	results, err := c.SearchChannels(context.Background(), workspace, "general", 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected 2 results, got %d", len(results))
	}

	r0 := results[0]
	if r0.ID != "C001" {
		t.Errorf("expected ID C001, got %q", r0.ID)
	}
	if r0.Name != "general" {
		t.Errorf("expected name general, got %q", r0.Name)
	}
	if r0.Topic != "General discussion" {
		t.Errorf("expected topic, got %q", r0.Topic)
	}
	if r0.MemberCount != 120 {
		t.Errorf("expected 120 members, got %d", r0.MemberCount)
	}
}

func TestSearchChannels_apiError(t *testing.T) {
	srv, workspace := channelsTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(channelsSearchResponse{OK: false, Error: "invalid_auth"})
	}))

	c := newChannelsClient(srv)
	_, err := c.SearchChannels(context.Background(), workspace, "general", 0)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "invalid_auth") {
		t.Errorf("expected invalid_auth in error, got %q", err.Error())
	}
}

func TestSearchChannels_httpError(t *testing.T) {
	srv, workspace := channelsTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal error"))
	}))

	c := newChannelsClient(srv)
	_, err := c.SearchChannels(context.Background(), workspace, "general", 5)
	if err == nil {
		t.Fatal("expected error for HTTP 500")
	}
	if !strings.Contains(err.Error(), "HTTP 500") {
		t.Errorf("expected HTTP 500 in error, got %q", err.Error())
	}
}

func TestSearchChannels_defaultCount(t *testing.T) {
	var gotCount string
	srv, workspace := channelsTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		gotCount = r.FormValue("count")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(channelsSearchResponse{OK: true})
	}))

	c := newChannelsClient(srv)
	_, _ = c.SearchChannels(context.Background(), workspace, "q", 0)
	if gotCount != "20" {
		t.Errorf("expected count=20 for zero input, got %q", gotCount)
	}
}

func TestSearchChannels_emptyResults(t *testing.T) {
	srv, workspace := channelsTestServer(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(channelsSearchResponse{OK: true, Items: nil})
	}))

	c := newChannelsClient(srv)
	results, err := c.SearchChannels(context.Background(), workspace, "zzznomatch", 5)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("expected 0 results, got %d", len(results))
	}
}
