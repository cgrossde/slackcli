// Package cmd — open.go implements the "open" command.
//
// Layer 1: Open builds a slack:// deep-link URL for the given target and
// invokes the macOS `open` command so the native Slack desktop client jumps
// to the channel/DM/message/thread/file. With --print, the URL is returned
// for piping or testing instead of being launched.
//
// Layer 2 wiring (presenter) is applied in main.go.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"runtime"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cgrossde/slackcli/internal/keychain"
	"github.com/cgrossde/slackcli/internal/slack"
)

// OpenFlags holds parsed flag values for the open command.
type OpenFlags struct {
	Workspace string
	Print     bool // --print: emit the slack:// URL to stdout instead of launching it
}

// NewOpenCmd builds the "open" Cobra command.
func NewOpenCmd() *cobra.Command {
	var flags OpenFlags

	cmd := &cobra.Command{
		Use:   "open <url | channelID[:ts[:replyTs]] | channelID | #channel | @user | file-url>",
		Short: "Open a Slack channel, DM, message, thread, or file in the Slack desktop app",
		Long: `Open a Slack target in the native desktop app via the slack:// URL scheme.

Accepted target forms:

  Message permalink (jumps to the exact message; thread reply opens the thread):
    https://myorg.slack.com/archives/C012ABC/p1718197925001234
    https://myorg.slack.com/archives/C012ABC/p1718197925001234?thread_ts=1718197000.000001

  Channel permalink:
    https://myorg.slack.com/archives/C012ABC

  channelID:ts compact ref:
    C012ABC3456:1718197925.001234
    C012ABC3456:1718197000.000001:1718197925.001234   (three-part: thread root + reply)

  Bare channel/DM/MPDM ID:
    C012ABC3456    public/private channel
    D012ABC3456    1:1 DM
    G012ABC3456    multi-party DM

  Channel name:
    #general       resolved via search.modules.channels

  User mention (opens or resumes a 1:1 DM):
    @alice         resolved via the user cache + edge user search

  File permalink:
    https://myorg.slack.com/files/UUSER/F012ABC/filename.ext

The Slack desktop app must be installed. The per-workspace team ID is resolved
on first use via client.userBoot and cached in the keychain entry; subsequent
opens are zero round-trip.`,
		Example: `  slackcli open https://myorg.slack.com/archives/C012ABC/p1718197925001234
  slackcli open C012ABC3456:1718197925.001234
  slackcli open C012ABC3456
  slackcli open '#general'
  slackcli open @alice
  slackcli open --print C012ABC3456`,
		Args: cobra.ExactArgs(1),
		RunE: func(c *cobra.Command, args []string) error {
			out, err := Open(args[0], flags)
			if err != nil {
				return err
			}
			_, werr := fmt.Fprint(c.OutOrStdout(), out)
			return werr
		},
	}

	f := cmd.Flags()
	f.StringVarP(&flags.Workspace, "workspace", "w", "", "Workspace (defaults to saved default)")
	f.BoolVar(&flags.Print, "print", false, "Print the slack:// URL instead of launching it")

	return cmd
}

// Open resolves target into a slack:// URL and either launches it via macOS
// `open` or returns it for printing (when flags.Print is true).
func Open(target string, flags OpenFlags) (string, error) {
	target = strings.TrimSpace(target)
	if target == "" {
		return "", errors.New("open: target must not be empty")
	}

	url, err := buildDeepLink(target, flags)
	if err != nil {
		return "", err
	}
	return launch(url, flags)
}

