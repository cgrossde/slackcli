// Package slack — canvashtml.go converts the HTML export produced by
// Slack's canvas (Quip) download into a readable mixed format:
//
//   - Headings        → Markdown  (# / ## / ###)
//   - Paragraphs      → plain text, blank-line separated
//   - Bold/italic/code spans → Markdown (**…** / *…* / `…`)
//   - Unordered lists → Markdown  (- item)
//   - Ordered lists   → Markdown  (1. item)
//   - Tables          → clean HTML (quip id/class/style stripped)
//   - Everything else → plain text, whitespace collapsed
//
// The output is intended for LLM consumption: compact, no noise.
package slack

import (
	"bytes"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// CanvasHTMLToText converts the Quip/Canvas HTML export to a mixed
// Markdown+HTML string suitable for LLM consumption.
// Tables are preserved as clean HTML; all other elements become Markdown.
func CanvasHTMLToText(src string) string {
	doc, err := html.Parse(strings.NewReader(src))
	if err != nil {
		// Unparseable: return raw rather than nothing.
		return src
	}

	var buf bytes.Buffer
	c := &canvasConverter{out: &buf}
	c.walk(doc)

	result := collapseBlankLines(buf.String())
	return strings.TrimSpace(result) + "\n"
}

// canvasConverter holds conversion state.
type canvasConverter struct {
	out       *bytes.Buffer
	listDepth int    // 0 = not in a list; 1+ = nesting level
	listType  []bool // stack: true = ordered, false = unordered
	listIndex []int  // per-level counter for ordered lists
}

func (c *canvasConverter) walk(n *html.Node) {
	switch n.Type {
	case html.DocumentNode:
		c.walkChildren(n)
	case html.ElementNode:
		c.element(n)
	case html.TextNode:
		c.text(n.Data)
	}
}

func (c *canvasConverter) element(n *html.Node) {
	switch n.Data {
	// ── structural pass-through ─────────────────────────────────────────
	case "html", "head", "body", "style", "script", "meta", "link":
		c.walkChildren(n)

	// ── headings ────────────────────────────────────────────────────────
	case "h1", "h2", "h3":
		prefix := map[string]string{"h1": "# ", "h2": "## ", "h3": "### "}[n.Data]
		c.ensureBlankLine()
		c.out.WriteString(prefix)
		c.walkChildren(n)
		c.out.WriteString("\n\n")

	// ── block elements ───────────────────────────────────────────────────
	// div is a generic container — walk structurally so nested tables,
	// headings, and lists are handled by their own cases.
	case "div":
		c.ensureNewline()
		c.walkChildren(n)
		c.ensureNewline()

	// p is a leaf paragraph — walk inline children, then emit a newline.
	case "p":
		var item bytes.Buffer
		child := &canvasConverter{out: &item, listDepth: c.listDepth, listType: c.listType, listIndex: c.listIndex}
		child.walkChildren(n)
		text := strings.TrimSpace(item.String())
		if text != "" {
			c.ensureNewline()
			c.out.WriteString(text)
			c.out.WriteString("\n")
		}

	case "br":
		c.out.WriteString("\n")

	case "hr":
		c.ensureBlankLine()
		c.out.WriteString("---\n\n")

	case "pre":
		c.ensureBlankLine()
		c.out.WriteString("```\n")
		c.out.WriteString(strings.TrimRight(nodeText(n), "\n"))
		c.out.WriteString("\n```\n\n")

	// ── inline formatting ────────────────────────────────────────────────
	case "strong", "b":
		c.out.WriteString("**")
		c.walkChildren(n)
		c.out.WriteString("**")

	case "em", "i":
		c.out.WriteString("*")
		c.walkChildren(n)
		c.out.WriteString("*")

	case "code":
		c.out.WriteString("`")
		c.walkChildren(n)
		c.out.WriteString("`")

	case "s", "del", "strike":
		c.out.WriteString("~~")
		c.walkChildren(n)
		c.out.WriteString("~~")

	case "a":
		href := attrVal(n, "href")
		if href == "" {
			c.walkChildren(n)
			return
		}
		c.out.WriteString("[")
		c.walkChildren(n)
		c.out.WriteString("](")
		c.out.WriteString(href)
		c.out.WriteString(")")

	// ── lists ────────────────────────────────────────────────────────────
	case "ul":
		c.pushList(false)
		c.ensureNewline()
		c.walkChildren(n)
		c.popList()
		if c.listDepth == 0 {
			c.out.WriteString("\n")
		}

	case "ol":
		c.pushList(true)
		c.ensureNewline()
		c.walkChildren(n)
		c.popList()
		if c.listDepth == 0 {
			c.out.WriteString("\n")
		}

	case "li":
		c.writeListItem(n)

	// ── tables → clean HTML ───────────────────────────────────────────────
	case "table":
		c.ensureBlankLine()
		c.out.WriteString(renderCleanTable(n))
		c.out.WriteString("\n")

	// ── default: recurse ─────────────────────────────────────────────────
	default:
		c.walkChildren(n)
	}
}

// writeListItem renders one <li> using a child converter so we can capture
// the item text without disturbing the parent buffer mid-write.
func (c *canvasConverter) writeListItem(n *html.Node) {
	var item bytes.Buffer
	child := &canvasConverter{
		out:       &item,
		listDepth: c.listDepth,
		listType:  c.listType,
		listIndex: c.listIndex,
	}
	child.walkChildren(n)
	// Sync the ordered-list counters back (child may have incremented them).
	c.listIndex = child.listIndex

	line := strings.TrimSpace(item.String())
	indent := strings.Repeat("  ", c.listDepth-1)

	if c.isOrdered() {
		c.listIndex[len(c.listIndex)-1]++
		idx := c.listIndex[len(c.listIndex)-1]
		c.out.WriteString(indent)
		c.out.WriteString(strconv.Itoa(idx))
		c.out.WriteString(". ")
	} else {
		c.out.WriteString(indent)
		c.out.WriteString("- ")
	}
	c.out.WriteString(line)
	c.out.WriteString("\n")
}

func (c *canvasConverter) walkChildren(n *html.Node) {
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		c.walk(ch)
	}
}

