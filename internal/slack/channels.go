// Package slack — channels.go fetches the user's channel membership list from
// internal Slack APIs that are not blocked on Enterprise Grid.
//
// Two endpoints are used:
//
//   client.counts  — fast; returns all channel/IM/MPIM IDs with unread counts.
//                    Used every cycle to discover what has unread activity.
//
//   client.userBoot — slower; returns full channel metadata (name, is_private,
//                     is_starred, is_mpim, is_im).
package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// ChannelInfo is a Slack channel or conversation returned by the internal APIs.
type ChannelInfo struct {
	ID          string
	Name        string // empty for IMs (peer user ID is in User field instead)
	IsChannel   bool   // public or private channel (not DM, not MPIM)
	IsIM        bool   // 1:1 DM
	IsMpIM      bool   // multi-party DM
	IsPrivate   bool
	IsArchived  bool
	IsStarred   bool
	User        string // for IMs: peer user ID
	HasUnreads  bool
	MentionCount int
	LatestTs    string
	LastRead    string
}

// ChannelCountsResult holds the result of a client.counts call.
type ChannelCountsResult struct {
	// All combines channels, IMs, and MPIMs in one slice, sorted by the
	// caller. Each entry has ID, HasUnreads, MentionCount, LatestTs.
	All []ChannelInfo
}

