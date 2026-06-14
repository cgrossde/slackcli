// Package cmd — read.go implements the "read" command.
//
// Layer 1: ReadMessage / ReadMessagePretty / ReadFile fetch and format Slack
// content. ReadFile handles file permalink URLs; the others handle message
// references. Layer 2 wiring (presenter) is applied in main.go.
package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/cgrossde/slackcli/internal/keychain"
	"github.com/cgrossde/slackcli/internal/slack"
)

// NewReadCmd builds the "read" Cobra command.
func NewReadCmd() *cobra.Command {
	var pretty bool
	var jsonMode bool
	var workspace string
	var output string
	var threadTs string
	readCmd := &cobra.Command{
		Use:   "read <url | channelID:ts | channelID:threadTs:replyTs | file-url>",
		Short: "Print a Slack message, thread, or download a file",
		Long: `Fetch and print a Slack message or full thread, or download a file attachment.

Accepted reference forms:

  slackcli read <permalink-url>
      https://myorg.slack.com/archives/C012ABC/p1718197925001234
      Workspace is extracted from the URL; --workspace is ignored.

  slackcli read <channelID>:<ts>
      C012ABC3456:1718197925.001234
      Workspace is resolved from --workspace, the stored default
      (slackcli auth default --workspace <name>), or the single
      saved workspace when only one exists.

  slackcli read <channelID>:<threadTs>:<replyTs>
      C012ABC3456:1718197000.000001:1718197925.001234
      Three-part form: fetches the thread rooted at threadTs and
      anchors to the specific reply at replyTs.
      Use --thread-ts as an alternative to the three-part form.

  slackcli read <file-permalink-url>
      https://myorg.slack.com/files/WUSER/FILEID/filename.ext
      Downloads the file. Saved to --output path or ./filename by default.

Credentials must already be saved (run: slackcli auth login).
If the message is a thread root or a reply, the full thread is printed.`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			ref := args[0]
			if slack.IsFileURL(ref) {
				out, err := ReadFile(ref, workspace, output)
				if out != "" {
					fmt.Fprint(c.OutOrStdout(), out)
				}
				return err
			}
			var (
				out string
				err error
			)
			switch {
			case jsonMode:
				out, err = ReadMessageJSON(ref, workspace, threadTs)
			case pretty:
				out, err = ReadMessagePretty(ref, workspace, threadTs)
			default:
				out, err = ReadMessage(ref, workspace, threadTs)
			}
			if out != "" {
				fmt.Fprint(c.OutOrStdout(), out)
			}
			return err
		},
	}
	readCmd.Flags().BoolVar(&pretty, "pretty", false, "Render with ANSI colours and markdown formatting")
	readCmd.Flags().BoolVar(&jsonMode, "json", false, "Output messages as NDJSON (one object per line)")
	readCmd.Flags().StringVarP(&workspace, "workspace", "w", "", "Workspace for channel:ts references (default: stored default or sole saved workspace)")
	readCmd.Flags().StringVarP(&output, "output", "o", "", "Output path for file downloads (default: ./<filename>)")
	readCmd.Flags().StringVar(&threadTs, "thread-ts", "", "Thread root timestamp; use with channelID:replyTs to read a specific reply in context")
	return readCmd
}

// resolveRef parses the user-supplied reference string into a MessageRef with
// all three fields populated.
//
//   - URL form: workspace is extracted from the URL; the workspace and
//     threadTs arguments are ignored for workspace, but threadTs is applied
//     when not already set by the URL's ?thread_ts= param.
//   - channel:ts form: workspace is used as-is if non-empty; otherwise
//     keychain.ResolveDefault() is called to find the default workspace.
//     threadTs, when non-empty, overrides any ThreadTs already set on the ref
//     (e.g. from the three-part channel:threadTs:replyTs form).
func resolveRef(raw, workspace, threadTs string) (slack.MessageRef, error) {
	ref, err := slack.ParseMessageRef(raw)
	if err != nil {
		// Check for the common case: a channel URL with no message timestamp.
		if slack.IsChannelURL(raw) {
			chRef, pErr := slack.ParseChannelURL(raw)
			chID := raw
			if pErr == nil {
				chID = chRef.ChannelID
			}
			return slack.MessageRef{}, fmt.Errorf(
				"this is a channel link, not a message permalink. To read recent channel messages:\n  slackcli history %s\nTo read a specific message, right-click it in Slack → Copy link.",
				chID,
			)
		}
		return slack.MessageRef{}, fmt.Errorf("%w\nUse: slackcli read <slack-permalink-url> or <channelID>:<ts>", err)
	}

	// --thread-ts flag overrides whatever the parsed ref already has.
	if threadTs != "" {
		ref.ThreadTs = threadTs
	}

	if ref.Workspace != "" {
		// URL form — workspace already set, ignore the flag.
		return ref, nil
	}

	// channel:ts form — need to resolve workspace.
	if workspace != "" {
		ref.Workspace = CanonicalDomain(workspace)
		return ref, nil
	}

	ws, err := keychain.ResolveDefault()
	if err != nil {
		return slack.MessageRef{}, fmt.Errorf("resolving workspace: %w", err)
	}
	ref.Workspace = ws
	return ref, nil
}

