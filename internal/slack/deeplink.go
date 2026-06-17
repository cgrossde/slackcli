// Package slack — deeplink.go builds slack:// URIs that the macOS Slack
// desktop app handles natively.
//
// Verified behaviour (macOS Slack 4.x, 2025):
//
//	slack://channel?team=<T>&id=<C>                                     → opens channel
//	slack://channel?team=<T>&id=<D…>                                    → opens 1:1 DM
//	slack://channel?team=<T>&id=<G…>                                    → opens MPDM
//	slack://channel?team=<T>&id=<C>&message=<dotted-ts>                 → channel + scroll/highlight message
//	slack://channel?team=<T>&id=<C>&message=<reply-ts>&thread_ts=<root> → opens thread side-pane on the reply
//	slack://file?team=<T>&id=<F…>                                       → opens file viewer (per Slack docs)
//	slack://open?team=<T>                                               → switches workspace
//
// Notes:
//   - The `message=` parameter takes the dotted Slack timestamp ("1781608222.892579"),
//     not the dot-stripped p-form used in HTTPS permalinks. Callers MUST pass the
//     API form; ParseSlackURL already normalises permalinks to that form.
//   - The `slack://user?team=&id=USERID` form documented by Slack only opens the
//     user profile, not a DM. To start/open a DM use the IM channel ID (D…) with
//     the `channel` form.
//   - team must be the per-workspace team ID (T…) returned by client.userBoot,
//     not the enterprise ID (E…). On Enterprise Grid the enterprise ID can route
//     to a different workspace's channel of the same name.
package slack

import (
	"fmt"
	"net/url"
	"strings"
)

// DeepLinkChannel returns slack://channel?team=<team>&id=<channel>.
// Works for public/private channels (C…), 1:1 DMs (D…), and MPDMs (G…).
func DeepLinkChannel(team, channel string) (string, error) {
	if err := requireTeam(team); err != nil {
		return "", err
	}
	if channel == "" {
		return "", fmt.Errorf("deep link: channel ID must not be empty")
	}
	return "slack://channel?team=" + url.QueryEscape(team) +
		"&id=" + url.QueryEscape(channel), nil
}

// DeepLinkMessage returns a slack:// URL that opens the channel and scrolls
// to the message at ts. ts must be the dotted Slack API form
// ("1718197925.001234"), not the p-form from HTTPS permalinks.
//
// When threadTs is non-empty and differs from ts, the URL opens the thread
// side-pane anchored at the reply.
func DeepLinkMessage(team, channel, ts, threadTs string) (string, error) {
	if err := requireTeam(team); err != nil {
		return "", err
	}
	if channel == "" {
		return "", fmt.Errorf("deep link: channel ID must not be empty")
	}
	if ts == "" {
		return "", fmt.Errorf("deep link: message ts must not be empty")
	}
	if !strings.Contains(ts, ".") {
		return "", fmt.Errorf("deep link: ts %q must be in dotted form (e.g. 1718197925.001234)", ts)
	}

	var b strings.Builder
	b.Grow(80 + len(team) + len(channel) + len(ts) + len(threadTs))
	b.WriteString("slack://channel?team=")
	b.WriteString(url.QueryEscape(team))
	b.WriteString("&id=")
	b.WriteString(url.QueryEscape(channel))
	b.WriteString("&message=")
	b.WriteString(url.QueryEscape(ts))
	if threadTs != "" && threadTs != ts {
		b.WriteString("&thread_ts=")
		b.WriteString(url.QueryEscape(threadTs))
	}
	return b.String(), nil
}

// DeepLinkFile returns slack://file?team=<team>&id=<fileID>.
func DeepLinkFile(team, fileID string) (string, error) {
	if err := requireTeam(team); err != nil {
		return "", err
	}
	if fileID == "" {
		return "", fmt.Errorf("deep link: file ID must not be empty")
	}
	return "slack://file?team=" + url.QueryEscape(team) +
		"&id=" + url.QueryEscape(fileID), nil
}

// DeepLinkWorkspace returns slack://open?team=<team>, which switches the
// desktop client to that workspace.
func DeepLinkWorkspace(team string) (string, error) {
	if err := requireTeam(team); err != nil {
		return "", err
	}
	return "slack://open?team=" + url.QueryEscape(team), nil
}

func requireTeam(team string) error {
	if team == "" {
		return fmt.Errorf("deep link: team ID must not be empty")
	}
	if team[0] != 'T' && team[0] != 'E' {
		return fmt.Errorf("deep link: team ID %q must start with T or E", team)
	}
	return nil
}
