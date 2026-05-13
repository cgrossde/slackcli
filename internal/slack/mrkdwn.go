// Package slack — mrkdwn.go converts standard Markdown to Slack's mrkdwn
// format using goldmark's AST parser. The conversion is correct by construction
// because it operates on the parse tree rather than regex-transforming text.
//
// goldmark is already an indirect dependency (via charm.land/glamour); this
// file promotes it to direct use — no new module dependency is added.
package slack

import (
	"bytes"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	east "github.com/yuin/goldmark/extension/ast"
	"github.com/yuin/goldmark/renderer"
	"github.com/yuin/goldmark/util"
)

// MarkdownToMrkdwn converts standard Markdown text to Slack mrkdwn format.
// It uses goldmark (GFM-compliant) for parsing and walks the AST to emit
// mrkdwn, so all edge cases — nested emphasis, code spans, links — are handled
// correctly without regex double-transformation hazards.
func MarkdownToMrkdwn(text string) string {
	r := newMrkdwnRenderer()
	md := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRenderer(
			renderer.NewRenderer(
				renderer.WithNodeRenderers(util.Prioritized(r, 1)),
			),
		),
	)
	var buf bytes.Buffer
	if err := md.Convert([]byte(text), &buf); err != nil {
		// Fallback: return input unchanged on parse failure.
		return text
	}
	return cleanMrkdwn(buf.String())
}

// cleanMrkdwn strips trailing whitespace from each line and normalises the
// trailing newline to none (callers add their own if needed).
func cleanMrkdwn(s string) string {
	lines := strings.Split(s, "\n")
	for i, l := range lines {
		lines[i] = strings.TrimRight(l, " \t")
	}
	s = strings.Join(lines, "\n")
	return strings.TrimRight(s, "\n")
}

// ---------------------------------------------------------------------------
// Renderer
// ---------------------------------------------------------------------------

// mrkdwnRenderer is a goldmark NodeRenderer that emits Slack mrkdwn.
type mrkdwnRenderer struct {
	// list state
	listDepth        int
	listCounters     []int
	orderedListStack []bool

	// link buffering — collect link label text before emitting <url|text>
	inLink  bool
	linkBuf *bytes.Buffer
}

func newMrkdwnRenderer() *mrkdwnRenderer { return &mrkdwnRenderer{} }

// RegisterFuncs implements renderer.NodeRenderer.
func (r *mrkdwnRenderer) RegisterFuncs(reg renderer.NodeRendererFuncRegisterer) {
	// Block nodes
	reg.Register(ast.KindDocument, r.renderDocument)
	reg.Register(ast.KindHeading, r.renderHeading)
	reg.Register(ast.KindParagraph, r.renderParagraph)
	reg.Register(ast.KindFencedCodeBlock, r.renderFencedCode)
	reg.Register(ast.KindCodeBlock, r.renderIndentedCode)
	reg.Register(ast.KindBlockquote, r.renderBlockquote)
	reg.Register(ast.KindList, r.renderList)
	reg.Register(ast.KindListItem, r.renderListItem)
	reg.Register(ast.KindThematicBreak, r.renderThematicBreak)
	reg.Register(ast.KindTextBlock, r.renderTextBlock)
	reg.Register(ast.KindHTMLBlock, r.renderHTMLBlock)

	// Inline nodes
	reg.Register(ast.KindText, r.renderText)
	reg.Register(ast.KindEmphasis, r.renderEmphasis)
	reg.Register(ast.KindCodeSpan, r.renderCodeSpan)
	reg.Register(ast.KindLink, r.renderLink)
	reg.Register(ast.KindImage, r.renderImage)
	reg.Register(ast.KindAutoLink, r.renderAutoLink)
	reg.Register(ast.KindRawHTML, r.renderRawHTML)
	reg.Register(ast.KindString, r.renderString)

	// GFM extensions
	reg.Register(east.KindStrikethrough, r.renderStrikethrough)
	reg.Register(east.KindTable, r.renderTable)
	reg.Register(east.KindTableHeader, r.noOp)
	reg.Register(east.KindTableRow, r.noOp)
	reg.Register(east.KindTableCell, r.noOp)
	reg.Register(east.KindTaskCheckBox, r.renderTaskCheckBox)
}

