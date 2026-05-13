// Package cmd — snippet_test.go tests snippet command logic without network or
// keychain access.
package cmd

import (
	"bytes"
	"strings"
	"testing"
)

// ---------------------------------------------------------------------------
// SnippetCreate argument validation
// ---------------------------------------------------------------------------

func TestSnippetCreate_missingChannel(t *testing.T) {
	_, err := SnippetCreate([]string{}, SnippetCreateFlags{}, bytes.NewReader(nil))
	if err == nil {
		t.Fatal("expected error when --channel missing, got nil")
	}
	if !strings.Contains(err.Error(), "--channel") {
		t.Errorf("error should mention --channel, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// SnippetTypes
// ---------------------------------------------------------------------------

func TestSnippetTypes_containsAll(t *testing.T) {
	out := SnippetTypes()
	// Spot-check a few required entries.
	expected := []string{"auto", "text", "go", "python", "typescript", "yaml"}
	for _, want := range expected {
		if !strings.Contains(out, want) {
			t.Errorf("SnippetTypes() output missing %q", want)
		}
	}
}

func TestSnippetTypes_noEmpty(t *testing.T) {
	out := SnippetTypes()
	if strings.TrimSpace(out) == "" {
		t.Error("SnippetTypes() returned empty string")
	}
}

// ---------------------------------------------------------------------------
// inferFiletype
// ---------------------------------------------------------------------------

func TestInferFiletype(t *testing.T) {
	cases := []struct {
		ext  string
		want string
	}{
		{"go", "go"},
		{"py", "python"},
		{"ts", "typescript"},
		{"tsx", "typescript"},
		{"js", "javascript"},
		{"mjs", "javascript"},
		{"rs", "rust"},
		{"rb", "ruby"},
		{"yaml", "yaml"},
		{"yml", "yaml"},
		{"md", "markdown"},
		{"markdown", "markdown"},
		{"sh", "shell"},
		{"bash", "shell"},
		{"sql", "sql"},
		{"json", "json"},
		{"xml", "xml"},
		{"css", "css"},
		{"html", "html"},
		{"htm", "html"},
		{"kt", "kotlin"},
		{"kts", "kotlin"},
		{"cs", "csharp"},
		{"c", "c"},
		{"cpp", "cpp"},
		{"cc", "cpp"},
		{"txt", "text"},
		{"pdf", ""},
		{"", ""},
		{"GO", "go"}, // case-insensitive
	}
	for _, tc := range cases {
		got := inferFiletype(tc.ext)
		if got != tc.want {
			t.Errorf("inferFiletype(%q) = %q, want %q", tc.ext, got, tc.want)
		}
	}
}
