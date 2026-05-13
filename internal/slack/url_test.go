package slack

import (
	"testing"
)

func TestParseSlackURL_valid(t *testing.T) {
	cases := []struct {
		name          string
		input         string
		wantWorkspace string
		wantChannel   string
		wantTs        string
		wantThreadTs  string
	}{
		{
			name:          "standard message URL",
			input:         "https://myorg.slack.com/archives/C01234ABCDE/p1700000000000001",
			wantWorkspace: "myorg.slack.com",
			wantChannel:   "C01234ABCDE",
			wantTs:        "1700000000.000001",
		},
		{
			name:          "public channel URL with different org",
			input:         "https://otherorg.slack.com/archives/C09999ZZZZZ/p1700000099000001",
			wantWorkspace: "otherorg.slack.com",
			wantChannel:   "C09999ZZZZZ",
			wantTs:        "1700000099.000001",
		},
		{
			name:          "URL with trailing query string",
			input:         "https://myorg.slack.com/archives/C01234ABCDE/p1700000000000001?thread_ts=1234",
			wantWorkspace: "myorg.slack.com",
			wantChannel:   "C01234ABCDE",
			wantTs:        "1700000000.000001",
			wantThreadTs:  "1234",
		},
		{
			name:          "thread reply URL",
			input:         "https://concur-blue.slack.com/archives/C0B3PCPL0CF/p1779023515154839?thread_ts=1779023514.528229&cid=C0B3PCPL0CF",
			wantWorkspace: "concur-blue.slack.com",
			wantChannel:   "C0B3PCPL0CF",
			wantTs:        "1779023515.154839",
			wantThreadTs:  "1779023514.528229",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseSlackURL(tc.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Workspace != tc.wantWorkspace {
				t.Errorf("Workspace: got %q, want %q", got.Workspace, tc.wantWorkspace)
			}
			if got.ChannelID != tc.wantChannel {
				t.Errorf("ChannelID: got %q, want %q", got.ChannelID, tc.wantChannel)
			}
			if got.Ts != tc.wantTs {
				t.Errorf("Ts: got %q, want %q", got.Ts, tc.wantTs)
			}
			if got.ThreadTs != tc.wantThreadTs {
				t.Errorf("ThreadTs: got %q, want %q", got.ThreadTs, tc.wantThreadTs)
			}
		})
	}
}

func TestParseSlackURL_invalid(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"not a URL", "not-a-url"},
		{"wrong host", "https://google.com/archives/C123/p1700000000000001"},
		{"missing archives", "https://myorg.slack.com/channels/C123/p1700000000000001"},
		{"missing timestamp segment", "https://myorg.slack.com/archives/C123"},
		{"timestamp without p prefix", "https://myorg.slack.com/archives/C123/1700000000000001"},
		{"timestamp too short", "https://myorg.slack.com/archives/C123/p12345"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseSlackURL(tc.input)
			if err == nil {
				t.Errorf("expected error for input %q, got nil", tc.input)
			}
		})
	}
}

func TestParseChannelTs_valid(t *testing.T) {
	cases := []struct {
		name   string
		input  string
		wantCh string
		wantTs string
	}{
		{
			name:   "public channel",
			input:  "C012ABC3456:1718197925.001234",
			wantCh: "C012ABC3456",
			wantTs: "1718197925.001234",
		},
		{
			name:   "DM channel",
			input:  "D0B22865CQ4:1778689976.670749",
			wantCh: "D0B22865CQ4",
			wantTs: "1778689976.670749",
		},
		{
			name:   "group DM (G prefix)",
			input:  "G012XYZ:1700000000.000001",
			wantCh: "G012XYZ",
			wantTs: "1700000000.000001",
		},
		{
			name:   "W prefix workspace channel",
			input:  "W4UDRQJNR:1700000000.000001",
			wantCh: "W4UDRQJNR",
			wantTs: "1700000000.000001",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseChannelTs(tc.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Workspace != "" {
				t.Errorf("Workspace should be empty for channel:ts form, got %q", got.Workspace)
			}
			if got.ChannelID != tc.wantCh {
				t.Errorf("ChannelID: got %q, want %q", got.ChannelID, tc.wantCh)
			}
			if got.Ts != tc.wantTs {
				t.Errorf("Ts: got %q, want %q", got.Ts, tc.wantTs)
			}
		})
	}
}

