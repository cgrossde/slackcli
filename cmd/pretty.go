// Package cmd — pretty.go implements the --pretty rendering path for "read".
//
// Output format per message:
//
//	Alice Johnson (u123456)  Yesterday at 07:23
//	<markdown body, word-wrapped at 118, no indent>
//
// User name is bold + per-user blue-green colour. Timestamp is faint.
// Mentions show DisplayName only, coloured by user ID hash.
// Body is rendered by glamour: syntax-highlighted code blocks, bold, italic,
// inline code. Two blank lines separate messages.
package cmd

import (
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	glamour "charm.land/glamour/v2"
	glamstyles "charm.land/glamour/v2/styles"
	goemoji "github.com/kyokomi/emoji/v2"
	lipgloss "charm.land/lipgloss/v2"

	"github.com/cgrossde/slackcli/internal/slack"
)

func init() {
	// No padding after emoji glyphs — the Unicode character is self-spacing.
	goemoji.ReplacePadding = ""
}

// prettyRenderer holds the shared glamour renderer and lipgloss styles so they
// are constructed once per PrettyThread call.
type prettyRenderer struct {
	md          *glamour.TermRenderer
	bold        lipgloss.Style
	faint       lipgloss.Style
	mentionFn   func(id, label string) string
	fileFetcher func(url string) ([]byte, string, error) // nil → skip inline images
}

// userColor is the single fixed colour for all user names.
const userColorHex = "#61AFEF"

// mentionColorHex is a slightly less saturated blue for inline @mentions.
const mentionColorHex = "#4A90C4"

// headerBg is the background colour applied to the full 120-char header line.
const headerBg = "#1E2A35"

// tsFg is the muted foreground for the timestamp — low contrast on headerBg.
const tsFg = "#6B7A8A"

func newPrettyRenderer(hasDark bool) (*prettyRenderer, error) {
	// Copy the base style so we can patch it without mutating the package var.
	cfg := glamstyles.DarkStyleConfig
	if !hasDark {
		cfg = glamstyles.LightStyleConfig
	}

	// Zero document margin — no outer padding.
	zero := uint(0)
	cfg.Document.Margin = &zero
	cfg.Document.BlockPrefix = ""
	cfg.Document.BlockSuffix = ""

	// Inline code: OMP green on a near-black background.
	ompGreen := "#3DDC84"
	codeBg := "#1A1A1A"
	codeSpace := " "
	cfg.Code.Color = &ompGreen
	cfg.Code.BackgroundColor = &codeBg
	cfg.Code.Prefix = codeSpace
	cfg.Code.Suffix = codeSpace

	// Code blocks: margin 0, sentinels on prefix/suffix so we can locate every
	// block in the rendered output and add the accent stripe ourselves.
	blockBg := "#212121"
	cfg.CodeBlock.BackgroundColor = &blockBg
	cfg.CodeBlock.Margin = &zero
	cfg.CodeBlock.BlockPrefix = codeBlockSentinelOpen
	cfg.CodeBlock.BlockSuffix = codeBlockSentinelClose
	if cfg.CodeBlock.Chroma != nil {
		cfg.CodeBlock.Chroma.Background.BackgroundColor = &blockBg
	}

	r, err := glamour.NewTermRenderer(
		glamour.WithStyles(cfg),
		glamour.WithWordWrap(118),
		glamour.WithPreservedNewLines(),
	)
	if err != nil {
		return nil, fmt.Errorf("creating glamour renderer: %w", err)
	}

	pr := &prettyRenderer{
		md:    r,
		bold:  lipgloss.NewStyle().Bold(true),
		faint: lipgloss.NewStyle().Faint(true),
		mentionFn: func(_, label string) string {
			return lipgloss.NewStyle().Foreground(lipgloss.Color(mentionColorHex)).Render("@" + label)
		},
	}
	return pr, nil
}

