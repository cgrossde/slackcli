// Package slack — users_search.go calls the Flannel edge API
// (edgeapi.slack.com/cache/<enterpriseID>/users/search) to search for users
// by display name, employee ID, or email fragment.
//
// This endpoint is not part of Slack's public API and is not exposed by
// slack-go. It requires browser credentials (xoxc token + xoxd cookie), which
// our Client already carries. Results are returned directly to the caller and
// are NOT written to the UserCache — the cache intentionally contains only
// people encountered through actual message activity.
package slack

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
)

// edgeSearchURL is the Flannel edge API endpoint for user search.
// The enterprise ID is interpolated at call time.
const edgeSearchURL = "https://edgeapi.slack.com/cache/%s/users/search"

// edgeSearchRequest is the JSON body sent to the edge search endpoint.
type edgeSearchRequest struct {
	Token        string `json:"token"`
	Query        string `json:"query"`
	Count        int    `json:"count"`
	Fuzz         int    `json:"fuzz"`
	EnterpriseID string `json:"enterprise_id"`
}

// edgeSearchResponse is the JSON envelope returned by the edge search endpoint.
type edgeSearchResponse struct {
	OK      bool              `json:"ok"`
	Error   string            `json:"error,omitempty"`
	Results []edgeSearchUser  `json:"results"`
}

// edgeSearchUser is one result from the edge search endpoint.
type edgeSearchUser struct {
	ID      string           `json:"id"`
	Name    string           `json:"name"`
	Profile edgeSearchProfile `json:"profile"`
}

type edgeSearchProfile struct {
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
}

// employeeIDRe matches employee ID patterns (letter + digits).
// d<digits>, c<digits>. Used to select exact (fuzz=0) vs fuzzy (fuzz=1) mode.
var employeeIDRe = regexp.MustCompile(`^[a-zA-Z]\d{4,}$`)

// SearchUsers calls the Flannel edge API and returns users matching query.
// enterpriseID is the Slack enterprise ID (e.g. "E7RBBBXHB"), obtained from
// AuthTest. Results are ephemeral — they are NOT written to the UserCache.
//
// When query looks like an employee ID (letter + digits), fuzz=0 is used for
// an exact handle match. Otherwise fuzz=1 enables prefix/fuzzy matching.
func (c *Client) SearchUsers(ctx context.Context, query, enterpriseID string) ([]CachedUser, error) {
	fuzz := 1
	if employeeIDRe.MatchString(query) {
		fuzz = 0
	}

	reqBody := edgeSearchRequest{
		Token:        c.token,
		Query:        query,
		Count:        25,
		Fuzz:         fuzz,
		EnterpriseID: enterpriseID,
	}

	data, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("users/search: marshal request: %w", err)
	}

	url := fmt.Sprintf(edgeSearchURL, enterpriseID)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("users/search: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	// Use the same http.Client that carries the xoxd cookie and Chrome TLS
	// fingerprint — already wired up in NewClient.
	httpClient := c.httpClient
	if httpClient == nil {
		httpClient = http.DefaultClient
	}

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("users/search: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("users/search: read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("users/search: HTTP %d: %s", resp.StatusCode, body)
	}

	var result edgeSearchResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("users/search: parse response: %w", err)
	}
	if !result.OK {
		return nil, fmt.Errorf("users/search: %s", result.Error)
	}

	users := make([]CachedUser, 0, len(result.Results))
	for _, u := range result.Results {
		users = append(users, CachedUser{
			ID:          u.ID,
			Name:        u.Name,
			DisplayName: u.Profile.DisplayName,
			Email:       u.Profile.Email,
		})
	}
	return users, nil
}
