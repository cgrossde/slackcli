// Package slack — whitelist.go defines the set of channel IDs to which write
// operations (send message, add reaction, delete, forward, snippet) are permitted.
//
// The allowlist is loaded at init time from allowlist.txt, embedded into the
// binary at build time via go:embed. Create allowlist.txt (gitignored) next to
// this file before building; see allowlist.txt.example for the format.
// If allowlist.txt is absent the embed is an empty string and all write
// operations are denied — the binary is safe to distribute without it.
package slack

import (
	_ "embed"
	"strings"
)

//go:embed allowlist.txt
var allowlistEmbed string

// AllowedWriteChannels is populated at init time from allowlist.txt.
var AllowedWriteChannels map[string]bool

func init() {
	AllowedWriteChannels = make(map[string]bool)
	for _, line := range strings.Split(allowlistEmbed, "\n") {
		// Strip inline comments (# …) and surrounding whitespace.
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		id := strings.TrimSpace(line)
		if id != "" {
			AllowedWriteChannels[id] = true
		}
	}
}

// IsWriteAllowed reports whether channelID is in AllowedWriteChannels.
func IsWriteAllowed(channelID string) bool {
	return AllowedWriteChannels[channelID]
}