// fetchThreadWithClient loads credentials, fetches the thread, and returns the
// slack.Client alongside the messages and cache. ReadMessagePretty uses this
// to pass a file fetcher to PrettyThread.
//
// Enterprise Grid: if GetMessage returns ErrMessageNotFound for the default
// workspace, we check the channel cache for a previously resolved workspace
// and use that directly; otherwise we iterate the entry's GridWorkspaces list
// and retry.  resolvedWorkspace is the workspace that ultimately served the
// request — callers surface it when it differs from the default.
func fetchThreadWithClient(ref slack.MessageRef) (msgs []slack.Message, cache *slack.UserCache, client *slack.Client, resolvedWorkspace string, err error) {
	ws, entry, err := loadCredentials(ref.Workspace)
	if err != nil {
		return nil, nil, nil, "", err
	}

	// Fast path: check the channel cache.
	cc, _ := slack.LoadChannelCache()
	if cc != nil {
		if cachedWS, ok := cc.Get(ref.ChannelID); ok && cachedWS != ws {
			// Load credentials for the cached workspace.
			_, cachedEntry, cacheErr := loadCredentials(cachedWS)
			if cacheErr == nil {
				msgs, cache, client, retryErr := tryFetchThread(ref, cachedWS, cachedEntry, nil)
				if retryErr == nil {
					return msgs, cache, client, cachedWS, nil
				}
				// Cache hit but fetch failed — fall through to normal path.
			}
		}
	}

	msgs, cache, client, err = tryFetchThread(ref, ws, entry, nil)
	if err == nil {
		if cc != nil {
			cc.Set(ref.ChannelID, ws)
		}
		return msgs, cache, client, ws, nil
	}
	if !errors.Is(err, slack.ErrMessageNotFound) {
		return nil, nil, nil, "", err
	}

	// ErrMessageNotFound — try grid sibling workspaces from the keychain entry.
	siblings := gridWorkspaces(ws)
	if len(siblings) == 0 {
		// No grid metadata: fall back to scanning all saved workspaces (legacy).
		allEntries, _, listErr := keychain.List()
		if listErr == nil {
			for _, e := range allEntries {
				if e.Workspace == ws {
					continue
				}
				siblings = append(siblings, e.Workspace)
			}
		}
	}
	if len(siblings) == 0 {
		return nil, nil, nil, "", fmt.Errorf("fetching message: %w", err)
	}
	for _, sibWS := range siblings {
		if sibWS == ws {
			continue // already tried
		}
		_, sibEntry, loadErr := loadCredentials(sibWS)
		if loadErr != nil {
			continue
		}
		sibMsgs, sibCache, sibClient, altErr := tryFetchThread(ref, sibWS, sibEntry, nil)
		if altErr == nil {
			if cc != nil {
				cc.Set(ref.ChannelID, sibWS)
			}
			return sibMsgs, sibCache, sibClient, sibWS, nil
		}
		if !errors.Is(altErr, slack.ErrMessageNotFound) {
			return nil, nil, nil, "", fmt.Errorf("fetching message: %w", altErr)
		}
	}
	return nil, nil, nil, "", fmt.Errorf("fetching message: %w (tried %d workspace(s); use --workspace to specify one explicitly)", err, 1+len(siblings))
}

