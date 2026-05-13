package cmd

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/cgrossde/slackcli/internal/slack"
)

// ---------------------------------------------------------------------------
// formatUser
// ---------------------------------------------------------------------------

func TestFormatUser_fullFields(t *testing.T) {
	u := slack.CachedUser{
		ID:          "WH1K7QTFU",
		Name:        "u123456",
		DisplayName: "Alice Johnson",
		Email:       "alice.johnson@example.com",
	}
	got := formatUser(u)
	want := "Alice Johnson (u123456) · alice.johnson@example.com · WH1K7QTFU"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatUser_noEmail(t *testing.T) {
	u := slack.CachedUser{
		ID:          "WH1K7QTFU",
		Name:        "u123456",
		DisplayName: "Alice Johnson",
	}
	got := formatUser(u)
	want := "Alice Johnson (u123456) · WH1K7QTFU"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestFormatUser_noDisplayName(t *testing.T) {
	u := slack.CachedUser{
		ID:   "WH1K7QTFU",
		Name: "u123456",
	}
	got := formatUser(u)
	// Label() falls back to Name when DisplayName is empty.
	want := "u123456 · WH1K7QTFU"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// ---------------------------------------------------------------------------
// formatUserResults
// ---------------------------------------------------------------------------

func makeUser(id, name, display, email string) slack.CachedUser {
	return slack.CachedUser{ID: id, Name: name, DisplayName: display, Email: email}
}

func TestFormatUserResults_noResults(t *testing.T) {
	out := formatUserResults("alice", nil, nil, nil)
	if !strings.Contains(out, `no users matching "alice"`) {
		t.Errorf("expected no-results message, got: %q", out)
	}
}

func TestFormatUserResults_cacheOnly(t *testing.T) {
	cached := []slack.CachedUser{
		makeUser("W1", "u123456", "Alice Johnson", "alice.johnson@example.com"),
	}
	out := formatUserResults("alice", cached, nil, nil)

	if !strings.Contains(out, "[1]") {
		t.Errorf("expected [1] index, got: %q", out)
	}
	if !strings.Contains(out, "Alice Johnson") {
		t.Errorf("expected display name, got: %q", out)
	}
	// No "also found" section when there are no edge results.
	if strings.Contains(out, "also found") {
		t.Errorf("unexpected edge section, got: %q", out)
	}
}

func TestFormatUserResults_cacheAndEdge(t *testing.T) {
	cached := []slack.CachedUser{
		makeUser("W1", "u123456", "Alice Johnson", "alice.johnson@example.com"),
	}
	extra := []slack.CachedUser{
		makeUser("W2", "u234567", "Carol White", "carol.white@example.com"),
	}
	out := formatUserResults("alice", cached, extra, nil)

	if !strings.Contains(out, "[1]") {
		t.Errorf("expected [1], got: %q", out)
	}
	if !strings.Contains(out, "[2]") {
		t.Errorf("expected [2], got: %q", out)
	}
	if !strings.Contains(out, "also found via Slack") {
		t.Errorf("expected separator, got: %q", out)
	}
	if !strings.Contains(out, "Carol White") {
		t.Errorf("expected edge result name, got: %q", out)
	}
}

func TestFormatUserResults_edgeOnly(t *testing.T) {
	extra := []slack.CachedUser{
		makeUser("W2", "u234567", "Carol White", "carol.white@example.com"),
	}
	out := formatUserResults("alice", nil, extra, nil)

	// No separator when there are no cache results.
	if strings.Contains(out, "also found") {
		t.Errorf("unexpected separator when no cache results, got: %q", out)
	}
	if !strings.Contains(out, "[1]") {
		t.Errorf("expected [1], got: %q", out)
	}
}

func TestFormatUserResults_edgeError(t *testing.T) {
	cached := []slack.CachedUser{
		makeUser("W1", "u123456", "Alice Johnson", ""),
	}
	out := formatUserResults("alice", cached, nil, fmt.Errorf("HTTP 429"))
	if !strings.Contains(out, "edge API error") {
		t.Errorf("expected edge error note, got: %q", out)
	}
}

func TestFormatUserResults_edgeErrorNoResults(t *testing.T) {
	out := formatUserResults("alice", nil, nil, fmt.Errorf("HTTP 429"))
	if !strings.Contains(out, "edge API error") {
		t.Errorf("expected edge error in no-results message, got: %q", out)
	}
}

// ---------------------------------------------------------------------------
// UserCache.Search (white-box via NewUserCacheFromMap)
// ---------------------------------------------------------------------------

func TestUserCacheSearch_byDisplayName(t *testing.T) {
	uc := slack.NewUserCacheFromMap("ws", map[string]slack.CachedUser{
		"W1": makeUser("W1", "u123456", "Alice Johnson", "alice.johnson@example.com"),
		"W2": makeUser("W2", "u789012", "Bob Smith", "bob.smith@example.com"),
	})
	results := uc.Search("alice")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].ID != "W1" {
		t.Errorf("expected W1, got %s", results[0].ID)
	}
}

func TestUserCacheSearch_byHandle(t *testing.T) {
	uc := slack.NewUserCacheFromMap("ws", map[string]slack.CachedUser{
		"W1": makeUser("W1", "u123456", "Alice Johnson", "alice.johnson@example.com"),
		"W2": makeUser("W2", "u789012", "Bob Smith", "bob.smith@example.com"),
	})
	results := uc.Search("u123456")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Name != "u123456" {
		t.Errorf("expected u123456, got %s", results[0].Name)
	}
}

func TestUserCacheSearch_byEmail(t *testing.T) {
	uc := slack.NewUserCacheFromMap("ws", map[string]slack.CachedUser{
		"W1": makeUser("W1", "u123456", "Alice Johnson", "alice.johnson@example.com"),
		"W2": makeUser("W2", "u789012", "Bob Smith", "bob.smith@example.com"),
	})
	results := uc.Search("bob")
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].ID != "W2" {
		t.Errorf("expected W2, got %s", results[0].ID)
	}
}

func TestUserCacheSearch_caseInsensitive(t *testing.T) {
	uc := slack.NewUserCacheFromMap("ws", map[string]slack.CachedUser{
		"W1": makeUser("W1", "u123456", "Alice Johnson", "alice.johnson@example.com"),
	})
	for _, q := range []string{"ALICE", "Alice", "alice", "JOHNSON"} {
		results := uc.Search(q)
		if len(results) != 1 {
			t.Errorf("query %q: expected 1, got %d", q, len(results))
		}
	}
}

func TestUserCacheSearch_noMatch(t *testing.T) {
	uc := slack.NewUserCacheFromMap("ws", map[string]slack.CachedUser{
		"W1": makeUser("W1", "u123456", "Alice Johnson", "alice.johnson@example.com"),
	})
	results := uc.Search("zzznomatch")
	if len(results) != 0 {
		t.Errorf("expected 0, got %d", len(results))
	}
}

func TestUserCacheSearch_sortedByDisplayName(t *testing.T) {
	uc := slack.NewUserCacheFromMap("ws", map[string]slack.CachedUser{
		"W1": makeUser("W1", "u123456", "Zara Smith", "zara@example.com"),
		"W2": makeUser("W2", "u789012", "Alice Jones", "alice@example.com"),
		"W3": makeUser("W3", "u999999", "Bob Allen", "bob@example.com"),
	})
	results := uc.Search("example.com")
	if len(results) != 3 {
		t.Fatalf("expected 3, got %d", len(results))
	}
	if results[0].DisplayName != "Alice Jones" || results[1].DisplayName != "Bob Allen" || results[2].DisplayName != "Zara Smith" {
		t.Errorf("wrong order: %v", results)
	}
}

// ---------------------------------------------------------------------------
// formatUserResultsJSON
// ---------------------------------------------------------------------------

func TestFormatUserResultsJSON_cacheSource(t *testing.T) {
	cached := []slack.CachedUser{
		makeUser("W1", "u123456", "Alice Johnson", "alice.johnson@example.com"),
	}
	out := formatUserResultsJSON(cached, nil)
	lines := splitNonEmptyUsers(out)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d: %q", len(lines), out)
	}
	assertContainsUsers(t, lines[0], `"id":"W1"`)
	assertContainsUsers(t, lines[0], `"name":"u123456"`)
	assertContainsUsers(t, lines[0], `"display_name":"Alice Johnson"`)
	assertContainsUsers(t, lines[0], `"email":"alice.johnson@example.com"`)
	assertContainsUsers(t, lines[0], `"source":"cache"`)
}

