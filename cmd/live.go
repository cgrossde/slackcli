// Package cmd — live.go implements the "live" command.
//
// Layer 1: LiveEventTypes returns static type info. All event formatting and
// filtering logic lives here as pure functions. The streaming RunE is injected
// by main.go (like login/reauth) because it requires OS signal handling and
// direct stdout access.
package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/cgrossde/slackcli/internal/slack"
)

const maxTextRunes = 200

// eventTypeDescriptions maps each allowed event type to its human description.
// Ordering follows slack.AllowedEventTypes.
var eventTypeDescriptions = map[string]string{
	"message":               "New message, edit, or deletion",
	"reaction_added":        "Emoji reaction added to a message",
	"reaction_removed":      "Emoji reaction removed from a message",
	"member_joined_channel": "User joined a channel",
	"member_left_channel":   "User left a channel",
	"channel_created":       "New channel created",
	"channel_deleted":       "Channel archived or deleted",
	"channel_rename":        "Channel renamed",
	"team_join":             "New workspace member joined",
	"desktop_notification":  "Unread mention / push notification",
}

// LiveEventTypes returns a formatted table of supported event types.
// Ordering follows slack.AllowedEventTypes so the list stays in sync
// with the allowlist automatically.
func LiveEventTypes() string {
	var b strings.Builder
	b.WriteString("Supported event types:\n\n")
	for _, t := range slack.AllowedEventTypes {
		fmt.Fprintf(&b, "  %-30s %s\n", t, eventTypeDescriptions[t])
	}
	return b.String()
}

// LiveFilter holds the active filter criteria for the live stream.
// Zero value means "no filter" (accept everything).
type LiveFilter struct {
	Channels             []string // channel IDs or names (empty = all)
	FromUser             string   // user display name or ID (empty = all)
	Types                []string // event types (empty = all)
	SelfUserID           string   // when non-empty, --mention: only events that mention this ID or are in threads the user participated in
	IsThreadParticipant  func(channelID, threadTs string) bool // nil = skip thread check
}

// Accept reports whether the event passes all filter criteria.
// cache is used to resolve user display names; may be nil.
// chanNames maps channel ID → name (without #); may be nil.
func (f *LiveFilter) Accept(e slack.Event, cache *slack.UserCache, chanNames map[string]string) bool {
	if len(f.Types) > 0 && !containsString(f.Types, e.Type) {
		return false
	}

	if len(f.Channels) > 0 {
		matched := false
		for _, ch := range f.Channels {
			if ch == e.Channel {
				matched = true
				break
			}
			// Also match by name (strip leading # if user supplied it).
			name := strings.TrimPrefix(ch, "#")
			if chanNames[e.Channel] == name {
				matched = true
				break
			}
		}
		if !matched {
			return false
		}
	}

	if f.FromUser != "" {
		// Direct ID match.
		if e.User == f.FromUser {
			return true
		}
		// Name match via cache.
		if cache != nil {
			u, err := cache.GetUser(e.User)
			if err == nil {
				if strings.EqualFold(u.ShortLabel(), f.FromUser) ||
					strings.EqualFold(u.Label(), f.FromUser) {
					return true
				}
			}
		}
		return false
	}

	if f.SelfUserID != "" {
		// Direct @mention in the message text.
		for _, id := range e.Mentions {
			if id == f.SelfUserID {
				return true
			}
		}
		// Thread participation: if this is a reply, fetch the root and check
		// whether selfUserID appears in reply_users.
		if e.Type == "message" && e.ThreadTs != "" && e.ThreadTs != e.Ts &&
			f.IsThreadParticipant != nil {
			if f.IsThreadParticipant(e.Channel, e.ThreadTs) {
				return true
			}
		}
		return false
	}

	return true
}