// tryFetchThread performs the actual GetMessage/GetThread calls for a given
// credential set. It is called by fetchThreadWithClient and retried across
// workspaces on ErrMessageNotFound.
// clientOverride, when non-nil, is used instead of building a new client from
// entry credentials. Intended for testing only.
func tryFetchThread(ref slack.MessageRef, ws string, entry keychain.Entry, clientOverride *slack.Client) ([]slack.Message, *slack.UserCache, *slack.Client, error) {
	client := clientOverride
	if client == nil {
		client = slack.NewClient(entry.Token, entry.Cookie)
	}

	cache, err := slack.NewUserCache(ws, client)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("opening user cache: %w", err)
	}

	// When the caller already knows the thread root (e.g. from a three-part
	// channelID:threadTs:replyTs ref or --thread-ts flag), skip the prefetch
	// and go straight to GetThread. This avoids a round-trip and sidesteps the
	// "message not found" failure that occurs when ref.Ts is a reply ts that
	// conversations.history cannot locate directly.
	if ref.ThreadTs != "" {
		messages, err := client.GetThread(ref.ChannelID, ref.ThreadTs)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("fetching thread: %w", err)
		}
		return messages, cache, client, nil
	}

	msg, err := client.GetMessage(ref.ChannelID, ref.Ts)
	if err != nil {
		return nil, nil, nil, err
	}

	isThreadRoot := msg.ThreadTs == msg.Ts && msg.ReplyCount > 0
	isReply := msg.ThreadTs != "" && msg.ThreadTs != msg.Ts

	if !isThreadRoot && !isReply {
		return []slack.Message{msg}, cache, client, nil
	}

	threadTs := msg.Ts
	if isReply {
		threadTs = msg.ThreadTs
	}

	messages, err := client.GetThread(ref.ChannelID, threadTs)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("fetching thread: %w", err)
	}
	return messages, cache, client, nil
}

// fetchThread loads credentials and user cache for ref.Workspace, fetches the
// target message, and returns the full thread (or single message) together
// with the populated cache.
func fetchThread(ref slack.MessageRef) ([]slack.Message, *slack.UserCache, string, error) {
	msgs, cache, _, resolvedWS, err := fetchThreadWithClient(ref)
	return msgs, cache, resolvedWS, err
}

// ReadMessage fetches and formats a Slack message or thread as plain text.
// ref is a permalink URL, channelID:ts, or channelID:threadTs:replyTs.
// workspace is used when ref is the channel:ts form.
// threadTs, when non-empty, overrides any thread root already in the ref.
func ReadMessage(ref, workspace, threadTs string) (string, error) {
	resolved, err := resolveRef(ref, workspace, threadTs)
	if err != nil {
		return "", err
	}
	messages, cache, client, resolvedWS, err := fetchThreadWithClient(resolved)
	if err != nil {
		return "", err
	}

	selfID := ""
	if client != nil {
		if auth, authErr := client.AuthTest(); authErr == nil && auth.OK {
			selfID = auth.UserID
		}
	}
	dmPeer := resolveDMPeer(resolved.ChannelID, selfID, messages, cache)

	var b strings.Builder
	if resolvedWS != "" && resolvedWS != resolved.Workspace {
		fmt.Fprintf(&b, "[workspace: %s]\n", resolvedWS)
	}
	for i, m := range messages {
		if i > 0 {
			b.WriteString("\n")
		}
		b.WriteString(formatMessage(m, i, cache, selfID, dmPeer))
	}
	return b.String(), nil
}

// ReadMessagePretty fetches and renders a Slack message or thread with ANSI
// colour, markdown formatting, and syntax-highlighted code blocks.
// When running in iTerm2, image file attachments are rendered inline.
// threadTs, when non-empty, overrides any thread root already in the ref.
func ReadMessagePretty(ref, workspace, threadTs string) (string, error) {
	resolved, err := resolveRef(ref, workspace, threadTs)
	if err != nil {
		return "", err
	}
	messages, cache, client, resolvedWS, err := fetchThreadWithClient(resolved)
	if err != nil {
		return "", err
	}
	var fetcher func(url string) ([]byte, string, error)
	if supportsInlineImages() {
		fetcher = client.FetchFileBytes
	}
	selfID := ""
	if client != nil {
		if auth, authErr := client.AuthTest(); authErr == nil && auth.OK {
			selfID = auth.UserID
		}
	}
	dmPeer := resolveDMPeer(resolved.ChannelID, selfID, messages, cache)
	out, err := PrettyThread(messages, cache, fetcher, selfID, dmPeer)
	if err != nil {
		return "", err
	}
	if resolvedWS != "" && resolvedWS != resolved.Workspace {
		out = fmt.Sprintf("[workspace: %s]\n", resolvedWS) + out
	}
	return out, nil
}

