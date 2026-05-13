// Package slack — channels_search.go queries the undocumented
// search.modules.channels endpoint used by the Slack web client's Cmd+K
// Quick Switcher and Channel Browser.
//
// This endpoint is not part of Slack's public API and is not exposed by
// slack-go. It requires browser credentials (xoxc token + xoxd cookie), which
// our Client already carries.
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

// ChannelResult is one result from a search.modules.channels response.
type ChannelResult struct {
	ID          string
	Name        string
	Topic       string
	Purpose     string
	MemberCount int
	IsArchived  bool
}

// channelsSearchURL is the endpoint for channel search.
// The workspace hostname is interpolated at call time.
const channelsSearchURL = "https://%s/api/search.modules.channels"

// channelsSearchResponse is the JSON envelope returned by the endpoint.
type channelsSearchResponse struct {
	OK    bool                  `json:"ok"`
	Error string                `json:"error,omitempty"`
	Items []channelsSearchItem  `json:"items"`
}

type channelsSearchItem struct {
	ID         string             `json:"id"`
	Name       string             `json:"name"`
	Topic      channelsSearchText `json:"topic"`
	Purpose    channelsSearchText `json:"purpose"`
	NumMembers int                `json:"num_members"`
	IsPrivate  bool               `json:"is_private"`
	IsArchived bool               `json:"is_archived"`
}

type channelsSearchText struct {
	Value string `json:"value"`
}

// SearchChannels queries the undocumented search.modules.channels endpoint and
// returns channels whose names match query. workspace is the full Slack
// hostname (e.g. "myorg.slack.com"). count is the maximum number of results to
// return (0 → default of 20).
func (c *Client) SearchChannels(ctx context.Context, workspace, query string, count int) ([]ChannelResult, error) {
	if count <= 0 {
		count = 20
	}

	form := url.Values{}
	form.Set("token", c.token)
	form.Set("module", "channels")
	form.Set("query", query)
	form.Set("count", strconv.Itoa(count))
	form.Set("sort", "name")
	form.Set("sort_dir", "asc")
	form.Set("browse", "standard")
	form.Set("search_context", "desktop_channel_browser")
	form.Set("no_user_profile", "1")
	form.Set("highlight", "0")
	form.Set("extracts", "0")

	endpoint := fmt.Sprintf(channelsSearchURL, workspace)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("search.modules.channels: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	// Use the same http.Client that carries the xoxd cookie and Chrome TLS
	// fingerprint — already wired up in NewClient.
	httpClient := c.httpClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("search.modules.channels: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("search.modules.channels: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("search.modules.channels: HTTP %d: %s", resp.StatusCode, body)
	}

	var result channelsSearchResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("search.modules.channels: parse response: %w", err)
	}
	if !result.OK {
		return nil, fmt.Errorf("search.modules.channels: %s", result.Error)
	}

	channels := make([]ChannelResult, 0, len(result.Items))
	for _, item := range result.Items {
		channels = append(channels, ChannelResult{
			ID:          item.ID,
			Name:        item.Name,
			Topic:       item.Topic.Value,
			Purpose:     item.Purpose.Value,
			MemberCount: item.NumMembers,
			IsArchived:  item.IsArchived,
		})
	}
	return channels, nil
}