func TestParseChannelTs_invalid(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"is a URL", "https://myorg.slack.com/archives/C123/p1700000000000001"},
		{"no colon", "C012ABC31718197925001234"},
		{"empty channel", ":1718197925.001234"},
		{"empty ts", "C012ABC3456:"},
		{"bad channel prefix (lowercase)", "c012ABC:1718197925.001234"},
		{"bad channel prefix (digit)", "0012ABC:1718197925.001234"},
		{"ts no dot", "C012ABC:1718197925001234"},
		{"ts dot at start", "C012ABC:.001234"},
		{"ts dot at end", "C012ABC:1718197925."},
		{"ts non-numeric", "C012ABC:1718abc925.001234"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseChannelTs(tc.input)
			if err == nil {
				t.Errorf("expected error for input %q, got nil", tc.input)
			}
		})
	}
}

func TestParseChannelTs_threePartValid(t *testing.T) {
	cases := []struct {
		name         string
		input        string
		wantCh       string
		wantThreadTs string
		wantTs       string
	}{
		{
			name:         "public channel thread reply",
			input:        "C012ABC3456:1718197000.000001:1718197925.001234",
			wantCh:       "C012ABC3456",
			wantThreadTs: "1718197000.000001",
			wantTs:       "1718197925.001234",
		},
		{
			name:         "DM thread reply",
			input:        "D0B22865CQ4:1718197000.000001:1718197925.001234",
			wantCh:       "D0B22865CQ4",
			wantThreadTs: "1718197000.000001",
			wantTs:       "1718197925.001234",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ParseChannelTs(tc.input)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got.Workspace != "" {
				t.Errorf("Workspace should be empty, got %q", got.Workspace)
			}
			if got.ChannelID != tc.wantCh {
				t.Errorf("ChannelID: got %q, want %q", got.ChannelID, tc.wantCh)
			}
			if got.ThreadTs != tc.wantThreadTs {
				t.Errorf("ThreadTs: got %q, want %q", got.ThreadTs, tc.wantThreadTs)
			}
			if got.Ts != tc.wantTs {
				t.Errorf("Ts: got %q, want %q", got.Ts, tc.wantTs)
			}
		})
	}
}

func TestParseChannelTs_threePartInvalid(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"empty thread ts", "C012ABC:  :1718197925.001234"},
		{"empty reply ts", "C012ABC:1718197000.000001:"},
		{"bad thread ts no dot", "C012ABC:1718197000000001:1718197925.001234"},
		{"bad reply ts no dot", "C012ABC:1718197000.000001:1718197925001234"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := ParseChannelTs(tc.input)
			if err == nil {
				t.Errorf("expected error for input %q, got nil", tc.input)
			}
		})
	}
}

func TestParseMessageRef_dispatches(t *testing.T) {
	// URL form — workspace populated from URL.
	ref, err := ParseMessageRef("https://myorg.slack.com/archives/C01234ABCDE/p1700000000000001")
	if err != nil {
		t.Fatalf("URL form: unexpected error: %v", err)
	}
	if ref.Workspace != "myorg.slack.com" {
		t.Errorf("URL form: Workspace = %q, want myorg.slack.com", ref.Workspace)
	}
	if ref.ChannelID != "C01234ABCDE" {
		t.Errorf("URL form: ChannelID = %q, want C01234ABCDE", ref.ChannelID)
	}

	// channel:ts form — workspace empty.
	ref2, err := ParseMessageRef("C012ABC3456:1718197925.001234")
	if err != nil {
		t.Fatalf("channel:ts form: unexpected error: %v", err)
	}
	if ref2.Workspace != "" {
		t.Errorf("channel:ts form: Workspace should be empty, got %q", ref2.Workspace)
	}
	if ref2.ChannelID != "C012ABC3456" {
		t.Errorf("channel:ts form: ChannelID = %q, want C012ABC3456", ref2.ChannelID)
	}
	if ref2.Ts != "1718197925.001234" {
		t.Errorf("channel:ts form: Ts = %q, want 1718197925.001234", ref2.Ts)
	}
}