// formatMessage renders a single message as plain text suitable for LLM consumption.
// index is the 0-based position in the thread (0 = root, ≥1 = reply).
// cache resolves user IDs to display names; nil is safe (falls back to raw ID).
// selfID is the authenticated user's Slack user ID; pass "" to skip self-annotation.
// dmPeer is the display name of the DM conversation partner; non-empty only for
// 1:1 DM channels, resolved by the caller via resolveDMPeer.
//
// Header format (exactly 120 chars):
//
//	== <author>  <ts> ═══…═══[ message ]==      (regular channel)
//	== DM: You → Peer  <ts> ═══…═══[ message ]==  (DM, sender is self)
//	== DM: Peer → You  <ts> ═══…═══[ message ]==  (DM, sender is peer)
func formatMessage(m slack.Message, index int, cache *slack.UserCache, selfID, dmPeer string) string {
	var b strings.Builder

	// Right anchor: "[ message ]==" or "[ reply N ]=="
	label := fmt.Sprintf("[ reply %d ]==", index)
	if index == 0 {
		label = "[ message ]=="
	}

	// Resolve author; substitute "You" when the sender is the authenticated user.
	author := resolveAuthor(m, cache)
	isSelf := selfID != "" && m.User == selfID
	if isSelf {
		author = "You"
	}

	// For DM channels, replace the bare author with the directional DM label.
	// Use ShortLabel for the sender name — the peer name already uses ShortLabel
	// (from resolveDMPeer), and matching formats keeps the label compact.
	if dmPeer != "" {
		senderShort := author
		if !isSelf && cache != nil && m.User != "" {
			if u, err := cache.GetUser(m.User); err == nil {
				senderShort = u.ShortLabel()
			}
		}
		author = dmLabel(isSelf, senderShort, dmPeer)
	}

	// Parse ts: "1778570615.840589" → "2026-05-12 07:23" (UTC, no seconds, no TZ label).
	tsHuman := m.Ts
	if f, err := strconv.ParseFloat(m.Ts, 64); err == nil {
		sec := int64(math.Trunc(f))
		tsHuman = time.Unix(sec, 0).UTC().Format("2006-01-02 15:04")
	}

	// "== <author> <ts> " is the left part; fill with '=' to hit exactly 120.
	const lineWidth = 120
	left := fmt.Sprintf("== %s %s ", author, tsHuman)
	fill := lineWidth - len(left) - len(label)
	if fill < 1 {
		fill = 1
	}
	fmt.Fprintf(&b, "%s%s%s\n", left, strings.Repeat("=", fill), label)

	// Resolve <@WXXXX> mentions in the message body.
	text := m.Text
	if cache != nil {
		text = cache.ResolveUserMentions(text)
	}
	fmt.Fprintf(&b, "%s\n", text)

	// Files — one line per file: "  [file] name (PrettyType)"
	// followed by "  → slackcli read <permalink>" when a permalink is available,
	// or the raw url_private when there is no permalink.
	for _, f := range m.Files {
		label := f.Name
		if label == "" {
			label = f.Title
		}
		typ := f.PrettyType
		if typ == "" {
			typ = f.Mimetype
		}
		if typ != "" {
			fmt.Fprintf(&b, "  [file] %s (%s)\n", label, typ)
		} else {
			fmt.Fprintf(&b, "  [file] %s\n", label)
		}
		if f.Permalink != "" {
			fmt.Fprintf(&b, "  → slackcli read %s\n", f.Permalink)
		} else if f.URLPrivate != "" {
			fmt.Fprintf(&b, "  → slackcli read %s\n", f.URLPrivate)
		}
	}

	// Reactions — one line: "  Reactions: :thumbsup: ×3  :ok: ×1"
	if len(m.Reactions) > 0 {
		b.WriteString("  Reactions: ")
		b.WriteString(formatReactions(m.Reactions, selfID))
		b.WriteByte('\n')
	}

	// Attachments — each rendered as an indented block.
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
		if a.TitleLink != "" && a.Title == "" {
			header = a.TitleLink
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

// formatReactions formats a slice of reactions as a compact string.
// If selfID is non-empty and appears in a reaction's Users list, the count
// is annotated: "×1 (you)", "×4 (you + 3 others)"; bare "×N" when the
// authenticated user did not react.
func formatReactions(reactions []slack.Reaction, selfID string) string {
	var b strings.Builder
	for i, r := range reactions {
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
		switch {
		case isSelf && r.Count == 1:
			fmt.Fprintf(&b, ":%s: ×1 (you)", r.Name)
		case isSelf:
			fmt.Fprintf(&b, ":%s: ×%d (you + %d others)", r.Name, r.Count, r.Count-1)
		default:
			fmt.Fprintf(&b, ":%s: ×%d", r.Name, r.Count)
		}
	}
	return b.String()
}

// resolveAuthor returns a display string for the message author.
// Priority: UserCache lookup → bot username → bot ID → "(unknown)".
func resolveAuthor(m slack.Message, cache *slack.UserCache) string {
	if m.User != "" && cache != nil {
		if u, err := cache.GetUser(m.User); err == nil {
			return u.Label()
		}
	}
	if m.User != "" {
		return m.User
	}
	if m.Username != "" {
		return m.Username + " (bot)"
	}
	if m.BotID != "" {
		return m.BotID + " (bot)"
	}
	return "(unknown)"
}

// channelTypeFromID infers channel type from Slack channel ID prefix.
// C → "channel", D → "dm", G → "group" (legacy private group).
func channelTypeFromID(id string) string {
	if len(id) == 0 {
		return "channel"
	}
	switch id[0] {
	case 'D':
		return "dm"
	case 'G':
		return "group"
	default:
		return "channel"
	}
}

// dmLabel builds the directional DM header prefix shown instead of the bare
// author name for 1:1 direct-message channels.
//
//	sender is self  →  "DM: You → PeerName"
//	sender is peer  →  "DM: PeerName → You"
//	self-DM         →  "DM: You → Self"  (peerName == "You")
func dmLabel(senderIsSelf bool, senderName, peerName string) string {
	if peerName == "You" {
		// self-DM: both sides are the same user
		return "DM: You → Self"
	}
	if senderIsSelf {
		return "DM: You → " + peerName
	}
	return "DM: " + senderName + " → You"
}

// resolveDMPeer returns the display name of the other party in a 1:1 DM
// thread or history result. It scans messages for the first user ID that
// differs from selfID and resolves it via cache (ShortLabel). Returns "" when
// selfID is empty, the channel is not a DM, or no peer can be identified.
func resolveDMPeer(channelID, selfID string, messages []slack.Message, cache *slack.UserCache) string {
	if selfID == "" || len(channelID) == 0 || channelID[0] != 'D' {
		return ""
	}
	for _, m := range messages {
		if m.User == "" || m.User == selfID {
			continue
		}
		if cache != nil {
			if u, err := cache.GetUser(m.User); err == nil {
				return u.ShortLabel()
			}
		}
		return m.User
	}
	// All messages are from self (self-DM or single-message thread). Return
	// "You" so dmLabel can produce "DM: You → Self".
	if len(messages) > 0 {
		return "You"
	}
	return ""
}

// readMessageJSON is the JSON representation of one message in a thread.
type readMessageJSON struct {
	UserID      string           `json:"user_id"`
	Username    string           `json:"username"`
	DisplayName string           `json:"display_name"`
	Ts          string           `json:"ts"`
	ThreadTs    string           `json:"thread_ts"`
	Text        string           `json:"text"`
	IsRoot      bool             `json:"is_root"`
	ReplyCount  int              `json:"reply_count,omitempty"`
	ChannelID   string           `json:"channel_id"`
	ChannelType string           `json:"channel_type"`
	Workspace   string           `json:"workspace,omitempty"`
	Files       []fileJSON       `json:"files,omitempty"`
	Reactions   []reactionJSON   `json:"reactions,omitempty"`
	Attachments []attachmentJSON `json:"attachments,omitempty"`
}

type fileJSON struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	PrettyType string `json:"pretty_type,omitempty"`
	Mimetype   string `json:"mimetype,omitempty"`
	Permalink  string `json:"permalink,omitempty"`
	URLPrivate string `json:"url_private,omitempty"`
}

type reactionJSON struct {
	Name  string   `json:"name"`
	Count int      `json:"count"`
	Users []string `json:"users,omitempty"`
}

type attachmentJSON struct {
	AuthorName  string `json:"author_name,omitempty"`
	AuthorLink  string `json:"author_link,omitempty"`
	Title       string `json:"title,omitempty"`
	TitleLink   string `json:"title_link,omitempty"`
	Pretext     string `json:"pretext,omitempty"`
	Text        string `json:"text,omitempty"`
	FromURL     string `json:"from_url,omitempty"`
	ServiceName string `json:"service_name,omitempty"`
	ImageURL    string `json:"image_url,omitempty"`
	ThumbURL    string `json:"thumb_url,omitempty"`
	Footer      string `json:"footer,omitempty"`
}

// ReadMessageJSON fetches and formats a Slack message or thread as NDJSON.
// Each message in the thread is emitted as one JSON object per line.
// threadTs, when non-empty, overrides any thread root already in the ref.
func ReadMessageJSON(ref, workspace, threadTs string) (string, error) {
	resolved, err := resolveRef(ref, workspace, threadTs)
	if err != nil {
		return "", err
	}
	messages, cache, resolvedWS, err := fetchThread(resolved)
	if err != nil {
		return "", err
	}

	chanType := channelTypeFromID(resolved.ChannelID)
	var sb strings.Builder

	for i, m := range messages {
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
			IsRoot:      i == 0,
			ReplyCount:  m.ReplyCount,
			ChannelID:   resolved.ChannelID,
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
	return sb.String(), nil
}

// ReadFile downloads a Slack file identified by a file permalink URL and writes
// it to disk. outputPath overrides the destination; if empty the file is saved
// to ./<filename> (the name from the URL, or from files.info if the URL has no
// name segment). Returns a one-line summary: "Saved: <path> (<size> bytes)".
//
// workspace is used only when the URL does not contain an explicit workspace
// (unusual for file permalinks, but accepted for consistency).
func ReadFile(rawURL, workspace, outputPath string) (string, error) {
	ref, err := slack.ParseFileRef(rawURL)
	if err != nil {
		return "", fmt.Errorf("invalid file URL: %w", err)
	}

	// Resolve workspace — file permalinks always embed the org-level host
	// (e.g. "acme.enterprise.slack.com") which differs from the workspace domain
	// the user logged into (e.g. "myorg.slack.com"). In Slack Enterprise
	// Grid, a token from any member workspace is valid for the whole org, so if
	// the exact domain isn't in the keychain we fall back to the default.
	ws := ref.Workspace
	if ws == "" {
		if workspace != "" {
			ws = CanonicalDomain(workspace)
		} else {
			ws, err = keychain.ResolveDefault()
			if err != nil {
				return "", fmt.Errorf("resolving workspace: %w", err)
			}
		}
	}

	entry, err := keychain.Load(ws)
	if err != nil {
		if !errors.Is(err, keychain.ErrNotFound) {
			return "", fmt.Errorf("loading credentials for %q: %w", ws, err)
		}
		// Enterprise Grid: the file URL domain is the org domain, not a member
		// workspace domain. Fall back to the default stored workspace.
		ws, err = keychain.ResolveDefault()
		if err != nil {
			return "", fmt.Errorf("no credentials for %q and no default workspace (run: slackcli auth login): %w", ref.Workspace, err)
		}
		entry, err = keychain.Load(ws)
		if err != nil {
			return "", fmt.Errorf("no credentials for workspace %q (run: slackcli auth login --workspace %s): %w", ws, ws, err)
		}
	}
	client := slack.NewClient(entry.Token, entry.Cookie)
	return downloadFile(client, ref, outputPath)
}

// fileClient is the subset of slack.Client used by downloadFile.
// Extracted as an interface so the download logic is unit-testable without
// a real Slack API or keychain.
type fileClient interface {
	GetFileInfo(fileID string) (slack.File, error)
	FetchFileBytes(url string) ([]byte, string, error)
}

// downloadFile performs the actual files.info lookup, download, and disk
// write. Separated from ReadFile so tests can inject a fake client.
func downloadFile(client fileClient, ref slack.FileRef, outputPath string) (string, error) {
	// Fetch file metadata to get url_private and the canonical filename.
	info, err := client.GetFileInfo(ref.FileID)
	if err != nil {
		return "", fmt.Errorf("fetching file info: %w", err)
	}

	// Determine download URL — url_private is always authenticated.
	downloadURL := info.URLPrivate
	if downloadURL == "" {
		return "", fmt.Errorf("file %s has no download URL (may have been deleted)", ref.FileID)
	}

	// Determine the save path.
	filename := info.Name
	if filename == "" {
		filename = ref.Filename
	}
	if filename == "" {
		filename = ref.FileID
	}
	dest := outputPath
	if dest == "" {
		dest = filename
	}

	data, _, err := client.FetchFileBytes(downloadURL)
	if err != nil {
		return "", fmt.Errorf("downloading file: %w", err)
	}

	if err := os.WriteFile(dest, data, 0o644); err != nil {
		return "", fmt.Errorf("writing %s: %w", dest, err)
	}

	absPath, err := filepath.Abs(dest)
	if err != nil {
		absPath = dest // fallback; extremely unlikely
	}
	return fmt.Sprintf("Saved: %s (%d bytes)\n", absPath, len(data)), nil
}
