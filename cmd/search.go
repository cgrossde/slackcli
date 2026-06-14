// Package cmd — search.go implements the "search" command.
//
// Layer 1: Search fetches and formats Slack search.messages results.
// --channels mode queries search.modules.channels; --users mode delegates to
// SearchUsers. Layer 2 wiring (presenter) is applied in main.go.
package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/cgrossde/slackcli/internal/keychain"
	"github.com/cgrossde/slackcli/internal/slack"
)

// SearchFlags holds the parsed flag values for the search command.
type SearchFlags struct {
	Workspace string
	Channel   string
	From      string
	After     string
	Before    string
	Count     int
	Page      int
	Sort      string
	Asc       bool
	InDM      bool   // --in-dm: restrict to direct messages (is:dm)
	InChannel bool   // --in-channel: restrict to public/private channels (is:channel)
	With      string // --with: restrict to conversations with user (with:@user)
	JSON      bool   // --json: emit NDJSON instead of plain text
	Channels  bool   // --channels: search channels by name instead of messages
	Users     bool   // --users: search users by name/email/ID instead of messages
}

// NewSearchCmd builds the "search" Cobra command.
func NewSearchCmd() *cobra.Command {
	var flags SearchFlags

	cmd := &cobra.Command{
		Use:   "search [keywords]",
		Short: "Search Slack messages, channels, or users",
		Long: `Search Slack messages (default), channels (--channels), or users (--users).

MODE FLAGS (mutually exclusive):
  --channels   Search channels by name via search.modules.channels
  --users      Search users by name, employee ID, or email
  (default)    Search messages using standard Slack modifiers

MESSAGE SEARCH — flags build a Slack search query:
Channel can be a name (e.g. "ops").
From accepts a display name or a Slack user ID (U.../W...).

Date flags accept YYYY-MM-DD, relative values, or day names:
  Nd/Nw/Nm/Ny  — N days/weeks/months/years ago (e.g. 7d, 2w)
  today / yesterday
  monday … sunday  — most recent past occurrence

Slack modifiers can be passed directly in the keyword argument:
  has:link        messages with a URL
  has:reaction    messages with any emoji reaction
  is:dm           direct messages only  (same as --in-dm)
  is:channel      channel messages only (same as --in-channel)
  with:@alice     conversations where alice participated
  -word           exclude messages containing word
  "exact phrase"  exact phrase match`,
		Example: `  slackcli search "deployment" --channel ops --after 7d
  slackcli search "from:me" --in-dm --after monday
  slackcli search "has:link" --after 2026-05-11
  slackcli search "incident" --after 2024-06-01 --before 2024-06-30
  slackcli search "with:@alice -bot" --sort timestamp --asc
  slackcli search --channels general
  slackcli search --channels ops --json
  slackcli search --users alice
	slackcli search --users u123456`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			keywords := ""
			if len(args) > 0 {
				keywords = args[0]
			}
			out, err := Search(keywords, flags)
			if err != nil {
				return err
			}
			_, werr := fmt.Fprint(c.OutOrStdout(), out)
			return werr
		},
	}

	f := cmd.Flags()
	f.StringVarP(&flags.Workspace, "workspace", "w", "", "Workspace to search (required if >1 saved)")
	f.StringVarP(&flags.Channel, "channel", "c", "", "Restrict to channel (e.g. general)")
	f.StringVarP(&flags.From, "from", "f", "", "Restrict to messages from user (display name or U.../W... ID)")
	f.StringVar(&flags.After, "after", "", "Messages on or after date, inclusive (YYYY-MM-DD, Nd/Nw/Nm/Ny, or day name e.g. monday)")
	f.StringVar(&flags.Before, "before", "", "Messages on or before date, inclusive (YYYY-MM-DD, Nd/Nw/Nm/Ny, or day name e.g. friday)")
	f.IntVarP(&flags.Count, "count", "n", 20, "Results per page (1–100)")
	f.IntVarP(&flags.Page, "page", "p", 1, "Page number, 1-indexed")
	f.StringVar(&flags.Sort, "sort", "score", `Sort field: "score" or "timestamp"`)
	f.BoolVar(&flags.Asc, "asc", false, "Sort ascending (default is descending)")
	f.BoolVar(&flags.InDM, "in-dm", false, "Restrict to direct messages (is:dm)")
	f.BoolVar(&flags.InChannel, "in-channel", false, "Restrict to public/private channels (is:channel)")
	f.StringVar(&flags.With, "with", "", "Restrict to conversations with user (e.g. @alice or U012ABC)")
	f.BoolVar(&flags.JSON, "json", false, "Output results as NDJSON (one object per line)")
	f.BoolVar(&flags.Channels, "channels", false, "Search channels by name (mutually exclusive with --users)")
	f.BoolVar(&flags.Users, "users", false, "Search users by name, employee ID, or email (mutually exclusive with --channels)")

	return cmd
}

