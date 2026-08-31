package slack

import (
	"strings"
	"testing"
)

func TestCanvasHTMLToText_headings(t *testing.T) {
	in := `<h1>Title</h1><h2>Sub</h2><h3>Sub-sub</h3>`
	out := CanvasHTMLToText(in)
	if !strings.Contains(out, "# Title") {
		t.Errorf("expected h1 → '# Title', got: %q", out)
	}
	if !strings.Contains(out, "## Sub") {
		t.Errorf("expected h2 → '## Sub', got: %q", out)
	}
	if !strings.Contains(out, "### Sub-sub") {
		t.Errorf("expected h3 → '### Sub-sub', got: %q", out)
	}
}

func TestCanvasHTMLToText_paragraph(t *testing.T) {
	in := `<p id="quip-junk" class="line">Hello world</p>`
	out := CanvasHTMLToText(in)
	if !strings.Contains(out, "Hello world") {
		t.Errorf("expected paragraph text, got: %q", out)
	}
	if strings.Contains(out, "quip-junk") || strings.Contains(out, `class=`) {
		t.Errorf("attributes should be stripped, got: %q", out)
	}
}

func TestCanvasHTMLToText_inlineFormatting(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{`<strong>bold</strong>`, "**bold**"},
		{`<b>bold</b>`, "**bold**"},
		{`<em>italic</em>`, "*italic*"},
		{`<i>italic</i>`, "*italic*"},
		{`<code>snippet</code>`, "`snippet`"},
		{`<s>struck</s>`, "~~struck~~"},
		{`<del>struck</del>`, "~~struck~~"},
	}
	for _, tc := range cases {
		out := CanvasHTMLToText(tc.in)
		if !strings.Contains(out, tc.want) {
			t.Errorf("CanvasHTMLToText(%q): want %q in output, got %q", tc.in, tc.want, out)
		}
	}
}

func TestCanvasHTMLToText_link(t *testing.T) {
	in := `<a href="https://example.com">click</a>`
	out := CanvasHTMLToText(in)
	if !strings.Contains(out, "[click](https://example.com)") {
		t.Errorf("expected markdown link, got: %q", out)
	}
}

func TestCanvasHTMLToText_unorderedList(t *testing.T) {
	in := `<ul><li>Apple</li><li>Banana</li></ul>`
	out := CanvasHTMLToText(in)
	if !strings.Contains(out, "- Apple") {
		t.Errorf("expected '- Apple', got: %q", out)
	}
	if !strings.Contains(out, "- Banana") {
		t.Errorf("expected '- Banana', got: %q", out)
	}
}

func TestCanvasHTMLToText_orderedList(t *testing.T) {
	in := `<ol><li>First</li><li>Second</li></ol>`
	out := CanvasHTMLToText(in)
	if !strings.Contains(out, "1. First") {
		t.Errorf("expected '1. First', got: %q", out)
	}
	if !strings.Contains(out, "2. Second") {
		t.Errorf("expected '2. Second', got: %q", out)
	}
}

func TestCanvasHTMLToText_tablePreservedClean(t *testing.T) {
	in := `<table><tr><td id="temp:C:abc" class="cell" colspan="2">Feature</td><td>Notes</td></tr></table>`
	out := CanvasHTMLToText(in)
	// Table stays as HTML.
	if !strings.Contains(out, "<table>") {
		t.Errorf("expected <table>, got: %q", out)
	}
	// colspan is a structural attr — must be kept.
	if !strings.Contains(out, `colspan="2"`) {
		t.Errorf("expected colspan preserved, got: %q", out)
	}
	// Quip id and class must be stripped.
	if strings.Contains(out, "temp:C:abc") {
		t.Errorf("quip id should be stripped, got: %q", out)
	}
	if strings.Contains(out, `class="cell"`) {
		t.Errorf("class should be stripped, got: %q", out)
	}
}

func TestCanvasHTMLToText_pre(t *testing.T) {
	in := `<pre>func main() {}</pre>`
	out := CanvasHTMLToText(in)
	if !strings.Contains(out, "```") {
		t.Errorf("expected fenced code block, got: %q", out)
	}
	if !strings.Contains(out, "func main() {}") {
		t.Errorf("expected code content, got: %q", out)
	}
}

func TestCanvasHTMLToText_trailingNewline(t *testing.T) {
	out := CanvasHTMLToText(`<p>Hello</p>`)
	if !strings.HasSuffix(out, "\n") {
		t.Errorf("output should end with newline, got: %q", out)
	}
}

func TestCanvasHTMLToText_realWorldFragment(t *testing.T) {
	// Mirrors the actual canvas HTML structure from the user's canvas.
	in := `<div class="quip-canvas-content">` +
		`<h1 id="temp:C:cVKc473d95f053343e09a2a36f85">Q4 Planning 2026</h1>` +
		`<p id="temp:C:cVK048563ee9690468ca80b5ecdf" class="line"></p>` +
		`<table>` +
		`<tr><td><p id="temp:C:abc">Feature Name</p></td><td><p id="temp:C:def">Notes</p></td></tr>` +
		`<tr><td><p id="temp:C:ghi">Enable delivery</p></td><td><p id="temp:C:jkl">via CarTrawler</p></td></tr>` +
		`</table>` +
		`</div>`

	out := CanvasHTMLToText(in)

	if !strings.Contains(out, "# Q4 Planning 2026") {
		t.Errorf("expected heading, got: %q", out)
	}
	if !strings.Contains(out, "<table>") {
		t.Errorf("expected table HTML, got: %q", out)
	}
	if strings.Contains(out, "temp:C:") {
		t.Errorf("quip ids should be stripped, got: %q", out)
	}
}

func TestCanvasHTMLToText_tableCompactCells(t *testing.T) {
	// <p> wrappers stripped, <br> becomes " · ", one <tr> per line.
	in := `<table><tr><td><p>Alpha</p><br><p>Beta</p></td><td><p>Notes</p></td></tr></table>`
	out := CanvasHTMLToText(in)
	if !strings.Contains(out, "<td>Alpha · Beta</td>") {
		t.Errorf("expected compact cell with · separator, got: %q", out)
	}
	// Each <tr> on its own line.
	lines := strings.Split(strings.TrimSpace(out), "\n")
	trLines := 0
	for _, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "<tr>") {
			trLines++
		}
	}
	if trLines != 1 {
		t.Errorf("expected 1 <tr> line, got %d in:\n%s", trLines, out)
	}
}

func TestCanvasHTMLToText_lnkPlainText(t *testing.T) {
	in := `<table><tr><td><lnk>https://example.com/TRAVEL-123</lnk></td></tr></table>`
	out := CanvasHTMLToText(in)
	if !strings.Contains(out, "https://example.com/TRAVEL-123") {
		t.Errorf("expected lnk text as plain URL, got: %q", out)
	}
	if strings.Contains(out, "<lnk>") {
		t.Errorf("lnk tag should not appear in output, got: %q", out)
	}
}