// codeBlockSentinelOpen and codeBlockSentinelClose are NUL-delimited tokens
// embedded by glamour via CodeBlock.BlockPrefix/BlockSuffix. They appear
// verbatim in the rendered ANSI output and are used to locate code block
// extents reliably — no heuristic matching of leading spaces.
const (
	codeBlockSentinelOpen  = "\x00CODESTART\x00"
	codeBlockSentinelClose = "\x00CODEEND\x00"
)

// accentCodeBlocks finds every code block region in the glamour-rendered string
// (delimited by the sentinels), prepends a headerBg-coloured space to each
// non-empty line inside, and removes the sentinel markers.
//
// The accent space gives each code block line a left-edge stripe in the same
// colour as the message header, visually framing the block.
func accentCodeBlocks(s string) string {
	stripe := lipgloss.NewStyle().Background(lipgloss.Color(headerBg)).Render(" ")

	var out strings.Builder
	remaining := s
	for {
		openIdx := strings.Index(remaining, codeBlockSentinelOpen)
		if openIdx < 0 {
			// No more code blocks — write the rest verbatim.
			out.WriteString(remaining)
			break
		}
		// Write everything before this block unchanged.
		out.WriteString(remaining[:openIdx])
		after := remaining[openIdx+len(codeBlockSentinelOpen):]

		closeIdx := strings.Index(after, codeBlockSentinelClose)
		if closeIdx < 0 {
			// Malformed (no closing sentinel) — write remainder as-is.
			out.WriteString(after)
			break
		}
		blockContent := after[:closeIdx]
		remaining = after[closeIdx+len(codeBlockSentinelClose):]

		// Prepend accent stripe to every line that has visible content.
		// Lines that contain only ANSI escape sequences (glamour's trailing
		// reset line after the block) are left bare — they become the blank
		// separator line after the block.
		lines := strings.Split(blockContent, "\n")
		for i, line := range lines {
			if hasPrintable(line) {
				lines[i] = stripe + "  " + line
			}
		}
		out.WriteString(strings.Join(lines, "\n"))
	}
	return out.String()
}

// hasPrintable reports whether s contains at least one printable rune outside
// ANSI escape sequences. Used to distinguish real content lines from
// ANSI-only reset lines that glamour emits at code block boundaries.
func hasPrintable(s string) bool {
	inEsc := false
	for _, r := range s {
		if inEsc {
			if (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z') {
				inEsc = false
			}
			continue
		}
		if r == '\x1b' {
			inEsc = true
			continue
		}
		if r > ' ' {
			return true
		}
	}
	return false
}

// PrettyThread renders messages as a human-readable, ANSI-styled block.
// fileFetcher, when non-nil, is called to download image file attachments for
// inline rendering (iTerm2 protocol). Pass nil to skip image rendering.
// dmPeer is the resolved display name of the DM conversation partner (non-empty
// only for 1:1 DM channels); pass "" for regular channels.
func PrettyThread(messages []slack.Message, cache *slack.UserCache, fileFetcher func(url string) ([]byte, string, error), selfID, dmPeer string) (string, error) {
	hasDark := lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
	pr, err := newPrettyRenderer(hasDark)
	if err != nil {
		return "", err
	}
	pr.fileFetcher = fileFetcher

	var b strings.Builder
	for i, m := range messages {
		if i > 0 {
			b.WriteString("\n")
		}
		rendered, err := pr.renderMessage(m, cache, selfID, dmPeer)
		if err != nil {
			return "", fmt.Errorf("rendering message %d: %w", i, err)
		}
		b.WriteString(rendered)
	}
	return b.String(), nil
}