// GetChannelCounts calls client.counts and returns unread/mention state for
// every channel, IM, and MPIM the authenticated user is a member of.
// Names are NOT populated — cross with GetChannelDirectory for full metadata.
func (c *Client) GetChannelCounts(workspace string) (ChannelCountsResult, error) {
	apiBase := "https://slack.com"
	if workspace != "" {
		apiBase = "https://" + strings.TrimPrefix(workspace, "https://")
		apiBase = strings.TrimSuffix(apiBase, "/")
	}

	form := url.Values{}
	form.Set("token", c.token)
	form.Set("_x_reason", "client.counts")
	form.Set("_x_mode", "online")
	form.Set("_x_sonic", "true")
	form.Set("_x_app_name", "client")

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		apiBase+"/api/client.counts",
		strings.NewReader(form.Encode()))
	if err != nil {
		return ChannelCountsResult{}, fmt.Errorf("client.counts: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return ChannelCountsResult{}, fmt.Errorf("client.counts: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ChannelCountsResult{}, fmt.Errorf("client.counts: read body: %w", err)
	}

	var raw struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
		Channels []struct {
			ID           string  `json:"id"`
			HasUnreads   bool    `json:"has_unreads"`
			MentionCount int     `json:"mention_count"`
			Latest       string  `json:"latest"`
			LastRead     string  `json:"last_read"`
		} `json:"channels"`
		IMs []struct {
			ID           string  `json:"id"`
			HasUnreads   bool    `json:"has_unreads"`
			MentionCount int     `json:"mention_count"`
			Latest       string  `json:"latest"`
			LastRead     string  `json:"last_read"`
		} `json:"ims"`
		MPIMs []struct {
			ID           string  `json:"id"`
			HasUnreads   bool    `json:"has_unreads"`
			MentionCount int     `json:"mention_count"`
			Latest       string  `json:"latest"`
			LastRead     string  `json:"last_read"`
		} `json:"mpims"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return ChannelCountsResult{}, fmt.Errorf("client.counts: parse: %w", err)
	}
	if !raw.OK {
		return ChannelCountsResult{}, fmt.Errorf("client.counts: %s", raw.Error)
	}

	all := make([]ChannelInfo, 0, len(raw.Channels)+len(raw.IMs)+len(raw.MPIMs))
	for _, ch := range raw.Channels {
		all = append(all, ChannelInfo{
			ID: ch.ID, IsChannel: true,
			HasUnreads: ch.HasUnreads, MentionCount: ch.MentionCount,
			LatestTs: ch.Latest, LastRead: ch.LastRead,
		})
	}
	for _, im := range raw.IMs {
		all = append(all, ChannelInfo{
			ID: im.ID, IsIM: true,
			HasUnreads: im.HasUnreads, MentionCount: im.MentionCount,
			LatestTs: im.Latest, LastRead: im.LastRead,
		})
	}
	for _, mp := range raw.MPIMs {
		all = append(all, ChannelInfo{
			ID: mp.ID, IsMpIM: true,
			HasUnreads: mp.HasUnreads, MentionCount: mp.MentionCount,
			LatestTs: mp.Latest, LastRead: mp.LastRead,
		})
	}
	return ChannelCountsResult{All: all}, nil
}

// ChannelDirectory is the full metadata for all channels from client.userBoot.
// Used for the slow scan / channel list UI. Keyed by channel ID.
type ChannelDirectory struct {
	Channels []ChannelInfo
	// Starred is the set of starred channel IDs.
	Starred map[string]bool
}

// GetChannelDirectory calls client.userBoot and returns full channel metadata
// including names, private/archived flags, and starred status.
// This is a slow call (~1–3s) suitable for one-time scans, not hot-path polling.
func (c *Client) GetChannelDirectory(workspace string) (ChannelDirectory, error) {
	apiBase := "https://slack.com"
	if workspace != "" {
		apiBase = "https://" + strings.TrimPrefix(workspace, "https://")
		apiBase = strings.TrimSuffix(apiBase, "/")
	}

	form := url.Values{}
	form.Set("token", c.token)
	form.Set("_x_reason", "client.userBoot")
	form.Set("_x_mode", "online")
	form.Set("_x_sonic", "true")
	form.Set("_x_app_name", "client")

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		apiBase+"/api/client.userBoot",
		strings.NewReader(form.Encode()))
	if err != nil {
		return ChannelDirectory{}, fmt.Errorf("client.userBoot: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return ChannelDirectory{}, fmt.Errorf("client.userBoot: %w", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ChannelDirectory{}, fmt.Errorf("client.userBoot: read body: %w", err)
	}

	var raw struct {
		OK    bool   `json:"ok"`
		Error string `json:"error"`
		Channels []struct {
			ID         string `json:"id"`
			Name       string `json:"name"`
			IsChannel  bool   `json:"is_channel"`
			IsIM       bool   `json:"is_im"`
			IsMpIM     bool   `json:"is_mpim"`
			IsPrivate  bool   `json:"is_private"`
			IsArchived bool   `json:"is_archived"`
			User       string `json:"user"` // for IMs
		} `json:"channels"`
		IMs []struct {
			ID   string `json:"id"`
			User string `json:"user"`
			IsIM bool   `json:"is_im"`
		} `json:"ims"`
		Starred []json.RawMessage `json:"starred"` // array of channel ID strings
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return ChannelDirectory{}, fmt.Errorf("client.userBoot: parse: %w", err)
	}
	if !raw.OK {
		return ChannelDirectory{}, fmt.Errorf("client.userBoot: %s", raw.Error)
	}

	// Build starred set.
	starred := make(map[string]bool, len(raw.Starred))
	for _, s := range raw.Starred {
		var id string
		if json.Unmarshal(s, &id) == nil && id != "" {
			starred[id] = true
		}
	}

	channels := make([]ChannelInfo, 0, len(raw.Channels)+len(raw.IMs))
	for _, ch := range raw.Channels {
		channels = append(channels, ChannelInfo{
			ID:         ch.ID,
			Name:       ch.Name,
			IsChannel:  ch.IsChannel && !ch.IsMpIM && !ch.IsIM,
			IsIM:       ch.IsIM,
			IsMpIM:     ch.IsMpIM,
			IsPrivate:  ch.IsPrivate,
			IsArchived: ch.IsArchived,
			IsStarred:  starred[ch.ID],
			User:       ch.User,
		})
	}
	// IMs from userBoot come separately as well; add any not already in channels.
	seen := make(map[string]bool, len(channels))
	for _, ch := range channels {
		seen[ch.ID] = true
	}
	for _, im := range raw.IMs {
		if !seen[im.ID] {
			channels = append(channels, ChannelInfo{
				ID: im.ID, IsIM: true, User: im.User, IsStarred: starred[im.ID],
			})
		}
	}

	return ChannelDirectory{Channels: channels, Starred: starred}, nil
}