// Search is the Layer 1 implementation. It resolves credentials, calls the
// API, and returns formatted plain-text results. query is the keyword string;
// flags carry the parsed CLI flags.
func Search(query string, flags SearchFlags) (string, error) {
	if flags.Channels && flags.Users {
		return "", fmt.Errorf("--channels and --users are mutually exclusive")
	}
	if flags.Channels {
		return searchChannels(query, flags)
	}
	if flags.Users {
		return SearchUsers(query, flags.Workspace, flags.JSON)
	}

	workspace, err := resolveWorkspace(flags.Workspace)
	if err != nil {
		return "", err
	}

	workspace, entry, err := loadCredentials(workspace)
	if err != nil {
		return "", err
	}

	client := slack.NewClient(entry.Token, entry.Cookie)

	cache, err := slack.NewUserCache(workspace, client)
	if err != nil {
		return "", fmt.Errorf("opening user cache: %w", err)
	}

	fullQuery := buildSearchQuery(query, flags)

	if fullQuery == "" {
		return "", fmt.Errorf("provide keywords or at least one filter flag (--channel, --from, --after, --before)")
	}

	// Clamp count to [1, 100] — Slack hard-caps at 100.
	count := flags.Count
	if count < 1 {
		count = 1
	} else if count > 100 {
		count = 100
	}

	sortDir := "desc"
	if flags.Asc {
		sortDir = "asc"
	}

	params := slack.SearchParams{
		Sort:    flags.Sort,
		SortDir: sortDir,
		Count:   count,
		Page:    flags.Page,
	}

	result, err := client.SearchMessages(fullQuery, params)
	if err != nil {
		return "", fmt.Errorf("searching messages: %w", err)
	}

	// Resolve self ID for "You" annotation in plain-text output. Best-effort:
	// auth.test is a fast cached call; skip on error to avoid blocking output.
	selfID := ""
	if auth, authErr := client.AuthTest(); authErr == nil && auth.OK {
		selfID = auth.UserID
	}

	if flags.JSON {
		return formatSearchResultsJSON(result, cache, workspace), nil
	}
	return formatSearchResults(result, cache, flags, selfID), nil
}

// resolveWorkspace picks the workspace domain for a command.
//
//  1. If --workspace was given, canonicalize it.
//  2. Delegate to keychain.ResolveDefault (stored default → single saved workspace → error).
func resolveWorkspace(flag string) (string, error) {
	if flag != "" {
		return CanonicalDomain(flag), nil
	}
	return keychain.ResolveDefault()
}

// loadCredentials resolves the keychain entry for workspace, applying an
// Enterprise Grid fallback: if the exact domain is not saved (e.g. the URL
// carries the org-level host "acme.enterprise.slack.com" but credentials were
// stored under a member workspace like "acme.slack.com"), we retry with
// keychain.ResolveDefault. The returned workspace string reflects whichever
// domain was actually used to load the entry.
func loadCredentials(workspace string) (string, keychain.Entry, error) {
	entry, err := keychain.Load(workspace)
	if err == nil {
		return workspace, entry, nil
	}
	if !errors.Is(err, keychain.ErrNotFound) {
		return "", keychain.Entry{}, fmt.Errorf(
			"no credentials for workspace %q (run: slackcli auth login --workspace %s): %w",
			workspace, workspace, err,
		)
	}
	// Enterprise Grid: the domain in the URL is the org host, not the member
	// workspace the user logged into. Fall back to the stored default.
	def, defErr := keychain.ResolveDefault()
	if defErr != nil {
		// Surface the original workspace in the error so the user knows what was tried.
		return "", keychain.Entry{}, fmt.Errorf(
			"no credentials for workspace %q (run: slackcli auth login --workspace %s): %w",
			workspace, workspace, err,
		)
	}
	entry, err = keychain.Load(def)
	if err != nil {
		return "", keychain.Entry{}, fmt.Errorf(
			"no credentials for workspace %q (run: slackcli auth login --workspace %s): %w",
			def, def, err,
		)
	}
	return def, entry, nil
}

