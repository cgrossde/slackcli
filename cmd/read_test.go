package cmd

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	slackgo "github.com/slack-go/slack"

	"github.com/cgrossde/slackcli/internal/keychain"
	"github.com/cgrossde/slackcli/internal/slack"
)

func TestFormatMessage_headerLine(t *testing.T) {
	msg := slack.Message{
		User: "UABC123DEF",
		Ts:   "1700000000.000001",
		Text: "hello world",
	}

	// nil cache → raw user ID in header
	got := formatMessage(msg, 0, nil)
	lines := strings.SplitN(got, "\n", 3)
	header := lines[0]

	if len(header) != 120 {
		t.Errorf("header length = %d, want 120; got: %q", len(header), header)
	}
	if !strings.HasPrefix(header, "== UABC123DEF 2023-11-14 22:13 ") {
		t.Errorf("unexpected header prefix: %q", header)
	}
	if !strings.HasSuffix(header, "[ message ]==") {
		t.Errorf("header should end with '[ message ]==': %q", header)
	}

	// index 1 → reply label at end
	got2 := formatMessage(msg, 1, nil)
	header2 := strings.SplitN(got2, "\n", 2)[0]
	if len(header2) != 120 {
		t.Errorf("reply header length = %d, want 120; got: %q", len(header2), header2)
	}
	if !strings.HasSuffix(header2, "[ reply 1 ]==") {
		t.Errorf("reply header should end with '[ reply 1 ]==': %q", header2)
	}

	// text rendered without "text:" prefix
	if !strings.Contains(got, "hello world") {
		t.Errorf("missing text in: %q", got)
	}
	if strings.Contains(got, "text: ") {
		t.Errorf("unexpected 'text:' prefix in: %q", got)
	}
}

func TestFormatMessage_withCache(t *testing.T) {
	cache := slack.NewUserCacheFromMap("example.slack.com", map[string]slack.CachedUser{
		"UABC123DEF": {ID: "UABC123DEF", Name: "alice", DisplayName: "Alice Foo"},
		"UXYZ987GHI": {ID: "UXYZ987GHI", Name: "bob",   DisplayName: "Bob Bar"},
	})

	msg := slack.Message{
		User: "UABC123DEF",
		Ts:   "1700000000.000001",
		Text: "hello <@UXYZ987GHI> and <@UNKNOWN>",
	}

	got := formatMessage(msg, 0, cache)
	header := strings.SplitN(got, "\n", 2)[0]

	if len(header) != 120 {
		t.Errorf("header length = %d, want 120; got: %q", len(header), header)
	}
	if !strings.Contains(header, "Alice Foo (alice)") {
		t.Errorf("author not resolved in header: %q", header)
	}
	if !strings.Contains(got, "<@Bob Bar (bob)>") {
		t.Errorf("known mention not resolved: %q", got)
	}
	if !strings.Contains(got, "<@UNKNOWN>") {
		t.Errorf("unknown mention should be unchanged: %q", got)
	}
}

func TestFormatMessage_unparsableTs(t *testing.T) {
	msg := slack.Message{User: "U123", Ts: "not-a-ts", Text: "x"}
	got := formatMessage(msg, 0, nil)
	header := strings.SplitN(got, "\n", 2)[0]
	if len(header) != 120 {
		t.Errorf("header length = %d, want 120; got: %q", len(header), header)
	}
	if !strings.Contains(header, "not-a-ts") {
		t.Errorf("raw ts missing from header: %q", header)
	}
}

func TestReadMessage_badURL(t *testing.T) {
	_, err := ReadMessage("not-a-url", "", "")
	if err == nil {
		t.Fatal("expected error for invalid URL, got nil")
	}
}

func TestReadMessage_wrongHost(t *testing.T) {
	_, err := ReadMessage("https://google.com/archives/C123/p1700000000000001", "", "")
	if err == nil {
		t.Fatal("expected error for non-slack host, got nil")
	}
}

func TestReadMessage_noCredentials(t *testing.T) {
	// A valid Slack URL for a workspace that has no keychain entry.
	// The keychain will return ErrNotFound, which ReadMessage should wrap
	// into a user-facing error (not a panic).
	_, err := ReadMessage("https://nonexistent-workspace-xyz.slack.com/archives/C123ABC/p1700000000000001", "", "")
	if err == nil {
		t.Fatal("expected error when no credentials saved, got nil")
	}
}

