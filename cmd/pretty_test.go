package cmd

import (
	"regexp"
	"strings"
	"testing"

	"github.com/cgrossde/slackcli/internal/slack"
)

// ansiRe matches ANSI escape sequences so we can strip them for content checks.
var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[mKHF]`)

func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

func renderOneMessage(t *testing.T, m slack.Message) string {
	t.Helper()
	pr, err := newPrettyRenderer(true)
	if err != nil {
		t.Fatalf("newPrettyRenderer: %v", err)
	}
	out, err := pr.renderMessage(m, nil, "")
	if err != nil {
		t.Fatalf("renderMessage: %v", err)
	}
	return stripANSI(out)
}

func TestRenderMessage_fileAttachment(t *testing.T) {
	m := slack.Message{
		User: "U1",
		Ts:   "1700000000.000001",
		Text: "see attached",
		Files: []slack.File{
			{
				Name:       "report.pdf",
				PrettyType: "PDF",
				Permalink:  "https://files.slack.com/report.pdf",
			},
		},
	}
	got := renderOneMessage(t, m)
	if !strings.Contains(got, "📎") {
		t.Errorf("missing paperclip in file line: %q", got)
	}
	if !strings.Contains(got, "report.pdf") {
		t.Errorf("missing filename in: %q", got)
	}
	if !strings.Contains(got, "PDF") {
		t.Errorf("missing type in: %q", got)
	}
	if !strings.Contains(got, "https://files.slack.com/report.pdf") {
		t.Errorf("missing permalink in: %q", got)
	}
}

func TestRenderMessage_fileURLPrivateFallback(t *testing.T) {
	m := slack.Message{
		User: "U1",
		Ts:   "1700000000.000001",
		Text: "blob",
		Files: []slack.File{
			{Name: "data.bin", URLPrivate: "https://files.slack.com/data.bin"},
		},
	}
	got := renderOneMessage(t, m)
	if !strings.Contains(got, "data.bin") {
		t.Errorf("missing filename in: %q", got)
	}
	if !strings.Contains(got, "https://files.slack.com/data.bin") {
		t.Errorf("missing url_private fallback in: %q", got)
	}
}

func TestRenderMessage_reactions(t *testing.T) {
	m := slack.Message{
		User: "U1",
		Ts:   "1700000000.000001",
		Text: "nice",
		Reactions: []slack.Reaction{
			{Name: "thumbsup", Count: 3, Users: []string{"U2", "U3", "U4"}},
			{Name: "ok_hand", Count: 1, Users: []string{"U5"}},
		},
	}
	got := renderOneMessage(t, m)
	// goemoji resolves :thumbsup: → 👍; also accept the raw name as fallback.
	if !strings.Contains(got, "👍") && !strings.Contains(got, "thumbsup") {
		t.Errorf("missing thumbsup emoji/name in: %q", got)
	}
	if !strings.Contains(got, "3") {
		t.Errorf("missing count 3 in: %q", got)
	}
	if !strings.Contains(got, "1") {
		t.Errorf("missing count 1 in: %q", got)
	}
	// No (you) annotation when selfID is empty.
	if strings.Contains(got, "(you") {
		t.Errorf("unexpected (you) annotation when selfID empty: %q", got)
	}
}

func TestRenderMessage_reactionsSelfAnnotation(t *testing.T) {
	pr, err := newPrettyRenderer(true)
	if err != nil {
		t.Fatalf("newPrettyRenderer: %v", err)
	}
	m := slack.Message{
		User: "U1",
		Ts:   "1700000000.000001",
		Text: "nice",
		Reactions: []slack.Reaction{
			{Name: "thumbsup", Count: 3, Users: []string{"USELF", "U2", "U3"}},
			{Name: "ok_hand", Count: 1, Users: []string{"USELF"}},
		},
	}
	out, err := pr.renderMessage(m, nil, "USELF")
	if err != nil {
		t.Fatalf("renderMessage: %v", err)
	}
	got := stripANSI(out)
	if !strings.Contains(got, "you + 2 others") {
		t.Errorf("expected 'you + 2 others' annotation: %q", got)
	}
	if !strings.Contains(got, "1 (you)") {
		t.Errorf("expected '1 (you)' annotation: %q", got)
	}
}

func TestRenderMessage_noFilesNoReactions(t *testing.T) {
	m := slack.Message{
		User: "U1",
		Ts:   "1700000000.000001",
		Text: "plain message",
	}
	got := renderOneMessage(t, m)
	if strings.Contains(got, "📎") {
		t.Errorf("unexpected file marker in plain message: %q", got)
	}
}
