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
		// channel modes are handled by chatsFetchWithChannels, not chatsTypes
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

// TestResolveMpdmName verifies MPDM name parsing from raw Slack name.
func TestResolveMpdmName(t *testing.T) {
	tests := []struct {
		rawName string
		want    string
	}{
		{"mpdm-alice--bob--carol-1", "alice, bob, carol"},
		{"mpdm-d072584--d070402-1", "d072584, d070402"},
		{"mpdm-gregor.hollmig--d070465--d070402-1", "gregor.hollmig, d070465, d070402"},
	}
	for _, tc := range tests {
		got := resolveMpdmName(tc.rawName, nil, nil)
		if got != tc.want {
			t.Errorf("resolveMpdmName(%q) = %q, want %q", tc.rawName, got, tc.want)
		}
	}
}

// TestBuildChatEntries verifies sorting by latest_ts descending.
func TestBuildChatEntries(t *testing.T) {
	convs := []slack.Conversation{
		{ID: "D001", IsIM: true, User: "U001", LatestTs: "1000000000.000000"},
		{ID: "D002", IsIM: true, User: "U002", LatestTs: "1780000000.000000"},
		{ID: "C003", IsMpIM: true, Name: "mpdm-a--b-1", LatestTs: "1500000000.000000"},
		{ID: "D004", IsIM: true, User: "U004", LatestTs: ""}, // no messages
	}
	entries := buildChatEntries(convs, nil)

	if len(entries) != 4 {
		t.Fatalf("expected 4 entries, got %d", len(entries))
	}
	// Most recent first.
	if entries[0].ID != "D002" {
		t.Errorf("entries[0].ID = %q, want D002", entries[0].ID)
	}
	if entries[1].ID != "C003" {
		t.Errorf("entries[1].ID = %q, want C003", entries[1].ID)
	}
	if entries[2].ID != "D001" {
		t.Errorf("entries[2].ID = %q, want D001", entries[2].ID)
	}
	// No-ts entry sorts to bottom.
	if entries[3].ID != "D004" {
		t.Errorf("entries[3].ID = %q, want D004 (no ts)", entries[3].ID)
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

// TestChatsCmd_badType verifies the command returns an error for unknown --type.
func TestChatsCmd_badType(t *testing.T) {
	_, err := chatsTypes("invalid")
	if err == nil {
		t.Fatal("expected error for unknown type, got nil")
	}
	if !strings.Contains(err.Error(), "invalid") {
		t.Errorf("error should mention the bad value: %v", err)
	}
}
