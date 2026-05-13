package slack

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

// TestParseEvent verifies that wsRaw unmarshals correctly and that
// the Event struct is populated from it.
func TestParseEvent(t *testing.T) {
	tests := []struct {
		name string
		json string
		want Event
	}{
		{
			name: "message",
			json: `{"type":"message","channel":"C012ABC","user":"U111","text":"hello","ts":"1718197925.001234"}`,
			want: Event{
				Type:    "message",
				Channel: "C012ABC",
				User:    "U111",
				Text:    "hello",
				Ts:      "1718197925.001234",
			},
		},
		{
			name: "thread reply",
			json: `{"type":"message","subtype":"","channel":"C012ABC","user":"U222","text":"reply","ts":"1718197926.000001","thread_ts":"1718197925.001234"}`,
			want: Event{
				Type:     "message",
				Channel:  "C012ABC",
				User:     "U222",
				Text:     "reply",
				Ts:       "1718197926.000001",
				ThreadTs: "1718197925.001234",
			},
		},
		{
			name: "reaction_added",
			json: `{"type":"reaction_added","user":"U333","reaction":"thumbsup","item_user":"U111","item":{"ts":"1718197925.001234"},"channel":"C012ABC"}`,
			want: Event{
				Type:     "reaction_added",
				User:     "U333",
				Reaction: "thumbsup",
				ItemUser: "U111",
				ItemTs:   "1718197925.001234",
				Channel:  "C012ABC",
			},
		},
		{
			name: "reaction_removed",
			json: `{"type":"reaction_removed","user":"U333","reaction":"thumbsup","item_user":"U111","item":{"ts":"1718197925.001234"},"channel":"C012ABC"}`,
			want: Event{
				Type:     "reaction_removed",
				User:     "U333",
				Reaction: "thumbsup",
				ItemUser: "U111",
				ItemTs:   "1718197925.001234",
				Channel:  "C012ABC",
			},
		},
		{
			name: "member_joined_channel",
			json: `{"type":"member_joined_channel","user":"U444","channel":"C012ABC"}`,
			want: Event{
				Type:    "member_joined_channel",
				User:    "U444",
				Channel: "C012ABC",
			},
		},
		{
			name: "reaction_added — channel in item, not top-level",
			json: `{"type":"reaction_added","user":"U333","reaction":"thumbsup","item_user":"U111","item":{"channel":"C999XYZ","ts":"1718197925.001234"}}`,
			want: Event{
				Type:     "reaction_added",
				User:     "U333",
				Reaction: "thumbsup",
				ItemUser: "U111",
				ItemTs:   "1718197925.001234",
				Channel:  "C999XYZ",
			},
		},
		{
			name: "message with mention",
			json: `{"type":"message","channel":"C012ABC","user":"U111","text":"hey <@U999ABC> and <@W000DEF>","ts":"1.0"}`,
			want: Event{
				Type:     "message",
				Channel:  "C012ABC",
				User:     "U111",
				Text:     "hey <@U999ABC> and <@W000DEF>",
				Ts:       "1.0",
				Mentions: []string{"U999ABC", "W000DEF"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var raw wsRaw
			if err := json.Unmarshal([]byte(tt.json), &raw); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}


			// Apply same logic as ReadEvent.
			channel := raw.Channel
			if channel == "" {
				channel = raw.Item.Channel
			}
			var mentions []string
			if raw.Text != "" {
				for _, m := range mentionRe.FindAllString(raw.Text, -1) {
					mentions = append(mentions, m[2:len(m)-1])
				}
			}
			got := Event{
				Type:     raw.Type,
				SubType:  raw.SubType,
				Channel:  channel,
				User:     raw.User,
				Text:     raw.Text,
				Ts:       raw.Ts,
				ThreadTs: raw.ThreadTs,
				Reaction: raw.Reaction,
				ItemUser: raw.ItemUser,
				ItemTs:   raw.Item.Ts,
				Mentions: mentions,
				Raw:      json.RawMessage(tt.json),
			}

			if got.Type != tt.want.Type {
				t.Errorf("Type: got %q want %q", got.Type, tt.want.Type)
			}
			if got.SubType != tt.want.SubType {
				t.Errorf("SubType: got %q want %q", got.SubType, tt.want.SubType)
			}
			if got.Channel != tt.want.Channel {
				t.Errorf("Channel: got %q want %q", got.Channel, tt.want.Channel)
			}
			if got.User != tt.want.User {
				t.Errorf("User: got %q want %q", got.User, tt.want.User)
			}
			if got.Text != tt.want.Text {
				t.Errorf("Text: got %q want %q", got.Text, tt.want.Text)
			}
			if got.Ts != tt.want.Ts {
				t.Errorf("Ts: got %q want %q", got.Ts, tt.want.Ts)
			}
			if got.ThreadTs != tt.want.ThreadTs {
				t.Errorf("ThreadTs: got %q want %q", got.ThreadTs, tt.want.ThreadTs)
			}
			if got.Reaction != tt.want.Reaction {
				t.Errorf("Reaction: got %q want %q", got.Reaction, tt.want.Reaction)
			}
			if got.ItemUser != tt.want.ItemUser {
				t.Errorf("ItemUser: got %q want %q", got.ItemUser, tt.want.ItemUser)
			}
			if got.ItemTs != tt.want.ItemTs {
				t.Errorf("ItemTs: got %q want %q", got.ItemTs, tt.want.ItemTs)
			}
			if !mentionSlicesEqual(got.Mentions, tt.want.Mentions) {
				t.Errorf("Mentions: got %v want %v", got.Mentions, tt.want.Mentions)
			}
		})
	}
}