func TestIsChannelTs(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"C012ABC:1718197925.001234", true},
		{"D0B22865CQ4:1778689976.670749", true},
		{"G012XYZ:1700000000.000001", true},
		{"W4UDRQJNR:1700000000.000001", true},
		{"https://myorg.slack.com/archives/C123/p1700000000000001", false},
		{"http://myorg.slack.com/archives/C123/p1700000000000001", false},
		{"just-text", false},
		{"X012ABC:1718197925.001234", false}, // X is not a valid prefix
		{":1718197925.001234", false},        // empty before colon
	}
	for _, tc := range cases {
		got := IsChannelTs(tc.input)
		if got != tc.want {
			t.Errorf("IsChannelTs(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}

func TestParseFileRef_valid(t *testing.T) {
	cases := []struct {
		input    string
		wantWS   string
		wantID   string
		wantName string
	}{
		{
			"https://myorg.slack.com/files/WH1K7QTFU/F0B3HRU6ZA7/image.png",
			"myorg.slack.com", "F0B3HRU6ZA7", "image.png",
		},
		{
			// No filename segment — still valid
			"https://myorg.slack.com/files/WH1K7QTFU/F0B3HRU6ZA7",
			"myorg.slack.com", "F0B3HRU6ZA7", "",
		},
		{
			// Enterprise workspace prefix
			"https://acme.enterprise.slack.com/files/WH1K7QTFU/F0B3HRU6ZA7/report.pdf",
			"acme.enterprise.slack.com", "F0B3HRU6ZA7", "report.pdf",
		},
	}
	for _, tc := range cases {
		got, err := ParseFileRef(tc.input)
		if err != nil {
			t.Errorf("ParseFileRef(%q): unexpected error: %v", tc.input, err)
			continue
		}
		if got.Workspace != tc.wantWS {
			t.Errorf("ParseFileRef(%q).Workspace = %q, want %q", tc.input, got.Workspace, tc.wantWS)
		}
		if got.FileID != tc.wantID {
			t.Errorf("ParseFileRef(%q).FileID = %q, want %q", tc.input, got.FileID, tc.wantID)
		}
		if got.Filename != tc.wantName {
			t.Errorf("ParseFileRef(%q).Filename = %q, want %q", tc.input, got.Filename, tc.wantName)
		}
	}
}

func TestParseFileRef_invalid(t *testing.T) {
	cases := []struct {
		input string
	}{
		{"not-a-url"},
		{"https://google.com/files/U123/F456/img.png"}, // not .slack.com
		{"https://myorg.slack.com/archives/C123/p1700000000000001"}, // message URL
		{"https://myorg.slack.com/files/Uonly"},        // only one path segment under /files/
		{"https://myorg.slack.com/files/Uonly/"},       // empty fileID
	}
	for _, tc := range cases {
		_, err := ParseFileRef(tc.input)
		if err == nil {
			t.Errorf("ParseFileRef(%q): expected error, got nil", tc.input)
		}
	}
}

func TestIsFileURL(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"https://myorg.slack.com/files/WH1K7QTFU/F0B3HRU6ZA7/image.png", true},
		{"https://myorg.slack.com/files/WH1K7QTFU/F0B3HRU6ZA7", true},
		{"https://myorg.slack.com/archives/C123/p1700000000000001", false},
		{"C012ABC:1718197925.001234", false},
		{"not-a-url", false},
	}
	for _, tc := range cases {
		got := IsFileURL(tc.input)
		if got != tc.want {
			t.Errorf("IsFileURL(%q) = %v, want %v", tc.input, got, tc.want)
		}
	}
}
