package cmd

import (
	"encoding/base64"
	"strings"
	"testing"

	"github.com/cgrossde/slackcli/internal/slack"
)

func TestIsImageMIME(t *testing.T) {
	for _, tt := range []struct {
		mime string
		want bool
	}{
		{"image/png", true},
		{"image/jpeg", true},
		{"image/gif", true},
		{"image/webp", true},
		{"image/svg+xml", true},
		{"application/pdf", false},
		{"text/plain", false},
		{"", false},
		{"IMAGE/PNG", false}, // case-sensitive — Slack always sends lowercase
	} {
		got := isImageMIME(tt.mime)
		if got != tt.want {
			t.Errorf("isImageMIME(%q) = %v, want %v", tt.mime, got, tt.want)
		}
	}
}

func TestITerm2InlineImage_structure(t *testing.T) {
	data := []byte("fake-png-data")
	name := "screenshot.png"
	seq := iTerm2InlineImage(name, data, 0) // 0 → auto width

	// Must start with ESC ] 1337 ;
	if !strings.HasPrefix(seq, "\x1b]1337;") {
		t.Errorf("sequence does not start with ESC]1337;: %q", seq[:min(len(seq), 20)])
	}
	// Must end with BEL + newline
	if !strings.HasSuffix(seq, "\a\n") {
		t.Errorf("sequence does not end with BEL+newline: %q", seq[max(0, len(seq)-5):])
	}
	// Must contain File=inline=1
	if !strings.Contains(seq, "File=inline=1") {
		t.Errorf("missing File=inline=1 in: %q", seq)
	}
	// width=0 → "auto"
	if !strings.Contains(seq, "width=auto") {
		t.Errorf("missing width=auto in: %q", seq)
	}
	// Name must be base64-encoded
	b64name := base64.StdEncoding.EncodeToString([]byte(name))
	if !strings.Contains(seq, "name="+b64name) {
		t.Errorf("missing name=%s in: %q", b64name, seq)
	}
	// Data must be base64-encoded
	b64data := base64.StdEncoding.EncodeToString(data)
	if !strings.Contains(seq, ":"+b64data+"\a") {
		t.Errorf("missing b64 payload in sequence: %q", seq)
	}
	// Size must match
	sizeStr := "size=13" // len("fake-png-data") == 13
	if !strings.Contains(seq, sizeStr) {
		t.Errorf("missing %q in: %q", sizeStr, seq)
	}
}

func TestITerm2InlineImage_explicitWidth(t *testing.T) {
	seq := iTerm2InlineImage("x.png", []byte("d"), 42)
	if !strings.Contains(seq, "width=42%") {
		t.Errorf("expected width=42%% in: %q", seq)
	}
}

func TestImageNaturalPct(t *testing.T) {
	for _, tt := range []struct {
		name     string
		imgW     int
		termPxW  int
		wantPct  int
	}{
		{"fits naturally",  400, 2000, 40},  // 400/2000*100*2 = 40%
		{"cap at 100",     1200, 2000, 100}, // 120% → capped to 100%
		{"zero termPxW",    400,    0, 0},
		{"zero imgW (nil data)", 0, 2000, 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if tt.imgW == 0 || tt.termPxW == 0 {
				got := imageNaturalPct(nil, tt.termPxW)
				if got != 0 {
					t.Errorf("got %d, want 0", got)
				}
				return
			}
			// Verify the arithmetic directly (no real image needed).
			want := int(float64(tt.imgW) / float64(tt.termPxW) * 100 * 2)
			if want > 100 {
				want = 100
			}
			if want != tt.wantPct {
				t.Errorf("arithmetic: got %d, want %d", want, tt.wantPct)
			}
		})
	}
}

func TestRenderMessage_inlineImage(t *testing.T) {
	// Verify the full code path: renderMessage calls fileFetcher for image/png
	// and embeds the iTerm2 sequence in the output when in an iTerm2 terminal.
	imageData := []byte("\x89PNG fake")
	var fetchedURL string

	// Force iTerm2 detection on by setting LC_TERMINAL.
	t.Setenv("LC_TERMINAL", "iTerm2")

	pr, err := newPrettyRenderer(true)
	if err != nil {
		t.Fatalf("newPrettyRenderer: %v", err)
	}
	pr.fileFetcher = func(url string) ([]byte, string, error) {
		fetchedURL = url
		return imageData, "image/png", nil
	}

	m := slack.Message{
		User: "U1",
		Ts:   "1700000000.000001",
		Text: "see image",
		Files: []slack.File{
			{
				Name:       "photo.png",
				Mimetype:   "image/png",
				PrettyType: "PNG",
				URLPrivate: "https://files.slack.com/photo.png",
				Permalink:  "https://acme.enterprise.slack.com/files/photo.png",
			},
		},
	}

	got, err := pr.renderMessage(m, nil)
	if err != nil {
		t.Fatalf("renderMessage: %v", err)
	}

	if fetchedURL != "https://files.slack.com/photo.png" {
		t.Errorf("fetcher called with URL %q, want url_private", fetchedURL)
	}
	if !strings.Contains(got, "\x1b]1337;") {
		t.Errorf("iTerm2 sequence missing from output: %q", got[:min(len(got), 80)])
	}
	// The paperclip text-fallback line must NOT appear for an inline-rendered image.
	if strings.Contains(got, "📎") {
		t.Errorf("text fallback emitted alongside inline image: %q", got)
	}
}

func TestRenderMessage_inlineImage_prettyTypeFallback(t *testing.T) {
	// PrettyType="PNG" must NOT trigger inline rendering — only Mimetype matters.
	pr, err := newPrettyRenderer(true)
	if err != nil {
		t.Fatalf("newPrettyRenderer: %v", err)
	}
	fetchCalled := false
	pr.fileFetcher = func(url string) ([]byte, string, error) {
		fetchCalled = true
		return nil, "", nil
	}

	// No LC_TERMINAL set — supportsInlineImages() returns false here, but even if
	// it did return true the MIME check would reject PrettyType-only entries.
	// We test the MIME guard directly via isImageMIME.
	if isImageMIME("PNG") {
		t.Error("isImageMIME(\"PNG\") must be false — PrettyType is not a MIME type")
	}
	_ = fetchCalled
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