// ---------------------------------------------------------------------------
// Block nodes
// ---------------------------------------------------------------------------

func (r *mrkdwnRenderer) renderDocument(w util.BufWriter, _ []byte, _ ast.Node, _ bool) (ast.WalkStatus, error) {
	return ast.WalkContinue, nil
}

func (r *mrkdwnRenderer) renderHeading(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkSkipChildren, nil
	}
	r.blockSep(w, node)
	// Render inline children into a temporary buffer so we can wrap the
	// entire content in *…* without an embedded newline breaking the markers.
	var inner bytes.Buffer
	bw := newBufWriter(&inner)
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		_ = ast.Walk(child, func(n ast.Node, enter bool) (ast.WalkStatus, error) {
			return r.dispatch(bw, source, n, enter)
		})
	}
	text := strings.TrimRight(inner.String(), "\n")
	w.WriteString("*")
	w.WriteString(text)
	w.WriteString("*\n")
	return ast.WalkSkipChildren, nil
}

func (r *mrkdwnRenderer) renderParagraph(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		r.blockSep(w, node)
	} else {
		w.WriteByte('\n')
	}
	return ast.WalkContinue, nil
}

func (r *mrkdwnRenderer) renderFencedCode(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		r.blockSep(w, node)
		w.WriteString("```\n")
		writeLines(w, source, node)
		w.WriteString("```\n")
	}
	return ast.WalkSkipChildren, nil
}

func (r *mrkdwnRenderer) renderIndentedCode(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		r.blockSep(w, node)
		w.WriteString("```\n")
		writeLines(w, source, node)
		w.WriteString("```\n")
	}
	return ast.WalkSkipChildren, nil
}

func (r *mrkdwnRenderer) renderBlockquote(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	r.blockSep(w, node)

	// Render the blockquote children into a temporary buffer, then
	// prefix each output line with "> ".
	var inner bytes.Buffer
	bw := newBufWriter(&inner)
	innerR := newMrkdwnRenderer()
	innerMD := goldmark.New(
		goldmark.WithExtensions(extension.GFM),
		goldmark.WithRenderer(renderer.NewRenderer(
			renderer.WithNodeRenderers(util.Prioritized(innerR, 1)),
		)),
	)
	// Re-render the raw source for each child paragraph into inner.
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		_ = ast.Walk(child, func(n ast.Node, enter bool) (ast.WalkStatus, error) {
			return innerR.dispatch(bw, source, n, enter)
		})
	}
	_ = innerMD // suppress unused; we walk directly above

	content := strings.TrimRight(inner.String(), "\n")
	for _, line := range strings.Split(content, "\n") {
		w.WriteString("> ")
		w.WriteString(strings.TrimRight(line, " \t"))
		w.WriteByte('\n')
	}
	return ast.WalkSkipChildren, nil
}

func (r *mrkdwnRenderer) renderList(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*ast.List)
	if entering {
		if r.listDepth == 0 {
			r.blockSep(w, node)
		}
		r.listDepth++
		r.orderedListStack = append(r.orderedListStack, n.IsOrdered())
		start := 1
		if n.IsOrdered() {
			start = n.Start
		}
		r.listCounters = append(r.listCounters, start)
	} else {
		r.listDepth--
		r.orderedListStack = r.orderedListStack[:len(r.orderedListStack)-1]
		r.listCounters = r.listCounters[:len(r.listCounters)-1]
	}
	return ast.WalkContinue, nil
}

func (r *mrkdwnRenderer) renderListItem(w util.BufWriter, _ []byte, _ ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	indent := strings.Repeat("  ", r.listDepth-1)
	w.WriteString(indent)
	idx := len(r.orderedListStack) - 1
	if r.orderedListStack[idx] {
		fmt.Fprintf(w, "%d. ", r.listCounters[idx])
		r.listCounters[idx]++
	} else {
		w.WriteString("• ")
	}
	return ast.WalkContinue, nil
}