// buildDeepLink dispatches on target form and returns the slack:// URL. The
// network is touched only when needed (team-ID lookup, channel-name search,
// user search, conversations.open).
func buildDeepLink(target string, flags OpenFlags) (string, error) {
	// 1. File permalink.
	if slack.IsFileURL(target) {
		ref, err := slack.ParseFileRef(target)
		if err != nil {
			return "", fmt.Errorf("open: %w", err)
		}
		team, err := teamIDFor(ref.Workspace)
		if err != nil {
			return "", err
		}
		return slack.DeepLinkFile(team, ref.FileID)
	}

	// 2. Other Slack URLs.
	if strings.HasPrefix(target, "https://") || strings.HasPrefix(target, "http://") {
		if slack.IsChannelURL(target) {
			ref, err := slack.ParseChannelURL(target)
			if err != nil {
				return "", fmt.Errorf("open: %w", err)
			}
			return buildChannelLink(ref.Workspace, ref.ChannelID, flags)
		}
		ref, err := slack.ParseSlackURL(target)
		if err != nil {
			return "", fmt.Errorf("open: %w", err)
		}
		return buildMessageLink(ref, flags)
	}

	// 3. channelID:ts (or three-part) compact ref.
	if slack.IsChannelTs(target) {
		ref, err := slack.ParseChannelTs(target)
		if err != nil {
			return "", fmt.Errorf("open: %w", err)
		}
		return buildMessageLink(ref, flags)
	}

	// 4. Bare channel/DM/MPDM ID.
	if looksLikeChannelID(target) {
		return buildChannelLink("", target, flags)
	}

	// 5. User mention.
	if strings.HasPrefix(target, "@") {
		return buildUserLink(strings.TrimPrefix(target, "@"), flags)
	}

	// 6. Channel name (with or without leading #).
	if strings.HasPrefix(target, "#") || isPlainName(target) {
		return buildChannelByNameLink(strings.TrimPrefix(target, "#"), flags)
	}

	return "", fmt.Errorf("open: cannot interpret target %q (expected a Slack URL, channelID, channelID:ts, #channel, or @user)", target)
}

// buildMessageLink resolves the workspace + team ID and builds a slack:// URL
// with message= (and thread_ts= when applicable).
func buildMessageLink(ref slack.MessageRef, flags OpenFlags) (string, error) {
	workspace, err := pickWorkspace(ref.Workspace, flags.Workspace)
	if err != nil {
		return "", err
	}
	team, err := teamIDFor(workspace)
	if err != nil {
		return "", err
	}
	return slack.DeepLinkMessage(team, ref.ChannelID, ref.Ts, ref.ThreadTs)
}

// buildChannelLink builds a slack://channel deep link with no message anchor.
// workspace may be empty; pickWorkspace falls back through flags and keychain
// default.
func buildChannelLink(workspace, channelID string, flags OpenFlags) (string, error) {
	ws, err := pickWorkspace(workspace, flags.Workspace)
	if err != nil {
		return "", err
	}
	team, err := teamIDFor(ws)
	if err != nil {
		return "", err
	}
	return slack.DeepLinkChannel(team, channelID)
}

// buildChannelByNameLink resolves "name" (or "#name") to a channel ID and
// builds the deep link.
func buildChannelByNameLink(name string, flags OpenFlags) (string, error) {
	ws, err := pickWorkspace("", flags.Workspace)
	if err != nil {
		return "", err
	}
	_, entry, err := loadCredentials(ws)
	if err != nil {
		return "", err
	}
	client := slack.NewClient(entry.Token, entry.Cookie)
	channelID, err := resolveChannelName(client, ws, name)
	if err != nil {
		return "", fmt.Errorf("open: %w", err)
	}
	return buildChannelLink(ws, channelID, flags)
}

// buildUserLink resolves "@handle" to a user ID, opens (or resumes) the 1:1
// DM, and deep-links to the resulting IM channel.
func buildUserLink(handle string, flags OpenFlags) (string, error) {
	if handle == "" {
		return "", errors.New("open: empty user handle")
	}
	ws, err := pickWorkspace("", flags.Workspace)
	if err != nil {
		return "", err
	}
	_, entry, err := loadCredentials(ws)
	if err != nil {
		return "", err
	}
	client := slack.NewClient(entry.Token, entry.Cookie)
	userID, err := resolveUserHandle(client, ws, handle)
	if err != nil {
		return "", fmt.Errorf("open: %w", err)
	}
	channelID, err := client.OpenIM(context.Background(), userID)
	if err != nil {
		return "", fmt.Errorf("open: %w", err)
	}
	return buildChannelLink(ws, channelID, flags)
}