func (pr *prettyRenderer) renderMessage(m slack.Message, cache *slack.UserCache, selfID, dmPeer string) (string, error) {
	var b strings.Builder

	// ── Header line — filled to 120 cols with headerBg background ────────────
	// Each segment must carry the headerBg itself so ANSI resets don't produce
	// a visible seam. Width(120) then fills trailing space with the same bg.
	author, _ := resolveAuthorPretty(m, cache)
	isSelf := selfID != "" && m.User == selfID
	if isSelf {
		author = "You"
	}
	// For DM channels, replace the bare author with the directional DM label so
	// the header shows e.g. "DM: You → Alice" or "DM: Alice → You".
	// ShortLabel (no handle suffix) keeps the label compact — peer name from
	// resolveDMPeer already uses ShortLabel.
	if dmPeer != "" {
		senderShort := author
		if !isSelf && cache != nil && m.User != "" {
			if u, err := cache.GetUser(m.User); err == nil {
				senderShort = u.ShortLabel()
			}
		}
		author = dmLabel(isSelf, senderShort, dmPeer)
	}
	authorStyled := lipgloss.NewStyle().
		Background(lipgloss.Color(headerBg)).
		Foreground(lipgloss.Color(userColorHex)).
		Bold(true).
		Render(author)
	tsStyled := lipgloss.NewStyle().
		Background(lipgloss.Color(headerBg)).
		Foreground(lipgloss.Color(tsFg)).
		Render("  " + humanTime(m.Ts))
	headerLine := lipgloss.NewStyle().
		Background(lipgloss.Color(headerBg)).
		Width(120).
		Render(authorStyled + tsStyled)
	fmt.Fprintf(&b, "%s\n", headerLine)

	// ── Body ─────────────────────────────────────────────────────────────────
	md := slackMrkdwnToMarkdown(m.Text)

	rendered, err := pr.md.Render(md)
	if err != nil {
		// Graceful fallback: print raw text with no indentation.
		for _, line := range strings.Split(m.Text, "\n") {
			fmt.Fprintf(&b, "%s\n", line)
		}
		return b.String(), nil
	}
	// Resolve <@ID> mentions in the rendered ANSI output.
	rendered = resolveMentionsPretty(rendered, cache, pr.mentionFn)
	// Paint the accent stripe on every line inside code blocks, then strip sentinels.
	rendered = accentCodeBlocks(rendered)
	// Strip trailing blank lines; PrettyThread controls inter-message spacing.
	rendered = strings.TrimRight(rendered, "\n")
	b.WriteString(rendered)
	b.WriteString("\n")

	// ── Files ────────────────────────────────────────────────────────────────
	for _, f := range m.Files {
		name := f.Name
		if name == "" {
			name = f.Title
		}
		typ := f.PrettyType
		if typ == "" {
			typ = f.Mimetype
		}
		// Prefer url_private for download (full resolution); use permalink for display.
		downloadURL := f.URLPrivate
		if downloadURL == "" {
			downloadURL = f.Permalink
		}

		// Attempt inline image rendering when the terminal supports it and the
		// file is an image type. Use f.Mimetype (e.g. "image/png") for the MIME
		// check — typ may be PrettyType ("PNG") which is not a MIME string.
		if pr.fileFetcher != nil && supportsInlineImages() && isImageMIME(f.Mimetype) && downloadURL != "" {
			data, _, err := pr.fileFetcher(downloadURL)
			if err == nil {
				pxW, _, ok := termPixelWidth()
				widthPct := 0
				if ok {
					widthPct = imageNaturalPct(data, pxW)
				}
				b.WriteString("  ")
				b.WriteString(pr.faint.Render(name))
				b.WriteString("\n")
				b.WriteString(iTerm2InlineImage(name, data, widthPct))
				continue
			}
			// Fetcher failed — fall through to text line.
		}

		// Text fallback: "  📎 name (type)  url"
		var fileLine strings.Builder
		fileLine.WriteString("  📎 ")
		fileLine.WriteString(name)
		if typ != "" {
			fmt.Fprintf(&fileLine, " (%s)", typ)
		}
		displayURL := f.Permalink
		if displayURL == "" {
			displayURL = f.URLPrivate
		}
		if displayURL != "" {
			fmt.Fprintf(&fileLine, "  %s", pr.faint.Render(displayURL))
		}
		b.WriteString(fileLine.String())
		b.WriteByte('\n')
	}

	// ── Reactions ────────────────────────────────────────────────────────────
	if len(m.Reactions) > 0 {
		b.WriteString("  ")
		b.WriteString(pr.faint.Render("Reactions:"))
		b.WriteString(" ")
		for i, r := range m.Reactions {
			if i > 0 {
				b.WriteString("  ")
			}
			isSelf := false
			if selfID != "" {
				for _, uid := range r.Users {
					if uid == selfID {
						isSelf = true
						break
					}
				}
			}
			emoji := goemoji.Sprint(":" + r.Name + ":")
			switch {
			case isSelf && r.Count == 1:
				fmt.Fprintf(&b, "%s 1 (you)", emoji)
			case isSelf:
				fmt.Fprintf(&b, "%s %d (you + %d others)", emoji, r.Count, r.Count-1)
			default:
				fmt.Fprintf(&b, "%s %d", emoji, r.Count)
			}
		}
		b.WriteByte('\n')
	}

	return b.String(), nil
}