// gridWorkspaces returns the sibling workspace domains stored in the keychain
// entry for workspace. Returns nil when not on an Enterprise Grid (i.e. the
// entry has no GridWorkspaces) or when the entry cannot be loaded.
func gridWorkspaces(workspace string) []string {
	entry, err := keychain.Load(workspace)
	if err != nil {
		return nil
	}
	return entry.GridWorkspaces
}

// buildSearchQuery combines the keyword query with Slack search modifiers
// derived from the command flags. Modifiers are appended to the keyword string
// in a deterministic order: in: from: with: is: after: before:.
func buildSearchQuery(keywords string, flags SearchFlags) string {
	var parts []string
	if kw := strings.TrimSpace(keywords); kw != "" {
		parts = append(parts, kw)
	}

	if flags.Channel != "" {
		// Channel IDs start with C, G, D, or W followed by alphanumerics.
		// If it looks like a raw ID, use it without the "#" prefix.
		if looksLikeChannelID(flags.Channel) {
			parts = append(parts, "in:"+flags.Channel)
		} else {
			ch := strings.TrimPrefix(flags.Channel, "#")
			parts = append(parts, "in:#"+ch)
		}
	}

	if flags.From != "" {
		if looksLikeUserID(flags.From) {
			parts = append(parts, "from:<"+flags.From+">")
		} else {
			parts = append(parts, "from:"+flags.From)
		}
	}

	if flags.With != "" {
		w := flags.With
		// Strip leading @ if the user typed @alice — Slack expects "with:alice".
		w = strings.TrimPrefix(w, "@")
		if looksLikeUserID(w) {
			parts = append(parts, "with:<"+w+">")
		} else {
			parts = append(parts, "with:"+w)
		}
	}

	if flags.InDM {
		parts = append(parts, "is:dm")
	}
	if flags.InChannel {
		parts = append(parts, "is:channel")
	}

	if flags.After != "" {
		date, err := resolveDate(flags.After, time.Now())
		if err == nil {
			// Slack's after: is exclusive (strictly after the given date).
			// Shift back one day so --after behaves as an inclusive lower bound.
			if t, perr := time.Parse("2006-01-02", date); perr == nil {
				date = t.AddDate(0, 0, -1).Format("2006-01-02")
			}
			parts = append(parts, "after:"+date)
		}
		// If parsing fails, omit the modifier (caller got an error at parse time
		// during validation — but search is best-effort here).
	}

	if flags.Before != "" {
		date, err := resolveDate(flags.Before, time.Now())
		if err == nil {
			// Slack's before: is exclusive (strictly before the given date).
			// Shift forward one day so --before behaves as an inclusive upper bound.
			if t, perr := time.Parse("2006-01-02", date); perr == nil {
				date = t.AddDate(0, 0, 1).Format("2006-01-02")
			}
			parts = append(parts, "before:"+date)
		}
	}

	return strings.Join(parts, " ")
}

