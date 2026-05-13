package keychain

import (
	"strings"
	"testing"
	"time"
)

// ---------------------------------------------------------------------------
// parseKeychainDump
// ---------------------------------------------------------------------------

func TestParseKeychainDump_EmptyInput(t *testing.T) {
	entries, corrupt, err := parseKeychainDump("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 0 || len(corrupt) != 0 {
		t.Fatalf("expected empty slices, got %v / %v", entries, corrupt)
	}
}

func TestParseKeychainDump_SingleEntry(t *testing.T) {
	now := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)
	dump := buildDump(serviceName, "myorg.slack.com", `{"workspace":"myorg.slack.com","token":"xoxc-1-2-3-abcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcdefabcd","cookie":"xoxd-abc","saved_at":"2024-01-15T12:00:00Z"}`)
	entries, corrupt, err := parseKeychainDump(dump)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(corrupt) != 0 {
		t.Fatalf("unexpected corrupt entries: %v", corrupt)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	e := entries[0]
	if e.Workspace != "myorg.slack.com" {
		t.Errorf("workspace: got %q, want %q", e.Workspace, "myorg.slack.com")
	}
	if !strings.HasPrefix(e.Token, "xoxc-") {
		t.Errorf("token missing xoxc- prefix: %q", e.Token)
	}
	if !strings.HasPrefix(e.Cookie, "xoxd-") {
		t.Errorf("cookie missing xoxd- prefix: %q", e.Cookie)
	}
	if !e.SavedAt.Equal(now) {
		t.Errorf("saved_at: got %v, want %v", e.SavedAt, now)
	}
}

func TestParseKeychainDump_MultipleEntries(t *testing.T) {
	dump := buildDump(serviceName, "org1.slack.com", `{"workspace":"org1.slack.com","token":"xoxc-1-2-3-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","cookie":"xoxd-1","saved_at":"2024-01-01T00:00:00Z"}`) +
		buildDump(serviceName, "org2.slack.com", `{"workspace":"org2.slack.com","token":"xoxc-1-2-3-bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb","cookie":"xoxd-2","saved_at":"2024-01-02T00:00:00Z"}`)
	entries, corrupt, err := parseKeychainDump(dump)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(corrupt) != 0 {
		t.Fatalf("unexpected corrupt: %v", corrupt)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d: %+v", len(entries), entries)
	}
}

func TestParseKeychainDump_SkipsOtherServices(t *testing.T) {
	dump := buildDump("other-service", "user@example.com", "somepassword") +
		buildDump(serviceName, "myorg.slack.com", `{"workspace":"myorg.slack.com","token":"xoxc-1-2-3-cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc","cookie":"xoxd-c","saved_at":"2024-01-01T00:00:00Z"}`)
	entries, _, err := parseKeychainDump(dump)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry (other service should be skipped), got %d", len(entries))
	}
	if entries[0].Workspace != "myorg.slack.com" {
		t.Errorf("wrong workspace: %q", entries[0].Workspace)
	}
}

func TestParseKeychainDump_CorruptJSON(t *testing.T) {
	dump := buildDump(serviceName, "broken.slack.com", `not-valid-json`)
	entries, corrupt, err := parseKeychainDump(dump)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no valid entries, got %d", len(entries))
	}
	if len(corrupt) != 1 || corrupt[0] != "broken.slack.com" {
		t.Errorf("corrupt: got %v, want [broken.slack.com]", corrupt)
	}
}

// ---------------------------------------------------------------------------
// extractKeychainValue / extractPasswordValue
// ---------------------------------------------------------------------------

