// Package cmd — search_users.go provides the Layer 1 implementation for
// `search --users`. It searches the local user cache first, then the Flannel
// edge API.
//
// The Cobra command wrapper that previously lived in users.go has been removed;
// this functionality is now accessed via `search --users <query>`.
// Layer 2 wiring (presenter) is applied in main.go.
package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"


	"github.com/cgrossde/slackcli/internal/slack"
)

// SearchUsers is the Layer 1 implementation. It searches the local user cache
// first, then the Flannel edge API. query is matched case-insensitively
// against display name, employee ID (Name field), and email.
func SearchUsers(query, workspaceFlag string, jsonMode bool) (string, error) {
	if strings.TrimSpace(query) == "" {
		return "", fmt.Errorf("query must not be empty")
	}

	workspace, err := resolveWorkspace(workspaceFlag)
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

	// --- Step 1: cache search (instant) ---
	cached := cache.Search(query)

	// --- Step 2: edge API search ---
	// Resolve the enterprise ID required by the edge API endpoint.
	authResult, err := client.AuthTest()
	if err != nil {
		return "", fmt.Errorf("auth.test: %w", err)
	}
	if !authResult.OK {
		return "", fmt.Errorf("auth.test failed: %s", authResult.Error)
	}

	enterpriseID := authResult.EnterpriseID
	if enterpriseID == "" {
		// Non-enterprise workspace: fall back to team ID.
		enterpriseID = authResult.TeamID
	}

	edgeResults, edgeErr := client.SearchUsers(context.Background(), query, enterpriseID)
	// edgeErr is non-fatal — we still have the cache results.

	// Deduplicate: remove edge results already present in the cache.
	cachedIDs := make(map[string]bool, len(cached))
	for _, u := range cached {
		cachedIDs[u.ID] = true
	}
	var extra []slack.CachedUser
	for _, u := range edgeResults {
		if !cachedIDs[u.ID] {
			extra = append(extra, u)
		}
	}

	if jsonMode {
		return formatUserResultsJSON(cached, extra), nil
	}
	return formatUserResults(query, cached, extra, edgeErr), nil
}

// formatUserResults renders the combined cache + edge API results as
// plain-text, LLM-friendly output consistent with the search command style.
func formatUserResults(query string, cached, extra []slack.CachedUser, edgeErr error) string {
	var sb strings.Builder

	total := len(cached) + len(extra)
	if total == 0 {
		if edgeErr != nil {
			fmt.Fprintf(&sb, "no users matching %q in cache (edge API error: %v)\n", query, edgeErr)
		} else {
			fmt.Fprintf(&sb, "no users matching %q\n", query)
		}
		return sb.String()
	}

	idx := 1

	if len(cached) > 0 {
		for _, u := range cached {
			fmt.Fprintf(&sb, "[%d] %s\n", idx, formatUser(u))
			idx++
		}
	}

	if len(extra) > 0 {
		if len(cached) > 0 {
			sb.WriteString("\n--- also found via Slack ---\n\n")
		}
		for _, u := range extra {
			fmt.Fprintf(&sb, "[%d] %s\n", idx, formatUser(u))
			idx++
		}
	}

	if edgeErr != nil {
		fmt.Fprintf(&sb, "\n(edge API error: %v)\n", edgeErr)
	}

	return sb.String()
}

// formatUser renders a single CachedUser as a one-line string.
//
//	Alice Johnson (u123456) · alice.johnson@example.com · WH1K7QTFU
func formatUser(u slack.CachedUser) string {
	parts := []string{u.Label()}
	if u.Email != "" {
		parts = append(parts, u.Email)
	}
	parts = append(parts, u.ID)
	return strings.Join(parts, " · ")
}

// userJSON is the JSON representation of a single user result.
type userJSON struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
	Email       string `json:"email"`
	Source      string `json:"source"`
}

// formatUserResultsJSON emits one JSON object per user (cache results first,
// then edge API results). No pagination trailer — result set is bounded.
func formatUserResultsJSON(cached, extra []slack.CachedUser) string {
	var sb strings.Builder
	for _, u := range cached {
		rec := userJSON{
			ID:          u.ID,
			Name:        u.Name,
			DisplayName: u.DisplayName,
			Email:       u.Email,
			Source:      "cache",
		}
		line, _ := json.Marshal(rec)
		sb.Write(line)
		sb.WriteByte('\n')
	}
	for _, u := range extra {
		rec := userJSON{
			ID:          u.ID,
			Name:        u.Name,
			DisplayName: u.DisplayName,
			Email:       u.Email,
			Source:      "edge",
		}
		line, _ := json.Marshal(rec)
		sb.Write(line)
		sb.WriteByte('\n')
	}
	return sb.String()
}
