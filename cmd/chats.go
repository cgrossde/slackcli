// Package cmd — chats.go implements the "chats" command.
//
// Layer 1: Chats lists non-channel conversations (DMs and MPDMs) ordered by
// most-recently active. Layer 2 wiring (presenter) is applied in main.go.
package cmd

import (
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/cgrossde/slackcli/internal/slack"
)

// ChatsFlags holds parsed flag values for the chats command.
type ChatsFlags struct {
	Workspace string
	Count     int
	Type      string // "all", "dm", "mpdm"
	Cursor    string
	JSON      bool
}

// NewChatsCmd builds the "chats" Cobra command.
func NewChatsCmd() *cobra.Command {
	var flags ChatsFlags

	cmd := &cobra.Command{
		Use:   "chats",
		Short: "List recent DMs and group chats (MPDMs)",
		Long: `List your most-recently active direct messages (DMs) and multi-party DMs (MPDMs).

Results are sorted by last activity (most recent first).

Use --type to narrow the output:
  all    both DMs and MPDMs (default)
  dm     1:1 direct messages only
  mpdm   multi-party direct messages only

Credentials must already be saved (run: slackcli auth login).`,
		Example: `  slackcli chats
  slackcli chats --type dm
  slackcli chats --type mpdm --count 10
  slackcli chats --json`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			if flags.JSON {
				out, err := ChatsJSON(flags)
				if out != "" {
					fmt.Fprint(c.OutOrStdout(), out)
				}
				return err
			}
			out, err := Chats(flags)
			if out != "" {
				fmt.Fprint(c.OutOrStdout(), out)
			}
			return err
		},
	}

	cmd.Flags().StringVarP(&flags.Workspace, "workspace", "w", "", "Workspace (default: stored default)")
	cmd.Flags().IntVarP(&flags.Count, "count", "n", 20, "Number of chats to return (1–200)")
	cmd.Flags().StringVarP(&flags.Type, "type", "t", "all", "Filter by type: all, dm, mpdm")
	cmd.Flags().StringVar(&flags.Cursor, "cursor", "", "Pagination cursor from a previous response")
	cmd.Flags().BoolVar(&flags.JSON, "json", false, "Output as NDJSON (one object per line)")
	return cmd
}

// chatsTypes returns the conversations.list types slice for the given flag value.
func chatsTypes(t string) ([]string, error) {
	switch strings.ToLower(t) {
	case "all", "":
		return []string{"im", "mpim"}, nil
	case "dm", "im":
		return []string{"im"}, nil
	case "mpdm", "mpim":
		return []string{"mpim"}, nil
	default:
		return nil, fmt.Errorf("unknown --type %q: valid values are all, dm, mpdm", t)
	}
}

// sortConversationsByLatest sorts conversations by LatestTs descending.
func sortConversationsByLatest(convs []slack.Conversation) {
	sort.SliceStable(convs, func(i, j int) bool {
		ti, tj := convs[i].LatestTs, convs[j].LatestTs
		if ti == "" {
			return false
		}
		if tj == "" {
			return true
		}
		return ti > tj
	})
}

// chatsFetch loads credentials, calls conversations.list, resolves display
// names, and returns a sorted slice ready for formatting.
func chatsFetch(flags ChatsFlags) ([]chatEntry, slack.ConversationListResult, *slack.UserCache, error) {
	workspace, err := resolveWorkspace(flags.Workspace)
	if err != nil {
		return nil, slack.ConversationListResult{}, nil, err
	}

	_, entry, err := loadCredentials(workspace)
	if err != nil {
		return nil, slack.ConversationListResult{}, nil, err
	}

	client := slack.NewClient(entry.Token, entry.Cookie)
	cache, _ := slack.NewUserCache(workspace, client) // non-fatal if cache fails

	types, err := chatsTypes(flags.Type)
	if err != nil {
		return nil, slack.ConversationListResult{}, nil, err
	}

	count := flags.Count
	if count < 1 {
		count = 1
	} else if count > 200 {
		count = 200
	}

	result, err := client.ListConversations(slack.ConversationListParams{
		Types:     types,
		Limit:     count,
		Cursor:    flags.Cursor,
		Workspace: workspace,
	})
	if err != nil {
		return nil, slack.ConversationListResult{}, nil, fmt.Errorf("listing chats: %w", err)
	}

	// For client.counts fallback results, names are not yet resolved.
	// Sort first, trim to count, then resolve names only for displayed entries
	// to minimise conversations.info API calls.
	sortConversationsByLatest(result.Conversations)
	if len(result.Conversations) > count {
		result.Conversations = result.Conversations[:count]
		result.HasMore = false // we trimmed client-side, pagination is n/a
	}
	client.ResolveConversationNames(result.Conversations)

	entries := buildChatEntries(result.Conversations, cache)
	return entries, result, cache, nil
}

// chatEntry is a resolved conversation ready for display.
type chatEntry struct {
	ID          string
	Type        string // "dm" or "mpdm"
	Name        string // human-readable name
	RawName     string // Slack's internal name
	PeerID      string // for DMs: the peer user ID
	MemberIDs   []string
	LatestTs    string  // epoch ts of latest message
	LatestHuman string  // formatted time
	Priority    float64 // Slack priority score
}