// resolveAuthorPretty returns (display label, user ID for colour hashing).
func resolveAuthorPretty(m slack.Message, cache *slack.UserCache) (label, id string) {
	if m.User != "" {
		if cache != nil {
			if u, err := cache.GetUser(m.User); err == nil {
				return u.Label(), u.ID
			}
		}
		return m.User, m.User
	}
	if m.Username != "" {
		return m.Username + " (bot)", m.Username
	}
	if m.BotID != "" {
		return m.BotID + " (bot)", m.BotID
	}
	return "(unknown)", ""
}

// humanTime converts a Slack ts ("1778570615.840589") to a relative, readable
// string anchored to now (UTC).
//
//	< 1 min  → "just now"
//	< 1 hr   → "42 minutes ago"
//	today    → "Today at 07:23"
//	yesterday→ "Yesterday at 20:15"
//	≤ 6 days → "Monday at 14:00"
//	< 1 yr   → "April 10 at 02:22"
//	older    → "2024-04-10 at 02:22"
func humanTime(ts string) string {
	f, err := strconv.ParseFloat(ts, 64)
	if err != nil {
		return ts
	}
	t := time.Unix(int64(math.Trunc(f)), 0).UTC()
	now := time.Now().UTC()
	diff := now.Sub(t)

	switch {
	case diff < time.Minute:
		return "just now"
	case diff < time.Hour:
		mins := int(diff.Minutes())
		if mins == 1 {
			return "1 minute ago"
		}
		return fmt.Sprintf("%d minutes ago", mins)
	default:
		// Compare calendar days in UTC.
		ny, nm, nd := now.Date()
		ty, tm, td := t.Date()
		todayOrd := ny*10000 + int(nm)*100 + nd
		msgOrd := ty*10000 + int(tm)*100 + td
		dayDiff := todayOrd - msgOrd

		hhmm := t.Format("15:04")
		switch dayDiff {
		case 0:
			return "Today at " + hhmm
		case 1:
			return "Yesterday at " + hhmm
		case 2, 3, 4, 5, 6:
			return t.Weekday().String() + " at " + hhmm
		default:
			if ty == ny {
				return fmt.Sprintf("%s %s at %s", t.Month().String(), ordinal(td), hhmm)
			}
			return fmt.Sprintf("%d-%02d-%02d at %s", ty, int(tm), td, hhmm)
		}
	}
}

func ordinal(n int) string {
	switch {
	case n%100 >= 11 && n%100 <= 13:
		return fmt.Sprintf("%dth", n)
	case n%10 == 1:
		return fmt.Sprintf("%dst", n)
	case n%10 == 2:
		return fmt.Sprintf("%dnd", n)
	case n%10 == 3:
		return fmt.Sprintf("%drd", n)
	default:
		return fmt.Sprintf("%dth", n)
	}
}

