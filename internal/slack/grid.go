// Package slack — grid.go implements Enterprise Grid workspace enumeration via
// client.userBoot.
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

// GridWorkspace is one member workspace visible via an Enterprise Grid token.
type GridWorkspace struct {
	ID     string
	Domain string // bare domain without ".slack.com", e.g. "myorg"
}

// GridWorkspaces calls client.userBoot and returns all workspaces accessible
// with the receiver's credentials.  workspace is the anchor domain used to
// build the API URL (e.g. "myorg.slack.com").
//
// On Enterprise Grid a single xoxc/xoxd credential grants access to every
// member workspace.  The returned slice includes the anchor workspace itself.
func (c *Client) GridWorkspaces(ctx context.Context, workspace string) ([]GridWorkspace, error) {
	apiURL := fmt.Sprintf("https://%s/api/client.userBoot", workspace)

	form := strings.NewReader("token=" + url.QueryEscape(c.token))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, apiURL, form)
	if err != nil {
		return nil, fmt.Errorf("client.userBoot: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("client.userBoot: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("client.userBoot: read body: %w", err)
	}

	var boot userBootResponse
	if err := json.Unmarshal(body, &boot); err != nil {
		return nil, fmt.Errorf("client.userBoot: parse response: %w", err)
	}
	if !boot.OK {
		return nil, fmt.Errorf("client.userBoot: %s", boot.Error)
	}

	out := make([]GridWorkspace, len(boot.Workspaces))
	for i, ws := range boot.Workspaces {
		out[i] = GridWorkspace{ID: ws.ID, Domain: ws.Domain}
	}
	return out, nil
}