// buildChatEntries resolves user display names for conversations.
func buildChatEntries(convs []slack.Conversation, cache *slack.UserCache) []chatEntry {
	entries := make([]chatEntry, 0, len(convs))
	for _, c := range convs {
		e := chatEntry{
			ID:        c.ID,
			RawName:   c.Name,
			PeerID:    c.User,
			MemberIDs: c.Members,
			LatestTs:  c.LatestTs,
			Priority:  c.Priority,
		}

		if c.IsIM {
			e.Type = "dm"
			e.Name = resolveUserDisplay(c.User, cache)
		} else {
			e.Type = "mpdm"
			e.Name = resolveMpdmName(c.Name, c.Members, cache)
		}

		if c.LatestTs != "" {
			if f, err := strconv.ParseFloat(c.LatestTs, 64); err == nil {
				e.LatestHuman = time.Unix(int64(f), 0).UTC().Format("2006-01-02 15:04")
			}
		}

		entries = append(entries, e)
	}

	// Sort by LatestTs descending (most recently active first).
	// Conversations without a latest message sort to the bottom.
	sort.SliceStable(entries, func(i, j int) bool {
		ti, tj := entries[i].LatestTs, entries[j].LatestTs
		if ti == "" && tj == "" {
			return false
		}
		if ti == "" {
			return false
		}
		if tj == "" {
			return true
		}
		// Lexicographic comparison works for Slack epoch ts strings.
		return ti > tj
	})

	return entries
}

// resolveUserDisplay returns "@DisplayName" for a user ID, falling back to
// the raw ID when the cache is unavailable or lookup fails.
func resolveUserDisplay(userID string, cache *slack.UserCache) string {
	if cache == nil || userID == "" {
		return "@" + userID
	}
	u, err := cache.GetUser(userID)
	if err != nil {
		return "@" + userID
	}
	label := u.ShortLabel()
	if label == "" {
		return "@" + userID
	}
	return "@" + label
}

// resolveMpdmName builds a readable name from raw MPDM name or member IDs.
// It strips the "mpdm-" prefix and "-N" suffix, then replaces username tokens
// with display names when a cache is available.
func resolveMpdmName(rawName string, members []string, cache *slack.UserCache) string {
	// If we have member IDs, resolve them directly.
	if len(members) > 0 && cache != nil {
		names := make([]string, 0, len(members))
		for _, id := range members {
			names = append(names, resolveUserDisplay(id, cache))
		}
		return strings.Join(names, ", ")
	}

	// Fall back to parsing the raw name: "mpdm-alice--bob--carol-1"
	name := rawName
	name = strings.TrimPrefix(name, "mpdm-")
	// Remove trailing "-N" suffix (e.g. "-1").
	if idx := strings.LastIndex(name, "-"); idx > 0 {
		suffix := name[idx+1:]
		allDigits := true
		for _, r := range suffix {
			if r < '0' || r > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			name = name[:idx]
		}
	}
	// Split on "--" double-dash separator.
	parts := strings.Split(name, "--")
	return strings.Join(parts, ", ")
}

// Chats is the Layer 1 plain-text implementation.
func Chats(flags ChatsFlags) (string, error) {
	entries, result, _, err := chatsFetch(flags)
	if err != nil {
		return "", err
	}
	return formatChatsPlain(entries, result), nil
}

// ChatsJSON is the --json variant.
func ChatsJSON(flags ChatsFlags) (string, error) {
	entries, result, _, err := chatsFetch(flags)
	if err != nil {
		return "", err
	}

	type chatJSON struct {
		ID        string   `json:"id"`
		Type      string   `json:"type"`
		Name      string   `json:"name"`
		RawName   string   `json:"raw_name,omitempty"`
		PeerID    string   `json:"peer_id,omitempty"`
		MemberIDs []string `json:"member_ids,omitempty"`
		LatestTs  string   `json:"latest_ts,omitempty"`
	}

	var sb strings.Builder
	for _, e := range entries {
		rec := chatJSON{
			ID:        e.ID,
			Type:      e.Type,
			Name:      e.Name,
			RawName:   e.RawName,
			PeerID:    e.PeerID,
			MemberIDs: e.MemberIDs,
			LatestTs:  e.LatestTs,
		}
		line, _ := json.Marshal(rec)
		sb.Write(line)
		sb.WriteByte('\n')
	}

	if result.HasMore && result.Cursor != "" {
		trailer, _ := json.Marshal(map[string]any{
			"_pagination": map[string]any{
				"has_more":    true,
				"next_cursor": result.Cursor,
			},
		})
		sb.Write(trailer)
		sb.WriteByte('\n')
	}

	return sb.String(), nil
}

// formatChatsPlain renders the chat list as plain text.
func formatChatsPlain(entries []chatEntry, result slack.ConversationListResult) string {
	var b strings.Builder

	if len(entries) == 0 {
		b.WriteString("No chats found.\n")
		return b.String()
	}

	for _, e := range entries {
		ts := e.LatestHuman
		if ts == "" {
			ts = "(no messages)"
		}
		fmt.Fprintf(&b, "%-12s  %-6s  %-40s  %s\n", e.ID, e.Type, e.Name, ts)
	}

	b.WriteString("\n")
	hasMoreStr := "false"
	if result.HasMore {
		hasMoreStr = "true"
	}
	fmt.Fprintf(&b, "--- %d chat%s | has_more: %s\n", len(entries), pluralS(len(entries)), hasMoreStr)
	if result.HasMore && result.Cursor != "" {
		fmt.Fprintf(&b, "    next: slackcli chats --cursor %s\n", result.Cursor)
	}
	b.WriteString("    Tip: slackcli history <id> to read messages\n")
	return b.String()
}