// slackMrkdwnToMarkdown converts Slack mrkdwn to CommonMark so glamour can
// render it correctly.
//
// Translations:
//   - &amp; &lt; &gt;  → & < >
//   - ```content```     → properly fenced CommonMark code block (no lang tag)
//   - > line\nnext      → blockquote terminated by blank line
//   - :emoji:           → Unicode glyph (skips code spans/fences)
//   - *text*            → **text**   (Slack bold → MD bold)
//   - ~text~            → ~~text~~   (Slack strikethrough → MD strikethrough)
//   - <URL|text>        → [text](URL)
//   - <URL>             → <URL>
//   - <@ID>             → left as-is (resolved post-render by resolveMentionsPretty)
func slackMrkdwnToMarkdown(s string) string {
	// HTML entities first (must precede fence normalisation so &gt; in quoted
	// code blocks is decoded before we inspect for ```).
	s = strings.ReplaceAll(s, "&amp;", "&")
	s = strings.ReplaceAll(s, "&lt;", "<")
	s = strings.ReplaceAll(s, "&gt;", ">")

	// Normalise Slack triple-backtick blocks to proper CommonMark fences.
	// Must run before bold/strike so backtick spans aren't mangled.
	s = normaliseCodeFences(s)

	// Insert blank line after blockquote runs so goldmark terminates them.
	s = ensureBlockquoteTerminators(s)

	// <URL|text> → [text](URL)
	s = linkRe.ReplaceAllStringFunc(s, func(match string) string {
		inner := match[1 : len(match)-1] // strip < >
		if idx := strings.IndexByte(inner, '|'); idx >= 0 {
			url := inner[:idx]
			text := inner[idx+1:]
			return "[" + text + "](" + url + ")"
		}
		return match // plain <URL> — leave for MD auto-linking
	})

	// Slack bold: *text* → **text** (rune-walk skips code spans).
	s = convertSlackBold(s)

	// Slack strikethrough: ~text~ → ~~text~~ (rune-walk skips code spans).
	s = convertSlackStrike(s)

	// :emoji: → Unicode glyph, skipping code fences and inline code spans.
	s = resolveEmoji(s)

	return s
}

// normaliseCodeFences ensures Slack triple-backtick code blocks are valid
// CommonMark fenced code blocks.
//
// Slack sends: ```content\nmore content```
// — opener immediately followed by content, closer immediately after last line.
// CommonMark requires ``` alone on its own line.
//
// Slack does not use language tags, so the entire post-``` token is content.
func normaliseCodeFences(s string) string {
	// Opener: ``` immediately followed by non-backtick, non-newline content →
	// split to ```\ncontent.
	s = openFenceRe.ReplaceAllString(s, "```\n$1")
	// Closer: content immediately followed by ``` → split to content\n```.
	s = closeFenceRe.ReplaceAllString(s, "$1\n```")
	return s
}

// ensureBlockquoteTerminators inserts a blank line after a run of "> " lines
// when the next line is non-empty and not itself a blockquote. Without this,
// goldmark (with PreservedNewLines) folds the following paragraph into the
// blockquote block.
func ensureBlockquoteTerminators(s string) string {
	lines := strings.Split(s, "\n")
	out := make([]string, 0, len(lines)+4)
	for i, line := range lines {
		out = append(out, line)
		if strings.HasPrefix(line, ">") {
			next := ""
			if i+1 < len(lines) {
				next = lines[i+1]
			}
			if next != "" && !strings.HasPrefix(next, ">") {
				out = append(out, "")
			}
		}
	}
	return strings.Join(out, "\n")
}

// openFenceRe matches ``` immediately followed by content on the same line.
// Capture group 1 = first content line (no lang tag extracted).
var openFenceRe = regexp.MustCompile("```([^`\\n][^\\n]*)")

// closeFenceRe matches content immediately followed by ```.
var closeFenceRe = regexp.MustCompile("([^`\\n])```")

// resolveEmoji replaces :name: tokens with their Unicode emoji, skipping
// content inside ``` fences and inline ` code spans.
func resolveEmoji(s string) string {
	var out strings.Builder
	inFence := false
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		if i > 0 {
			out.WriteByte('\n')
		}
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			inFence = !inFence
			out.WriteString(line)
			continue
		}
		if inFence {
			out.WriteString(line)
			continue
		}
		// Outside fences: resolve emoji but skip inline code spans.
		out.WriteString(resolveEmojiInLine(line))
	}
	return out.String()
}

