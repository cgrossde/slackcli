package cmd

import (
	"strings"
	"testing"

	"github.com/cgrossde/slackcli/internal/slack"
)

// TestChatsTypes verifies the type-flag to API-types mapping.
func TestChatsTypes(t *testing.T) {
	tests := []struct {
		input   string
		want    []string
		wantErr bool
	}{
		{"all", []string{"im", "mpim"}, false},
		{"", []string{"im", "mpim"}, false},
		{"dm", []string{"im"}, false},
		{"im", []string{"im"}, false},
		{"mpdm", []string{"mpim"}, false},
		{"mpim", []string{"mpim"}, false},
		// channel modes are routed to chatsFetchWithChannels, not chatsTypes
		{"channel", nil, true},
		{"all-with-channels", nil, true},
		{"unread", nil, true},
		{"bad", nil, true},
	}
	for _, tc := range tests {
		got, err := chatsTypes(tc.input)
		if tc.wantErr {
			if err == nil {
				t.Errorf("chatsTypes(%q): expected error, got nil", tc.input)
			}
			continue
		}
		if err != nil {
			t.Errorf("chatsTypes(%q): unexpected error: %v", tc.input, err)
			continue
		}
		if len(got) != len(tc.want) {
			t.Errorf("chatsTypes(%q) = %v, want %v", tc.input, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("chatsTypes(%q)[%d] = %q, want %q", tc.input, i, got[i], tc.want[i])
			}
		}
	}
}

// TestResolveMpdmName verifies MPDM name resolution: cache hit, cache miss
// (no enterprise ID → raw handle fallback), and the member-IDs path.
func TestResolveMpdmName(t *testing.T) {
	cache := slack.NewUserCacheFromMap("test.slack.com", map[string]slack.CachedUser{
		"W1": {ID: "W1", Name: "u100001", DisplayName: "Alice Example"},
		"W2": {ID: "W2", Name: "u100002", DisplayName: "Bob Example"},
		"U3": {ID: "U3", Name: "u100003", DisplayName: "Carol Example"},
	})

	tests := []struct {
		desc    string
		rawName string
		members []string
		cache   *slack.UserCache
		want    string
	}{
		// No cache → raw handles, no resolution attempted.
		{
			desc:    "no cache, raw handles",
			rawName: "mpdm-alice--bob--carol-1",
			want:    "alice, bob, carol",
		},
		{
			desc:    "no cache, employee-ID handles",
			rawName: "mpdm-u100001--u100002-1",
			want:    "u100001, u100002",
		},
		// Cache present, handles known → display names.
		{
			desc:    "all handles in cache",
			rawName: "mpdm-u100001--u100002--u100003-1", cache: cache,
			want: "Alice Example, Bob Example, Carol Example",
		},
		// Cache present, one handle unknown and no enterpriseID → raw fallback.
		{
			desc:    "one handle unknown, no enterpriseID → raw",
			rawName: "mpdm-u100001--unknown-1", cache: cache,
			want: "Alice Example, unknown",
		},
		// member IDs path (Conversation.Members populated) always resolves via GetUser.
		{
			desc:    "member IDs path",
			rawName: "", members: []string{"W1", "W2"}, cache: cache,
			want: "@Alice Example, @Bob Example",
		},
	}

	for _, tc := range tests {
		t.Run(tc.desc, func(t *testing.T) {
			got := resolveMpdmName(tc.rawName, tc.members, tc.cache, "")
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// TestBuildChatEntries verifies that buildChatEntries preserves the order of
// its input slice and does NOT sort (sorting is the caller's responsibility).
func TestBuildChatEntries(t *testing.T) {
	convs := []slack.Conversation{
		{ID: "D002", IsIM: true, User: "U002", LatestTs: "1780000000.000000"},
		{ID: "C003", IsMpIM: true, Name: "mpdm-a--b-1", LatestTs: "1500000000.000000"},
		{ID: "D001", IsIM: true, User: "U001", LatestTs: "1000000000.000000"},
		{ID: "D004", IsIM: true, User: "U004", LatestTs: ""}, // no messages
	}
	entries := buildChatEntries(convs, nil, "")

	if len(entries) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(entries))
	}
	if entries[0].ID != "D002" {
		t.Errorf("entries[0].ID = %q, want D002 (input order preserved)", entries[0].ID)
	}
	if entries[1].ID != "C003" {
		t.Errorf("entries[1].ID = %q, want C003 (input order preserved)", entries[1].ID)
	}
	if entries[2].ID != "D001" {
		t.Errorf("entries[2].ID = %q, want D001 (input order preserved)", entries[2].ID)
	}
	if entries[3].ID != "D004" {
		t.Errorf("entries[3].ID = %q, want D004 (input order preserved)", entries[3].ID)
	}
}

// TestFormatChatsPlain verifies plain output contains IDs and types.
func TestFormatChatsPlain(t *testing.T) {
	entries := []chatEntry{
		{ID: "D001", Type: "dm", Name: "@Alice", LatestTs: "1780000000.000000", LatestHuman: "2026-05-30 10:00"},
		{ID: "C002", Type: "mpdm", Name: "alice, bob", LatestTs: "1700000000.000000", LatestHuman: "2023-11-15 06:13"},
	}
	result := slack.ConversationListResult{HasMore: false}
	out := formatChatsPlain(entries, result)

	for _, want := range []string{"D001", "dm", "@Alice", "C002", "mpdm", "alice, bob", "2 chats"} {
		if !strings.Contains(out, want) {
			t.Errorf("formatChatsPlain output missing %q\noutput:\n%s", want, out)
		}
	}
}

// Note: command-layer tests for bad --type and --json footer suppression live in
// main_test.go (TestRun_chats_badType, TestRun_chats_jsonFlagSuppressesFooter)
// because they require buildRoot from the main package.
