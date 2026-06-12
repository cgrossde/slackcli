// Package slack — url.go parses Slack message references in two forms:
//
//   - Permalink URL: https://<workspace>.slack.com/archives/<channelID>/<pTIMESTAMP>
//   - Compact ref:   <channelID>:<ts>  (e.g. C012ABC3456:1718197925.001234)
//
// It also parses Slack file permalink URLs:
//
//   - File permalink: https://<workspace>.slack.com/files/<userID>/<fileID>/<name>
//
// Both forms resolve to a MessageRef or FileRef used throughout the codebase.
package slack

import (
	"fmt"
	"net/url"
	"strings"
)

// MessageRef identifies a Slack message by the triple (Workspace, ChannelID,
// Ts) that every Slack API method uses. It is the canonical in-memory
// representation regardless of how the reference was provided by the user.
//
// When parsed from a permalink URL, all three fields are populated.
// When parsed from a compact "channelID:ts" token, Workspace is left empty
// and must be filled in by the caller (e.g. from --workspace or the stored
// default).
type MessageRef struct {
	// Workspace is the bare domain, e.g. "myorg.slack.com".
	Workspace string
	// ChannelID is the channel or DM ID, e.g. "C01234567" or "D0B22865CQ4".
	ChannelID string
	// Ts is the API-format message timestamp, e.g. "1752672853.184209".
	Ts string
	// ThreadTs is the parent thread timestamp when Ts refers to a thread reply
	// (i.e. the URL contained a ?thread_ts= query parameter). Empty for
	// top-level channel messages.
	ThreadTs string
}

// ParseMessageRef parses any accepted message reference form:
//   - Permalink URL:  https://<workspace>.slack.com/archives/<ch>/<pTS>
//   - Compact ref:    <channelID>:<ts>
//
// For the compact form, Workspace is left empty; the caller must fill it in.
func ParseMessageRef(raw string) (MessageRef, error) {
	if IsChannelTs(raw) {
		return ParseChannelTs(raw)
	}
	return ParseSlackURL(raw)
}

// ParseSlackURL parses a Slack message permalink URL and returns a MessageRef
// with all three fields populated.
//
// Accepted form:
//
//	https://myorg.slack.com/archives/CHANNEL/pTIMESTAMP
func ParseSlackURL(raw string) (MessageRef, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return MessageRef{}, fmt.Errorf("invalid URL %q: %w", raw, err)
	}

	if u.Scheme != "https" && u.Scheme != "http" {
		return MessageRef{}, fmt.Errorf("invalid Slack URL %q: scheme must be http(s)", raw)
	}

	if !strings.HasSuffix(u.Host, ".slack.com") {
		return MessageRef{}, fmt.Errorf("invalid Slack URL %q: host %q does not end with .slack.com", raw, u.Host)
	}

	// Path must be /archives/<channelID>/<pTIMESTAMP>
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 3 || parts[0] != "archives" {
		// Distinguish the common case of a channel URL (no timestamp segment)
		// from a genuinely malformed path.
		if len(parts) == 2 && parts[0] == "archives" && parts[1] != "" {
			return MessageRef{}, fmt.Errorf(
				"%q looks like a channel link, not a message permalink — open the message in Slack and copy its link (right-click the message → Copy link)",
				raw,
			)
		}
		return MessageRef{}, fmt.Errorf("invalid Slack URL %q: expected path /archives/<channel>/<pTS>", raw)
	}

	channelID := parts[1]
	tsRaw := parts[2]
	if channelID == "" {
		return MessageRef{}, fmt.Errorf("invalid Slack URL %q: missing channel ID", raw)
	}

	// tsRaw must start with "p" and have at least 8 chars (≥2 integer digits + 6 decimal).
	if !strings.HasPrefix(tsRaw, "p") || len(tsRaw) < 8 {
		return MessageRef{}, fmt.Errorf("invalid Slack URL %q: timestamp segment %q must start with 'p' and be at least 8 chars", raw, tsRaw)
	}
	digits := tsRaw[1:] // strip "p"
	if len(digits) <= 6 {
		return MessageRef{}, fmt.Errorf("invalid Slack URL %q: timestamp %q too short", raw, tsRaw)
	}
	ts := digits[:len(digits)-6] + "." + digits[len(digits)-6:]

	return MessageRef{
		Workspace: u.Host,
		ChannelID: channelID,
		Ts:        ts,
		ThreadTs:  u.Query().Get("thread_ts"),
	}, nil
}

