package slack

import (
	"testing"
)

// TestFromSearchRawMessage verifies that fromSearchRawMessage correctly maps
// all fields from a searchRawMessage to our SearchMatch type.
func TestFromSearchRawMessage(t *testing.T) {
	input := searchRawMessage{
		Channel: searchRawChannel{
			ID:     "C012ABC3456",
			Name:   "ops",
			IsMPIM: false,
		},
		User:     "UABC123DEF",
		Username: "alice",
		Ts:       "1718200320.123456",
		Text:     "We rolled back the deployment.",
	}

	got := fromSearchRawMessage(input)

	if got.ChannelID != "C012ABC3456" {
		t.Errorf("ChannelID: got %q, want %q", got.ChannelID, "C012ABC3456")
	}
	if got.ChannelName != "ops" {
		t.Errorf("ChannelName: got %q, want %q", got.ChannelName, "ops")
	}
	if got.IsMPIM {
		t.Errorf("IsMPIM: got true, want false")
	}
	if got.UserID != "UABC123DEF" {
		t.Errorf("UserID: got %q, want %q", got.UserID, "UABC123DEF")
	}
	if got.Username != "alice" {
		t.Errorf("Username: got %q, want %q", got.Username, "alice")
	}
	if got.Ts != "1718200320.123456" {
		t.Errorf("Ts: got %q, want %q", got.Ts, "1718200320.123456")
	}
	if got.ThreadTs != "" {
		t.Errorf("ThreadTs: got %q, want empty for top-level message", got.ThreadTs)
	}
	if got.Text != "We rolled back the deployment." {
		t.Errorf("Text: got %q, want %q", got.Text, "We rolled back the deployment.")
	}
}

// TestFromSearchRawMessage_threadReply verifies ThreadTs is populated for
// thread replies — via direct field (rare) and via permalink query param (normal).
func TestFromSearchRawMessage_threadReply(t *testing.T) {
	// Direct field (API may return it in future).
	direct := searchRawMessage{
		Channel:  searchRawChannel{ID: "C012ABC3456", Name: "ops"},
		User:     "UABC123DEF",
		Ts:       "1718200400.000001",
		ThreadTs: "1718200320.123456",
		Text:     "agreed",
	}
	got := fromSearchRawMessage(direct)
	if got.ThreadTs != "1718200320.123456" {
		t.Errorf("direct: ThreadTs: got %q, want %q", got.ThreadTs, "1718200320.123456")
	}

	// Permalink extraction (the normal case — API encodes it in the URL).
	viaPermalink := searchRawMessage{
		Channel:   searchRawChannel{ID: "C012ABC3456", Name: "ops"},
		User:      "UABC123DEF",
		Ts:        "1718200400.000001",
		Permalink: "https://myorg.slack.com/archives/C012ABC3456/p1718200400000001?thread_ts=1718200320.123456&cid=C012ABC3456",
		Text:      "agreed",
	}
	got2 := fromSearchRawMessage(viaPermalink)
	if got2.ThreadTs != "1718200320.123456" {
		t.Errorf("permalink: ThreadTs: got %q, want %q", got2.ThreadTs, "1718200320.123456")
	}

	// Thread root: thread_ts == ts → should be treated as top-level.
	root := searchRawMessage{
		Channel:   searchRawChannel{ID: "C012ABC3456", Name: "ops"},
		User:      "UABC123DEF",
		Ts:        "1718200320.123456",
		Permalink: "https://myorg.slack.com/archives/C012ABC3456/p1718200320123456?thread_ts=1718200320.123456",
		Text:      "root message",
	}
	got3 := fromSearchRawMessage(root)
	if got3.ThreadTs != "" {
		t.Errorf("root: ThreadTs should be empty, got %q", got3.ThreadTs)
	}
}

// TestSearchResult_zeroTotal verifies the zero-total path compiles and returns
// the expected shape.
func TestSearchResult_zeroTotal(t *testing.T) {
	r := SearchResult{
		Query:   "nonexistent",
		Total:   0,
		Page:    1,
		Pages:   0,
		Count:   20,
		Matches: nil,
	}
	if r.Total != 0 {
		t.Errorf("expected Total=0, got %d", r.Total)
	}
	if len(r.Matches) != 0 {
		t.Errorf("expected empty matches")
	}
}

// ---------------------------------------------------------------------------
// parseMPDMParticipants
// ---------------------------------------------------------------------------

