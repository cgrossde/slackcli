package browser

import (
	"testing"

	"github.com/playwright-community/playwright-go"
)

func TestTokenRE(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{
			name:  "valid xoxc token",
			input: "xoxc-1234567890-9876543210-1122334455-abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
			want:  true,
		},
		{
			name:  "too short hex segment",
			input: "xoxc-123-456-789-abc",
			want:  false,
		},
		{
			name:  "xoxb token not matched",
			input: "xoxb-1234567890-9876543210-abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890",
			want:  false,
		},
		{
			name:  "empty string",
			input: "",
			want:  false,
		},
		{
			name:  "uppercase hex rejected",
			input: "xoxc-1234567890-9876543210-1122334455-ABCDEF1234567890ABCDEF1234567890ABCDEF1234567890ABCDEF1234567890",
			want:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := tokenRE.MatchString(tt.input)
			if got != tt.want {
				t.Errorf("tokenRE.MatchString(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

func TestNormalizeWorkspaceURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		want      string
		wantError bool
	}{
		{
			name:  "bare name",
			input: "myorg",
			want:  "https://myorg.slack.com",
		},
		{
			name:  "name with .slack.com suffix",
			input: "myorg.slack.com",
			want:  "https://myorg.slack.com",
		},
		{
			name:  "full https URL",
			input: "https://myorg.slack.com",
			want:  "https://myorg.slack.com",
		},
		{
			name:  "full https URL with path",
			input: "https://myorg.slack.com/messages/general",
			want:  "https://myorg.slack.com/messages/general",
		},
		{
			name:  "enterprise URL passed through",
			input: "https://mycompany.enterprise.slack.com",
			want:  "https://mycompany.enterprise.slack.com",
		},
		{
			name:      "empty string",
			input:     "",
			wantError: true,
		},
		{
			name:      "whitespace only",
			input:     "   ",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := normalizeWorkspaceURL(tt.input)
			if tt.wantError {
				if err == nil {
					t.Errorf("normalizeWorkspaceURL(%q) expected error, got nil (result: %q)", tt.input, got)
				}
				return
			}
			if err != nil {
				t.Errorf("normalizeWorkspaceURL(%q) unexpected error: %v", tt.input, err)
				return
			}
			if got != tt.want {
				t.Errorf("normalizeWorkspaceURL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestExtractTokenFromURL(t *testing.T) {
	t.Parallel()

	validToken := "xoxc-1234567890-9876543210-1122334455-abcdef1234567890abcdef1234567890abcdef1234567890abcdef1234567890"

	tests := []struct {
		name      string
		input     string
		want      string
		wantError bool
	}{
		{
			name:  "token in query string",
			input: "https://myorg.slack.com/api/auth.test?token=" + validToken,
			want:  validToken,
		},
		{
			name:  "no token param",
			input: "https://myorg.slack.com/api/auth.test?foo=bar",
			want:  "",
		},
		{
			name:  "token param with invalid value",
			input: "https://myorg.slack.com/api/auth.test?token=xoxb-bad",
			want:  "",
		},
		{
			name:      "malformed URL",
			input:     "://bad url",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := extractTokenFromURL(tt.input)
			if tt.wantError {
				if err == nil {
					t.Errorf("extractTokenFromURL(%q) expected error, got nil", tt.input)
				}
				return
			}
			if err != nil {
				t.Errorf("extractTokenFromURL(%q) unexpected error: %v", tt.input, err)
				return
			}
			if got != tt.want {
				t.Errorf("extractTokenFromURL(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestFindDCookie(t *testing.T) {
	t.Parallel()

	validXoxd := "xoxd-example-session-cookie-value"

	tests := []struct {
		name    string
		cookies []playwright.Cookie
		want    string
	}{
		{
			name: "d cookie present",
			cookies: []playwright.Cookie{
				{Name: "other", Value: "v1", Domain: ".slack.com"},
				{Name: "d", Value: validXoxd, Domain: ".slack.com"},
			},
			want: validXoxd,
		},
		{
			name: "d cookie on subdomain",
			cookies: []playwright.Cookie{
				{Name: "d", Value: validXoxd, Domain: "app.slack.com"},
			},
			want: validXoxd,
		},
		{
			name: "d cookie wrong domain ignored",
			cookies: []playwright.Cookie{
				{Name: "d", Value: validXoxd, Domain: ".otherdomain.com"},
			},
			want: "",
		},
		{
			name:    "empty cookie list",
			cookies: []playwright.Cookie{},
			want:    "",
		},
		{
			name:    "nil cookie list",
			cookies: nil,
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := findDCookie(tt.cookies)
			if got != tt.want {
				t.Errorf("findDCookie() = %q, want %q", got, tt.want)
			}
		})
	}
}