// ParseChannelTs parses the compact "channelID:ts" form and its three-part
// "channelID:threadTs:replyTs" variant.
//
// Accepted forms:
//
//	C012ABC3456:1718197925.001234
//	C012ABC3456:1718197000.000001:1718197925.001234
//
// Two-part form: Ts is the message timestamp; ThreadTs is empty.
// Three-part form: ThreadTs is the thread root; Ts is the reply timestamp.
// In both forms the channel ID must start with C, D, G, or W and every
// timestamp must contain a '.' with digits on both sides.
// Workspace is left empty; callers must supply it from context.
func ParseChannelTs(raw string) (MessageRef, error) {
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return MessageRef{}, fmt.Errorf("ParseChannelTs: %q looks like a URL; use ParseSlackURL instead", raw)
	}

	idx := strings.IndexByte(raw, ':')
	if idx < 0 {
		return MessageRef{}, fmt.Errorf("invalid channel:ts %q: missing ':'", raw)
	}

	channelID := raw[:idx]
	rest := raw[idx+1:]

	if channelID == "" {
		return MessageRef{}, fmt.Errorf("invalid channel:ts %q: empty channel ID", raw)
	}
	if rest == "" {
		return MessageRef{}, fmt.Errorf("invalid channel:ts %q: empty timestamp", raw)
	}

	switch channelID[0] {
	case 'C', 'D', 'G', 'W':
	default:
		return MessageRef{}, fmt.Errorf("invalid channel:ts %q: channel ID must start with C, D, G, or W", raw)
	}

	// Determine whether this is the two-part or three-part form by looking for
	// a second colon in the remainder.
	var threadTs, ts string
	if idx2 := strings.IndexByte(rest, ':'); idx2 >= 0 {
		// Three-part: channelID:threadTs:replyTs
		threadTs = rest[:idx2]
		ts = rest[idx2+1:]
		if threadTs == "" {
			return MessageRef{}, fmt.Errorf("invalid channel:ts %q: empty thread timestamp", raw)
		}
		if ts == "" {
			return MessageRef{}, fmt.Errorf("invalid channel:ts %q: empty reply timestamp", raw)
		}
		if err := validateTs(threadTs, raw); err != nil {
			return MessageRef{}, err
		}
		threadTs = normalizeTs(threadTs)
	} else {
		ts = rest
	}

	if err := validateTs(ts, raw); err != nil {
		return MessageRef{}, err
	}
	ts = normalizeTs(ts)

	return MessageRef{
		ChannelID: channelID,
		ThreadTs:  threadTs,
		Ts:        ts,
	}, nil
}

// normalizeTs converts a dot-free p-form timestamp (e.g. "1780412248027909")
// to the API form ("1780412248.027909"). Timestamps that already contain a dot
// are returned unchanged. It must only be called after validateTs succeeds.
func normalizeTs(ts string) string {
	if strings.IndexByte(ts, '.') >= 0 {
		return ts
	}
	return ts[:len(ts)-6] + "." + ts[len(ts)-6:]
}

// validateTs checks that ts is a valid Slack timestamp (digits around a single dot).
// It also accepts the dot-free "p-form" digit string that Slack uses in permalink
// URLs and that users paste directly (e.g. "1780412248027909"), normalising it to
// "1780412248.027909" by treating the last 6 digits as the fractional part — the
// same rule ParseSlackURL applies to the <pTIMESTAMP> path segment.
func validateTs(ts, raw string) error {
	// Fast path: already contains a dot — validate the conventional form.
	dotIdx := strings.IndexByte(ts, '.')
	if dotIdx >= 0 {
		if dotIdx < 1 || dotIdx == len(ts)-1 {
			return fmt.Errorf("invalid channel:ts %q: timestamp %q must contain '.' with digits on both sides", raw, ts)
		}
		for _, r := range ts {
			if r != '.' && (r < '0' || r > '9') {
				return fmt.Errorf("invalid channel:ts %q: timestamp %q contains non-numeric character", raw, ts)
			}
		}
		return nil
	}

	// No dot: accept an all-digit string with more than 6 digits (p-form convention).
	if len(ts) <= 6 {
		return fmt.Errorf("invalid channel:ts %q: timestamp %q must contain '.' with digits on both sides", raw, ts)
	}
	for _, r := range ts {
		if r < '0' || r > '9' {
			return fmt.Errorf("invalid channel:ts %q: timestamp %q contains non-numeric character", raw, ts)
		}
	}
	return nil
}

