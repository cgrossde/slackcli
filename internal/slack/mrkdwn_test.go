// Package slack — mrkdwn_test.go tests the MarkdownToMrkdwn converter.
// Every transformation rule from the plan is covered by at least one case.
package slack

import (
	"strings"
	"testing"
)

func TestMarkdownToMrkdwn(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string
	}{
		// Rule 1 — **bold** → *bold*
		{
			name:  "bold double-asterisk",
			input: "**hello world**",
			want:  "*hello world*",
		},
		// Rule 2 — __bold__ → *bold*
		{
			name:  "bold double-underscore",
			input: "__hello world__",
			want:  "*hello world*",
		},
		// Rule 3 — *italic* → _italic_
		{
			name:  "italic single-asterisk",
			input: "*hello*",
			want:  "_hello_",
		},
		// Rule 4 — _italic_ unchanged
		{
			name:  "italic underscore unchanged",
			input: "_hello_",
			want:  "_hello_",
		},
		// Rule 5 — ***bold italic*** → _*bold italic*_ (goldmark: italic wraps bold)
		{
			name:  "bold italic triple-asterisk",
			input: "***bold and italic***",
			want:  "_*bold and italic*_",
		},
		// Rule 6 — ~~strikethrough~~ → ~strikethrough~
		{
			name:  "strikethrough",
			input: "~~delete me~~",
			want:  "~delete me~",
		},
		// Rule 7 — [text](url) → <url|text>
		{
			name:  "link",
			input: "[Slack](https://slack.com)",
			want:  "<https://slack.com|Slack>",
		},
		// Rule 8 — ![alt](url) → <url|alt>
		{
			name:  "image",
			input: "![logo](https://example.com/logo.png)",
			want:  "<https://example.com/logo.png|logo>",
		},
		// Rule 9 — # H1 → *H1*
		{
			name:  "heading h1",
			input: "# My Heading",
			want:  "*My Heading*",
		},
		// Rule 10 — ## H2 → *H2*
		{
			name:  "heading h2",
			input: "## Sub-heading",
			want:  "*Sub-heading*",
		},
		// Rule 11 — `code` unchanged
		{
			name:  "inline code unchanged",
			input: "`some code`",
			want:  "`some code`",
		},
		// Rule 12 — fenced code block: strip language label
		{
			name:  "fenced code block strips language label",
			input: "```go\nfmt.Println(\"hi\")\n```",
			want:  "```\nfmt.Println(\"hi\")\n```",
		},
		// Rule 13 — > blockquote unchanged
		{
			name:  "blockquote unchanged",
			input: "> This is a quote",
			want:  "> This is a quote",
		},
		// Rule 14 — - item → • item
		{
			name:  "bullet dash",
			input: "- first item",
			want:  "• first item",
		},
		{
			name:  "bullet asterisk",
			input: "* second item",
			want:  "• second item",
		},
		// Rule 15 — 1. item unchanged
		{
			name:  "numbered list unchanged",
			input: "1. first",
			want:  "1. first",
		},
		// Rule 16 — --- → (empty separator, no em-dashes)
		{
			name:  "hr dash",
			input: "---",
			want:  "",
		},
		{
			name:  "hr asterisk",
			input: "***",
			want:  "",
		},
		{
			name:  "hr underscore",
			input: "___",
			want:  "",
		},
		// Rule 17 — pipe table → aligned plain text
		{
			name:  "pipe table",
			input: "| Name  | Score |\n|-------|-------|\n| Alice | 100   |\n| Bob   | 95    |",
			want:  "+-------+-------+\n| Name  | Score |\n+-------+-------+\n| Alice | 100   |\n| Bob   | 95    |\n+-------+-------+",
		},

		// --- Code span protection ---
		{
			name:  "code span protects bold inside",
			input: "`**not bold**`",
			want:  "`**not bold**`",
		},
		{
			name:  "inline code with surrounding bold",
			input: "**see** `code` here",
			want:  "*see* `code` here",
		},

		// --- Multi-element line ---
		{
			name:  "multiple inline transforms",
			input: "**bold** and *italic* and ~~strike~~",
			want:  "*bold* and _italic_ and ~strike~",
		},

		// --- No-op cases ---
		{
			name:  "plain text unchanged",
			input: "just a plain sentence",
			want:  "just a plain sentence",
		},
		{
			name:  "empty string",
			input: "",
			want:  "",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got := MarkdownToMrkdwn(tc.input)
			if got != tc.want {
				t.Errorf("\ninput: %q\n  got: %q\n want: %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestMarkdownToMrkdwn_fencedCodePassthrough verifies that content inside a
// fenced code block is never transformed.
func TestMarkdownToMrkdwn_fencedCodePassthrough(t *testing.T) {
	input := "```\n**bold** *italic* [link](url)\n```"
	got := MarkdownToMrkdwn(input)
	// The inner line must be untouched.
	if !strings.Contains(got, "**bold** *italic* [link](url)") {
		t.Errorf("content inside fenced code was transformed:\n%s", got)
	}
}

// TestMarkdownToMrkdwn_multiline tests a realistic multi-paragraph message.
func TestMarkdownToMrkdwn_multiline(t *testing.T) {
	input := strings.Join([]string{
		"## Deployment summary",
		"",
		"**Service:** api-gateway",
		"**Status:** ~~failed~~ *in progress*",
		"",
		"Steps:",
		"- Build image",
		"- Push to registry",
		"",
		"See [runbook](https://wiki.example.com/runbook) for details.",
	}, "\n")

	got := MarkdownToMrkdwn(input)

	checks := []string{
		"*Deployment summary*",
		"*Service:* api-gateway",
		"*Status:* ~failed~ _in progress_",
		"• Build image",
		"• Push to registry",
		"<https://wiki.example.com/runbook|runbook>",
	}
	for _, want := range checks {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q\nfull output:\n%s", want, got)
		}
	}
}
