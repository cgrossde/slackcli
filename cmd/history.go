// Package cmd — history.go implements the "history" command.
//
// Layer 1: History fetches recent messages from a Slack channel using
// conversations.history. Layer 2 wiring (presenter) is applied in main.go.
package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/cgrossde/slackcli/internal/keychain"
	"github.com/cgrossde/slackcli/internal/slack"
)

// HistoryFlags holds parsed flag values for the history command.
type HistoryFlags struct {
	Workspace string
	Channel   string
	Count     int
	Before    string
	After     string
	Cursor    string
	Pretty    bool
	JSON      bool
}

// NewHistoryCmd builds the "history" Cobra command.
func NewHistoryCmd() *cobra.Command {
	var flags HistoryFlags

	cmd := &cobra.Command{
		Use:   "history [<channel-url> | <channelID> | <channel-name>]",
		Short: "Print recent messages from a Slack channel",
		Long: `Fetch and print recent messages from a Slack channel.

Accepted channel forms:

  slackcli history https://myorg.slack.com/archives/C0B3Z1KT80K
  slackcli history C0B3Z1KT80K
  slackcli history general
  slackcli history --channel C0B3Z1KT80K

Messages are returned newest-first (most recent at the top).

Use --before/--after to time-box the window. Both flags accept:
  YYYY-MM-DD, Nd (days), Nw (weeks), Nm (months), Ny (years),
  today, yesterday, monday–sunday (most recent past occurrence).

Credentials must already be saved (run: slackcli auth login).`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			switch {
			case flags.JSON:
				out, err := HistoryJSON(args, flags)
				if out != "" {
					fmt.Fprint(c.OutOrStdout(), out)
				}
				return err
			case flags.Pretty:
				out, err := HistoryPretty(args, flags)
				if out != "" {
					fmt.Fprint(c.OutOrStdout(), out)
				}
				return err
			default:
				out, err := History(args, flags)
				if out != "" {
					fmt.Fprint(c.OutOrStdout(), out)
				}
				return err
			}
		},
	}

	cmd.Flags().StringVarP(&flags.Workspace, "workspace", "w", "", "Workspace (default: stored default)")
	cmd.Flags().StringVar(&flags.Channel, "channel", "", "Channel ID, name, or URL")
	cmd.Flags().IntVarP(&flags.Count, "count", "n", 25, "Number of messages to fetch (1–200)")
	cmd.Flags().StringVar(&flags.Before, "before", "", "Only messages before this date/time")
	cmd.Flags().StringVar(&flags.After, "after", "", "Only messages after this date/time")
	cmd.Flags().StringVar(&flags.Cursor, "cursor", "", "Pagination cursor from a previous response")
	cmd.Flags().BoolVar(&flags.Pretty, "pretty", false, "Render with ANSI colours and markdown formatting")
	cmd.Flags().BoolVar(&flags.JSON, "json", false, "Output messages as NDJSON (one object per line)")
	return cmd
}

// parseHistoryChannel resolves the channel argument (positional or --channel)
// to a raw identifier (URL, bare ID, or name) and the workspace (if extracted
// from a URL). The returned identifier is suitable for passing to
// resolveHistoryChannelID.
//
// Rules:
//   - Positional arg takes precedence.
//   - If both a positional arg and --channel are provided, they must agree or an
//     error is returned.
//   - If neither is provided, an error is returned.
func parseHistoryChannel(args []string, flags HistoryFlags) (channelID, workspace string, err error) {
	var pos string
	if len(args) > 0 {
		pos = args[0]
	}
	flag := flags.Channel

	switch {
	case pos != "" && flag != "" && pos != flag:
		return "", "", fmt.Errorf("channel specified twice: positional %q and --channel %q", pos, flag)
	case pos != "":
		return resolveHistoryChannelArg(pos)
	case flag != "":
		return resolveHistoryChannelArg(flag)
	default:
		return "", "", fmt.Errorf("channel required: provide a channel URL, ID, or name as a positional argument or via --channel")
	}
}