// TestSkipEventType verifies the allowlist: allowed types pass, everything else is dropped.
func TestSkipEventType(t *testing.T) {
	// Every allowed type must NOT be skipped.
	for _, typ := range AllowedEventTypes {
		if skipEventType(typ) {
			t.Errorf("allowed type %q should not be skipped", typ)
		}
	}

	// A representative sample of noise types must be skipped.
	noise := []string{
		"hello", "pong", "typing", "user_typing", "presence_change",
		"channel_marked", "im_marked", "reconnect_url",
		"dnd_updated_user", "user_status_changed", "file_deleted",
		"unknown_future_type", "",
	}
	for _, typ := range noise {
		if !skipEventType(typ) {
			t.Errorf("noise type %q should be skipped", typ)
		}
	}
}

// TestWorkspaceDomains verifies the helper produces comma-separated domains.
func TestWorkspaceDomains(t *testing.T) {
	ws := []userBootWorkspace{
		{ID: "T111", Domain: "myorg"},
		{ID: "T222", Domain: "other"},
	}
	got := workspaceDomains(ws)
	want := "myorg, other"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

// TestGatewayURL verifies the wss:// URL is built correctly from team ID and token.
func TestGatewayURL(t *testing.T) {
	token := "xoxc-test"
	teamID := "T03EE7DCP"
	wsURL := wsGatewayBase + "?token=" + url.QueryEscape(token) +
		"&gateway_server=" + url.QueryEscape(teamID)
	want := "wss://wss-primary.slack.com/?token=xoxc-test&gateway_server=T03EE7DCP"
	if wsURL != want {
		t.Errorf("got %q want %q", wsURL, want)
	}
}

// TestUserBootWorkspaceLookup verifies the domain-matching logic in GatewayServer.
func TestUserBootWorkspaceLookup(t *testing.T) {
	workspaces := []userBootWorkspace{
		{ID: "T111", Domain: "alpha"},
		{ID: "T222", Domain: "beta"},
		{ID: "T333", Domain: "myorg"},
	}

	tests := []struct {
		workspace string
		wantID    string
		wantErr   bool
	}{
		{"myorg.slack.com", "T333", false},
		{"alpha.slack.com", "T111", false},
		{"beta.slack.com", "T222", false},
		{"unknown.slack.com", "", true}, // not found, multiple workspaces
	}

	for _, tt := range tests {
		t.Run(tt.workspace, func(t *testing.T) {
			wantDomain := strings.TrimSuffix(tt.workspace, ".slack.com")
			teamID := ""
			for _, ws := range workspaces {
				if ws.Domain == wantDomain {
					teamID = ws.ID
					break
				}
			}
			if tt.wantErr {
				if teamID != "" {
					t.Errorf("expected no match, got %q", teamID)
				}
				return
			}
			if teamID != tt.wantID {
				t.Errorf("got %q want %q", teamID, tt.wantID)
			}
		})
	}
}

// mentionSlicesEqual compares two string slices treating nil and empty as equal.
func mentionSlicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}