func TestParseMPDMParticipants(t *testing.T) {
	cases := []struct {
		input string
		want  []string
	}{
		{"mpdm-u123456--u345678--u567890-1", []string{"u123456", "u345678", "u567890"}},
		{"mpdm-u345678--u456789--u123456--u678901--u789013-1", []string{"u345678", "u456789", "u123456", "u678901", "u789013"}},
		{"mpdm-alice--bob-1", []string{"alice", "bob"}},
		{"mpdm-solo-1", []string{"solo"}},
		{"not-an-mpdm", nil},
		{"", nil},
	}
	for _, tc := range cases {
		got := parseMPDMParticipants(tc.input)
		if len(got) != len(tc.want) {
			t.Errorf("parseMPDMParticipants(%q): got %v, want %v", tc.input, got, tc.want)
			continue
		}
		for i := range tc.want {
			if got[i] != tc.want[i] {
				t.Errorf("parseMPDMParticipants(%q)[%d]: got %q, want %q", tc.input, i, got[i], tc.want[i])
			}
		}
	}
}

// TestFromSearchRawMessage_DM verifies DMPeerID is populated for 1:1 DMs.
func TestFromSearchRawMessage_DM(t *testing.T) {
	input := searchRawMessage{
		Channel: searchRawChannel{
			ID:   "D0B22865CQ4",
			Name: "W4UDRQJNR",
		},
		User: "UABC123",
		Ts:   "1718200320.123456",
		Text: "hey",
	}
	got := fromSearchRawMessage(input)
	if got.DMPeerID != "W4UDRQJNR" {
		t.Errorf("DMPeerID: got %q, want %q", got.DMPeerID, "W4UDRQJNR")
	}
	if len(got.ParticipantIDs) != 0 {
		t.Errorf("ParticipantIDs should be empty for 1:1 DM, got %v", got.ParticipantIDs)
	}
}

// TestFromSearchRawMessage_MPIM verifies ParticipantIDs are parsed for MPDMs.
func TestFromSearchRawMessage_MPIM(t *testing.T) {
	input := searchRawMessage{
		Channel: searchRawChannel{
			ID:     "C0B3CH1GCNP",
			Name:   "mpdm-u123456--u345678--u567890-1",
			IsMPIM: true,
		},
		User: "WH1K7QTFU",
		Ts:   "1718200320.123456",
		Text: "hey",
	}
	got := fromSearchRawMessage(input)
	if got.DMPeerID != "" {
		t.Errorf("DMPeerID should be empty for MPIM, got %q", got.DMPeerID)
	}
	want := []string{"u123456", "u345678", "u567890"}
	if len(got.ParticipantIDs) != len(want) {
		t.Fatalf("ParticipantIDs: got %v, want %v", got.ParticipantIDs, want)
	}
	for i, id := range want {
		if got.ParticipantIDs[i] != id {
			t.Errorf("ParticipantIDs[%d]: got %q, want %q", i, got.ParticipantIDs[i], id)
		}
	}
}

// TestFromSearchRawMessage_WorkspaceFromPermalink verifies that Workspace is
// populated from the permalink host.
func TestFromSearchRawMessage_WorkspaceFromPermalink(t *testing.T) {
	input := searchRawMessage{
		Channel:   searchRawChannel{ID: "C012ABC", Name: "ops"},
		User:      "UABC123",
		Ts:        "1718200320.123456",
		Permalink: "https://myorg.slack.com/archives/C012ABC/p1718200320123456",
		Text:      "hello",
	}
	got := fromSearchRawMessage(input)
	if got.Workspace != "myorg.slack.com" {
		t.Errorf("Workspace: got %q, want %q", got.Workspace, "myorg.slack.com")
	}
}

// TestFromSearchRawMessage_WorkspaceEmptyWithoutPermalink verifies that
// Workspace is empty when no permalink is present.
func TestFromSearchRawMessage_WorkspaceEmptyWithoutPermalink(t *testing.T) {
	input := searchRawMessage{
		Channel: searchRawChannel{ID: "C012ABC", Name: "ops"},
		User:    "UABC123",
		Ts:      "1718200320.123456",
		Text:    "hello",
	}
	got := fromSearchRawMessage(input)
	if got.Workspace != "" {
		t.Errorf("Workspace should be empty without permalink, got %q", got.Workspace)
	}
}