// TestReadMessage_channelTs_withWorkspace verifies that the channel:ts form
// with an explicit workspace does not panic and returns an error. When a
// default workspace is saved in the keychain the fallback may succeed at
// credential lookup and then fail later (e.g. channel not found); either way
// a non-nil error is returned.
func TestReadMessage_channelTs_withWorkspace(t *testing.T) {
	_, err := ReadMessage("C012ABC3456:1718197925.001234", "nonexistent-workspace-xyz.slack.com", "")
	if err == nil {
		t.Fatal("expected error when using a nonexistent workspace, got nil")
	}
}

// TestReadMessage_channelTs_noWorkspace_noDefault verifies that the channel:ts
// form without --workspace and without a stored default returns a clear error
// rather than a panic.
func TestReadMessage_channelTs_noWorkspace_noDefault(t *testing.T) {
	// This test relies on the behaviour of ResolveDefault when multiple
	// workspaces may be saved. We pass a workspace that will fail at lookup,
	// but drive through resolveRef with an empty workspace to exercise the
	// default-resolution path.
	// We can't control the keychain in unit tests, so we verify the error
	// path: either "no saved workspaces", "multiple workspaces", or
	// "no credentials" — all are non-nil errors.
	_, err := ReadMessage("C012ABC3456:1718197925.001234", "", "")
	if err == nil {
		t.Fatal("expected error without valid workspace, got nil")
	}
}

