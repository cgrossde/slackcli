// Package cmd — search_channels_test.go tests the --channels mode and the
// resolveChannelName helper. The tests use an httptest TLS server to avoid
// real network or keychain access.
package cmd

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cgrossde/slackcli/internal/slack"
)

// channelsCmdTestServer starts a TLS server that responds to
// /api/search.modules.channels with the given items.
func channelsCmdTestServer(t *testing.T, items []slack.ChannelResult) (*httptest.Server, string) {
	t.Helper()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/search.modules.channels" {
			http.NotFound(w, r)
			return
		}
		_ = r.ParseForm()
		type responseItem struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			Topic      struct{ Value string `json:"value"` } `json:"topic"`
			Purpose    struct{ Value string `json:"value"` } `json:"purpose"`
			NumMembers int    `json:"num_members"`
			IsArchived bool   `json:"is_archived"`
		}
		var respItems []responseItem
		for _, item := range items {
			ri := responseItem{
				ID:         item.ID,
				Name:       item.Name,
				NumMembers: item.MemberCount,
				IsArchived: item.IsArchived,
			}
			ri.Topic.Value = item.Topic
			ri.Purpose.Value = item.Purpose
			respItems = append(respItems, ri)
		}
		resp := map[string]any{"ok": true, "items": respItems}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	}))
	t.Cleanup(srv.Close)
	workspace := strings.TrimPrefix(srv.URL, "https://")
	return srv, workspace
}

// newCmdChannelsClient creates a *slack.Client whose httpClient uses the TLS
// test server's certificate.
func newCmdChannelsClient(srv *httptest.Server) *slack.Client {
	return slack.NewClientWithHTTP("xoxc-test", "xoxd-test", srv.Client())
}

// ---------------------------------------------------------------------------
// resolveChannelName
// ---------------------------------------------------------------------------

func TestResolveChannelName_exactMatch(t *testing.T) {
	srv, workspace := channelsCmdTestServer(t, []slack.ChannelResult{
		{ID: "C001", Name: "general"},
		{ID: "C002", Name: "general-ops"},
	})
	c := newCmdChannelsClient(srv)

	id, err := resolveChannelName(c, workspace, "general")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "C001" {
		t.Errorf("expected C001, got %q", id)
	}
}

func TestResolveChannelName_stripsHash(t *testing.T) {
	srv, workspace := channelsCmdTestServer(t, []slack.ChannelResult{
		{ID: "C003", Name: "ops"},
	})
	c := newCmdChannelsClient(srv)

	id, err := resolveChannelName(c, workspace, "#ops")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "C003" {
		t.Errorf("expected C003, got %q", id)
	}
}

func TestResolveChannelName_caseInsensitive(t *testing.T) {
	srv, workspace := channelsCmdTestServer(t, []slack.ChannelResult{
		{ID: "C004", Name: "DevOps"},
	})
	c := newCmdChannelsClient(srv)

	id, err := resolveChannelName(c, workspace, "devops")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if id != "C004" {
		t.Errorf("expected C004, got %q", id)
	}
}

func TestResolveChannelName_noMatch(t *testing.T) {
	srv, workspace := channelsCmdTestServer(t, []slack.ChannelResult{})
	c := newCmdChannelsClient(srv)

	_, err := resolveChannelName(c, workspace, "zzznomatch")
	if err == nil {
		t.Fatal("expected error for no match, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got %q", err.Error())
	}
}

func TestResolveChannelName_suggestionsOnMiss(t *testing.T) {
	srv, workspace := channelsCmdTestServer(t, []slack.ChannelResult{
		{ID: "C005", Name: "general-announcements"},
		{ID: "C006", Name: "general-random"},
	})
	c := newCmdChannelsClient(srv)

	_, err := resolveChannelName(c, workspace, "general")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !strings.Contains(err.Error(), "general-announcements") {
		t.Errorf("expected suggestion in error, got %q", err.Error())
	}
}

// ---------------------------------------------------------------------------
// formatChannelResults
// ---------------------------------------------------------------------------

func TestFormatChannelResults_empty(t *testing.T) {
	out := formatChannelResults("foo", nil)
	if !strings.Contains(out, "no channels") {
		t.Errorf("expected 'no channels', got %q", out)
	}
}

func TestFormatChannelResults_basic(t *testing.T) {
	results := []slack.ChannelResult{
		{ID: "C001", Name: "general", Topic: "discuss", MemberCount: 50},
		{ID: "C002", Name: "secret", MemberCount: 5, IsArchived: true},
	}
	out := formatChannelResults("gen", results)
	if !strings.Contains(out, "#general") {
		t.Errorf("expected #general in output, got %q", out)
	}
	if !strings.Contains(out, "C001") {
		t.Errorf("expected C001 in output, got %q", out)
	}
	if !strings.Contains(out, "50 members") {
		t.Errorf("expected 50 members in output, got %q", out)
	}
	if !strings.Contains(out, "Topic: discuss") {
		t.Errorf("expected topic in output, got %q", out)
	}
	if !strings.Contains(out, "[archived]") {
		t.Errorf("expected [archived] in output, got %q", out)
	}
}

// ---------------------------------------------------------------------------
// formatChannelResultsJSON
// ---------------------------------------------------------------------------

func TestFormatChannelResultsJSON_empty(t *testing.T) {
	out := formatChannelResultsJSON(nil)
	if out != "" {
		t.Errorf("expected empty output for nil, got %q", out)
	}
}

func TestFormatChannelResultsJSON_fields(t *testing.T) {
	results := []slack.ChannelResult{
		{ID: "C001", Name: "general", Topic: "t", Purpose: "p", MemberCount: 10, IsArchived: false},
	}
	out := formatChannelResultsJSON(results)
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected 1 JSON line, got %d", len(lines))
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &m); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if m["id"] != "C001" {
		t.Errorf("expected id=C001, got %v", m["id"])
	}
	if m["name"] != "general" {
		t.Errorf("expected name=general, got %v", m["name"])
	}
	if m["member_count"] != float64(10) {
		t.Errorf("expected member_count=10, got %v", m["member_count"])
	}
}
