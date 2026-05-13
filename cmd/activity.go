// Package cmd — activity.go implements the "activity" command.
//
// Layer 1: Activity fetches and formats the Slack Activity feed
// (mentions, reactions, thread replies, DMs, keyword alerts, etc.).
// Layer 2 wiring (presenter) is applied in main.go.
package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/spf13/cobra"


	"github.com/cgrossde/slackcli/internal/slack"
)

// ActivityFlags holds parsed flag values for the activity command.
type ActivityFlags struct {
	Workspace string
	Unread    bool
	Count     int
	Cursor    string
	Type      string // raw flag value; comma-separated aliases or API names
	JSON      bool
}

// typeAliases maps user-friendly shorthand names to one or more raw API types.
// Values that are already raw API names pass through unchanged via the fallback
// in expandTypeAliases.
var typeAliases = map[string][]string{
	"reaction":        {"message_reaction"},
	"thread":          {"thread_v2"},
	"mention":         {"at_user"},
	"dm":              {"dm"},
	"keyword":         {"keyword"},
	"group_mention":   {"at_user_group"},
	"channel_mention": {"at_channel", "at_everyone"},
	"invite":          {"internal_channel_invite", "external_channel_invite"},
}

// expandTypeAliases converts a comma-separated --type flag value into the raw
// Slack API type names to pass to activity.feed. Unknown tokens are passed
// through verbatim so raw API names like "list_record_edited" still work.
// Returns nil when input is empty.
func expandTypeAliases(input string) []string {
	if strings.TrimSpace(input) == "" {
		return nil
	}
	tokens := strings.Split(input, ",")
	out := make([]string, 0, len(tokens))
	seen := make(map[string]bool, len(tokens))
	for _, tok := range tokens {
		tok = strings.TrimSpace(tok)
		if tok == "" {
			continue
		}
		if expanded, ok := typeAliases[tok]; ok {
			for _, t := range expanded {
				if !seen[t] {
					seen[t] = true
					out = append(out, t)
				}
			}
		} else {
			if !seen[tok] {
				seen[tok] = true
				out = append(out, tok)
			}
		}
	}
	return out
}

// NewActivityCmd builds the "activity" Cobra command.
func NewActivityCmd() *cobra.Command {
	var flags ActivityFlags

	cmd := &cobra.Command{
		Use:   "activity",
		Short: "Show your Slack Activity feed",
		Long: `Fetch your Slack Activity feed — the same items shown in the Activity panel.

Includes @mentions, emoji reactions on your messages, thread replies, DMs,
keyword alerts, and channel invites.

Message text is fetched automatically so each item is immediately actionable.
Use "slackcli read <channel_id:ts>" for the full thread.

TYPE ALIASES  (comma-separated with --type):
  reaction        message_reaction
  thread          thread_v2
  mention         at_user
  dm              dm
  keyword         keyword
  group_mention   at_user_group
  channel_mention at_channel,at_everyone
  invite          internal_channel_invite,external_channel_invite

Raw API type names (e.g. list_record_edited) are also accepted.
Omit --type to receive all activity types (Slack default).`,
		Example: `  slackcli activity
  slackcli activity --unread
  slackcli activity --count 10 --type reaction,mention
  slackcli activity --type thread
  slackcli activity --json --count 5
  slackcli activity --cursor dXNlcjpVMDYx`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, _ []string) error {
			out, err := Activity(flags)
			if err != nil {
				return err
			}
			_, werr := fmt.Fprint(c.OutOrStdout(), out)
			return werr
		},
	}

	f := cmd.Flags()
	f.StringVarP(&flags.Workspace, "workspace", "w", "", "Workspace (required if >1 saved)")
	f.BoolVar(&flags.Unread, "unread", false, "Show only unread activity")
	f.IntVarP(&flags.Count, "count", "n", 20, "Items per request (1–50)")
	f.StringVar(&flags.Cursor, "cursor", "", "Pagination cursor from a previous response")
	f.StringVarP(&flags.Type, "type", "t", "", "Filter by type alias or raw API name (comma-separated)")
	f.BoolVar(&flags.JSON, "json", false, "Output as NDJSON (one object per line)")

	return cmd
}

