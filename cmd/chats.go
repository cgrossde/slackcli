// Package cmd — chats.go implements the "chats" command.
//
// Layer 1: Chats lists conversations sorted by most-recently active.
// Supported types: dm, mpdm, channel, all (DMs+MPDMs only), all-with-channels.
// Layer 2 wiring (presenter) is applied in main.go.
package cmd

import (
	"context"
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
	// Type filters output:
	//   all                — DMs + MPDMs (default, fast via client.counts)
	//   dm                 — 1:1 DMs only
	//   mpdm               — multi-party DMs only
	//   channel            — joined channels only (slow, via client.counts)
	//   all-with-channels  — everything (slow)
	//   unread             — all conversations with has_unreads=true (fast)
	Type   string
	Cursor string
	JSON   bool
}

// NewChatsCmd builds the "chats" Cobra command.
func NewChatsCmd() *cobra.Command {
	var flags ChatsFlags

	cmd := &cobra.Command{
		Use:   "chats",
		Short: "List recent DMs, group chats, and channels",
		Long: `List your most-recently active conversations sorted by last activity.

Use --type to control what is returned:

  all                  DMs + MPDMs (default, fast)
  dm                   1:1 direct messages only
  mpdm                 multi-party DMs only
  channel              joined channels only
  all-with-channels    DMs + MPDMs + channels
  unread               all conversations with unread messages

The "channel", "all-with-channels", and "unread" modes use client.counts
which works on Enterprise Grid workspaces where conversations.list is blocked.
Names for channels are resolved via conversations.info (adds ~1ms per channel).

Credentials must already be saved (run: slackcli auth login).`,
		Example: `  slackcli chats
  slackcli chats --type dm
  slackcli chats --type channel
  slackcli chats --type unread
  slackcli chats --type all-with-channels --count 50
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
	cmd.Flags().IntVarP(&flags.Count, "count", "n", 20, "Number of conversations to return (1–200)")
	cmd.Flags().StringVarP(&flags.Type, "type", "t", "all", "Filter: all, dm, mpdm, channel, all-with-channels, unread")
	cmd.Flags().StringVar(&flags.Cursor, "cursor", "", "Pagination cursor from a previous response")
	cmd.Flags().BoolVar(&flags.JSON, "json", false, "Output as NDJSON (one object per line)")
	return cmd
}

// chatsFetch is the core fetch logic shared by Chats and ChatsJSON.
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
	cache, _ := slack.NewUserCache(workspace, client)

	count := flags.Count
	if count < 1 {
		count = 1
	} else if count > 200 {
		count = 200
	}

	t := strings.ToLower(flags.Type)

	// Modes that include channels use client.counts (Enterprise Grid safe).
	if t == "channel" || t == "all-with-channels" || t == "unread" {
		return chatsFetchWithChannels(flags, t, count, workspace, client, cache)
	}

	// DM/MPDM-only modes use the existing ListConversations path.
	types, err := chatsTypes(t)
	if err != nil {
		return nil, slack.ConversationListResult{}, nil, err
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

	sortConversationsByLatest(result.Conversations)
	if len(result.Conversations) > count {
		result.Conversations = result.Conversations[:count]
		result.HasMore = false
	}
	client.ResolveConversationNames(result.Conversations)
	entries := buildChatEntries(result.Conversations, cache)
	return entries, result, cache, nil
}

// chatsFetchWithChannels handles the channel-aware modes by calling
// client.counts and optionally resolving names via conversations.info.
func chatsFetchWithChannels(flags ChatsFlags, t string, count int, workspace string, client *slack.Client, cache *slack.UserCache) ([]chatEntry, slack.ConversationListResult, *slack.UserCache, error) {
	counts, err := client.GetChannelCounts(workspace)
	if err != nil {
		return nil, slack.ConversationListResult{}, nil, fmt.Errorf("listing conversations: %w", err)
	}

	// Filter by type.
	filtered := make([]slack.ChannelInfo, 0, len(counts.All))
	for _, ch := range counts.All {
		switch t {
		case "channel":
			if ch.IsChannel {
				filtered = append(filtered, ch)
			}
		case "unread":
			if ch.HasUnreads || ch.MentionCount > 0 {
				filtered = append(filtered, ch)
			}
		case "all-with-channels":
			filtered = append(filtered, ch)
		}
	}

	// Sort by LatestTs descending, then mentions, then has_unreads.
	sort.SliceStable(filtered, func(i, j int) bool {
		ti, tj := filtered[i].LatestTs, filtered[j].LatestTs
		if ti != tj {
			if ti == "" {
				return false
			}
			if tj == "" {
				return true
			}
			return ti > tj
		}
		if filtered[i].MentionCount != filtered[j].MentionCount {
			return filtered[i].MentionCount > filtered[j].MentionCount
		}
		return filtered[i].HasUnreads && !filtered[j].HasUnreads
	})

	if len(filtered) > count {
		filtered = filtered[:count]
	}

	// Resolve names: for IMs use peer lookup; for channels use conversations.info.
	// We do this for the trimmed set only to minimise round-trips.
	ctx := context.Background()
	for i := range filtered {
		ch := &filtered[i]
		if ch.Name == "" {
			name, err := client.GetChannelName(ctx, ch.ID)
			if err == nil {
				ch.Name = name
				if ch.IsIM {
					ch.User = name // GetChannelName returns peer user ID for DMs
				}
			}
		}
	}

	// Convert to chatEntry.
	entries := make([]chatEntry, 0, len(filtered))
	for _, ch := range filtered {
		e := chatEntry{
			ID:        ch.ID,
			RawName:   ch.Name,
			PeerID:    ch.User,
			LatestTs:  ch.LatestTs,
			IsStarred: ch.IsStarred,
			HasUnreads: ch.HasUnreads,
			MentionCount: ch.MentionCount,
		}
		switch {
		case ch.IsIM:
			e.Type = "dm"
			e.Name = resolveUserDisplay(ch.User, cache)
		case ch.IsMpIM:
			e.Type = "mpdm"
			e.Name = resolveMpdmName(ch.Name, nil, cache)
		default:
			e.Type = "channel"
			e.Name = "#" + ch.Name
		}
		if ch.LatestTs != "" {
			if f, err := strconv.ParseFloat(ch.LatestTs, 64); err == nil {
				e.LatestHuman = time.Unix(int64(f), 0).UTC().Format("2006-01-02 15:04")
			}
		}
		entries = append(entries, e)
	}

	result := slack.ConversationListResult{HasMore: false}
	return entries, result, cache, nil
}

// chatsTypes returns the conversations.list types slice for the given flag value.
func chatsTypes(t string) ([]string, error) {
	switch t {
	case "all", "":
		return []string{"im", "mpim"}, nil
	case "dm", "im":
		return []string{"im"}, nil
	case "mpdm", "mpim":
		return []string{"mpim"}, nil
	default:
		return nil, fmt.Errorf("unknown --type %q: valid values are all, dm, mpdm, channel, all-with-channels, unread", t)
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

// chatEntry is a resolved conversation ready for display.
type chatEntry struct {
	ID           string
	Type         string // "dm", "mpdm", "channel"
	Name         string // human-readable
	RawName      string
	PeerID       string
	Members      []string
	MemberIDs    []string
	LatestTs     string
	LatestHuman  string
	Priority     float64
	IsStarred    bool
	HasUnreads   bool
	MentionCount int
}

// buildChatEntries converts Conversation slices (from ListConversations) to chatEntries.
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
		return ti > tj
	})
	return entries
}

// resolveUserDisplay returns "@DisplayName" for a user ID.
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
func resolveMpdmName(rawName string, members []string, cache *slack.UserCache) string {
	if len(members) > 0 && cache != nil {
		names := make([]string, 0, len(members))
		for _, id := range members {
			names = append(names, resolveUserDisplay(id, cache))
		}
		return strings.Join(names, ", ")
	}
	name := rawName
	name = strings.TrimPrefix(name, "mpdm-")
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
		ID           string   `json:"id"`
		Type         string   `json:"type"`
		Name         string   `json:"name"`
		RawName      string   `json:"raw_name,omitempty"`
		PeerID       string   `json:"peer_id,omitempty"`
		MemberIDs    []string `json:"member_ids,omitempty"`
		LatestTs     string   `json:"latest_ts,omitempty"`
		IsStarred    bool     `json:"is_starred,omitempty"`
		HasUnreads   bool     `json:"has_unreads,omitempty"`
		MentionCount int      `json:"mention_count,omitempty"`
	}

	var sb strings.Builder
	for _, e := range entries {
		rec := chatJSON{
			ID:           e.ID,
			Type:         e.Type,
			Name:         e.Name,
			RawName:      e.RawName,
			PeerID:       e.PeerID,
			MemberIDs:    e.MemberIDs,
			LatestTs:     e.LatestTs,
			IsStarred:    e.IsStarred,
			HasUnreads:   e.HasUnreads,
			MentionCount: e.MentionCount,
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
		unread := ""
		if e.MentionCount > 0 {
			unread = fmt.Sprintf(" [%d mention(s)]", e.MentionCount)
		} else if e.HasUnreads {
			unread = " [unread]"
		}
		starred := ""
		if e.IsStarred {
			starred = " *"
		}
		fmt.Fprintf(&b, "%-12s  %-8s  %-40s  %s%s%s\n",
			e.ID, e.Type, e.Name, ts, unread, starred)
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