// resolveEmojiInLine resolves :name: tokens in a single line, skipping content
// inside backtick code spans.
func resolveEmojiInLine(line string) string {
	var out strings.Builder
	inCode := false
	parts := strings.Split(line, "`")
	for i, part := range parts {
		if i%2 == 1 {
			// Inside a code span (odd segment between backticks).
			out.WriteByte('`')
			out.WriteString(part)
			inCode = true
		} else {
			if inCode {
				out.WriteByte('`')
				inCode = false
			}
			out.WriteString(goemoji.Sprint(part))
		}
	}
	// Restore unclosed backtick if line has odd number.
	if inCode {
		out.WriteByte('`')
	}
	return out.String()
}

// linkRe matches Slack hyperlinks: <URL> or <URL|text> but not <@ID> or <!here>.
var linkRe = regexp.MustCompile(`<(https?://[^>|]+)(?:\|[^>]*)?>`)

// convertSlackStrike converts ~text~ → ~~text~~ while leaving ~~text~~ alone
// and skipping content inside inline code spans.
func convertSlackStrike(s string) string {
	var out strings.Builder
	inCode := false
	runes := []rune(s)
	n := len(runes)
	for i := 0; i < n; i++ {
		c := runes[i]
		if c == '`' {
			inCode = !inCode
			out.WriteRune(c)
			continue
		}
		if inCode {
			out.WriteRune(c)
			continue
		}
		// Already doubled: pass through.
		if c == '~' && i+1 < n && runes[i+1] == '~' {
			out.WriteString("~~")
			i++
			continue
		}
		// Single ~: find closing single ~.
		if c == '~' {
			j := i + 1
			for j < n && runes[j] != '~' && runes[j] != '\n' {
				j++
			}
			if j < n && runes[j] == '~' && j > i+1 {
				// Check it's not ~~.
				if j+1 >= n || runes[j+1] != '~' {
					out.WriteString("~~")
					out.WriteString(string(runes[i+1 : j]))
					out.WriteString("~~")
					i = j
					continue
				}
			}
		}
		out.WriteRune(c)
	}
	return out.String()
}


// convertSlackBold converts *word* or *phrase* → **word** avoiding code spans
// and already-doubled **.
func convertSlackBold(s string) string {
	var out strings.Builder
	inCode := false
	runes := []rune(s)
	n := len(runes)

	for i := 0; i < n; i++ {
		c := runes[i]

		// Track inline code spans.
		if c == '`' {
			inCode = !inCode
			out.WriteRune(c)
			continue
		}
		if inCode {
			out.WriteRune(c)
			continue
		}

		// Detect **: write as-is.
		if c == '*' && i+1 < n && runes[i+1] == '*' {
			out.WriteString("**")
			i++
			continue
		}

		// Single *: look ahead for closing * with non-space content between.
		if c == '*' {
			j := i + 1
			// Skip leading space — not a valid bold open.
			if j < n && runes[j] == ' ' {
				out.WriteRune(c)
				continue
			}
			// Find closing *.
			for j < n && runes[j] != '*' && runes[j] != '\n' {
				j++
			}
			if j < n && runes[j] == '*' && j > i+1 && !unicode.IsSpace(runes[j-1]) {
				// Valid Slack bold span.
				out.WriteString("**")
				out.WriteString(string(runes[i+1 : j]))
				out.WriteString("**")
				i = j
				continue
			}
		}

		out.WriteRune(c)
	}
	return out.String()
}

// mentionIDRe matches <@WXXXX> or <@UXXXX> in already-translated markdown.
var mentionIDRe = regexp.MustCompile(`<@([WU][A-Z0-9]+)>`)

// resolveMentionsPretty replaces <@ID> tokens with ANSI-styled mention strings.
// It operates on the markdown source before passing to glamour; glamour will
// pass the ANSI escapes through verbatim inside paragraph text.
func resolveMentionsPretty(md string, cache *slack.UserCache, style func(id, label string) string) string {
	return mentionIDRe.ReplaceAllStringFunc(md, func(match string) string {
		id := match[2 : len(match)-1]
		label := id
		if cache != nil {
			if u, err := cache.GetUser(id); err == nil {
				label = u.ShortLabel()
			}
		}
		return style(id, label)
	})
}
