package cmd

import (
	"bytes"
	"encoding/base64"
	"fmt"
	"image"
	_ "image/gif"
	_ "image/jpeg"
	_ "image/png"
	"os"
	"strings"

	"golang.org/x/sys/unix"
)

// iTerm2InlineImage returns the ESC sequence that renders data as an inline
// image in iTerm2 (protocol: https://iterm2.com/documentation-images.html).
//
// The sequence is:
//
//	ESC ] 1337 ; File=inline=1;size=N;name=<b64name>;width=N% : <b64data> BEL
//
// widthPct is the desired display width as a percentage of the terminal width
// (1–100). Pass 0 to let iTerm2 use its default ("auto").
// Using percentages is DPI-agnostic: iTerm2 maps them to the logical character
// grid, so Retina displays render at the correct physical size automatically.
// A trailing newline is appended so the next output starts on a fresh line.
func iTerm2InlineImage(filename string, data []byte, widthPct int) string {
	b64name := base64.StdEncoding.EncodeToString([]byte(filename))
	b64data := base64.StdEncoding.EncodeToString(data)
	width := "auto"
	if widthPct > 0 {
		width = fmt.Sprintf("%d%%", widthPct)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "\x1b]1337;File=inline=1;size=%d;name=%s;width=%s:%s\a\n",
		len(data), b64name, width, b64data)
	return b.String()
}

// supportsInlineImages reports whether the current terminal supports the
// iTerm2 inline image protocol.
func supportsInlineImages() bool {
	// LC_TERMINAL is set by iTerm2 and JetBrains terminals that support the protocol.
	if os.Getenv("LC_TERMINAL") == "iTerm2" {
		return true
	}
	// TERM_PROGRAM fallback — older iTerm2 versions / some CI contexts.
	if os.Getenv("TERM_PROGRAM") == "iTerm.app" {
		return true
	}
	return false
}

// isImageMIME reports whether mimeType is an image format suitable for
// inline rendering (image/png, image/jpeg, image/gif, image/webp, etc.).
func isImageMIME(mimeType string) bool {
	return strings.HasPrefix(mimeType, "image/")
}

// termPixelWidth returns the terminal's pixel width and column count by
// querying /dev/tty via TIOCGWINSZ. Returns (0, 0, false) if the query fails
// (e.g. not running in a real terminal, or pixel dimensions are unavailable).
func termPixelWidth() (pxWidth, cols int, ok bool) {
	tty, err := os.Open("/dev/tty")
	if err != nil {
		return 0, 0, false
	}
	defer tty.Close()
	ws, err := unix.IoctlGetWinsize(int(tty.Fd()), unix.TIOCGWINSZ)
	if err != nil || ws.Col == 0 || ws.Xpixel == 0 {
		return 0, 0, false
	}
	return int(ws.Xpixel), int(ws.Col), true
}

// imageNaturalPct returns the percentage of terminal width that the image
// would occupy at its natural pixel size, given the terminal's physical pixel
// width. The result is capped at 100.
//
// Using percentages (rather than character-cell counts) makes the result
// DPI-agnostic: iTerm2 resolves percentages against the logical character grid
// so Retina scaling is handled by the terminal, not by us.
//
// Returns 0 if the image dimensions cannot be decoded or terminal size is unknown.
func imageNaturalPct(data []byte, termPxWidth int) int {
	if termPxWidth == 0 {
		return 0
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width == 0 {
		return 0
	}
	pct := int(float64(cfg.Width) / float64(termPxWidth) * 100 * 2)
	if pct > 100 {
		pct = 100
	}
	if pct < 1 {
		pct = 1
	}
	return pct
}