// looksLikeChannelID returns true when s resembles a Slack channel ID
// (starts with C, D, G, or W and contains only uppercase letters and digits).
func looksLikeChannelID(s string) bool {
	if len(s) < 2 {
		return false
	}
	switch s[0] {
	case 'C', 'D', 'G', 'W':
	default:
		return false
	}
	for _, r := range s[1:] {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// looksLikeUserID returns true when s is a Slack user ID (starts with U or W,
// followed by uppercase alphanumerics).
func looksLikeUserID(s string) bool {
	if len(s) < 2 {
		return false
	}
	if s[0] != 'U' && s[0] != 'W' {
		return false
	}
	for _, r := range s[1:] {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

// resolveDate converts a date input to a YYYY-MM-DD string.
// Accepts:
//   - Absolute dates:   YYYY-MM-DD
//   - Relative durations: Nd (days), Nw (weeks), Nm (months), Ny (years); N=0 → today.
//   - Named days: "today", "yesterday", "monday"–"sunday" (most recent past occurrence).
func resolveDate(input string, now time.Time) (string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", fmt.Errorf("empty date")
	}

	// Try absolute YYYY-MM-DD.
	if _, err := time.Parse("2006-01-02", input); err == nil {
		return input, nil
	}

	// Named day shortcuts.
	lower := strings.ToLower(input)
	switch lower {
	case "today":
		return now.Format("2006-01-02"), nil
	case "yesterday":
		return now.AddDate(0, 0, -1).Format("2006-01-02"), nil
	case "monday", "tuesday", "wednesday", "thursday", "friday", "saturday", "sunday":
		target := map[string]time.Weekday{
			"monday":    time.Monday,
			"tuesday":   time.Tuesday,
			"wednesday": time.Wednesday,
			"thursday":  time.Thursday,
			"friday":    time.Friday,
			"saturday":  time.Saturday,
			"sunday":    time.Sunday,
		}[lower]
		// Walk backwards from today until we hit the target weekday.
		// If today is the target weekday, return today (0 days back).
		d := now
		for d.Weekday() != target {
			d = d.AddDate(0, 0, -1)
		}
		return d.Format("2006-01-02"), nil
	}

	// Relative: last char is the unit, rest is N.
	unit := input[len(input)-1]
	numStr := input[:len(input)-1]
	if numStr == "" {
		return "", fmt.Errorf("invalid relative date %q: missing number", input)
	}

	n := 0
	for _, r := range numStr {
		if r < '0' || r > '9' {
			return "", fmt.Errorf("invalid relative date %q: non-numeric prefix", input)
		}
		n = n*10 + int(r-'0')
	}

	var t time.Time
	switch unit {
	case 'd':
		t = now.AddDate(0, 0, -n)
	case 'w':
		t = now.AddDate(0, 0, -n*7)
	case 'm':
		t = now.AddDate(0, -n, 0)
	case 'y':
		t = now.AddDate(-n, 0, 0)
	default:
		return "", fmt.Errorf("invalid relative date %q: unknown unit %q (use d/w/m/y)", input, unit)
	}

	return t.Format("2006-01-02"), nil
}

// formatSearchResults renders a SearchResult as a plain-text, LLM-friendly
// string. User IDs in the text are resolved via cache when available.
// selfID is the authenticated user's Slack user ID; pass "" to skip self-annotation.
func formatSearchResults(result slack.SearchResult, cache *slack.UserCache, flags SearchFlags, selfID string) string {
	var sb strings.Builder

	// Header line.
	fmt.Fprintf(&sb, "search: %q\n", result.Query)

	pages := result.Pages
	if pages == 0 && result.Total > 0 {
		// Compute from count if API didn't set Pages.
		count := result.Count
		if count == 0 {
			count = flags.Count
		}
		if count > 0 {
			pages = (result.Total + count - 1) / count
		}
	}

	fmt.Fprintf(&sb, "total: %d  page: %d/%d  (%d per page)\n",
		result.Total, result.Page, pages, result.Count)

	if len(result.Matches) == 0 {
		return sb.String()
	}

	sb.WriteString("\n")

	for i, m := range result.Matches {
		// Resolve sender; substitute "You" when the sender is the authenticated user.
		isSelf := selfID != "" && m.UserID == selfID
		author := resolveSearchAuthor(m, cache)
		if isSelf {
			author = "You"
		}

		// For DM channels, replace channel label with a directional DM label.
		// For regular channels, use "author → #channel" order.
		var headerLabel string
		if len(m.ChannelID) > 0 && m.ChannelID[0] == 'D' {
			// DM labels use ShortLabel (no handle suffix) to stay compact.
			senderShort := author
			if !isSelf && cache != nil && m.UserID != "" {
				if u, err := cache.GetUser(m.UserID); err == nil {
					senderShort = u.ShortLabel()
				}
			}
			peerName := ""
			if m.DMPeerID != "" && cache != nil {
				if u, err := cache.GetUser(m.DMPeerID); err == nil {
					peerName = u.ShortLabel()
				}
			}
			if peerName == "" {
				peerName = m.DMPeerID // raw ID fallback
			}
			if peerName == "" {
				peerName = "You" // self-DM fallback
			}
			headerLabel = dmLabel(isSelf, senderShort, peerName)
		} else {
			channel := channelLabel(m, cache)
			headerLabel = author + " → " + channel
		}

		// Format timestamp.
		tsStr := formatSearchTs(m.Ts)

		fmt.Fprintf(&sb, "[%d] %s · %s\n", i+1, headerLabel, tsStr)

		// Use the API snippet as-is. When Slack has truncated the text it ends
		// with "…" or "..."; in that case strip any trailing partial word so we
		// don't hand broken mrkdwn tokens to the caller.
		text := m.Text
		if cache != nil {
			text = cache.ResolveUserMentions(text)
		}
		text = stripTrailingPartialWord(text)
		// Indent every line of the body consistently.
		indented := "    " + strings.ReplaceAll(text, "\n", "\n    ")
		fmt.Fprintf(&sb, "%s\n", indented)

		if m.ChannelID != "" && m.Ts != "" {
			fmt.Fprintf(&sb, "    → slackcli read %s:%s\n", m.ChannelID, m.Ts)
		}
		sb.WriteString("\n")
	}

	// Pagination footer.
	fmt.Fprintf(&sb, "--- page %d of %d", result.Page, pages)
	if result.Page < pages {
		// Emit the next-page hint. Reconstruct enough flags for a useful hint.
		sb.WriteString(" | next: slackcli search")
		if flags.Workspace != "" {
			fmt.Fprintf(&sb, " --workspace %s", flags.Workspace)
		}
		fmt.Fprintf(&sb, " --page %d", result.Page+1)
		if flags.Count != 20 {
			fmt.Fprintf(&sb, " --count %d", flags.Count)
		}
		if flags.Channel != "" {
			fmt.Fprintf(&sb, " --channel %s", flags.Channel)
		}
		if flags.From != "" {
			fmt.Fprintf(&sb, " --from %s", flags.From)
		}
		if flags.After != "" {
			fmt.Fprintf(&sb, " --after %s", flags.After)
		}
		if flags.Before != "" {
			fmt.Fprintf(&sb, " --before %s", flags.Before)
		}
		if flags.Sort != "score" {
			fmt.Fprintf(&sb, " --sort %s", flags.Sort)
		}
		if flags.Asc {
			sb.WriteString(" --asc")
		}
		if flags.With != "" {
			fmt.Fprintf(&sb, " --with %s", flags.With)
		}
		if flags.InDM {
			sb.WriteString(" --in-dm")
		}
		if flags.InChannel {
			sb.WriteString(" --in-channel")
		}
		// Keywords last — only emit when non-empty.
		if kw := strings.TrimSpace(extractKeywords(result.Query)); kw != "" {
			fmt.Fprintf(&sb, " %q", kw)
		}
	}
	sb.WriteString(" ---\n")
	sb.WriteString("Tip: slackcli read <channel>:<ts> fetches the full thread\n")
	sb.WriteString("Tip: pass Slack modifiers in the query — e.g. has:link  has:reaction  is:dm  with:@alice  -word\n")
	return sb.String()
}

// searchMatchJSON is the JSON representation of a single search result.
type searchMatchJSON struct {
	ChannelID      string   `json:"channel_id"`
	ChannelName    string   `json:"channel_name"`
	ChannelType    string   `json:"channel_type"`
	UserID         string   `json:"user_id"`
	Username       string   `json:"username"`
	DisplayName    string   `json:"display_name"`
	Ts             string   `json:"ts"`
	ThreadTs       string   `json:"thread_ts,omitempty"`
	Text           string   `json:"text"`
	Permalink      string   `json:"permalink,omitempty"`
	Workspace      string   `json:"workspace,omitempty"`
	DMPeerID       string   `json:"dm_peer_id,omitempty"`
	ParticipantIDs []string `json:"participant_ids,omitempty"`
}

// searchPaginationJSON is the trailer object emitted when more pages exist.
type searchPaginationJSON struct {
	Pagination struct {
		NextPage int  `json:"next_page"`
		HasMore  bool `json:"has_more"`
		Total    int  `json:"total"`
		Page     int  `json:"page"`
		Pages    int  `json:"pages"`
	} `json:"_pagination"`
}

// formatSearchResultsJSON emits one JSON object per match, then a pagination
// trailer when more pages are available. resolvedWorkspace is the workspace
// used to authenticate the search; the Workspace field is omitted from each
// record when it matches resolvedWorkspace.
func formatSearchResultsJSON(result slack.SearchResult, cache *slack.UserCache, resolvedWorkspace string) string {
	var sb strings.Builder

	for _, m := range result.Matches {
		// Resolve display name via cache.
		displayName := ""
		username := m.Username
		if cache != nil && m.UserID != "" {
			if u, err := cache.GetUser(m.UserID); err == nil {
				displayName = u.ShortLabel()
				if username == "" {
					username = u.Name
				}
			}
		}

		// Determine channel type.
		chanType := "channel"
		switch {
		case m.IsMPIM:
			chanType = "mpim"
		case len(m.ChannelID) > 0 && m.ChannelID[0] == 'D':
			chanType = "dm"
		}

		rec := searchMatchJSON{
			ChannelID:   m.ChannelID,
			ChannelName: m.ChannelName,
			ChannelType: chanType,
			UserID:      m.UserID,
			Username:    username,
			DisplayName: displayName,
			Ts:          m.Ts,
			ThreadTs:    m.ThreadTs,
			Text:        m.Text,
		}
		if m.Permalink != "" {
			rec.Permalink = m.Permalink
		}
		// Only emit workspace when it differs from the search workspace.
		if m.Workspace != "" && m.Workspace != resolvedWorkspace {
			rec.Workspace = m.Workspace
		}
		if chanType == "dm" {
			rec.DMPeerID = m.DMPeerID
		}
		if chanType == "mpim" {
			rec.ParticipantIDs = m.ParticipantIDs
		}

		line, _ := json.Marshal(rec)
		sb.Write(line)
		sb.WriteByte('\n')
	}

	// Pagination trailer — only when there are more pages.
	if result.Pages > 0 && result.Page < result.Pages {
		var trailer searchPaginationJSON
		trailer.Pagination.NextPage = result.Page + 1
		trailer.Pagination.HasMore = true
		trailer.Pagination.Total = result.Total
		trailer.Pagination.Page = result.Page
		trailer.Pagination.Pages = result.Pages
		line, _ := json.Marshal(trailer)
		sb.Write(line)
		sb.WriteByte('\n')
	}

	return sb.String()
}

// resolveSearchAuthor returns a display string for a search match author.
// Priority: UserCache lookup (returns "Display (handle)") → Username → UserID → "(unknown)".
func resolveSearchAuthor(m slack.SearchMatch, cache *slack.UserCache) string {
	if cache != nil && m.UserID != "" {
		u, err := cache.GetUser(m.UserID)
		if err == nil {
			return u.Label()
		}
	}
	if m.Username != "" {
		return m.Username
	}
	if m.UserID != "" {
		return m.UserID
	}
	return "(unknown)"
}

// channelLabel returns a human-readable channel label for display.
//
// For 1:1 DMs:   DM(PeerName)
// For MPDMs:     Group(Alice, Bob, +N more) — up to 3 names, self excluded
// For channels:  #channel-name
// Fallback:      channel ID
func channelLabel(m slack.SearchMatch, cache *slack.UserCache) string {
	// 1:1 DM: resolve the peer's display name from their user ID.
	if len(m.ChannelID) > 0 && m.ChannelID[0] == 'D' {
		if m.DMPeerID != "" && cache != nil {
			if u, err := cache.GetUser(m.DMPeerID); err == nil {
				return "DM(" + u.ShortLabel() + ")"
			}
		}
		return "DM"
	}

	// MPIM: resolve participant names, skip self (the message author).
	if m.IsMPIM {
		if cache != nil && len(m.ParticipantIDs) > 0 {
			// Resolve the author's handle for the self-skip check.
			// m.UserID is a Slack user ID; MPDM participant IDs are handles.
			authorHandle := m.Username // legacy handle field from the API
			if authorHandle == "" {
				if u, err := cache.GetUser(m.UserID); err == nil {
					authorHandle = u.Name
				}
			}

			var names []string
			for _, id := range m.ParticipantIDs {
				if strings.EqualFold(id, authorHandle) {
					continue // skip self
				}
				// MPDM participant IDs are Slack handles (e.g. "u123456"), not
				// Slack user IDs. Try FindByName first; fall back to GetUser for
				// workspaces that use real Slack IDs in the mpdm name.
				var displayName string
				if u, ok := cache.FindByName(id); ok {
					displayName = u.ShortLabel()
				} else if u, err := cache.GetUser(id); err == nil {
					displayName = u.ShortLabel()
				}
				if displayName != "" {
					names = append(names, displayName)
				}
			}
			if len(names) > 0 {
				const maxNames = 3
				if len(names) <= maxNames {
					return "Group(" + strings.Join(names, ", ") + ")"
				}
				shown := strings.Join(names[:maxNames], ", ")
				return fmt.Sprintf("Group(%s, +%d more)", shown, len(names)-maxNames)
			}
		}
		return "group DM"
	}

	if m.ChannelName != "" {
		return "#" + m.ChannelName
	}
	return m.ChannelID
}

// formatSearchTs parses a raw Slack timestamp (Unix seconds with fractional
// part) and returns a "2006-01-02 15:04" string in local time.
// On parse failure the raw ts is returned unchanged.
func formatSearchTs(ts string) string {
	if ts == "" {
		return ""
	}
	// Slack ts looks like "1718200320.123456"; take the integer part.
	dotIdx := strings.IndexByte(ts, '.')
	sec := ts
	if dotIdx >= 0 {
		sec = ts[:dotIdx]
	}
	// Parse as integer seconds.
	var secs int64
	for _, r := range sec {
		if r < '0' || r > '9' {
			return ts
		}
		secs = secs*10 + int64(r-'0')
	}
	t := time.Unix(secs, 0)
	return t.Format("2006-01-02 15:04")
}

// stripTrailingPartialWord handles API-truncated search snippets.
//
// Slack's search.messages API truncates long messages server-side and signals
// this by ending the text with "…" (U+2026) or "...". A truncated snippet may
// end mid-word, which can break mrkdwn tokens (bold, code, links). This
// function strips back to the preceding word boundary and re-appends "…".
//
// If the text does not end with a truncation marker it is returned unchanged —
// the API returned the complete message and there is nothing to strip.
func stripTrailingPartialWord(s string) string {
	const ellipsis = "…"
	const dots = "..."

	var marker string
	var body string
	switch {
	case strings.HasSuffix(s, ellipsis):
		marker = ellipsis
		body = s[:len(s)-len(ellipsis)]
	case strings.HasSuffix(s, dots):
		marker = dots
		body = s[:len(s)-len(dots)]
	default:
		return s // not truncated; return as-is
	}

	// If the body already ends at a word boundary (trailing space/newline),
	// the snippet was cut cleanly — just trim the whitespace and re-attach.
	if len(body) > 0 && (body[len(body)-1] == ' ' || body[len(body)-1] == '\t' || body[len(body)-1] == '\n') {
		return strings.TrimRight(body, " \t\n") + marker
	}

	// Body ends mid-word — strip back to the preceding whitespace boundary.
	trimmed := strings.TrimRight(body, " \t")
	if idx := strings.LastIndexAny(trimmed, " \t\n"); idx >= 0 {
		trimmed = strings.TrimRight(trimmed[:idx], " \t")
	}
	// If there's nothing left (e.g. single long word), keep the original.
	if trimmed == "" {
		return s
	}
	return trimmed + marker
}

// extractKeywords strips Slack search modifiers (in:, from:, after:, before:,
// with:, is:) from a full query string and returns the remaining keyword tokens.
// Used when building the next-page hint in the footer.
func extractKeywords(query string) string {
	var keywords []string
	for _, token := range strings.Fields(query) {
		lower := strings.ToLower(token)
		if strings.HasPrefix(lower, "in:") ||
			strings.HasPrefix(lower, "from:") ||
			strings.HasPrefix(lower, "after:") ||
			strings.HasPrefix(lower, "before:") ||
			strings.HasPrefix(lower, "with:") ||
			strings.HasPrefix(lower, "is:") {
			continue
		}
		keywords = append(keywords, token)
	}
	return strings.Join(keywords, " ")
}