func (r *mrkdwnRenderer) renderThematicBreak(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		r.blockSep(w, node)
	}
	return ast.WalkContinue, nil
}

func (r *mrkdwnRenderer) renderTextBlock(w util.BufWriter, _ []byte, _ ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		w.WriteByte('\n')
	}
	return ast.WalkContinue, nil
}

func (r *mrkdwnRenderer) renderHTMLBlock(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		r.blockSep(w, node)
		writeLines(w, source, node)
	}
	return ast.WalkContinue, nil
}

// ---------------------------------------------------------------------------
// Inline nodes
// ---------------------------------------------------------------------------

func (r *mrkdwnRenderer) renderText(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.Text)
	text := escapeText(string(n.Segment.Value(source)))
	r.write(w, text)
	if n.HardLineBreak() || n.SoftLineBreak() {
		r.write(w, "\n")
	}
	return ast.WalkContinue, nil
}

func (r *mrkdwnRenderer) renderString(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.String)
	r.write(w, escapeText(string(n.Value)))
	return ast.WalkContinue, nil
}

func (r *mrkdwnRenderer) renderEmphasis(w util.BufWriter, _ []byte, node ast.Node, _ bool) (ast.WalkStatus, error) {
	n := node.(*ast.Emphasis)
	if n.Level == 2 {
		r.write(w, "*")
	} else {
		r.write(w, "_")
	}
	return ast.WalkContinue, nil
}

func (r *mrkdwnRenderer) renderCodeSpan(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	var b strings.Builder
	b.WriteByte('`')
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		if t, ok := child.(*ast.Text); ok {
			b.WriteString(string(t.Segment.Value(source)))
		}
	}
	b.WriteByte('`')
	r.write(w, b.String())
	return ast.WalkSkipChildren, nil
}

func (r *mrkdwnRenderer) renderLink(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	n := node.(*ast.Link)
	if entering {
		r.inLink = true
		r.linkBuf = &bytes.Buffer{}
	} else {
		r.inLink = false
		text := r.linkBuf.String()
		url := string(n.Destination)
		if text == "" || text == url {
			fmt.Fprintf(w, "<%s>", url)
		} else {
			fmt.Fprintf(w, "<%s|%s>", url, text)
		}
		r.linkBuf = nil
	}
	return ast.WalkContinue, nil
}

func (r *mrkdwnRenderer) renderImage(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	n := node.(*ast.Image)
	url := string(n.Destination)
	alt := inlineText(source, node)
	if alt == "" || alt == url {
		fmt.Fprintf(w, "<%s>", url)
	} else {
		fmt.Fprintf(w, "<%s|%s>", url, escapeText(alt))
	}
	return ast.WalkSkipChildren, nil
}

func (r *mrkdwnRenderer) renderAutoLink(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		n := node.(*ast.AutoLink)
		fmt.Fprintf(w, "<%s>", string(n.URL(source)))
	}
	return ast.WalkContinue, nil
}

func (r *mrkdwnRenderer) renderRawHTML(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		n := node.(*ast.RawHTML)
		for i := 0; i < n.Segments.Len(); i++ {
			seg := n.Segments.At(i)
			r.write(w, escapeText(string(seg.Value(source))))
		}
	}
	return ast.WalkContinue, nil
}

// ---------------------------------------------------------------------------
// GFM extensions
// ---------------------------------------------------------------------------

func (r *mrkdwnRenderer) renderStrikethrough(w util.BufWriter, _ []byte, _ ast.Node, _ bool) (ast.WalkStatus, error) {
	r.write(w, "~")
	return ast.WalkContinue, nil
}

func (r *mrkdwnRenderer) renderTaskCheckBox(w util.BufWriter, _ []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if entering {
		n := node.(*east.TaskCheckBox)
		if n.IsChecked {
			r.write(w, "☑ ")
		} else {
			r.write(w, "☐ ")
		}
	}
	return ast.WalkContinue, nil
}