// IsChannelTs reports whether raw looks like a "channelID:ts" token rather
// than a URL. It is a fast heuristic: the first character must be a valid
// Slack channel ID prefix (C, D, G, W) and the string must contain ':'.
func IsChannelTs(raw string) bool {
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		return false
	}
	if len(raw) < 2 {
		return false
	}
	ch := raw[0]
	if ch != 'C' && ch != 'D' && ch != 'G' && ch != 'W' {
		return false
	}
	return strings.IndexByte(raw, ':') >= 1
}

// FileRef identifies a Slack file by the (Workspace, FileID, Filename) triple.
type FileRef struct {
	Workspace string // e.g. "myorg.slack.com"
	FileID    string // e.g. "F0B3HRU6ZA7"
	Filename  string // e.g. "image.png" (may be empty if URL has no name segment)
}

// ParseFileRef parses a Slack file permalink URL.
//
// Accepted form:
//
//	https://myorg.slack.com/files/<userID>/<fileID>[/<filename>]
//
// The userID segment is ignored; only FileID and Filename are extracted.
func ParseFileRef(raw string) (FileRef, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return FileRef{}, fmt.Errorf("invalid URL %q: %w", raw, err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return FileRef{}, fmt.Errorf("invalid Slack file URL %q: scheme must be http(s)", raw)
	}
	if !strings.HasSuffix(u.Host, ".slack.com") {
		return FileRef{}, fmt.Errorf("invalid Slack file URL %q: host %q does not end with .slack.com", raw, u.Host)
	}
	// Path: /files/<userID>/<fileID>[/<filename>]
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 3 || parts[0] != "files" {
		return FileRef{}, fmt.Errorf("invalid Slack file URL %q: expected path /files/<userID>/<fileID>[/<name>]", raw)
	}
	fileID := parts[2]
	if fileID == "" {
		return FileRef{}, fmt.Errorf("invalid Slack file URL %q: missing file ID", raw)
	}
	filename := ""
	if len(parts) >= 4 {
		filename = parts[3]
	}
	return FileRef{
		Workspace: u.Host,
		FileID:    fileID,
		Filename:  filename,
	}, nil
}

// IsFileURL reports whether raw looks like a Slack file permalink URL
// (contains "/files/" in the path).
func IsFileURL(raw string) bool {
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	parts := strings.SplitN(strings.Trim(u.Path, "/"), "/", 3)
	return len(parts) >= 2 && parts[0] == "files"
}

// ChannelRef identifies a Slack channel by workspace and channel ID.
// Parsed from a channel-only URL (no timestamp segment).
type ChannelRef struct {
	Workspace string // e.g. "myorg.slack.com"
	ChannelID string // e.g. "C0B3Z1KT80K"
}

// ParseChannelURL parses a Slack channel URL (no timestamp segment).
// Returns a ChannelRef with Workspace and ChannelID populated.
//
// Accepted form:
//
//	https://myorg.slack.com/archives/C0B3Z1KT80K
func ParseChannelURL(raw string) (ChannelRef, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return ChannelRef{}, fmt.Errorf("invalid URL %q: %w", raw, err)
	}
	if u.Scheme != "https" && u.Scheme != "http" {
		return ChannelRef{}, fmt.Errorf("invalid Slack URL %q: scheme must be http(s)", raw)
	}
	if !strings.HasSuffix(u.Host, ".slack.com") {
		return ChannelRef{}, fmt.Errorf("invalid Slack URL %q: host %q does not end with .slack.com", raw, u.Host)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) != 2 || parts[0] != "archives" || parts[1] == "" {
		return ChannelRef{}, fmt.Errorf("invalid Slack channel URL %q: expected path /archives/<channelID>", raw)
	}
	return ChannelRef{
		Workspace: u.Host,
		ChannelID: parts[1],
	}, nil
}

// IsChannelURL reports whether raw looks like a Slack channel-only URL
// (has /archives/<channelID> but no timestamp segment).
func IsChannelURL(raw string) bool {
	if !strings.HasPrefix(raw, "http://") && !strings.HasPrefix(raw, "https://") {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	if !strings.HasSuffix(u.Host, ".slack.com") {
		return false
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	return len(parts) == 2 && parts[0] == "archives" && parts[1] != ""
}