func (c *canvasConverter) text(s string) {
	s = collapseWhitespace(s)
	if s != "" {
		c.out.WriteString(s)
	}
}

// ensureNewline guarantees the buffer ends with \n before block content.
func (c *canvasConverter) ensureNewline() {
	b := c.out.Bytes()
	if len(b) > 0 && b[len(b)-1] != '\n' {
		c.out.WriteByte('\n')
	}
}

// ensureBlankLine guarantees the buffer ends with \n\n before block content.
func (c *canvasConverter) ensureBlankLine() {
	b := c.out.Bytes()
	n := len(b)
	switch {
	case n == 0:
	case n == 1 && b[0] == '\n':
		c.out.WriteByte('\n')
	case n >= 2 && b[n-1] == '\n' && b[n-2] == '\n':
		// already blank
	case b[n-1] == '\n':
		c.out.WriteByte('\n')
	default:
		c.out.WriteString("\n\n")
	}
}

func (c *canvasConverter) pushList(ordered bool) {
	c.listDepth++
	c.listType = append(c.listType, ordered)
	c.listIndex = append(c.listIndex, 0)
}

func (c *canvasConverter) popList() {
	if c.listDepth > 0 {
		c.listDepth--
		c.listType = c.listType[:len(c.listType)-1]
		c.listIndex = c.listIndex[:len(c.listIndex)-1]
	}
}

func (c *canvasConverter) isOrdered() bool {
	return len(c.listType) > 0 && c.listType[len(c.listType)-1]
}

// ── table rendering ──────────────────────────────────────────────────────────

// renderCleanTable serialises a <table> node as HTML with quip
// id/class/style attributes stripped. Structural attributes
// (colspan, rowspan, scope, headers) are preserved.
func renderCleanTable(n *html.Node) string {
	var buf bytes.Buffer
	writeCleanNode(&buf, n)
	return buf.String()
}

// structuralAttrs are the only table attributes preserved during clean render.
var structuralAttrs = map[string]bool{
	"colspan": true,
	"rowspan": true,
	"scope":   true,
	"headers": true,
}

// voidElements are HTML elements with no closing tag.
var voidElements = map[string]bool{
	"area": true, "base": true, "br": true, "col": true,
	"embed": true, "hr": true, "img": true, "input": true,
	"link": true, "meta": true, "param": true, "source": true,
	"track": true, "wbr": true,
}

// quipInlineTextTags are Quip-proprietary elements whose text content should
// be rendered verbatim (e.g. <lnk> carries a URL as its text child).
var quipInlineTextTags = map[string]bool{
	"lnk": true,
}