func TestExtractKeychainValue(t *testing.T) {
	cases := []struct {
		line string
		want string
	}{
		{`"svce"<blob>="slackcli"`, "slackcli"},
		{`"acct"<blob>="myorg.slack.com"`, "myorg.slack.com"},
		{`"svce"<blob>=<NULL>`, ""},
		{`no-equals-sign`, ""},
	}
	for _, tc := range cases {
		got := extractKeychainValue(tc.line)
		if got != tc.want {
			t.Errorf("extractKeychainValue(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
}

func TestExtractPasswordValue(t *testing.T) {
	cases := []struct {
		line string
		want string
	}{
		{
			`password: "{\"token\":\"xoxc-1\"}"`,
			`{"token":"xoxc-1"}`,
		},
		{
			`password: "simple"`,
			`simple`,
		},
		{
			`password: <NULL>`,
			"",
		},
	}
	for _, tc := range cases {
		got := extractPasswordValue(tc.line)
		if got != tc.want {
			t.Errorf("extractPasswordValue(%q) = %q, want %q", tc.line, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// buildDump generates a fake dump-keychain stanza for a single item.
// The password value has its double quotes escaped exactly as `security` does.
func buildDump(service, account, password string) string {
	escaped := strings.ReplaceAll(password, `"`, `\"`)
	return `keychain: "/Users/test/Library/Keychains/login.keychain-db"
class: "genp"
attributes:
    "acct"<blob>="` + account + `"
    "svce"<blob>="` + service + `"
password: "` + escaped + `"

`
}

// ---------------------------------------------------------------------------
// isNotFound
// ---------------------------------------------------------------------------

func TestIsNotFound(t *testing.T) {
	cases := []struct {
		out  string
		want bool
	}{
		{"SecKeychainSearchCopyNext: The specified item could not be found.", true},
		{"could not be found in any keychain.", true},
		{"some other error", false},
		{"", false},
	}
	for _, tc := range cases {
		got := isNotFound(tc.out)
		if got != tc.want {
			t.Errorf("isNotFound(%q) = %v, want %v", tc.out, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// Index round-trip (integration — requires macOS security CLI)
// ---------------------------------------------------------------------------

// TestIndexRoundTrip exercises indexAdd, indexLoad, and indexRemove against
// the real keychain using a synthetic workspace name that is cleaned up at
// the end of the test.
func TestIndexRoundTrip(t *testing.T) {
	const testWS = "slackcli-test-index-roundtrip.slack.com"

	// Save existing index so we can restore it after the test.
	existing, _ := indexLoad()

	t.Cleanup(func() {
		// Restore original index, ensuring the test workspace is absent.
		restored := existing[:0:len(existing)]
		for _, w := range existing {
			if w != testWS {
				restored = append(restored, w)
			}
		}
		if len(restored) == 0 {
			// Delete the index item entirely so the keychain is back to its
			// original state (no index item).
			_, _ = run("security", "delete-generic-password", "-s", serviceName, "-a", indexAccount)
		} else {
			_ = indexSave(restored)
		}
	})

	// Start from a clean slate for this test by removing only our test workspace.
	_ = indexRemove(testWS)

	// Add once — should appear.
	if err := indexAdd(testWS); err != nil {
		t.Fatalf("indexAdd: %v", err)
	}
	ws, err := indexLoad()
	if err != nil {
		t.Fatalf("indexLoad after add: %v", err)
	}
	found := false
	for _, w := range ws {
		if w == testWS {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected %s in index after add, got %v", testWS, ws)
	}

	// Add again — must be idempotent (count of testWS must remain 1).
	if err := indexAdd(testWS); err != nil {
		t.Fatalf("indexAdd (duplicate): %v", err)
	}
	ws, _ = indexLoad()
	count := 0
	for _, w := range ws {
		if w == testWS {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected exactly 1 occurrence after duplicate add, got %d", count)
	}

	// Remove — should be gone.
	if err := indexRemove(testWS); err != nil {
		t.Fatalf("indexRemove: %v", err)
	}
	ws, _ = indexLoad()
	for _, w := range ws {
		if w == testWS {
			t.Errorf("workspace still present in index after remove")
		}
	}
}
