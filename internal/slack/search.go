// Package slack — search.go calls search.messages directly via HTTP so we
// can capture fields (thread_ts) that slack-go's SearchMessage struct omits.
package slack

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// SearchParams controls sorting and pagination for SearchMessages.
type SearchParams struct {
	Sort    string // "score" (default) or "timestamp"
	SortDir string // "desc" (default) or "asc"
	Count   int    // results per page; 0 → API default (20)
	Page    int    // 1-indexed; 0 → API default (1)
}

// SearchMatch is one result from a search.messages response.
type SearchMatch struct {
	ChannelID      string
	ChannelName    string
	IsMPIM         bool     // true for multi-party DMs (mpdm-... channels)
	DMPeerID       string   // for 1:1 DMs: the peer's user/workspace ID (from channel name)
	ParticipantIDs []string // for MPDMs: participant IDs parsed from the mpdm-...-1 name
	UserID         string
	Username       string // legacy handle field from the API
	Ts             string // raw Slack timestamp (e.g. "1718200320.123456")
	ThreadTs       string // thread root timestamp; empty for top-level messages
	Permalink      string // full permalink URL; empty when the API omits it
	Workspace      string // host extracted from permalink (e.g. "myorg.slack.com"); empty when omitted
	Text           string
}

// SearchResult is the parsed response from search.messages.
type SearchResult struct {
	Query   string
	Total   int // total matches across all pages (from Paging.Total)
	Page    int // current page (from Paging.Page)
	Pages   int // total pages (from Paging.Pages)
	Count   int // results per page (from Paging.Count)
	Matches []SearchMatch
}

// searchAPIURL is the Slack Web API endpoint for message search.
const searchAPIURL = "https://slack.com/api/search.messages"

// searchRawChannel is the wire shape of the channel object in a search match.
type searchRawChannel struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	IsMPIM bool   `json:"is_mpim"`
}

// searchRawMessage is the wire shape of one match from search.messages.
// We define our own struct so we can capture thread_ts, which slack-go omits.
type searchRawMessage struct {
	Channel   searchRawChannel `json:"channel"`
	User      string           `json:"user"`
	Username  string           `json:"username"`
	Ts        string           `json:"ts"`
	ThreadTs  string           `json:"thread_ts"`
	Text      string           `json:"text"`
	Permalink string           `json:"permalink"`
}

// searchRawPaging mirrors Slack's paging object.
type searchRawPaging struct {
	Total int `json:"total"`
	Page  int `json:"page"`
	Pages int `json:"pages"`
	Count int `json:"count"`
}

// searchRawResponse is the top-level JSON envelope from search.messages.
type searchRawResponse struct {
	OK       bool   `json:"ok"`
	Error    string `json:"error"`
	Query    string `json:"query"`
	Messages struct {
		Matches []searchRawMessage `json:"matches"`
		Paging  searchRawPaging    `json:"paging"`
	} `json:"messages"`
}

// SearchMessages calls search.messages directly via HTTP and returns a
// SearchResult. An empty result (Total=0, Matches nil) is not an error —
// callers should check Total.
func (c *Client) SearchMessages(query string, params SearchParams) (SearchResult, error) {
	form := url.Values{}
	form.Set("token", c.token)
	form.Set("query", query)
	if params.Sort != "" {
		form.Set("sort", params.Sort)
	}
	if params.SortDir != "" {
		form.Set("sort_dir", params.SortDir)
	}
	if params.Count > 0 {
		form.Set("count", strconv.Itoa(params.Count))
	}
	if params.Page > 0 {
		form.Set("page", strconv.Itoa(params.Page))
	}

	req, err := http.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		searchAPIURL,
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return SearchResult{}, fmt.Errorf("search.messages: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	httpClient := c.httpClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return SearchResult{}, fmt.Errorf("search.messages: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return SearchResult{}, fmt.Errorf("search.messages: read response: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return SearchResult{}, fmt.Errorf("search.messages: HTTP %d", resp.StatusCode)
	}

	var raw searchRawResponse
	if err := json.Unmarshal(body, &raw); err != nil {
		return SearchResult{}, fmt.Errorf("search.messages: parse response: %w", err)
	}
	if !raw.OK {
		return SearchResult{}, slackError("search.messages", raw.Error)
	}

	matches := make([]SearchMatch, 0, len(raw.Messages.Matches))
	for _, m := range raw.Messages.Matches {
		matches = append(matches, fromSearchRawMessage(m))
	}

	return SearchResult{
		Query:   query,
		Total:   raw.Messages.Paging.Total,
		Page:    raw.Messages.Paging.Page,
		Pages:   raw.Messages.Paging.Pages,
		Count:   raw.Messages.Paging.Count,
		Matches: matches,
	}, nil
}

// fromSearchRawMessage converts a searchRawMessage to our SearchMatch type.
// thread_ts is not returned as a top-level field by search.messages; it is
// extracted from the ?thread_ts= query parameter of the permalink URL.
func fromSearchRawMessage(m searchRawMessage) SearchMatch {
	// Extract thread_ts and workspace from the permalink URL.
	threadTs := m.ThreadTs
	workspace := ""
	if m.Permalink != "" {
		if u, err := url.Parse(m.Permalink); err == nil {
			if threadTs == "" {
				threadTs = u.Query().Get("thread_ts")
			}
			workspace = u.Host
		}
	}
	// A message whose thread_ts equals its own ts is a thread root, not a
	// reply — treat it as top-level.
	if threadTs == m.Ts {
		threadTs = ""
	}

	sm := SearchMatch{
		ChannelID:   m.Channel.ID,
		ChannelName: m.Channel.Name,
		IsMPIM:      m.Channel.IsMPIM,
		UserID:      m.User,
		Username:    m.Username,
		Ts:          m.Ts,
		ThreadTs:    threadTs,
		Permalink:   m.Permalink,
		Workspace:   workspace,
		Text:        m.Text,
	}

	if m.Channel.IsMPIM {
		sm.ParticipantIDs = parseMPDMParticipants(m.Channel.Name)
	} else if len(m.Channel.ID) > 0 && m.Channel.ID[0] == 'D' {
		// 1:1 DM: the channel name field holds the peer's user/workspace ID.
		sm.DMPeerID = m.Channel.Name
	}

	return sm
}

// parseMPDMParticipants extracts participant IDs from an MPDM channel name.
//
// Slack's MPDM name format is: mpdm-<id>--<id>--...-1
// Example: "mpdm-u123456--u345678--u567890-1" → ["u123456", "u345678", "u567890"]
//
// The trailing "-1" (or "-N" for disambiguated names) is stripped by finding
// the last "--"-delimited segment and checking whether it ends in "-<digit>".
func parseMPDMParticipants(name string) []string {
	// Strip "mpdm-" prefix.
	s := strings.TrimPrefix(name, "mpdm-")
	if s == name {
		return nil // not an mpdm name
	}

	// The name ends with "-1" (or "-N") — strip the numeric suffix.
	// Find the last "-" that precedes a pure digit sequence.
	if idx := strings.LastIndex(s, "-"); idx >= 0 {
		suffix := s[idx+1:]
		allDigits := len(suffix) > 0
		for _, r := range suffix {
			if r < '0' || r > '9' {
				allDigits = false
				break
			}
		}
		if allDigits {
			s = s[:idx]
		}
	}

	// Split on "--" to get individual participant IDs.
	parts := strings.Split(s, "--")
	var ids []string
	for _, p := range parts {
		if p != "" {
			ids = append(ids, p)
		}
	}
	return ids
}