func writeCleanNode(buf *bytes.Buffer, n *html.Node) {
	switch n.Type {
	case html.TextNode:
		buf.WriteString(html.EscapeString(n.Data))
	case html.ElementNode:
		// <lnk> — Quip proprietary link; text content is the URL, render verbatim.
		if quipInlineTextTags[n.Data] {
			buf.WriteString(html.EscapeString(nodeText(n)))
			return
		}
		switch n.Data {
		case "table":
			buf.WriteString("<table>\n")
			for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
				writeCleanNode(buf, ch)
			}
			buf.WriteString("</table>")
		case "thead", "tbody", "tfoot":
			// transparent — just recurse
			for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
				writeCleanNode(buf, ch)
			}
		case "tr":
			buf.WriteString("<tr>")
			for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
				writeCleanNode(buf, ch)
			}
			buf.WriteString("</tr>\n")
		case "td", "th":
			buf.WriteByte('<')
			buf.WriteString(n.Data)
			for _, a := range n.Attr {
				if structuralAttrs[a.Key] {
					buf.WriteByte(' ')
					buf.WriteString(a.Key)
					buf.WriteString(`="`)
					buf.WriteString(html.EscapeString(a.Val))
					buf.WriteByte('"')
				}
			}
			buf.WriteByte('>')
			writeCleanCellContent(buf, n)
			buf.WriteString("</")
			buf.WriteString(n.Data)
			buf.WriteByte('>')
		default:
			buf.WriteByte('<')
			buf.WriteString(n.Data)
			for _, a := range n.Attr {
				if structuralAttrs[a.Key] {
					buf.WriteByte(' ')
					buf.WriteString(a.Key)
					buf.WriteString(`="`)
					buf.WriteString(html.EscapeString(a.Val))
					buf.WriteByte('"')
				}
			}
			if voidElements[n.Data] {
				buf.WriteString(">")
				return
			}
			buf.WriteByte('>')
			for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
				writeCleanNode(buf, ch)
			}
			buf.WriteString("</")
			buf.WriteString(n.Data)
			buf.WriteByte('>')
		}
	}
}

// writeCleanCellContent renders the content of a <td>/<th> compactly:
// <p> wrappers are stripped (their text is inlined), <br> becomes " · ",
// and <lnk> is rendered as plain text.
func writeCleanCellContent(buf *bytes.Buffer, cell *html.Node) {
	first := true
	for ch := cell.FirstChild; ch != nil; ch = ch.NextSibling {
		writeCellNode(buf, ch, &first)
	}
}

func writeCellNode(buf *bytes.Buffer, n *html.Node, first *bool) {
	switch n.Type {
	case html.TextNode:
		t := strings.TrimSpace(n.Data)
		if t != "" {
			if !*first {
				buf.WriteString(" ")
			}
			buf.WriteString(html.EscapeString(t))
			*first = false
		}
	case html.ElementNode:
		switch n.Data {
		case "p", "div", "span":
			// Strip wrapper — inline its children.
			for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
				writeCellNode(buf, ch, first)
			}
		case "br":
			if !*first {
				buf.WriteString(" · ")
				*first = true // next token skips its own leading space
			}
		case "lnk":
			t := strings.TrimSpace(nodeText(n))
			if t != "" {
				if !*first {
					buf.WriteString(" ")
				}
				buf.WriteString(html.EscapeString(t))
				*first = false
			}
		default:
			// For any other inline element, recurse into children.
			for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
				writeCellNode(buf, ch, first)
			}
		}
	}
}

// ── helpers ──────────────────────────────────────────────────────────────────

// nodeText extracts all text from a subtree, collapsing whitespace.
func nodeText(n *html.Node) string {
	var buf bytes.Buffer
	extractText(n, &buf)
	return collapseWhitespace(buf.String())
}

func extractText(n *html.Node, buf *bytes.Buffer) {
	if n.Type == html.TextNode {
		buf.WriteString(n.Data)
	}
	for ch := n.FirstChild; ch != nil; ch = ch.NextSibling {
		extractText(ch, buf)
	}
}

// attrVal returns the value of the named attribute, or "".
func attrVal(n *html.Node, key string) string {
	for _, a := range n.Attr {
		if a.Key == key {
			return a.Val
		}
	}
	return ""
}

// collapseWhitespace replaces runs of whitespace with a single space and
// trims leading/trailing whitespace.
func collapseWhitespace(s string) string {
	var buf bytes.Buffer
	inSpace := false
	for _, r := range s {
		switch r {
		case ' ', '\t', '\n', '\r', '\f', '\v':
			inSpace = true
		default:
			if inSpace && buf.Len() > 0 {
				buf.WriteByte(' ')
			}
			buf.WriteRune(r)
			inSpace = false
		}
	}
	return buf.String()
}

// collapseBlankLines reduces runs of 3+ consecutive blank lines to 2.
func collapseBlankLines(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines))
	blanks := 0
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			blanks++
			if blanks <= 2 {
				out = append(out, l)
			}
		} else {
			blanks = 0
			out = append(out, l)
		}
	}
	return strings.Join(out, "\n")
}