// Activity is the Layer 1 implementation. It resolves credentials, calls the
// API, batch-fetches message text, and returns formatted output.
func Activity(flags ActivityFlags) (string, error) {
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

	count := flags.Count
	if count < 1 {
		count = 1
	} else if count > 50 {
		count = 50
	}

	types := expandTypeAliases(flags.Type)

	result, err := client.GetActivityFeed(context.Background(), slack.ActivityFeedParams{
		Limit:  count,
		Cursor: flags.Cursor,
		Unread: flags.Unread,
		Types:  types,
	})
	if err != nil {
		return "", fmt.Errorf("fetching activity feed: %w", err)
	}

	// Fetch messages and channel names concurrently — independent operations.
	var (
		msgs     map[string]slack.Message
		chanNames map[string]string
	)
	done := make(chan struct{}, 2)
	go func() {
		msgs = fetchActivityMessages(client, result.Items)
		done <- struct{}{}
	}()
	go func() {
		chanNames = resolveActivityChannelNames(client, result.Items, cache)
		done <- struct{}{}
	}()
	<-done
	<-done

	// Backfill ActorUserID for items where the feed didn't carry one
	// (thread_v2 and some DM types). Use the fetched message's User field.
	for i, item := range result.Items {
		if item.ActorUserID == "" {
			key := item.ChannelID + ":" + item.MessageTs
			if msg, ok := msgs[key]; ok && msg.User != "" {
				result.Items[i].ActorUserID = msg.User
			}
		}
	}

	// Extract text map for formatters.
	texts := make(map[string]string, len(msgs))
	for k, m := range msgs {
		texts[k] = m.Text
	}

	if flags.JSON {
		return formatActivityJSON(result, texts, chanNames, cache), nil
	}
	return formatActivityPlain(result, texts, chanNames, cache, flags), nil
}

// ──────────────────────────────────────────────────────────────────────────────
// Message fetching
// ──────────────────────────────────────────────────────────────────────────────

// fetchActivityMessages fetches one Message per distinct (channelID, ts) pair
// across all items concurrently. Returns a map keyed by "channelID:ts".
// For thread_v2 items the latest reply lives in a thread, so conversations.history
// won't find it — GetReply (conversations.replies) is used instead.
func fetchActivityMessages(client *slack.Client, items []slack.ActivityItem) map[string]slack.Message {
	type work struct {
		key      string
		item     slack.ActivityItem
	}

	// Deduplicate by key before spawning goroutines.
	seen := make(map[string]bool, len(items))
	var jobs []work
	for _, item := range items {
		if item.ChannelID == "" || item.MessageTs == "" {
			continue
		}
		key := item.ChannelID + ":" + item.MessageTs
		if seen[key] {
			continue
		}
		seen[key] = true
		jobs = append(jobs, work{key: key, item: item})
	}

	type result struct {
		key string
		msg slack.Message
	}
	sem := make(chan struct{}, 5)
	ch := make(chan result, len(jobs))
	for _, j := range jobs {
		go func(j work) {
			sem <- struct{}{}
			defer func() { <-sem }()
			var msg slack.Message
			var err error
			if j.item.Type == "thread_v2" && j.item.ThreadTs != "" && j.item.ThreadTs != j.item.MessageTs {
				msg, err = client.GetReply(j.item.ChannelID, j.item.ThreadTs, j.item.MessageTs)
			} else {
				msg, err = client.GetMessage(j.item.ChannelID, j.item.MessageTs)
			}
			if err != nil {
				msg = slack.Message{}
			}
			ch <- result{key: j.key, msg: msg}
		}(j)
	}

	msgs := make(map[string]slack.Message, len(jobs))
	for range jobs {
		r := <-ch
		msgs[r.key] = r.msg
	}
	return msgs
}

// resolveActivityChannelNames returns a channelID → display label map for
// all distinct channel IDs in items concurrently. DM channels (D…) are
// mapped to "@DisplayName" via cache; others use conversations.info.
func resolveActivityChannelNames(client *slack.Client, items []slack.ActivityItem, cache *slack.UserCache) map[string]string {
	// Collect unique channel IDs.
	seen := make(map[string]bool, len(items))
	var channelIDs []string
	for _, item := range items {
		if item.ChannelID == "" || seen[item.ChannelID] {
			continue
		}
		seen[item.ChannelID] = true
		channelIDs = append(channelIDs, item.ChannelID)
	}

	type result struct {
		id    string
		label string
	}
	sem := make(chan struct{}, 5)
	ch := make(chan result, len(channelIDs))
	for _, id := range channelIDs {
		go func(id string) {
			sem <- struct{}{}
			defer func() { <-sem }()
			name, err := client.GetChannelName(context.Background(), id)
			if err != nil {
				ch <- result{id: id, label: id}
				return
			}
			if len(id) > 0 && id[0] == 'D' {
				label := "@" + name
				if cache != nil && name != "" {
					if u, uerr := cache.GetUser(name); uerr == nil {
						label = "@" + u.ShortLabel()
					}
				}
				ch <- result{id: id, label: label}
			} else {
				ch <- result{id: id, label: "#" + name}
			}
		}(id)
	}

	names := make(map[string]string, len(channelIDs))
	for range channelIDs {
		r := <-ch
		names[r.id] = r.label
	}
	return names
}