// resolveHistoryChannelArg extracts a channel ID and optional workspace from a
// raw channel argument (URL, bare ID, or name).
func resolveHistoryChannelArg(arg string) (channelID, workspace string, err error) {
	if slack.IsChannelURL(arg) {
		ref, parseErr := slack.ParseChannelURL(arg)
		if parseErr != nil {
			return "", "", parseErr
		}
		return ref.ChannelID, ref.Workspace, nil
	}
	// Bare channel ID or name — return as-is; workspace resolved later.
	return arg, "", nil
}

// History is the Layer 1 implementation. It resolves credentials, calls the
// API, and returns formatted plain-text output.
func History(args []string, flags HistoryFlags) (string, error) {
	_, channelID, _, cache, result, err := historyFetch(args, flags)
	if err != nil {
		return "", err
	}
	if cache != nil {
		// Pre-resolve all user IDs in the result set in a single batch.
		for _, m := range result.Messages {
			if m.User != "" {
				_, _ = cache.GetUser(m.User)
			}
		}
	}
	return formatHistoryPlain(result, channelID, cache), nil
}

// HistoryPretty is the --pretty variant of History.
func HistoryPretty(args []string, flags HistoryFlags) (string, error) {
	_, channelID, client, cache, result, err := historyFetch(args, flags)
	if err != nil {
		return "", err
	}
	var fetcher func(url string) ([]byte, string, error)
	if supportsInlineImages() {
		fetcher = client.FetchFileBytes
	}
	out, err := PrettyThread(result.Messages, cache, fetcher)
	if err != nil {
		return "", err
	}
	// Append pagination footer in pretty mode too.
	out += formatHistoryFooter(result, channelID, false)
	return out, nil
}