func (r *mrkdwnRenderer) renderTable(w util.BufWriter, source []byte, node ast.Node, entering bool) (ast.WalkStatus, error) {
	if !entering {
		return ast.WalkContinue, nil
	}
	r.blockSep(w, node)

	var rows [][]string
	for child := node.FirstChild(); child != nil; child = child.NextSibling() {
		if child.Kind() != east.KindTableHeader && child.Kind() != east.KindTableRow {
			continue
		}
		var row []string
		for cell := child.FirstChild(); cell != nil; cell = cell.NextSibling() {
			row = append(row, strings.TrimSpace(inlineText(source, cell)))
		}
		rows = append(rows, row)
	}

	if len(rows) > 0 {
		w.WriteString(formatTable(rows))
		w.WriteByte('\n')
	}
	return ast.WalkSkipChildren, nil
}

func (r *mrkdwnRenderer) noOp(_ util.BufWriter, _ []byte, _ ast.Node, _ bool) (ast.WalkStatus, error) {
	return ast.WalkContinue, nil
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// write routes output to the link buffer when inside a link, otherwise to w.
func (r *mrkdwnRenderer) write(w util.BufWriter, s string) {
	if r.inLink {
		r.linkBuf.WriteString(s)
	} else {
		w.WriteString(s)
	}
}

// blockSep emits a blank line before a block element when it has a sibling
// above it (i.e. it is not the very first block in the document). This
// produces the familiar double-newline paragraph separation.
func (r *mrkdwnRenderer) blockSep(w util.BufWriter, node ast.Node) {
	if node.PreviousSibling() == nil {
		return
	}
	// Don't double-space paragraphs inside list items.
	if node.Parent() != nil &&
		node.Parent().Kind() == ast.KindListItem &&
		node.Kind() == ast.KindParagraph {
		return
	}
	w.WriteByte('\n')
}

// dispatch routes a node to the correct render function. Used when walking
// a sub-tree (e.g. blockquote inner content) outside the goldmark pipeline.
func (r *mrkdwnRenderer) dispatch(w util.BufWriter, source []byte, n ast.Node, entering bool) (ast.WalkStatus, error) {
	switch n.Kind() {
	case ast.KindDocument:
		return ast.WalkContinue, nil
	case ast.KindParagraph:
		return r.renderParagraph(w, source, n, entering)
	case ast.KindHeading:
		return r.renderHeading(w, source, n, entering)
	case ast.KindText:
		return r.renderText(w, source, n, entering)
	case ast.KindString:
		return r.renderString(w, source, n, entering)
	case ast.KindEmphasis:
		return r.renderEmphasis(w, source, n, entering)
	case ast.KindCodeSpan:
		return r.renderCodeSpan(w, source, n, entering)
	case ast.KindLink:
		return r.renderLink(w, source, n, entering)
	case ast.KindImage:
		return r.renderImage(w, source, n, entering)
	case ast.KindAutoLink:
		return r.renderAutoLink(w, source, n, entering)
	case ast.KindTextBlock:
		return r.renderTextBlock(w, source, n, entering)
	case ast.KindFencedCodeBlock:
		return r.renderFencedCode(w, source, n, entering)
	case ast.KindCodeBlock:
		return r.renderIndentedCode(w, source, n, entering)
	case ast.KindList:
		return r.renderList(w, source, n, entering)
	case ast.KindListItem:
		return r.renderListItem(w, source, n, entering)
	case ast.KindThematicBreak:
		return r.renderThematicBreak(w, source, n, entering)
	case ast.KindRawHTML:
		return r.renderRawHTML(w, source, n, entering)
	case ast.KindHTMLBlock:
		return r.renderHTMLBlock(w, source, n, entering)
	case east.KindStrikethrough:
		return r.renderStrikethrough(w, source, n, entering)
	case east.KindTaskCheckBox:
		return r.renderTaskCheckBox(w, source, n, entering)
	}
	return ast.WalkContinue, nil
}

// writeLines writes the raw source lines of a node to w.
func writeLines(w util.BufWriter, source []byte, node ast.Node) {
	for i := 0; i < node.Lines().Len(); i++ {
		line := node.Lines().At(i)
		w.WriteString(string(line.Value(source)))
	}
}

// inlineText extracts the plain text content of a node's inline children,
// walking the sub-tree without emitting any mrkdwn markers.
func inlineText(source []byte, node ast.Node) string {
	var b strings.Builder
	_ = ast.Walk(node, func(n ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch n.Kind() {
		case ast.KindText:
			b.WriteString(string(n.(*ast.Text).Segment.Value(source)))
		case ast.KindString:
			b.WriteString(string(n.(*ast.String).Value))
		case ast.KindCodeSpan:
			for child := n.FirstChild(); child != nil; child = child.NextSibling() {
				if t, ok := child.(*ast.Text); ok {
					b.WriteString(string(t.Segment.Value(source)))
				}
			}
			return ast.WalkSkipChildren, nil
		}
		return ast.WalkContinue, nil
	})
	return b.String()
}

// escapeText escapes &, <, > for Slack mrkdwn text content.
// Must NOT be applied inside code spans/blocks or link URLs.
func escapeText(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	return s
}

// ---------------------------------------------------------------------------
// Table formatting
// ---------------------------------------------------------------------------

// formatTable renders rows as a box-drawing ASCII table suitable for Slack.
//
//	+-------+-------+
//	| Name  | Score |
//	+-------+-------+
//	| Alice | 100   |
//	+-------+-------+
func formatTable(rows [][]string) string {
	if len(rows) == 0 {
		return ""
	}
	widths := tableColWidths(rows)
	rule := tableRule(widths)
	var b strings.Builder
	b.WriteString(rule)
	for i, row := range rows {
		b.WriteByte('|')
		for j, cell := range row {
			w := 0
			if j < len(widths) {
				w = widths[j]
			}
			pad := w - utf8.RuneCountInString(cell)
			fmt.Fprintf(&b, " %s%s |", cell, strings.Repeat(" ", pad))
		}
		b.WriteByte('\n')
		if i == 0 || i == len(rows)-1 {
			b.WriteString(rule)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func tableRule(widths []int) string {
	var b strings.Builder
	b.WriteByte('+')
	for _, w := range widths {
		b.WriteString(strings.Repeat("-", w+2))
		b.WriteByte('+')
	}
	b.WriteByte('\n')
	return b.String()
}

func tableColWidths(rows [][]string) []int {
	maxCols := 0
	for _, row := range rows {
		if len(row) > maxCols {
			maxCols = len(row)
		}
	}
	widths := make([]int, maxCols)
	for _, row := range rows {
		for j, cell := range row {
			if w := utf8.RuneCountInString(cell); w > widths[j] {
				widths[j] = w
			}
		}
	}
	return widths
}

// ---------------------------------------------------------------------------
// bufWriterAdapter
// ---------------------------------------------------------------------------

// newBufWriter wraps a *bytes.Buffer to satisfy util.BufWriter.
func newBufWriter(buf *bytes.Buffer) util.BufWriter {
	return &bufWriterAdapter{buf: buf}
}

type bufWriterAdapter struct{ buf *bytes.Buffer }

func (b *bufWriterAdapter) Write(p []byte) (int, error)       { return b.buf.Write(p) }
func (b *bufWriterAdapter) WriteByte(c byte) error            { return b.buf.WriteByte(c) }
func (b *bufWriterAdapter) WriteRune(r rune) (int, error)     { return b.buf.WriteRune(r) }
func (b *bufWriterAdapter) WriteString(s string) (int, error) { return b.buf.WriteString(s) }
func (b *bufWriterAdapter) Flush() error                      { return nil }
func (b *bufWriterAdapter) Available() int                    { return 0 }
func (b *bufWriterAdapter) Buffered() int                     { return b.buf.Len() }
func (b *bufWriterAdapter) Bytes() []byte                     { return b.buf.Bytes() }