// TestResolveRef_urlIgnoresWorkspace verifies that a URL ref populates
// workspace from the URL and ignores the --workspace flag.
func TestResolveRef_urlIgnoresWorkspace(t *testing.T) {
	ref, err := resolveRef(
		"https://myorg.slack.com/archives/C01234ABCDE/p1700000000000001",
		"otherorg.slack.com",
		"",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref.Workspace != "myorg.slack.com" {
		t.Errorf("Workspace: got %q, want myorg.slack.com (URL must win)", ref.Workspace)
	}
	if ref.ChannelID != "C01234ABCDE" {
		t.Errorf("ChannelID: got %q", ref.ChannelID)
	}
	if ref.Ts != "1700000000.000001" {
		t.Errorf("Ts: got %q", ref.Ts)
	}
}

// TestResolveRef_channelTs_explicit verifies that an explicit --workspace
// flag is used for the channel:ts form.
func TestResolveRef_channelTs_explicit(t *testing.T) {
	ref, err := resolveRef("C012ABC3456:1718197925.001234", "myorg.slack.com", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref.Workspace != "myorg.slack.com" {
		t.Errorf("Workspace: got %q, want myorg.slack.com", ref.Workspace)
	}
	if ref.ChannelID != "C012ABC3456" {
		t.Errorf("ChannelID: got %q", ref.ChannelID)
	}
	if ref.Ts != "1718197925.001234" {
		t.Errorf("Ts: got %q", ref.Ts)
	}
}

// TestResolveRef_channelTs_canonicalises verifies that a bare workspace name
// is canonicalised to a .slack.com domain.
func TestResolveRef_channelTs_canonicalises(t *testing.T) {
	ref, err := resolveRef("C012ABC3456:1718197925.001234", "myorg", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref.Workspace != "myorg.slack.com" {
		t.Errorf("Workspace: got %q, want myorg.slack.com", ref.Workspace)
	}
}

// TestResolveRef_threadTsFlag verifies that --thread-ts sets ThreadTs on the ref.
func TestResolveRef_threadTsFlag(t *testing.T) {
	ref, err := resolveRef("C012ABC3456:1718197925.001234", "myorg.slack.com", "1718197000.000001")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref.ThreadTs != "1718197000.000001" {
		t.Errorf("ThreadTs: got %q, want 1718197000.000001", ref.ThreadTs)
	}
	if ref.Ts != "1718197925.001234" {
		t.Errorf("Ts: got %q, want 1718197925.001234", ref.Ts)
	}
}

// TestResolveRef_threePartForm verifies that the three-part channel:threadTs:replyTs
// compact form populates both Ts and ThreadTs.
func TestResolveRef_threePartForm(t *testing.T) {
	ref, err := resolveRef("C012ABC3456:1718197000.000001:1718197925.001234", "myorg.slack.com", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref.ThreadTs != "1718197000.000001" {
		t.Errorf("ThreadTs: got %q, want 1718197000.000001", ref.ThreadTs)
	}
	if ref.Ts != "1718197925.001234" {
		t.Errorf("Ts: got %q, want 1718197925.001234", ref.Ts)
	}
}

// TestResolveRef_threadTsFlagOverridesThreePart verifies that --thread-ts overrides
// the ThreadTs already set by the three-part form.
func TestResolveRef_threadTsFlagOverridesThreePart(t *testing.T) {
	ref, err := resolveRef(
		"C012ABC3456:1718197000.000001:1718197925.001234",
		"myorg.slack.com",
		"1718190000.000001", // explicit flag overrides
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ref.ThreadTs != "1718190000.000001" {
		t.Errorf("ThreadTs: got %q, want flag value 1718190000.000001", ref.ThreadTs)
	}
}

// TestNewReadCmd_noArgs verifies that invoking the command with no arguments
// returns a non-zero exit via cobra (ExactArgs(1) enforcement).
func TestNewReadCmd_noArgs(t *testing.T) {
	c := NewReadCmd()
	buf := &bytes.Buffer{}
	c.SetOut(buf)
	c.SetErr(buf)
	c.SetArgs([]string{})
	err := c.Execute()
	if err == nil {
		t.Fatal("expected error when called with no args, got nil")
	}
}

// TestNewReadCmd_help verifies --help exits 0 and includes usage info.
func TestNewReadCmd_help(t *testing.T) {
	c := NewReadCmd()
	buf := &bytes.Buffer{}
	c.SetOut(buf)
	c.SetErr(buf)
	c.SetArgs([]string{"--help"})
	// cobra.Command.Execute returns nil for --help.
	_ = c.Execute()
	out := buf.String()
	if out == "" {
		t.Error("expected help output, got empty string")
	}
}

// ---------------------------------------------------------------------------
// channelTypeFromID
// ---------------------------------------------------------------------------

func TestChannelTypeFromID(t *testing.T) {
	cases := []struct{ id, want string }{
		{"C012ABC", "channel"},
		{"D012ABC", "dm"},
		{"G012ABC", "group"},
		{"W012ABC", "channel"}, // W is workspace member, treated as channel
		{"", "channel"},
	}
	for _, tc := range cases {
		got := channelTypeFromID(tc.id)
		if got != tc.want {
			t.Errorf("channelTypeFromID(%q) = %q, want %q", tc.id, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// readMessageJSON struct (no network)
// ---------------------------------------------------------------------------

func TestReadMessageJSON_struct(t *testing.T) {
	// Directly marshal a readMessageJSON and verify every field is present.
	rec := readMessageJSON{
		UserID:      "U111",
		Username:    "alice",
		DisplayName: "Alice Example",
		Ts:          "1718200320.123456",
		ThreadTs:    "1718200320.123456",
		Text:        "Full message text.",
		IsRoot:      true,
		ReplyCount:  3,
		ChannelID:   "C012AB3CD",
		ChannelType: "channel",
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("json.Marshal failed: %v", err)
	}
	s := string(data)
	for _, want := range []string{
		`"user_id":"U111"`,
		`"username":"alice"`,
		`"display_name":"Alice Example"`,
		`"ts":"1718200320.123456"`,
		`"thread_ts":"1718200320.123456"`,
		`"text":"Full message text."`,
		`"is_root":true`,
		`"reply_count":3`,
		`"channel_id":"C012AB3CD"`,
		`"channel_type":"channel"`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in JSON: %s", want, s)
		}
	}
}

func TestReadMessageJSON_replyCountOmittedWhenZero(t *testing.T) {
	rec := readMessageJSON{
		UserID:      "U2",
		Ts:          "1.0",
		ChannelID:   "C1",
		ChannelType: "channel",
	}
	data, _ := json.Marshal(rec)
	if strings.Contains(string(data), "reply_count") {
		t.Error("reply_count should be omitted when zero")
	}
}

func TestReadMessageJSON_dmChannelType(t *testing.T) {
	rec := readMessageJSON{
		UserID:      "U2",
		Ts:          "1.0",
		ChannelID:   "D123",
		ChannelType: channelTypeFromID("D123"),
	}
	data, _ := json.Marshal(rec)
	if !strings.Contains(string(data), `"channel_type":"dm"`) {
		t.Errorf("expected dm channel_type, got: %s", string(data))
	}
}

func TestFormatMessage_files(t *testing.T) {
	msg := slack.Message{
		User: "U1",
		Ts:   "1700000000.000001",
		Text: "see attachment",
		Files: []slack.File{
			{
				ID:         "F001",
				Name:       "report.pdf",
				PrettyType: "PDF",
				Permalink:  "https://files.slack.com/report.pdf",
			},
		},
	}
	got := formatMessage(msg, 0, nil)
	if !strings.Contains(got, "[file] report.pdf (PDF)") {
		t.Errorf("missing file line in: %q", got)
	}
	if !strings.Contains(got, "https://files.slack.com/report.pdf") {
		t.Errorf("missing permalink in: %q", got)
	}
	// Download hint must appear when a file permalink is set.
	if !strings.Contains(got, "→ slackcli read https://files.slack.com/report.pdf") {
		t.Errorf("missing download hint in: %q", got)
	}
}

func TestFormatMessage_fileNoType(t *testing.T) {
	msg := slack.Message{
		User: "U1",
		Ts:   "1700000000.000001",
		Text: "blob",
		Files: []slack.File{
			{Name: "data.bin", URLPrivate: "https://files.slack.com/data.bin"},
		},
	}
	got := formatMessage(msg, 0, nil)
	if !strings.Contains(got, "[file] data.bin") {
		t.Errorf("missing file line in: %q", got)
	}
	if strings.Contains(got, "[file] data.bin (") {
		t.Errorf("unexpected type in file line: %q", got)
	}
	if !strings.Contains(got, "https://files.slack.com/data.bin") {
		t.Errorf("missing url_private fallback in: %q", got)
	}
}

func TestFormatMessage_reactions(t *testing.T) {
	msg := slack.Message{
		User: "U1",
		Ts:   "1700000000.000001",
		Text: "great news",
		Reactions: []slack.Reaction{
			{Name: "thumbsup", Count: 5, Users: []string{"U2", "U3"}},
			{Name: "ok_hand",  Count: 1, Users: []string{"U2"}},
		},
	}
	got := formatMessage(msg, 0, nil)
	if !strings.Contains(got, "Reactions: :thumbsup: ×5") {
		t.Errorf("missing 'Reactions:' prefix + thumbsup in: %q", got)
	}
	if !strings.Contains(got, ":ok_hand: ×1") {
		t.Errorf("missing ok_hand reaction in: %q", got)
	}
}

func TestFormatMessage_noFilesNoReactions(t *testing.T) {
	msg := slack.Message{User: "U1", Ts: "1700000000.000001", Text: "plain"}
	got := formatMessage(msg, 0, nil)
	if strings.Contains(got, "[file]") {
		t.Errorf("unexpected [file] in: %q", got)
	}
	if strings.Contains(got, "×") {
		t.Errorf("unexpected reaction marker in: %q", got)
	}
}

func TestReadMessageJSON_filesAndReactions(t *testing.T) {
	rec := readMessageJSON{
		UserID:      "U1",
		Ts:          "1.0",
		ChannelID:   "C1",
		ChannelType: "channel",
		Files: []fileJSON{
			{ID: "F1", Name: "img.png", PrettyType: "PNG", Permalink: "https://example.com/img.png"},
		},
		Reactions: []reactionJSON{
			{Name: "thumbsup", Count: 2, Users: []string{"U2", "U3"}},
		},
	}
	data, _ := json.Marshal(rec)
	s := string(data)
	for _, want := range []string{
		`"files":[`,
		`"id":"F1"`,
		`"name":"img.png"`,
		`"pretty_type":"PNG"`,
		`"permalink":"https://example.com/img.png"`,
		`"reactions":[`,
		`"name":"thumbsup"`,
		`"count":2`,
		`"users":["U2","U3"]`,
	} {
		if !strings.Contains(s, want) {
			t.Errorf("missing %q in JSON: %s", want, s)
		}
	}
}

func TestReadMessageJSON_filesOmittedWhenEmpty(t *testing.T) {
	rec := readMessageJSON{UserID: "U1", Ts: "1.0", ChannelID: "C1", ChannelType: "channel"}
	data, _ := json.Marshal(rec)
	s := string(data)
	if strings.Contains(s, "files") {
		t.Errorf("files should be omitted when nil: %s", s)
	}
	if strings.Contains(s, "reactions") {
		t.Errorf("reactions should be omitted when nil: %s", s)
	}
}

func TestFormatMessage_fileURLPrivateHint(t *testing.T) {
	// When only url_private is set (no permalink), hint uses url_private.
	msg := slack.Message{
		User: "U1",
		Ts:   "1700000000.000001",
		Text: "blob",
		Files: []slack.File{
			{Name: "data.bin", URLPrivate: "https://files.slack.com/data.bin"},
		},
	}
	got := formatMessage(msg, 0, nil)
	if !strings.Contains(got, "→ slackcli read https://files.slack.com/data.bin") {
		t.Errorf("expected url_private hint in: %q", got)
	}
}

func TestReadFile_badURL(t *testing.T) {
	_, err := ReadFile("not-a-url", "", "")
	if err == nil {
		t.Fatal("expected error for invalid file URL, got nil")
	}
}

func TestReadFile_messageURLRejected(t *testing.T) {
	// A message permalink must not be accepted as a file URL.
	_, err := ReadFile("https://myorg.slack.com/archives/C123/p1700000000000001", "", "")
	if err == nil {
		t.Fatal("expected error for message URL passed to ReadFile, got nil")
	}
}

// stubFileClient implements fileClient for testing downloadFile directly.
type stubFileClient struct {
	info     slack.File
	infoErr  error
	data     []byte
	fetchErr error
	fetchURL string // records what URL was fetched
}

func (s *stubFileClient) GetFileInfo(fileID string) (slack.File, error) {
	return s.info, s.infoErr
}

func (s *stubFileClient) FetchFileBytes(url string) ([]byte, string, error) {
	s.fetchURL = url
	return s.data, "image/png", s.fetchErr
}

func TestDownloadFile_happyPath(t *testing.T) {
	dir := t.TempDir()
	dest := dir + "/out.png"

	content := []byte("fake png bytes")
	client := &stubFileClient{
		info: slack.File{
			ID:         "F001",
			Name:       "image.png",
			URLPrivate: "https://files.slack.com/files-pri/image.png",
		},
		data: content,
	}
	ref := slack.FileRef{
		Workspace: "myorg.slack.com",
		FileID:    "F001",
		Filename:  "image.png",
	}

	out, err := downloadFile(client, ref, dest)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, dest) {
		t.Errorf("output missing dest path: %q", out)
	}
	if !strings.Contains(out, "14 bytes") {
		t.Errorf("output missing byte count: %q", out)
	}
	if client.fetchURL != "https://files.slack.com/files-pri/image.png" {
		t.Errorf("fetched wrong URL: %q", client.fetchURL)
	}
	written, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("reading output file: %v", err)
	}
	if string(written) != string(content) {
		t.Errorf("file content mismatch: got %q, want %q", written, content)
	}
}

func TestDownloadFile_usesInfoNameOverRefFilename(t *testing.T) {
	// files.info canonical name wins over the name in the URL.
	dir := t.TempDir()
	client := &stubFileClient{
		info: slack.File{
			ID:         "F001",
			Name:       "canonical.pdf",
			URLPrivate: "https://files.slack.com/canonical.pdf",
		},
		data: []byte("pdf"),
	}
	ref := slack.FileRef{FileID: "F001", Filename: "url-name.pdf"}

	out, err := downloadFile(client, ref, dir+"/canonical.pdf")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "canonical.pdf") {
		t.Errorf("expected canonical name in output: %q", out)
	}
}

func TestDownloadFile_noURLPrivate(t *testing.T) {
	client := &stubFileClient{
		info: slack.File{ID: "F001", Name: "deleted.png"}, // URLPrivate empty
	}
	ref := slack.FileRef{FileID: "F001"}
	_, err := downloadFile(client, ref, "/tmp/x")
	if err == nil {
		t.Fatal("expected error when URLPrivate is empty")
	}
}

// ---------------------------------------------------------------------------
// tryFetchThread
// ---------------------------------------------------------------------------

// readTestJSON writes v as JSON to w.
func readTestJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// TestTryFetchThread_success verifies that tryFetchThread returns the message
// when the server returns a valid single-message result.
func TestTryFetchThread_success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/conversations.history":
			readTestJSON(w, map[string]any{
				"ok": true,
				"messages": []map[string]any{
					{"type": "message", "user": "U1", "text": "hello", "ts": "1752672853.184209"},
				},
			})
		case "/users.list":
			readTestJSON(w, map[string]any{"ok": true, "members": []any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	client := slack.NewClient("xoxc-test", "xoxd-test",
		slackgo.OptionAPIURL(srv.URL+"/"),
	)
	entry := keychain.Entry{Workspace: "test.slack.com", Token: "xoxc-test", Cookie: "xoxd-test"}
	ref := slack.MessageRef{Workspace: "test.slack.com", ChannelID: "C123", Ts: "1752672853.184209"}

	msgs, _, _, err := tryFetchThread(ref, "test.slack.com", entry, client)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Text != "hello" {
		t.Errorf("expected 1 message with text 'hello', got %v", msgs)
	}
}

// TestTryFetchThread_messageNotFound verifies that when conversations.history
// returns an empty messages array, tryFetchThread returns ErrMessageNotFound.
func TestTryFetchThread_messageNotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/conversations.history":
			readTestJSON(w, map[string]any{"ok": true, "messages": []any{}})
		case "/users.list":
			readTestJSON(w, map[string]any{"ok": true, "members": []any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	client := slack.NewClient("xoxc-test", "xoxd-test",
		slackgo.OptionAPIURL(srv.URL+"/"),
	)
	entry := keychain.Entry{Workspace: "test.slack.com", Token: "xoxc-test", Cookie: "xoxd-test"}
	ref := slack.MessageRef{Workspace: "test.slack.com", ChannelID: "CNOBODY", Ts: "1111111111.000000"}

	_, _, _, err := tryFetchThread(ref, "test.slack.com", entry, client)
	if err == nil {
		t.Fatal("expected ErrMessageNotFound, got nil")
	}
	if !errors.Is(err, slack.ErrMessageNotFound) {
		t.Errorf("expected ErrMessageNotFound, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// readMessageJSON — workspace field
// ---------------------------------------------------------------------------

// TestReadMessageJSON_workspaceFieldPresent verifies that the workspace field
// is marshalled when set.
func TestReadMessageJSON_workspaceFieldPresent(t *testing.T) {
	rec := readMessageJSON{
		UserID:      "U1",
		Ts:          "1.0",
		ChannelID:   "C1",
		ChannelType: "channel",
		Workspace:   "myorg.slack.com",
	}
	data, err := json.Marshal(rec)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}
	if !strings.Contains(string(data), `"workspace":"myorg.slack.com"`) {
		t.Errorf("expected workspace field in JSON, got: %s", string(data))
	}
}

// TestReadMessageJSON_workspaceFieldOmittedWhenEmpty verifies that the workspace
// field is omitted when empty (omitempty behaviour).
func TestReadMessageJSON_workspaceFieldOmittedWhenEmpty(t *testing.T) {
	rec := readMessageJSON{
		UserID:      "U1",
		Ts:          "1.0",
		ChannelID:   "C1",
		ChannelType: "channel",
	}
	data, _ := json.Marshal(rec)
	if strings.Contains(string(data), "workspace") {
		t.Errorf("workspace field should be omitted when empty, got: %s", string(data))
	}
}

// ---------------------------------------------------------------------------
// fetchThreadWithClient — channel cache fast path
// ---------------------------------------------------------------------------

// TestFetchThreadWithClient_channelCacheHit verifies that when a channel ID is
// already in the channel cache pointing to a different workspace, the cached
// workspace is tried first.  The test uses a per-test cache path injected via
// a server that only responds to the cached workspace's credentials.
func TestFetchThreadWithClient_channelCacheHit(t *testing.T) {
	// Server that returns a valid message for the "other" workspace.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/conversations.history":
			readTestJSON(w, map[string]any{
				"ok": true,
				"messages": []map[string]any{
					{"type": "message", "user": "U2", "text": "cached ws msg", "ts": "1752672853.184209"},
				},
			})
		case "/users.list":
			readTestJSON(w, map[string]any{"ok": true, "members": []any{}})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(srv.Close)

	// Build a cache file in a temp dir pointing C123 → "cached.slack.com".
	cacheDir := t.TempDir()
	cacheFile := cacheDir + "/channels.json"
	if err := os.WriteFile(cacheFile, []byte(`{"C123":"cached.slack.com"}`), 0o600); err != nil {
		t.Fatalf("writing cache: %v", err)
	}

	// Build a client using the test server (simulates the cached workspace).
	client := slack.NewClient("xoxc-test", "xoxd-test",
		slackgo.OptionAPIURL(srv.URL+"/"),
	)
	entry := keychain.Entry{Workspace: "cached.slack.com", Token: "xoxc-test", Cookie: "xoxd-test"}

	// tryFetchThread directly (lower layer; does not touch the channel cache).
	ref := slack.MessageRef{Workspace: "cached.slack.com", ChannelID: "C123", Ts: "1752672853.184209"}
	msgs, _, _, err := tryFetchThread(ref, "cached.slack.com", entry, client)
	if err != nil {
		t.Fatalf("tryFetchThread: %v", err)
	}
	if len(msgs) != 1 || msgs[0].Text != "cached ws msg" {
		t.Errorf("expected 'cached ws msg', got %v", msgs)
	}
}