// HistoryJSON is the --json variant of History.
func HistoryJSON(args []string, flags HistoryFlags) (string, error) {
	resolvedWS, channelID, _, cache, result, err := historyFetch(args, flags)
	if err != nil {
		return "", err
	}

	chanType := channelTypeFromID(channelID)
	var sb strings.Builder
	for _, m := range result.Messages {
		displayName := ""
		username := m.Username
		if cache != nil && m.User != "" {
			if u, cErr := cache.GetUser(m.User); cErr == nil {
				displayName = u.ShortLabel()
				if username == "" {
					username = u.Name
				}
			}
		}

		files := make([]fileJSON, 0, len(m.Files))
		for _, f := range m.Files {
			files = append(files, fileJSON{
				ID:         f.ID,
				Name:       f.Name,
				PrettyType: f.PrettyType,
				Mimetype:   f.Mimetype,
				Permalink:  f.Permalink,
				URLPrivate: f.URLPrivate,
			})
		}
		reactions := make([]reactionJSON, 0, len(m.Reactions))
		for _, r := range m.Reactions {
			reactions = append(reactions, reactionJSON{
				Name:  r.Name,
				Count: r.Count,
				Users: r.Users,
			})
		}
		atts := make([]attachmentJSON, 0, len(m.Attachments))
		for _, a := range m.Attachments {
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
		rec := readMessageJSON{
			UserID:      m.User,
			Username:    username,
			DisplayName: displayName,
			Ts:          m.Ts,
			ThreadTs:    m.ThreadTs,
			Text:        m.Text,
			IsRoot:      false, // not a thread — every message is a channel root
			ReplyCount:  m.ReplyCount,
			ChannelID:   channelID,
			ChannelType: chanType,
			Workspace:   resolvedWS,
			Files:       files,
			Reactions:   reactions,
			Attachments: atts,
		}
		line, _ := json.Marshal(rec)
		sb.Write(line)
		sb.WriteByte('\n')
	}

	if result.HasMore {
		trailer, _ := json.Marshal(map[string]any{
			"_pagination": map[string]any{
				"has_more": true,
				"cursor":   result.Cursor,
			},
		})
		sb.Write(trailer)
		sb.WriteByte('\n')
	}

	return sb.String(), nil
}

// historyConnect resolves the workspace, loads credentials, and returns a
// ready-to-use client and user cache.
// urlWorkspace is non-empty when the channel was specified as a URL;
// flagWorkspace comes from --workspace.
func historyConnect(flagWorkspace, urlWorkspace string) (workspace string, client *slack.Client, cache *slack.UserCache, err error) {
	// Workspace resolution priority: URL > --workspace flag > keychain default.
	if urlWorkspace != "" {
		workspace = urlWorkspace
	} else {
		workspace, err = resolveWorkspace(flagWorkspace)
		if err != nil {
			return "", nil, nil, err
		}
	}

	workspace, entry, err := loadCredentials(workspace)
	if err != nil {
		return "", nil, nil, err
	}

	client = slack.NewClient(entry.Token, entry.Cookie)
	cache, err = slack.NewUserCache(workspace, client)
	if err != nil {
		return "", nil, nil, fmt.Errorf("opening user cache: %w", err)
	}
	return workspace, client, cache, nil
}

// historyFetch resolves the workspace/channel, calls GetHistory, and handles
// Enterprise Grid retry: if the channel is not found in the default workspace
// it retries each sibling workspace in the GridWorkspaces list.
//
// Returns the resolved workspace, channelID, a ready client, a user cache,
// and the HistoryResult.
func historyFetch(args []string, flags HistoryFlags) (resolvedWS string, channelID string, client *slack.Client, cache *slack.UserCache, result slack.HistoryResult, err error) {
	channelArg, urlWorkspace, parseErr := parseHistoryChannel(args, flags)
	if parseErr != nil {
		return "", "", nil, nil, slack.HistoryResult{}, parseErr
	}

	params, paramErr := buildHistoryParams(flags)
	if paramErr != nil {
		return "", "", nil, nil, slack.HistoryResult{}, paramErr
	}

	resolvedWS, client, cache, err = historyConnect(flags.Workspace, urlWorkspace)
	if err != nil {
		return "", "", nil, nil, slack.HistoryResult{}, err
	}

	// Fast path: check the channel cache.
	cc, _ := slack.LoadChannelCache()
	if cc != nil && looksLikeChannelID(channelArg) {
		if cachedWS, ok := cc.Get(channelArg); ok && cachedWS != resolvedWS {
			_, cachedEntry, cacheErr := loadCredentials(cachedWS)
			if cacheErr == nil {
				cachedClient := slack.NewClient(cachedEntry.Token, cachedEntry.Cookie)
				res, histErr := cachedClient.GetHistory(channelArg, params)
				if histErr == nil {
					cachedCache, _ := slack.NewUserCache(cachedWS, cachedClient)
					return cachedWS, channelArg, cachedClient, cachedCache, res, nil
				}
				// Cache hit but fetch failed — fall through.
			}
		}
	}

	channelID, err = resolveHistoryChannelID(client, resolvedWS, channelArg)
	if err != nil {
		return "", "", nil, nil, slack.HistoryResult{}, err
	}

	result, err = client.GetHistory(channelID, params)
	if err == nil {
		if cc != nil {
			cc.Set(channelID, resolvedWS)
		}
		return resolvedWS, channelID, client, cache, result, nil
	}
	if !errors.Is(err, slack.ErrChannelNotFound) {
		return "", "", nil, nil, slack.HistoryResult{}, fmt.Errorf("fetching history: %w", err)
	}

	// Channel not found — try grid siblings.
	siblings := gridWorkspaces(resolvedWS)
	if len(siblings) == 0 {
		allEntries, _, listErr := keychain.List()
		if listErr == nil {
			for _, e := range allEntries {
				if e.Workspace == resolvedWS {
					continue
				}
				siblings = append(siblings, e.Workspace)
			}
		}
	}
	for _, sibWS := range siblings {
		if sibWS == resolvedWS {
			continue
		}
		_, sibEntry, loadErr := loadCredentials(sibWS)
		if loadErr != nil {
			continue
		}
		sibClient := slack.NewClient(sibEntry.Token, sibEntry.Cookie)
		sibResult, sibErr := sibClient.GetHistory(channelID, params)
		if sibErr == nil {
			if cc != nil {
				cc.Set(channelID, sibWS)
			}
			sibCache, _ := slack.NewUserCache(sibWS, sibClient)
			return sibWS, channelID, sibClient, sibCache, sibResult, nil
		}
		if !errors.Is(sibErr, slack.ErrChannelNotFound) {
			return "", "", nil, nil, slack.HistoryResult{}, fmt.Errorf("fetching history: %w", sibErr)
		}
	}
	return "", "", nil, nil, slack.HistoryResult{}, fmt.Errorf("fetching history: %w", err)
}

// resolveHistoryChannelID converts a raw identifier to a Slack channel ID.
// If it already looks like a channel ID (starts with C, D, G, or W), it is
// returned as-is. Otherwise resolveChannelName is called.
func resolveHistoryChannelID(client *slack.Client, workspace, arg string) (string, error) {
	arg = strings.TrimPrefix(arg, "#")
	if len(arg) > 0 && isChannelIDPrefix(arg[0]) {
		return arg, nil
	}
	return resolveChannelName(client, workspace, arg)
}

// isChannelIDPrefix reports whether b is a valid Slack channel ID first byte.
func isChannelIDPrefix(b byte) bool {
	return b == 'C' || b == 'D' || b == 'G' || b == 'W'
}

// buildHistoryParams converts the flag values to HistoryParams, resolving
// date strings to epoch timestamps.
func buildHistoryParams(flags HistoryFlags) (slack.HistoryParams, error) {
	count := flags.Count
	if count < 1 {
		count = 1
	} else if count > 200 {
		count = 200
	}

	oldest, err := dateToEpoch(flags.After)
	if err != nil {
		return slack.HistoryParams{}, fmt.Errorf("--after: %w", err)
	}
	latest, err := dateToEpoch(flags.Before)
	if err != nil {
		return slack.HistoryParams{}, fmt.Errorf("--before: %w", err)
	}

	return slack.HistoryParams{
		Limit:  count,
		Oldest: oldest,
		Latest: latest,
		Cursor: flags.Cursor,
	}, nil
}

// dateToEpoch converts a date input string to an epoch timestamp string
// suitable for conversations.history oldest/latest parameters.
// Empty input returns "" (no bound). The date is resolved at the start of the
// day UTC.
func dateToEpoch(input string) (string, error) {
	if input == "" {
		return "", nil
	}
	date, err := resolveDate(input, time.Now())
	if err != nil {
		return "", err
	}
	t, err := time.Parse("2006-01-02", date)
	if err != nil {
		return "", fmt.Errorf("parsing resolved date %q: %w", date, err)
	}
	return strconv.FormatInt(t.Unix(), 10), nil
}

// formatHistoryPlain renders the HistoryResult as plain text.
func formatHistoryPlain(result slack.HistoryResult, channelID string, cache *slack.UserCache) string {
	var b strings.Builder
	for i, m := range result.Messages {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(formatHistoryMessage(m, channelID, cache))
	}
	b.WriteString(formatHistoryFooter(result, channelID, true))
	return b.String()
}

// formatHistoryMessage renders a single history message as plain text.
// It mirrors formatMessage from read.go but uses "[ message ]" label for all
// entries (no thread index) and shows a [N replies] indicator for thread roots.
func formatHistoryMessage(m slack.Message, channelID string, cache *slack.UserCache) string {
	var b strings.Builder

	label := "[ message ]=="
	author := resolveAuthor(m, cache)

	tsHuman := m.Ts
	if f, err := strconv.ParseFloat(m.Ts, 64); err == nil {
		sec := int64(f)
		tsHuman = time.Unix(sec, 0).UTC().Format("2006-01-02 15:04")
	}

	const lineWidth = 120
	left := fmt.Sprintf("== %s %s ", author, tsHuman)
	fill := lineWidth - len(left) - len(label)
	if fill < 1 {
		fill = 1
	}
	fmt.Fprintf(&b, "%s%s%s\n", left, strings.Repeat("=", fill), label)

	text := m.Text
	if cache != nil {
		text = cache.ResolveUserMentions(text)
	}
	fmt.Fprintf(&b, "%s\n", text)

	fmt.Fprintf(&b, "  → slackcli read %s:%s\n", channelID, m.Ts)

	if m.ReplyCount > 0 {
		fmt.Fprintf(&b, "  [%d repl%s]\n", m.ReplyCount, pluralY(m.ReplyCount))
	}

	for _, f := range m.Files {
		lbl := f.Name
		if lbl == "" {
			lbl = f.Title
		}
		typ := f.PrettyType
		if typ == "" {
			typ = f.Mimetype
		}
		if typ != "" {
			fmt.Fprintf(&b, "  [file] %s (%s)\n", lbl, typ)
		} else {
			fmt.Fprintf(&b, "  [file] %s\n", lbl)
		}
		if f.Permalink != "" {
			fmt.Fprintf(&b, "  → slackcli read %s\n", f.Permalink)
		} else if f.URLPrivate != "" {
			fmt.Fprintf(&b, "  → slackcli read %s\n", f.URLPrivate)
		}
	}

	if len(m.Reactions) > 0 {
		b.WriteString("  Reactions: ")
		for i, r := range m.Reactions {
			if i > 0 {
				b.WriteString("  ")
			}
			fmt.Fprintf(&b, ":%s: ×%d", r.Name, r.Count)
		}
		b.WriteByte('\n')
	}

	for _, a := range m.Attachments {
		if a.Pretext != "" {
			fmt.Fprintf(&b, "  %s\n", a.Pretext)
		}
		header := a.AuthorName
		if a.Title != "" {
			if header != "" {
				header += " — " + a.Title
			} else {
				header = a.Title
			}
		}
		if header != "" {
			fmt.Fprintf(&b, "  [attachment] %s\n", header)
		}
		if a.Text != "" {
			fmt.Fprintf(&b, "  %s\n", a.Text)
		}
		url := a.TitleLink
		if url == "" {
			url = a.FromURL
		}
		if url != "" {
			fmt.Fprintf(&b, "  → %s\n", url)
		}
	}

	b.WriteString("\n")
	return b.String()
}

// formatHistoryFooter renders the summary/pagination footer line(s).
// If addSeparator is true a "---" separator precedes the footer.
func formatHistoryFooter(result slack.HistoryResult, channelID string, addSeparator bool) string {
	var b strings.Builder
	n := len(result.Messages)
	if n == 0 && !result.HasMore {
		b.WriteString("--- 0 messages\n")
		return b.String()
	}

	if addSeparator {
		b.WriteString("\n")
	}
	hasMoreStr := "false"
	if result.HasMore {
		hasMoreStr = "true"
	}
	fmt.Fprintf(&b, "--- %d message%s | has_more: %s\n", n, pluralS(n), hasMoreStr)
	if result.HasMore && result.Cursor != "" {
		fmt.Fprintf(&b, "    next: slackcli history %s --cursor %s\n", channelID, result.Cursor)
	}
	if n > 0 {
		b.WriteString("    Tip: slackcli read <channel>:<ts> for full thread\n")
	}
	return b.String()
}

// pluralS returns "s" when n != 1 and "" otherwise.
func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// pluralY returns "y" when n == 1 and "ies" otherwise (for "reply"/"replies").
func pluralY(n int) string {
	if n == 1 {
		return "y"
	}
	return "ies"
}