// ──────────────────────────────────────────────────────────────────────────────
// Plain-text formatter
// ──────────────────────────────────────────────────────────────────────────────

func formatActivityPlain(
	result slack.ActivityFeedResult,
	texts map[string]string,
	chanNames map[string]string,
	cache *slack.UserCache,
	flags ActivityFlags,
) string {
	var sb strings.Builder

	if len(result.Items) == 0 {
		sb.WriteString("No activity items.\n")
		return sb.String()
	}

	for i, item := range result.Items {
		channel := activityChannelLabel(item, chanNames)
		desc := activityDescription(item, cache)
		tsStr := formatSearchTs(item.FeedTs)

		fmt.Fprintf(&sb, "[%d] %s · %s · %s\n", i+1, channel, desc, tsStr)

		key := item.ChannelID + ":" + item.MessageTs
		if text, ok := texts[key]; ok && text != "" {
			if cache != nil {
				text = cache.ResolveUserMentions(text)
			}
			truncated := truncateRunes(text, maxTextRunes)
			indented := "    " + strings.ReplaceAll(truncated, "\n", "\n    ")
			fmt.Fprintf(&sb, "%s\n", indented)
		}

		if item.ChannelID != "" && item.MessageTs != "" {
			// For thread_v2, emit the three-part channelID:threadTs:replyTs form
			// so the read hint carries both the thread root (for fetching) and
			// the specific reply ts (for context). For all other types the
			// two-part channel:ts form is sufficient.
			if item.Type == "thread_v2" && item.ThreadTs != "" {
				fmt.Fprintf(&sb, "    → slackcli read %s:%s:%s\n", item.ChannelID, item.ThreadTs, item.MessageTs)
			} else {
				fmt.Fprintf(&sb, "    → slackcli read %s:%s\n", item.ChannelID, item.MessageTs)
			}
		}
		sb.WriteByte('\n')
	}

	// Pagination footer.
	fmt.Fprintf(&sb, "--- %d items", len(result.Items))
	if result.HasMore && result.NextCursor != "" {
		sb.WriteString(" | next: slackcli activity")
		if flags.Workspace != "" {
			fmt.Fprintf(&sb, " --workspace %s", flags.Workspace)
		}
		if flags.Unread {
			sb.WriteString(" --unread")
		}
		if flags.Count != 20 {
			fmt.Fprintf(&sb, " --count %d", flags.Count)
		}
		if flags.Type != "" {
			fmt.Fprintf(&sb, " --type %s", flags.Type)
		}
		fmt.Fprintf(&sb, " --cursor %s", result.NextCursor)
	}
	sb.WriteString(" ---\n")

	return sb.String()
}

// activityChannelLabel returns the display label for the channel in an item.
func activityChannelLabel(item slack.ActivityItem, chanNames map[string]string) string {
	if label, ok := chanNames[item.ChannelID]; ok && label != "" {
		return label
	}
	if item.ChannelID != "" {
		return item.ChannelID
	}
	return "(unknown)"
}

// activityDescription returns a human-readable phrase for what happened.
func activityDescription(item slack.ActivityItem, cache *slack.UserCache) string {
	actor := resolveActivityActor(item, cache)
	switch item.Type {
	case "message_reaction":
		emoji := item.Reaction
		if emoji == "" {
			emoji = "?"
		}
		return fmt.Sprintf("%s reacted :%s:", actor, emoji)
	case "thread_v2":
		return fmt.Sprintf("%s replied in thread", actor)
	case "at_user":
		return fmt.Sprintf("%s mentioned you", actor)
	case "at_user_group":
		return fmt.Sprintf("%s mentioned your group", actor)
	case "at_channel":
		return fmt.Sprintf("%s used @channel", actor)
	case "at_everyone":
		return fmt.Sprintf("%s used @everyone", actor)
	case "keyword":
		return fmt.Sprintf("%s triggered keyword alert", actor)
	case "dm":
		return fmt.Sprintf("%s · DM", actor)
	case "bot_dm_bundle":
		return fmt.Sprintf("%s · bot DM", actor)
	case "internal_channel_invite", "external_channel_invite":
		return fmt.Sprintf("%s invited you", actor)
	case "shared_workspace_invite":
		return fmt.Sprintf("%s shared workspace invite", actor)
	case "external_dm_invite":
		return fmt.Sprintf("%s external DM invite", actor)
	default:
		if actor != "" {
			return fmt.Sprintf("%s · %s", actor, item.Type)
		}
		return item.Type
	}
}