// resolveUserHandle finds the user ID for handle. Looks in the local user
// cache first, then falls back to the edge SearchUsers API. Already-formed
// user IDs (U… / W…) pass through.
func resolveUserHandle(client *slack.Client, workspace, handle string) (string, error) {
	if looksLikeUserID(handle) {
		return handle, nil
	}
	if cache, err := slack.NewUserCache(workspace, client); err == nil {
		hits := cache.Search(handle)
		if id := pickUserExact(hits, handle); id != "" {
			return id, nil
		}
		if len(hits) == 1 {
			return hits[0].ID, nil
		}
	}

	auth, err := client.AuthTest()
	if err != nil {
		return "", fmt.Errorf("auth.test: %w", err)
	}
	enterpriseID := auth.EnterpriseID
	if enterpriseID == "" {
		enterpriseID = auth.TeamID
	}
	results, err := client.SearchUsers(context.Background(), handle, enterpriseID)
	if err != nil {
		return "", fmt.Errorf("user search %q: %w", handle, err)
	}
	if id := pickUserExact(results, handle); id != "" {
		return id, nil
	}
	switch len(results) {
	case 0:
		return "", fmt.Errorf("user %q not found", handle)
	case 1:
		return results[0].ID, nil
	}
	suggestions := make([]string, 0, len(results))
	for _, u := range results {
		label := u.DisplayName
		if label == "" {
			label = u.Name
		}
		suggestions = append(suggestions, label)
	}
	return "", fmt.Errorf("user %q is ambiguous; matches: %s", handle, strings.Join(suggestions, ", "))
}

// pickUserExact returns the ID of the user whose handle exactly matches name
// (case-insensitive against display name or username). Empty string when no
// exact match is found.
func pickUserExact(users []slack.CachedUser, name string) string {
	for _, u := range users {
		if strings.EqualFold(u.DisplayName, name) || strings.EqualFold(u.Name, name) {
			return u.ID
		}
	}
	return ""
}

// pickWorkspace returns the workspace domain to use, in priority order:
//  1. ref-extracted workspace (URL form)
//  2. --workspace flag
//  3. stored default (keychain)
//
// All three are canonicalised to a *.slack.com domain.
func pickWorkspace(refWorkspace, flagWorkspace string) (string, error) {
	switch {
	case refWorkspace != "":
		return CanonicalDomain(refWorkspace), nil
	case flagWorkspace != "":
		return CanonicalDomain(flagWorkspace), nil
	}
	ws, err := keychain.ResolveDefault()
	if err != nil {
		return "", fmt.Errorf("resolving workspace: %w", err)
	}
	return ws, nil
}

// teamIDFor returns the per-workspace team ID for workspace, populating it
// lazily via client.userBoot when the keychain entry has no cached value.
//
// Slack Enterprise Grid exposes both an enterprise ID (E…) and per-workspace
// IDs (T…); the slack:// deep link scheme resolves channels relative to the
// member workspace, so we MUST cache the T… value here even when an E… is
// available on the entry.
func teamIDFor(workspace string) (string, error) {
	resolved, entry, err := loadCredentials(workspace)
	if err != nil {
		return "", err
	}
	if entry.TeamID != "" {
		return entry.TeamID, nil
	}
	client := slack.NewClient(entry.Token, entry.Cookie)
	teamID, err := client.TeamID(context.Background(), resolved)
	if err != nil {
		return "", fmt.Errorf("team ID for %s: %w", resolved, err)
	}
	entry.TeamID = teamID
	if saveErr := keychain.Save(entry); saveErr != nil {
		// Non-fatal: we still have the team ID for this invocation. Surface
		// the failure in slog, not stdout, so it doesn't pollute the URL.
		fmt.Fprintf(stderrSink, "warning: caching team ID for %s failed: %v\n", resolved, saveErr)
	}
	return teamID, nil
}

// stderrSink lets tests redirect the warning above. Production uses os.Stderr.
var stderrSink io.Writer = os.Stderr

// launch runs the macOS `open` command on url, or returns the URL for printing
// when flags.Print is true. Printing always succeeds; launching is gated to
// macOS — slackcli is a Mac-only tool, but we prefer a clear error to a
// silent no-op on other platforms.
func launch(url string, flags OpenFlags) (string, error) {
	if flags.Print {
		return url + "\n", nil
	}
	if runtime.GOOS != "darwin" {
		return "", fmt.Errorf("open: only macOS is supported (got %s); use --print to emit the URL", runtime.GOOS)
	}
	cmd := exec.Command("/usr/bin/open", url)
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("open: launching %s: %w", url, err)
	}
	return url + "\n", nil
}

// isPlainName reports whether s looks like a bare Slack channel name without
// a leading "#": lowercase letters, digits, hyphens, underscores, optional
// dots. Used to disambiguate "#general" / "general" from a stray ID prefix.
func isPlainName(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
		default:
			return false
		}
	}
	return true
}