// FormatEvent converts a Slack WebSocket event into a human-readable text
// block suitable for stdout streaming.  chanNames maps channel ID → name;
// cache resolves user IDs to display labels.  Both may be nil.
func FormatEvent(e slack.Event, cache *slack.UserCache, chanNames map[string]string) string {
	ts := formatEventTs(e.Ts)
	chanStr := liveChannelStr(e.Channel, chanNames)
	author := liveAuthorStr(e.User, cache)

	var b strings.Builder

	switch e.Type {
	case "message":
		typeTag := "[message]"
		switch {
		case e.SubType == "message_changed" || e.SubType == "message_deleted":
			typeTag = "[message:" + e.SubType + "]"
		case e.ThreadTs != "" && e.ThreadTs != e.Ts:
			typeTag = "[message:thread_reply]"
		}
		fmt.Fprintf(&b, "%s %s · %s · %s\n", typeTag, chanStr, author, ts)
		if e.Text != "" {
			fmt.Fprintf(&b, "  %s\n", truncateRunes(e.Text, maxTextRunes))
		}
		for _, a := range e.Attachments {
			line := a.AuthorName
			if a.Title != "" {
				if line != "" {
					line += " — " + a.Title
				} else {
					line = a.Title
				}
			}
			if line != "" {
				fmt.Fprintf(&b, "  [attachment] %s\n", line)
			}
			if a.Text != "" {
				fmt.Fprintf(&b, "  %s\n", truncateRunes(a.Text, maxTextRunes))
			}
			url := a.TitleLink
			if url == "" {
				url = a.FromURL
			}
			if url != "" {
				fmt.Fprintf(&b, "  → %s\n", url)
			}
		}
		if e.Channel != "" && e.Ts != "" {
			fmt.Fprintf(&b, "  → slackcli read %s:%s\n", e.Channel, e.Ts)
		}

	case "reaction_added", "reaction_removed":
		fmt.Fprintf(&b, "[%s] %s · %s · %s\n", e.Type, chanStr, author, ts)
		targetAuthor := liveAuthorStr(e.ItemUser, cache)
		fmt.Fprintf(&b, "  :%s: on message by %s\n", e.Reaction, targetAuthor)
		if e.Channel != "" && e.ItemTs != "" {
			fmt.Fprintf(&b, "  → slackcli read %s:%s\n", e.Channel, e.ItemTs)
		}

	case "member_joined_channel", "member_left_channel":
		action := "joined"
		if e.Type == "member_left_channel" {
			action = "left"
		}
		fmt.Fprintf(&b, "[%s] %s · %s · %s\n", e.Type, chanStr, author, ts)
		fmt.Fprintf(&b, "  %s %s channel %s\n", author, action, chanStr)

	case "channel_created", "channel_deleted", "channel_rename":
		fmt.Fprintf(&b, "[%s] %s · %s\n", e.Type, chanStr, ts)

	case "team_join":
		fmt.Fprintf(&b, "[team_join] · %s · %s\n", author, ts)
		fmt.Fprintf(&b, "  New workspace member\n")

	case "desktop_notification":
		fmt.Fprintf(&b, "[desktop_notification] %s · %s · %s\n", chanStr, author, ts)
		if e.Text != "" {
			fmt.Fprintf(&b, "  %s\n", truncateRunes(e.Text, maxTextRunes))
		}

	default:
		fmt.Fprintf(&b, "[%s] %s · %s · %s\n", e.Type, chanStr, author, ts)
		if e.Text != "" {
			fmt.Fprintf(&b, "  %s\n", truncateRunes(e.Text, maxTextRunes))
		}
	}

	return b.String()
}

// liveEventJSON is the JSON representation of a single live event.
type liveEventJSON struct {
	Type        string           `json:"type"`
	SubType     string           `json:"subtype"`
	ChannelID   string           `json:"channel_id"`
	ChannelName string           `json:"channel_name"`
	UserID      string           `json:"user_id"`
	Username    string           `json:"username"`
	DisplayName string           `json:"display_name"`
	Ts          string           `json:"ts"`
	ThreadTs    string           `json:"thread_ts"`
	Text        string           `json:"text"`
	Reaction    string           `json:"reaction,omitempty"`
	ItemTs      string           `json:"item_ts,omitempty"`
	Attachments []attachmentJSON `json:"attachments,omitempty"`
}

// FormatEventJSON converts a Slack WebSocket event into a single-line JSON
// string (no trailing newline). chanNames maps channel ID → name; cache
// resolves user IDs to display labels. Both may be nil.
func FormatEventJSON(e slack.Event, cache *slack.UserCache, chanNames map[string]string) string {
	chanName := ""
	if chanNames != nil {
		chanName = chanNames[e.Channel]
	}
	username := ""
	displayName := ""
	if cache != nil && e.User != "" {
		if u, err := cache.GetUser(e.User); err == nil {
			username = u.Name
			displayName = u.Label()
		}
	}
	atts := make([]attachmentJSON, 0, len(e.Attachments))
	for _, a := range e.Attachments {
		atts = append(atts, attachmentJSON{
			AuthorName:  a.AuthorName,
			AuthorLink:  a.AuthorLink,
			Title:       a.Title,
			TitleLink:   a.TitleLink,
			Pretext:     a.Pretext,
			Text:        a.Text,
			FromURL:     a.FromURL,
			ServiceName: a.ServiceName,
			ImageURL:    a.ImageURL,
			ThumbURL:    a.ThumbURL,
			Footer:      a.Footer,
		})
	}
	rec := liveEventJSON{
		Type:        e.Type,
		SubType:     e.SubType,
		ChannelID:   e.Channel,
		ChannelName: chanName,
		UserID:      e.User,
		Username:    username,
		DisplayName: displayName,
		Ts:          e.Ts,
		ThreadTs:    e.ThreadTs,
		Text:        e.Text,
		Reaction:    e.Reaction,
		ItemTs:      e.ItemTs,
		Attachments: atts,
	}
	line, _ := json.Marshal(rec)
	return string(line)
}