// resolveActivityActor returns a display name for the actor in an item.
// For reactions the actor is ActorUserID; for all other types it falls back
// to AuthorUserID when ActorUserID is empty.
func resolveActivityActor(item slack.ActivityItem, cache *slack.UserCache) string {
	id := item.ActorUserID
	if id == "" {
		id = item.AuthorUserID
	}
	if id == "" {
		return ""
	}
	if cache != nil {
		if u, err := cache.GetUser(id); err == nil {
			return u.ShortLabel()
		}
	}
	return id
}

// ──────────────────────────────────────────────────────────────────────────────
// JSON formatter
// ──────────────────────────────────────────────────────────────────────────────

// activityItemJSON is the NDJSON representation of one activity item.
type activityItemJSON struct {
	Type        string `json:"type"`
	FeedTs      string `json:"feed_ts"`
	IsUnread    bool   `json:"is_unread"`
	ChannelID   string `json:"channel_id"`
	ChannelName string `json:"channel_name"`
	Ts          string `json:"ts"`
	ThreadTs    string `json:"thread_ts,omitempty"`
	UserID      string `json:"user_id"`
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
	Text        string `json:"text"`
	Reaction    string `json:"reaction,omitempty"`
	ReactorID   string `json:"reactor_id,omitempty"`
	ReactorName string `json:"reactor_name,omitempty"`
}

// activityPaginationJSON is the trailer emitted when more items exist.
type activityPaginationJSON struct {
	Pagination struct {
		HasMore    bool   `json:"has_more"`
		NextCursor string `json:"next_cursor"`
	} `json:"_pagination"`
}

func formatActivityJSON(
	result slack.ActivityFeedResult,
	texts map[string]string,
	chanNames map[string]string,
	cache *slack.UserCache,
) string {
	var sb strings.Builder

	for _, item := range result.Items {
		rec := activityItemJSON{
			Type:        item.Type,
			FeedTs:      item.FeedTs,
			IsUnread:    item.IsUnread,
			ChannelID:   item.ChannelID,
			ChannelName: activityChannelLabel(item, chanNames),
			Ts:          item.MessageTs,
			ThreadTs:    item.ThreadTs,
		}

		// Actor
		actorID := item.ActorUserID
		if actorID == "" {
			actorID = item.AuthorUserID
		}
		rec.UserID = actorID
		if cache != nil && actorID != "" {
			if u, err := cache.GetUser(actorID); err == nil {
				rec.Username = u.Name
				rec.DisplayName = u.ShortLabel()
			}
		}

		// Text preview
		key := item.ChannelID + ":" + item.MessageTs
		if text, ok := texts[key]; ok {
			if cache != nil {
				text = cache.ResolveUserMentions(text)
			}
			rec.Text = truncateRunes(text, maxTextRunes)
		}

		// Reaction fields
		if item.Type == "message_reaction" && item.Reaction != "" {
			rec.Reaction = item.Reaction
			rec.ReactorID = item.ActorUserID
			if cache != nil && item.ActorUserID != "" {
				if u, err := cache.GetUser(item.ActorUserID); err == nil {
					rec.ReactorName = u.ShortLabel()
				}
			}
			// UserID/Username/DisplayName should reflect the author (whose msg got the reaction).
			rec.UserID = item.AuthorUserID
			if cache != nil && item.AuthorUserID != "" {
				if u, err := cache.GetUser(item.AuthorUserID); err == nil {
					rec.Username = u.Name
					rec.DisplayName = u.ShortLabel()
				}
			}
		}

		line, _ := json.Marshal(rec)
		sb.Write(line)
		sb.WriteByte('\n')
	}

	if result.HasMore && result.NextCursor != "" {
		var trailer activityPaginationJSON
		trailer.Pagination.HasMore = true
		trailer.Pagination.NextCursor = result.NextCursor
		line, _ := json.Marshal(trailer)
		sb.Write(line)
		sb.WriteByte('\n')
	}

	return sb.String()
}