func TestFormatUserResultsJSON_edgeSource(t *testing.T) {
	extra := []slack.CachedUser{
		makeUser("W2", "u345678", "Bob Edge", "bob.edge@example.com"),
	}
	out := formatUserResultsJSON(nil, extra)
	lines := splitNonEmptyUsers(out)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d: %q", len(lines), out)
	}
	assertContainsUsers(t, lines[0], `"source":"edge"`)
	assertContainsUsers(t, lines[0], `"id":"W2"`)
}

func TestFormatUserResultsJSON_cacheBeforeEdge(t *testing.T) {
	cached := []slack.CachedUser{makeUser("W1", "c1", "Cache User", "")}
	extra := []slack.CachedUser{makeUser("W2", "e1", "Edge User", "")}
	out := formatUserResultsJSON(cached, extra)
	lines := splitNonEmptyUsers(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines, got %d: %q", len(lines), out)
	}
	assertContainsUsers(t, lines[0], `"source":"cache"`)
	assertContainsUsers(t, lines[1], `"source":"edge"`)
}

func TestFormatUserResultsJSON_emptyLists(t *testing.T) {
	out := formatUserResultsJSON(nil, nil)
	if out != "" {
		t.Errorf("expected empty output for nil inputs, got: %q", out)
	}
}

func TestFormatUserResultsJSON_validJSON(t *testing.T) {
	cached := []slack.CachedUser{
		makeUser("W1", "u123456", "Alice Johnson", "alice.johnson@example.com"),
	}
	out := formatUserResultsJSON(cached, nil)
	for _, line := range splitNonEmptyUsers(out) {
		var m map[string]interface{}
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Errorf("invalid JSON line %q: %v", line, err)
		}
	}
}

// splitNonEmptyUsers splits s by newlines and returns non-empty lines.
func splitNonEmptyUsers(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}

// assertContainsUsers is a helper for users_test subtests.
func assertContainsUsers(t *testing.T, s, substr string) {
	t.Helper()
	if !strings.Contains(s, substr) {
		t.Errorf("expected %q in output\ngot: %s", substr, s)
	}
}
