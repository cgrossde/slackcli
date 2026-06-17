package slack

import "testing"

func TestDeepLinkChannel(t *testing.T) {
	tests := []struct {
		name, team, channel, want string
		wantErr                    bool
	}{
		{"channel", "T03EE7DCP", "C0B3PCPL0CF", "slack://channel?team=T03EE7DCP&id=C0B3PCPL0CF", false},
		{"DM uses channel form", "T03EE7DCP", "D09SD70E1HU", "slack://channel?team=T03EE7DCP&id=D09SD70E1HU", false},
		{"MPDM uses channel form", "T03EE7DCP", "G0123456789", "slack://channel?team=T03EE7DCP&id=G0123456789", false},
		{"empty team", "", "C123", "", true},
		{"empty channel", "T03EE7DCP", "", "", true},
		{"team must start with T or E", "X123", "C123", "", true},
		{"enterprise team accepted", "E7RBBBXHB", "C123", "slack://channel?team=E7RBBBXHB&id=C123", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DeepLinkChannel(tc.team, tc.channel)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDeepLinkMessage(t *testing.T) {
	tests := []struct {
		name, team, channel, ts, threadTs, want string
		wantErr                                  bool
	}{
		{
			name:    "message in channel",
			team:    "T03EE7DCP",
			channel: "C0B3PCPL0CF",
			ts:      "1781608222.892579",
			want:    "slack://channel?team=T03EE7DCP&id=C0B3PCPL0CF&message=1781608222.892579",
		},
		{
			name:     "thread reply",
			team:     "T03EE7DCP",
			channel:  "C0B3PCPL0CF",
			ts:       "1781608225.037709",
			threadTs: "1781608222.892579",
			want:     "slack://channel?team=T03EE7DCP&id=C0B3PCPL0CF&message=1781608225.037709&thread_ts=1781608222.892579",
		},
		{
			name:     "thread root: thread_ts equal to ts is omitted",
			team:     "T03EE7DCP",
			channel:  "C0B3PCPL0CF",
			ts:       "1781608222.892579",
			threadTs: "1781608222.892579",
			want:     "slack://channel?team=T03EE7DCP&id=C0B3PCPL0CF&message=1781608222.892579",
		},
		{
			name: "p-form rejected — Slack expects dotted form here",
			team: "T03EE7DCP", channel: "C123", ts: "1718197925001234",
			wantErr: true,
		},
		{
			name: "missing ts",
			team: "T03EE7DCP", channel: "C123",
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := DeepLinkMessage(tc.team, tc.channel, tc.ts, tc.threadTs)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDeepLinkFile(t *testing.T) {
	got, err := DeepLinkFile("T03EE7DCP", "F0B3HRU6ZA7")
	if err != nil {
		t.Fatal(err)
	}
	want := "slack://file?team=T03EE7DCP&id=F0B3HRU6ZA7"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestDeepLinkWorkspace(t *testing.T) {
	got, err := DeepLinkWorkspace("T03EE7DCP")
	if err != nil {
		t.Fatal(err)
	}
	want := "slack://open?team=T03EE7DCP"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}
