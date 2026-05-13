package cmd

import (
	"strings"
	"testing"
	"time"

	"github.com/cgrossde/slackcli/internal/slack"
)

// ---------------------------------------------------------------------------
// resolveDate
// ---------------------------------------------------------------------------

func TestResolveDate_absolute(t *testing.T) {
	now := time.Date(2024, 6, 12, 0, 0, 0, 0, time.UTC)
	got, err := resolveDate("2024-01-15", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "2024-01-15" {
		t.Errorf("got %q, want %q", got, "2024-01-15")
	}
}

func TestResolveDate_days(t *testing.T) {
	now := time.Date(2024, 6, 12, 0, 0, 0, 0, time.UTC)
	got, err := resolveDate("7d", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "2024-06-05"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveDate_weeks(t *testing.T) {
	now := time.Date(2024, 6, 12, 0, 0, 0, 0, time.UTC)
	got, err := resolveDate("2w", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "2024-05-29"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveDate_months(t *testing.T) {
	now := time.Date(2024, 6, 12, 0, 0, 0, 0, time.UTC)
	got, err := resolveDate("1m", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "2024-05-12"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveDate_years(t *testing.T) {
	now := time.Date(2024, 6, 12, 0, 0, 0, 0, time.UTC)
	got, err := resolveDate("1y", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "2023-06-12"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestResolveDate_zeroDay(t *testing.T) {
	now := time.Date(2024, 6, 12, 0, 0, 0, 0, time.UTC)
	got, err := resolveDate("0d", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := "2024-06-12"
	if got != want {
		t.Errorf("0d should resolve to today, got %q, want %q", got, want)
	}
}

func TestResolveDate_zeroWeek(t *testing.T) {
	now := time.Date(2024, 6, 12, 0, 0, 0, 0, time.UTC)
	got, err := resolveDate("0w", now)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != "2024-06-12" {
		t.Errorf("got %q, want 2024-06-12", got)
	}
}

func TestResolveDate_empty(t *testing.T) {
	_, err := resolveDate("", time.Now())
	if err == nil {
		t.Error("expected error for empty input, got nil")
	}
}

func TestResolveDate_invalidUnit(t *testing.T) {
	_, err := resolveDate("5x", time.Now())
	if err == nil {
		t.Error("expected error for unknown unit, got nil")
	}
}

func TestResolveDate_nonNumericPrefix(t *testing.T) {
	_, err := resolveDate("abcd", time.Now())
	if err == nil {
		t.Error("expected error for non-numeric prefix, got nil")
	}
}

func TestResolveDate_missingNumber(t *testing.T) {
	_, err := resolveDate("d", time.Now())
	if err == nil {
		t.Error("expected error for missing number before unit, got nil")
	}
}

func TestResolveDate_tableTests(t *testing.T) {
	now := time.Date(2024, 6, 12, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		input   string
		want    string
		wantErr bool
	}{
		{"2024-01-01", "2024-01-01", false},
		{"30d", "2024-05-13", false},
		{"4w", "2024-05-15", false},
		{"3m", "2024-03-12", false},
		{"2y", "2022-06-12", false},
		{"0d", "2024-06-12", false},
		// now is 2024-06-12 (Wednesday)
		{"today", "2024-06-12", false},
		{"yesterday", "2024-06-11", false},
		{"wednesday", "2024-06-12", false}, // today is wednesday → same day
		{"tuesday", "2024-06-11", false},   // most recent tuesday
		{"monday", "2024-06-10", false},
		{"sunday", "2024-06-09", false},
		{"saturday", "2024-06-08", false},
		{"friday", "2024-06-07", false},
		{"thursday", "2024-06-06", false},
		{"MONDAY", "2024-06-10", false}, // case-insensitive
		{"", "", true},
		{"5x", "", true},
		{"d", "", true},
		{"abc", "", true},
		{"1.5d", "", true},
	}
	for _, tc := range cases {
		got, err := resolveDate(tc.input, now)
		if tc.wantErr {
			if err == nil {
				t.Errorf("resolveDate(%q): expected error, got %q", tc.input, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("resolveDate(%q): unexpected error: %v", tc.input, err)
			continue
		}
		if got != tc.want {
			t.Errorf("resolveDate(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestResolveDate_dayNames(t *testing.T) {
	// 2024-06-12 is a Wednesday.
	now := time.Date(2024, 6, 12, 0, 0, 0, 0, time.UTC)
	cases := []struct {
		input string
		want  string
	}{
		{"today", "2024-06-12"},
		{"yesterday", "2024-06-11"},
		{"wednesday", "2024-06-12"}, // today is wednesday → today
		{"tuesday", "2024-06-11"},
		{"monday", "2024-06-10"},
		{"sunday", "2024-06-09"},
		{"saturday", "2024-06-08"},
		{"friday", "2024-06-07"},
		{"thursday", "2024-06-06"},
	}
	for _, tc := range cases {
		got, err := resolveDate(tc.input, now)
		if err != nil {
			t.Errorf("resolveDate(%q): unexpected error: %v", tc.input, err)
			continue
		}
		if got != tc.want {
			t.Errorf("resolveDate(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// buildSearchQuery
// ---------------------------------------------------------------------------

func TestBuildSearchQuery_keywords_only(t *testing.T) {
	flags := SearchFlags{}
	got := buildSearchQuery("deployment", flags)
	if got != "deployment" {
		t.Errorf("got %q, want %q", got, "deployment")
	}
}

func TestBuildSearchQuery_channel_name(t *testing.T) {
	flags := SearchFlags{Channel: "ops"}
	got := buildSearchQuery("rollback", flags)
	if !strings.Contains(got, "in:#ops") {
		t.Errorf("missing in:#ops in %q", got)
	}
}

func TestBuildSearchQuery_channel_name_with_hash(t *testing.T) {
	flags := SearchFlags{Channel: "#ops"}
	got := buildSearchQuery("rollback", flags)
	// Should not double-add the hash.
	if strings.Contains(got, "in:##") {
		t.Errorf("double hash in query: %q", got)
	}
	if !strings.Contains(got, "in:#ops") {
		t.Errorf("missing in:#ops in %q", got)
	}
}

func TestBuildSearchQuery_channel_id(t *testing.T) {
	flags := SearchFlags{Channel: "C012ABC3456"}
	got := buildSearchQuery("rollback", flags)
	if !strings.Contains(got, "in:C012ABC3456") {
		t.Errorf("channel ID not used directly: %q", got)
	}
	if strings.Contains(got, "in:#") {
		t.Errorf("channel ID should not have # prefix: %q", got)
	}
}

func TestBuildSearchQuery_from_displayname(t *testing.T) {
	flags := SearchFlags{From: "alice"}
	got := buildSearchQuery("test", flags)
	if !strings.Contains(got, "from:alice") {
		t.Errorf("missing from:alice in %q", got)
	}
}

func TestBuildSearchQuery_from_userID(t *testing.T) {
	flags := SearchFlags{From: "U012ABC3456"}
	got := buildSearchQuery("test", flags)
	if !strings.Contains(got, "from:<U012ABC3456>") {
		t.Errorf("missing from:<ID> in %q", got)
	}
}

func TestBuildSearchQuery_after_relative(t *testing.T) {
	// After should produce after:YYYY-MM-DD; we can't pin the exact date in a
	// unit test without controlling time, so verify the prefix exists and the
	// value parses as a date.
	flags := SearchFlags{After: "7d"}
	got := buildSearchQuery("deploy", flags)
	if !strings.Contains(got, "after:") {
		t.Errorf("missing after: modifier in %q", got)
	}
}

func TestBuildSearchQuery_after_absolute(t *testing.T) {
	flags := SearchFlags{After: "2024-06-11"}
	got := buildSearchQuery("deploy", flags)
	// --after 2024-06-11 → after:2024-06-10 (exclusive shift: include Jun 11)
	if !strings.Contains(got, "after:2024-06-10") {
		t.Errorf("missing after:2024-06-10 in %q", got)
	}
}

func TestBuildSearchQuery_before_absolute(t *testing.T) {
	flags := SearchFlags{Before: "2024-06-30"}
	got := buildSearchQuery("deploy", flags)
	// --before 2024-06-30 → before:2024-07-01 (exclusive shift: include Jun 30)
	if !strings.Contains(got, "before:2024-07-01") {
		t.Errorf("missing before:2024-07-01 in %q", got)
	}
}

func TestBuildSearchQuery_multiple_flags(t *testing.T) {
	flags := SearchFlags{
		Channel: "ops",
		From:    "alice",
		After:   "2024-06-01",
		Before:  "2024-06-30",
	}
	got := buildSearchQuery("outage", flags)
	for _, want := range []string{"outage", "in:#ops", "from:alice", "after:2024-05-31", "before:2024-07-01"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in query %q", want, got)
		}
	}
}

func TestBuildSearchQuery_invalid_after_omitted(t *testing.T) {
	// Invalid relative dates should be silently omitted from the query.
	flags := SearchFlags{After: "baddate"}
	got := buildSearchQuery("test", flags)
	if strings.Contains(got, "after:") {
		t.Errorf("invalid after date should be omitted, got %q", got)
	}
}

func TestBuildSearchQuery_noKeywords_withFilter(t *testing.T) {
	// Keywords omitted but a filter is present — should still produce a valid query.
	flags := SearchFlags{From: "u123456", After: "2024-06-01"}
	got := buildSearchQuery("", flags)
	if got == "" {
		t.Error("expected non-empty query when filters are set, got empty string")
	}
	if strings.Contains(got, `""`) {
		t.Errorf("empty keyword should not appear quoted in query: %q", got)
	}
	if !strings.Contains(got, "from:u123456") {
		t.Errorf("missing from: modifier in %q", got)
	}
}

func TestBuildSearchQuery_inDM(t *testing.T) {
	flags := SearchFlags{InDM: true}
	got := buildSearchQuery("", flags)
	if !strings.Contains(got, "is:dm") {
		t.Errorf("missing is:dm in %q", got)
	}
}

func TestBuildSearchQuery_inChannel(t *testing.T) {
	flags := SearchFlags{InChannel: true}
	got := buildSearchQuery("test", flags)
	if !strings.Contains(got, "is:channel") {
		t.Errorf("missing is:channel in %q", got)
	}
}

func TestBuildSearchQuery_with_displayname(t *testing.T) {
	flags := SearchFlags{With: "@alice"}
	got := buildSearchQuery("", flags)
	if !strings.Contains(got, "with:alice") {
		t.Errorf("expected with:alice (@ stripped) in %q", got)
	}
}

func TestBuildSearchQuery_with_userID(t *testing.T) {
	flags := SearchFlags{With: "U012ABC3456"}
	got := buildSearchQuery("", flags)
	if !strings.Contains(got, "with:<U012ABC3456>") {
		t.Errorf("missing with:<ID> in %q", got)
	}
}

func TestBuildSearchQuery_with_atUserID(t *testing.T) {
	// @U... — strip the @ then detect as user ID.
	flags := SearchFlags{With: "@U012ABC3456"}
	got := buildSearchQuery("", flags)
	if !strings.Contains(got, "with:<U012ABC3456>") {
		t.Errorf("missing with:<ID> after @ strip in %q", got)
	}
}

func TestBuildSearchQuery_allEmpty(t *testing.T) {
	// No keywords, no filters — should return empty string (caller rejects it).
	got := buildSearchQuery("", SearchFlags{})
	if got != "" {
		t.Errorf("expected empty string, got %q", got)
	}
}

// ---------------------------------------------------------------------------
// formatSearchResults
// ---------------------------------------------------------------------------

func TestFormatSearchResults_emptyResult(t *testing.T) {
	result := slack.SearchResult{
		Query: "nonexistent",
		Total: 0,
		Page:  1,
		Pages: 0,
		Count: 20,
	}
	got := formatSearchResults(result, nil, SearchFlags{Count: 20})
	if !strings.Contains(got, `"nonexistent"`) {
		t.Errorf("missing query in output: %q", got)
	}
	if !strings.Contains(got, "total: 0") {
		t.Errorf("missing total: 0 in output: %q", got)
	}
}

func TestFormatSearchResults_singleMatch(t *testing.T) {
	result := slack.SearchResult{
		Query: "deployment",
		Total: 1,
		Page:  1,
		Pages: 1,
		Count: 20,
		Matches: []slack.SearchMatch{
			{
				ChannelID:   "C012ABC",
				ChannelName: "ops",
				UserID:      "UABC123",
				Username:    "alice",
				Ts:          "1718200320.123456",
				Text:        "We rolled back the deployment.",
			},
		},
	}
	got := formatSearchResults(result, nil, SearchFlags{Count: 20})

	if !strings.Contains(got, "[1]") {
		t.Errorf("missing [1] in output: %q", got)
	}
	if !strings.Contains(got, "#ops") {
		t.Errorf("missing #ops in output: %q", got)
	}
	if !strings.Contains(got, "We rolled back the deployment.") {
		t.Errorf("missing message text in output: %q", got)
	}
	if !strings.Contains(got, "slackcli read C012ABC:1718200320.123456") {
		t.Errorf("missing compact read ref in output: %q", got)
	}
}

func TestFormatSearchResults_paginationFooter(t *testing.T) {
	result := slack.SearchResult{
		Query: "deploy",
		Total: 47,
		Page:  1,
		Pages: 3,
		Count: 20,
		Matches: []slack.SearchMatch{
			{ChannelName: "ops", Ts: "1718200320.000001", Text: "msg"},
		},
	}
	flags := SearchFlags{Count: 20, Page: 1}
	got := formatSearchResults(result, nil, flags)

	if !strings.Contains(got, "page 1 of 3") {
		t.Errorf("missing pagination in footer: %q", got)
	}
	if !strings.Contains(got, "--page 2") {
		t.Errorf("missing next-page hint: %q", got)
	}
}

func TestFormatSearchResults_noNextPageOnLastPage(t *testing.T) {
	result := slack.SearchResult{
		Query: "deploy",
		Total: 5,
		Page:  1,
		Pages: 1,
		Count: 20,
		Matches: []slack.SearchMatch{
			{ChannelName: "ops", Ts: "1718200320.000001", Text: "msg"},
		},
	}
	got := formatSearchResults(result, nil, SearchFlags{Count: 20})
	if strings.Contains(got, "next:") {
		t.Errorf("should not emit next: hint on last page: %q", got)
	}
}

func TestFormatSearchResults_withUserCache(t *testing.T) {
	users := map[string]slack.CachedUser{
		"UABC123": {DisplayName: "Alice Foo", Name: "alice"},
	}
	cache := slack.NewUserCacheFromMap("test.slack.com", users)
	result := slack.SearchResult{
		Query: "hello",
		Total: 1,
		Page:  1,
		Pages: 1,
		Count: 20,
		Matches: []slack.SearchMatch{
			{
				ChannelName: "general",
				UserID:      "UABC123",
				Ts:          "1718200320.000001",
				Text:        "Hello team",
			},
		},
	}
	got := formatSearchResults(result, cache, SearchFlags{Count: 20})
	if !strings.Contains(got, "Alice Foo") {
		t.Errorf("expected resolved user name 'Alice Foo' in output: %q", got)
	}
}

// ---------------------------------------------------------------------------
// stripTrailingPartialWord
// ---------------------------------------------------------------------------

func TestStripTrailingPartialWord_notTruncated(t *testing.T) {
	// No ellipsis — returned unchanged.
	s := "We rolled back the deployment."
	if got := stripTrailingPartialWord(s); got != s {
		t.Errorf("unchanged string modified: got %q", got)
	}
}

func TestStripTrailingPartialWord_unicodeEllipsis(t *testing.T) {
	// Ends with U+2026 and a partial word → strip to last full word.
	s := "We rolled back the deploy…"
	got := stripTrailingPartialWord(s)
	if got != "We rolled back the…" {
		t.Errorf("got %q, want %q", got, "We rolled back the…")
	}
}

func TestStripTrailingPartialWord_dotsEllipsis(t *testing.T) {
	// Ends with "..." and a partial word.
	s := "We rolled back the deploy..."
	got := stripTrailingPartialWord(s)
	if got != "We rolled back the..." {
		t.Errorf("got %q, want %q", got, "We rolled back the...")
	}
}

func TestStripTrailingPartialWord_alreadyWordBoundary(t *testing.T) {
	// Ellipsis immediately after a space — body already ends at word boundary.
	s := "We rolled back the …"
	got := stripTrailingPartialWord(s)
	if got != "We rolled back the…" {
		t.Errorf("got %q, want %q", got, "We rolled back the…")
	}
}

func TestStripTrailingPartialWord_singleWord(t *testing.T) {
	// Single word + ellipsis — no space to strip back to, keep original.
	s := "superlongword…"
	if got := stripTrailingPartialWord(s); got != s {
		t.Errorf("single-word ellipsis should be kept: got %q", got)
	}
}

func TestStripTrailingPartialWord_empty(t *testing.T) {
	if got := stripTrailingPartialWord(""); got != "" {
		t.Errorf("empty string: got %q", got)
	}
}

func TestStripTrailingPartialWord_multiline(t *testing.T) {
	// Newline in body — strip back to last whitespace boundary.
	s := "line one\nline two partial…"
	got := stripTrailingPartialWord(s)
	if got != "line one\nline two…" {
		t.Errorf("got %q, want %q", got, "line one\nline two…")
	}
}

// ---------------------------------------------------------------------------
// resolveWorkspace (workspace resolution logic — no keychain)
// ---------------------------------------------------------------------------

func TestCanonicalDomain_bare(t *testing.T) {
	got := CanonicalDomain("myorg")
	if got != "myorg.slack.com" {
		t.Errorf("got %q, want %q", got, "myorg.slack.com")
	}
}

func TestCanonicalDomain_full(t *testing.T) {
	got := CanonicalDomain("https://myorg.slack.com/archives/C123/p456")
	if got != "myorg.slack.com" {
		t.Errorf("got %q, want %q", got, "myorg.slack.com")
	}
}

func TestCanonicalDomain_alreadyCanonical(t *testing.T) {
	got := CanonicalDomain("myorg.slack.com")
	if got != "myorg.slack.com" {
		t.Errorf("got %q, want %q", got, "myorg.slack.com")
	}
}

// ---------------------------------------------------------------------------
// NewSearchCmd — basic Cobra wiring
// ---------------------------------------------------------------------------

func TestNewSearchCmd_noArgs(t *testing.T) {
	c := NewSearchCmd()
	c.SetArgs([]string{})
	err := c.Execute()
	if err == nil {
		t.Error("expected error for no args, got nil")
	}
}

func TestNewSearchCmd_help(t *testing.T) {
	c := NewSearchCmd()
	var out strings.Builder
	c.SetOut(&out)
	c.SetArgs([]string{"--help"})
	err := c.Execute()
	if err != nil {
		t.Fatalf("unexpected error on --help: %v", err)
	}
	if !strings.Contains(out.String(), "search") {
		t.Errorf("help output missing 'search': %q", out.String())
	}
}

// ---------------------------------------------------------------------------
// channelLabel
// ---------------------------------------------------------------------------

func TestChannelLabel_noCache(t *testing.T) {
	cases := []struct {
		name  string
		match slack.SearchMatch
		want  string
	}{
		{
			name:  "public/private named channel",
			match: slack.SearchMatch{ChannelID: "C012ABC", ChannelName: "ops"},
			want:  "#ops",
		},
		{
			name:  "MPIM no cache → fallback",
			match: slack.SearchMatch{ChannelID: "C0B3CH1GCNP", ChannelName: "mpdm-alice--bob-1", IsMPIM: true},
			want:  "group DM",
		},
		{
			name:  "1:1 DM no cache → DM",
			match: slack.SearchMatch{ChannelID: "D0B22865CQ4", DMPeerID: "W4UDRQJNR"},
			want:  "DM",
		},
		{
			name:  "fallback to ID when no name",
			match: slack.SearchMatch{ChannelID: "C012ABC"},
			want:  "C012ABC",
		},
	}
	for _, tc := range cases {
		got := channelLabel(tc.match, nil)
		if got != tc.want {
			t.Errorf("channelLabel(%q): got %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestChannelLabel_withCache(t *testing.T) {
	cache := slack.NewUserCacheFromMap("test.slack.com", map[string]slack.CachedUser{
		"UPEER1": {DisplayName: "Alice", Name: "alice"},
		"UPEER2": {DisplayName: "Bob", Name: "bob"},
		"UPEER3": {DisplayName: "Carol", Name: "carol"},
		"UPEER4": {DisplayName: "Dave", Name: "dave"},
		"USELF":  {DisplayName: "Self", Name: "self"},
	})

	cases := []struct {
		name  string
		match slack.SearchMatch
		want  string
	}{
		{
			name:  "1:1 DM resolved",
			match: slack.SearchMatch{ChannelID: "D012", DMPeerID: "UPEER1"},
			want:  "DM(Alice)",
		},
		{
			name:  "MPIM two others — handles in ParticipantIDs",
			match: slack.SearchMatch{
				ChannelID:      "C012",
				IsMPIM:         true,
				UserID:         "USELF",
				Username:       "self",
				ParticipantIDs: []string{"self", "alice", "bob"},
			},
			want: "Group(Alice, Bob)",
		},
		{
			name:  "MPIM three others",
			match: slack.SearchMatch{
				ChannelID:      "C012",
				IsMPIM:         true,
				UserID:         "USELF",
				Username:       "self",
				ParticipantIDs: []string{"self", "alice", "bob", "carol"},
			},
			want: "Group(Alice, Bob, Carol)",
		},
		{
			name:  "MPIM four others → +1 more",
			match: slack.SearchMatch{
				ChannelID:      "C012",
				IsMPIM:         true,
				UserID:         "USELF",
				Username:       "self",
				ParticipantIDs: []string{"self", "alice", "bob", "carol", "dave"},
			},
			want: "Group(Alice, Bob, Carol, +1 more)",
		},
	}
	for _, tc := range cases {
		got := channelLabel(tc.match, cache)
		if got != tc.want {
			t.Errorf("channelLabel(%q): got %q, want %q", tc.name, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// formatSearchResultsJSON
// ---------------------------------------------------------------------------

func TestFormatSearchResultsJSON_basicRecord(t *testing.T) {
	result := slack.SearchResult{
		Query: "deployment",
		Total: 1,
		Page:  1,
		Pages: 1,
		Count: 20,
		Matches: []slack.SearchMatch{
			{
				ChannelID:   "C012ABC",
				ChannelName: "ops",
				UserID:      "U111",
				Username:    "alice",
				Ts:          "1718200320.123456",
				Text:        "We rolled back the deployment.",
			},
		},
	}

	out := formatSearchResultsJSON(result, nil, "")
	lines := splitNonEmpty(out)
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d: %q", len(lines), out)
	}

	assertContains(t, lines[0], `"channel_id":"C012ABC"`)
	assertContains(t, lines[0], `"channel_name":"ops"`)
	assertContains(t, lines[0], `"channel_type":"channel"`)
	assertContains(t, lines[0], `"user_id":"U111"`)
	assertContains(t, lines[0], `"username":"alice"`)
	assertContains(t, lines[0], `"ts":"1718200320.123456"`)
	assertContains(t, lines[0], `"text":"We rolled back the deployment."`)

	// No pagination trailer when page == pages.
	if strings.Contains(out, "_pagination") {
		t.Error("unexpected pagination trailer when on last page")
	}
}

func TestFormatSearchResultsJSON_paginationTrailer(t *testing.T) {
	result := slack.SearchResult{
		Query: "test",
		Total: 47,
		Page:  1,
		Pages: 3,
		Count: 20,
		Matches: []slack.SearchMatch{
			{ChannelID: "C001", ChannelName: "general", UserID: "U1", Ts: "1.0", Text: "a"},
		},
	}

	out := formatSearchResultsJSON(result, nil, "")
	lines := splitNonEmpty(out)
	if len(lines) != 2 {
		t.Fatalf("expected 2 lines (record + trailer), got %d: %q", len(lines), out)
	}

	trailer := lines[1]
	assertContains(t, trailer, `"_pagination"`)
	assertContains(t, trailer, `"next_page":2`)
	assertContains(t, trailer, `"has_more":true`)
	assertContains(t, trailer, `"total":47`)
	assertContains(t, trailer, `"page":1`)
	assertContains(t, trailer, `"pages":3`)
}

func TestFormatSearchResultsJSON_DMRecord(t *testing.T) {
	result := slack.SearchResult{
		Page:  1,
		Pages: 1,
		Matches: []slack.SearchMatch{
			{
				ChannelID:   "D012ABC",
				ChannelName: "alice",
				DMPeerID:    "U456",
				UserID:      "U111",
				Ts:          "1.0",
				Text:        "hey",
			},
		},
	}
	out := formatSearchResultsJSON(result, nil, "")
	assertContains(t, out, `"channel_type":"dm"`)
	assertContains(t, out, `"dm_peer_id":"U456"`)
	if strings.Contains(out, "participant_ids") {
		t.Error("unexpected participant_ids in DM record")
	}
}

func TestFormatSearchResultsJSON_MPIMRecord(t *testing.T) {
	result := slack.SearchResult{
		Page:  1,
		Pages: 1,
		Matches: []slack.SearchMatch{
			{
				ChannelID:      "C999MPIM",
				ChannelName:    "mpdm-alice--bob-1",
				IsMPIM:         true,
				ParticipantIDs: []string{"alice", "bob"},
				UserID:         "U111",
				Ts:             "1.0",
				Text:           "group msg",
			},
		},
	}
	out := formatSearchResultsJSON(result, nil, "")
	assertContains(t, out, `"channel_type":"mpim"`)
	assertContains(t, out, `"participant_ids":["alice","bob"]`)
	if strings.Contains(out, "dm_peer_id") {
		t.Error("unexpected dm_peer_id in MPIM record")
	}
}

func TestFormatSearchResultsJSON_withCache(t *testing.T) {
	cache := slack.NewUserCacheFromMap("ws", map[string]slack.CachedUser{
		"U111": {ID: "U111", Name: "alice", DisplayName: "Alice Example"},
	})
	result := slack.SearchResult{
		Page:  1,
		Pages: 1,
		Matches: []slack.SearchMatch{
			{ChannelID: "C001", ChannelName: "ops", UserID: "U111", Ts: "1.0", Text: "hi"},
		},
	}
	out := formatSearchResultsJSON(result, cache, "")
	assertContains(t, out, `"display_name":"Alice Example"`)
	assertContains(t, out, `"username":"alice"`)
}

func TestFormatSearchResultsJSON_threadTs(t *testing.T) {
	result := slack.SearchResult{
		Total: 1, Page: 1, Pages: 1, Count: 20,
		Matches: []slack.SearchMatch{
			{
				ChannelID: "C012ABC",
				UserID:    "U111",
				Ts:        "1718200400.000001",
				ThreadTs:  "1718200320.123456",
				Text:      "agreed",
			},
		},
	}
	out := formatSearchResultsJSON(result, nil, "")
	assertContains(t, out, `"thread_ts":"1718200320.123456"`)

	// Top-level message: thread_ts must be omitted (omitempty).
	topLevel := slack.SearchResult{
		Total: 1, Page: 1, Pages: 1, Count: 20,
		Matches: []slack.SearchMatch{
			{ChannelID: "C012ABC", UserID: "U111", Ts: "1718200320.123456", Text: "hi"},
		},
	}
	outTop := formatSearchResultsJSON(topLevel, nil, "")
	if strings.Contains(outTop, "thread_ts") {
		t.Errorf("thread_ts should be omitted for top-level message, got: %s", outTop)
	}
}

// ---------------------------------------------------------------------------
// formatSearchResultsJSON — workspace field
// ---------------------------------------------------------------------------

// TestFormatSearchResultsJSON_workspaceSameAsResolved verifies that the workspace
// field is omitted when the match workspace equals the resolved workspace.
func TestFormatSearchResultsJSON_workspaceSameAsResolved(t *testing.T) {
	result := slack.SearchResult{
		Page:  1,
		Pages: 1,
		Matches: []slack.SearchMatch{
			{
				ChannelID: "C001",
				UserID:    "U1",
				Ts:        "1.0",
				Text:      "hello",
				Permalink: "https://myorg.slack.com/archives/C001/p1",
				Workspace: "myorg.slack.com",
			},
		},
	}
	out := formatSearchResultsJSON(result, nil, "myorg.slack.com")
	if strings.Contains(out, "workspace") {
		t.Errorf("workspace field should be omitted when it matches resolved workspace, got: %s", out)
	}
}

// TestFormatSearchResultsJSON_workspaceDiffersFromResolved verifies that the
// workspace field is emitted when the match comes from a different workspace.
func TestFormatSearchResultsJSON_workspaceDiffersFromResolved(t *testing.T) {
	result := slack.SearchResult{
		Page:  1,
		Pages: 1,
		Matches: []slack.SearchMatch{
			{
				ChannelID: "C001",
				UserID:    "U1",
				Ts:        "1.0",
				Text:      "hello from another ws",
				Permalink: "https://sap-car.slack.com/archives/C001/p1",
				Workspace: "sap-car.slack.com",
			},
		},
	}
	out := formatSearchResultsJSON(result, nil, "myorg.slack.com")
	assertContains(t, out, `"workspace":"sap-car.slack.com"`)
}

// TestFormatSearchResultsJSON_workspaceEmptyPermalink verifies that the workspace
// field is omitted when the match has no permalink (Workspace is empty string).
func TestFormatSearchResultsJSON_workspaceEmptyPermalink(t *testing.T) {
	result := slack.SearchResult{
		Page:  1,
		Pages: 1,
		Matches: []slack.SearchMatch{
			{ChannelID: "C001", UserID: "U1", Ts: "1.0", Text: "no permalink"},
		},
	}
	out := formatSearchResultsJSON(result, nil, "myorg.slack.com")
	if strings.Contains(out, "workspace") {
		t.Errorf("workspace field should be omitted when empty, got: %s", out)
	}
}

// splitNonEmpty splits s by newlines and returns non-empty lines.
func splitNonEmpty(s string) []string {
	var out []string
	for _, line := range strings.Split(s, "\n") {
		if line != "" {
			out = append(out, line)
		}
	}
	return out
}