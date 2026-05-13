// Package cmd — search_channels.go implements the --channels mode of the
// search command. It queries the undocumented search.modules.channels endpoint
// and formats results as plain text or NDJSON.
package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"


	"github.com/cgrossde/slackcli/internal/slack"
)

// searchChannels is the Layer 1 implementation for `search --channels`.
// query is the channel name fragment to search for.
func searchChannels(query string, flags SearchFlags) (string, error) {
	if strings.TrimSpace(query) == "" {
		return "", fmt.Errorf("search --channels: query must not be empty")
	}

	workspace, err := resolveWorkspace(flags.Workspace)
	if err != nil {
		return "", err
	}

	workspace, entry, err := loadCredentials(workspace)
	if err != nil {
		return "", err
	}

	count := flags.Count
	if count < 1 {
		count = 1
	} else if count > 100 {
		count = 100
	}

	client := slack.NewClient(entry.Token, entry.Cookie)
	results, err := client.SearchChannels(context.Background(), workspace, query, count)
	if err != nil {
		return "", fmt.Errorf("searching channels: %w", err)
	}

	if flags.JSON {
		return formatChannelResultsJSON(results), nil
	}
	return formatChannelResults(query, results), nil
}

// resolveChannelName resolves a channel name (with or without leading #) to a
// Slack channel ID using SearchChannels. Returns an error with suggestions if
// no exact match is found.
func resolveChannelName(client *slack.Client, workspace, name string) (string, error) {
	name = strings.TrimPrefix(name, "#")
	results, err := client.SearchChannels(context.Background(), workspace, name, 5)
	if err != nil {
		return "", fmt.Errorf("resolving channel name %q: %w", name, err)
	}
	for _, r := range results {
		if strings.EqualFold(r.Name, name) {
			return r.ID, nil
		}
	}
	// No exact match — build a helpful error.
	if len(results) == 0 {
		return "", fmt.Errorf("channel %q not found", name)
	}
	suggestions := make([]string, 0, len(results))
	for _, r := range results {
		suggestions = append(suggestions, "#"+r.Name+" ("+r.ID+")")
	}
	return "", fmt.Errorf("channel %q not found; similar: %s", name, strings.Join(suggestions, ", "))
}

// formatChannelResults renders []ChannelResult as plain-text, LLM-friendly
// output consistent with the search command style.
func formatChannelResults(query string, results []slack.ChannelResult) string {
	if len(results) == 0 {
		return fmt.Sprintf("no channels matching %q\n", query)
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "%d channel(s) matching %q:\n\n", len(results), query)
	for i, r := range results {
		archived := ""
		if r.IsArchived {
			archived = " [archived]"
		}
		fmt.Fprintf(&sb, "[%d] #%s%s — %d members — ID: %s\n",
			i+1, r.Name, archived, r.MemberCount, r.ID)
		if r.Topic != "" {
			fmt.Fprintf(&sb, "    Topic: %s\n", r.Topic)
		}
		if r.Purpose != "" {
			fmt.Fprintf(&sb, "    Purpose: %s\n", r.Purpose)
		}
	}
	return sb.String()
}

// channelResultJSON is the JSON representation of a single channel result.
type channelResultJSON struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Topic       string `json:"topic,omitempty"`
	Purpose     string `json:"purpose,omitempty"`
	MemberCount int    `json:"member_count"`
	IsArchived  bool   `json:"is_archived"`
}

// formatChannelResultsJSON emits one JSON object per channel result.
func formatChannelResultsJSON(results []slack.ChannelResult) string {
	var sb strings.Builder
	for _, r := range results {
		rec := channelResultJSON{
			ID:          r.ID,
			Name:        r.Name,
			Topic:       r.Topic,
			Purpose:     r.Purpose,
			MemberCount: r.MemberCount,
			IsArchived:  r.IsArchived,
		}
		line, _ := json.Marshal(rec)
		sb.Write(line)
		sb.WriteByte('\n')
	}
	return sb.String()
}