// NewLiveCmd builds the "live" Cobra command tree.
// runE is injected by main.go because signal handling and streaming stdout
// require OS-level access that doesn't belong in this package.
func NewLiveCmd(runE func(*cobra.Command, []string) error) *cobra.Command {
	liveCmd := &cobra.Command{
		Use:   "live",
		Short: "Stream real-time Slack events to stdout",
		Long: `Connect to Slack's WebSocket gateway and stream events in real time.

Events are written to stdout immediately, one block per event, separated by
blank lines. Output is designed for LLM consumption: grep-friendly type tags,
copy-pasteable slackcli read references.

Credentials must already be saved (run: slackcli auth login).`,
		Example: `  # Stream all events from a workspace
  slackcli live --workspace myorg.slack.com

  # Filter to a channel and a user
  slackcli live --workspace myorg.slack.com --channel general --from alice

  # Only message events
  slackcli live --workspace myorg.slack.com --type message

  # Pipe into grep for mentions
  slackcli live --workspace myorg.slack.com | grep -i 'deployment'

  # Only events that mention you
  slackcli live --workspace myorg.slack.com --mention`,
		RunE: runE,
	}
	liveCmd.Flags().StringP("workspace", "w", "", "Workspace domain (e.g. myorg or myorg.slack.com); defaults to stored default when omitted")
	liveCmd.Flags().StringArrayP("channel", "c", nil, "Filter to channel(s) by name (e.g. general). Repeatable.")
	liveCmd.Flags().StringP("from", "f", "", "Filter to events from a specific user (name or ID)")
	liveCmd.Flags().StringArrayP("type", "t", nil, "Filter to event type(s) (repeatable; see: slackcli live types)")
	liveCmd.Flags().Bool("json", false, "Output events as NDJSON (one object per line)")
	liveCmd.Flags().Bool("mention", false, "Only show events that mention you (requires auth.test at startup)")

	typesCmd := &cobra.Command{
		Use:   "types",
		Short: "List supported real-time event types",
		RunE: func(c *cobra.Command, _ []string) error {
			fmt.Fprint(c.OutOrStdout(), LiveEventTypes())
			return nil
		},
	}
	liveCmd.AddCommand(typesCmd)

	return liveCmd
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// liveChannelStr returns "#name" when chanNames has an entry for id,
// otherwise returns the raw ID.
func liveChannelStr(id string, chanNames map[string]string) string {
	if id == "" {
		return "(unknown channel)"
	}
	if name, ok := chanNames[id]; ok {
		return "#" + name
	}
	return id
}

// liveAuthorStr returns a display label for the user.  Falls back through
// display name, name, then raw ID.
func liveAuthorStr(id string, cache *slack.UserCache) string {
	if id == "" {
		return "(unknown)"
	}
	if cache != nil {
		u, err := cache.GetUser(id)
		if err == nil {
			return u.Label()
		}
	}
	return id
}

// formatEventTs converts a Slack timestamp (e.g. "1718197925.001234") to
// a UTC human-readable string.  Falls back to the raw string on parse failure.
func formatEventTs(ts string) string {
	if ts == "" {
		return ""
	}
	// Slack timestamps: "<unix_seconds>.<microseconds>"
	dot := strings.IndexByte(ts, '.')
	if dot < 0 {
		return ts
	}
	var sec int64
	for _, ch := range ts[:dot] {
		if ch < '0' || ch > '9' {
			return ts
		}
		sec = sec*10 + int64(ch-'0')
	}
	return time.Unix(sec, 0).UTC().Format("2006-01-02 15:04:05")
}

// truncateRunes returns s with at most n Unicode code points.  If s is longer,
// the excess is replaced with a single "…" character.
//
// range over a string in Go iterates rune positions (byte offsets of rune
// boundaries), so s[:i] at the nth rune is always valid UTF-8.
func truncateRunes(s string, n int) string {
	count := 0
	for i := range s {
		if count == n {
			return s[:i] + "…"
		}
		count++
	}
	return s
}

// containsString reports whether haystack contains needle.
func containsString(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
