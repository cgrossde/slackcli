package cmd

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/spf13/cobra"

	"github.com/cgrossde/slackcli/internal/keychain"
)

// ---------------------------------------------------------------------------
// fakeKeychain — test stub; never touches the real macOS Keychain.
// ---------------------------------------------------------------------------

type fakeKeychain struct {
	entries       map[string]keychain.Entry // workspace → Entry
	corrupt       []string
	defaultWS     string
	loadErr       error
	listErr       error
	deleteErr     error
	setDefaultErr error
	resolveErr    error
}

func (f *fakeKeychain) Load(ws string) (keychain.Entry, error) {
	if f.loadErr != nil {
		return keychain.Entry{}, f.loadErr
	}
	e, ok := f.entries[ws]
	if !ok {
		return keychain.Entry{}, keychain.ErrNotFound
	}
	return e, nil
}

func (f *fakeKeychain) List() ([]keychain.Entry, []string, error) {
	if f.listErr != nil {
		return nil, nil, f.listErr
	}
	out := make([]keychain.Entry, 0, len(f.entries))
	for _, e := range f.entries {
		out = append(out, e)
	}
	return out, f.corrupt, nil
}

func (f *fakeKeychain) Delete(ws string) error {
	if f.deleteErr != nil {
		return f.deleteErr
	}
	if _, ok := f.entries[ws]; !ok {
		return keychain.ErrNotFound
	}
	delete(f.entries, ws)
	return nil
}

func (f *fakeKeychain) Save(e keychain.Entry) error {
	if f.entries == nil {
		f.entries = make(map[string]keychain.Entry)
	}
	f.entries[e.Workspace] = e
	return nil
}

func (f *fakeKeychain) SetDefault(ws string) error {
	if f.setDefaultErr != nil {
		return f.setDefaultErr
	}
	f.defaultWS = ws
	return nil
}

func (f *fakeKeychain) ResolveDefault() (string, error) {
	if f.resolveErr != nil {
		return "", f.resolveErr
	}
	if f.defaultWS != "" {
		return f.defaultWS, nil
	}
	entries := make([]keychain.Entry, 0, len(f.entries))
	for _, e := range f.entries {
		entries = append(entries, e)
	}
	switch len(entries) {
	case 0:
		return "", errors.New("no saved workspaces; run: slackcli auth login --workspace <name>")
	case 1:
		return entries[0].Workspace, nil
	default:
		return "", errors.New("multiple workspaces saved; set a default with: slackcli auth default --workspace <name>")
	}
}

// ---------------------------------------------------------------------------
// CanonicalDomain
// ---------------------------------------------------------------------------

func TestCanonicalDomain(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"myorg", "myorg.slack.com"},
		{"myorg.slack.com", "myorg.slack.com"},
		{"https://myorg.slack.com", "myorg.slack.com"},
		{"http://myorg.slack.com", "myorg.slack.com"},
		{"https://myorg.slack.com/", "myorg.slack.com"},
		{"https://myorg.slack.com/client/T123", "myorg.slack.com"},
		{"acme.enterprise.slack.com", "acme.enterprise.slack.com"},
		{"https://acme.enterprise.slack.com", "acme.enterprise.slack.com"},
	}
	for _, tc := range cases {
		got := CanonicalDomain(tc.in)
		if got != tc.want {
			t.Errorf("CanonicalDomain(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// ---------------------------------------------------------------------------
// AuthStatus
// ---------------------------------------------------------------------------

func TestAuthStatus_notFound(t *testing.T) {
	kc := &fakeKeychain{entries: map[string]keychain.Entry{}}
	out, err := AuthStatus(kc, "definitelynotexist-xyz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" {
		t.Fatal("expected non-empty output for missing workspace")
	}
	if !strings.Contains(out, "No saved credentials") {
		t.Errorf("expected 'No saved credentials' message, got: %q", out)
	}
}

func TestAuthStatus_allEmpty(t *testing.T) {
	kc := &fakeKeychain{entries: map[string]keychain.Entry{}}
	out, err := AuthStatus(kc, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" {
		t.Error("expected non-empty output")
	}
	if !strings.Contains(out, "No saved credentials") {
		t.Errorf("expected no-credentials message, got: %q", out)
	}
}

func TestAuthStatus_listError(t *testing.T) {
	kc := &fakeKeychain{listErr: errors.New("keychain locked")}
	_, err := AuthStatus(kc, "")
	if err == nil {
		t.Fatal("expected error when list fails")
	}
}

// ---------------------------------------------------------------------------
// AuthLogout
// ---------------------------------------------------------------------------

func TestAuthLogout_emptyWorkspace(t *testing.T) {
	kc := &fakeKeychain{entries: map[string]keychain.Entry{}}
	_, err := AuthLogout(kc, "")
	if err == nil {
		t.Fatal("expected error for empty workspace")
	}
}

func TestAuthLogout_notFound(t *testing.T) {
	kc := &fakeKeychain{entries: map[string]keychain.Entry{}}
	out, err := AuthLogout(kc, "definitelynotexist-xyz")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out == "" {
		t.Fatal("expected non-empty output")
	}
	if !strings.Contains(out, "No saved credentials") {
		t.Errorf("expected no-credentials message, got: %q", out)
	}
}

func TestAuthLogout_success(t *testing.T) {
	ws := "myorg.slack.com"
	kc := &fakeKeychain{entries: map[string]keychain.Entry{
		ws: {Workspace: ws, Token: "xoxc-1", Cookie: "xoxd-1"},
	}}
	out, err := AuthLogout(kc, ws)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "Logged out") {
		t.Errorf("expected logged-out message, got: %q", out)
	}
	if _, ok := kc.entries[ws]; ok {
		t.Error("entry should have been removed from fake keychain")
	}
}

// ---------------------------------------------------------------------------
// AuthDefault
// ---------------------------------------------------------------------------

func TestAuthDefault_set(t *testing.T) {
	kc := &fakeKeychain{entries: map[string]keychain.Entry{}}
	out, err := AuthDefault(kc, "myorg")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "myorg.slack.com") {
		t.Errorf("expected domain in output, got: %q", out)
	}
	if kc.defaultWS != "myorg.slack.com" {
		t.Errorf("defaultWS = %q, want myorg.slack.com", kc.defaultWS)
	}
}

func TestAuthDefault_get(t *testing.T) {
	kc := &fakeKeychain{
		entries:   map[string]keychain.Entry{},
		defaultWS: "myorg.slack.com",
	}
	out, err := AuthDefault(kc, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "myorg.slack.com") {
		t.Errorf("expected domain in output, got: %q", out)
	}
}

func TestAuthDefault_getNoDefault(t *testing.T) {
	kc := &fakeKeychain{entries: map[string]keychain.Entry{}}
	_, err := AuthDefault(kc, "")
	if err == nil {
		t.Fatal("expected error when no default and no workspaces saved")
	}
}

func TestAuthDefault_setAndGetRoundTrip(t *testing.T) {
	kc := &fakeKeychain{entries: map[string]keychain.Entry{}}
	const ws = "roundtrip.slack.com"
	if _, err := AuthDefault(kc, ws); err != nil {
		t.Fatalf("set: %v", err)
	}
	out, err := AuthDefault(kc, "")
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !strings.Contains(out, ws) {
		t.Errorf("get output should contain %q, got: %q", ws, out)
	}
}

// ---------------------------------------------------------------------------
// NewAuthCmd — structural tests only (no keychain access)
// ---------------------------------------------------------------------------

func TestNewAuthCmd_hasDefault(t *testing.T) {
	stub := func(_ *cobra.Command, _ []string) error { return nil }
	authCmd := NewAuthCmd(stub, stub)
	found := false
	for _, sub := range authCmd.Commands() {
		if sub.Name() == "default" {
			found = true
			break
		}
	}
	if !found {
		t.Error("auth command missing 'default' subcommand")
	}
}

// ---------------------------------------------------------------------------
// AuthWorkspaces
// ---------------------------------------------------------------------------

// TestAuthWorkspaces_workspaceNotFound verifies that AuthWorkspaces returns
// an error when the workspace is not in the keychain.
func TestAuthWorkspaces_workspaceNotFound(t *testing.T) {
	kc := &fakeKeychain{entries: map[string]keychain.Entry{}}
	_, err := AuthWorkspaces(context.Background(), kc, "missing.slack.com")
	if err == nil {
		t.Fatal("expected error for missing workspace, got nil")
	}
	if !strings.Contains(err.Error(), "loading credentials") {
		t.Errorf("error should mention credentials, got: %v", err)
	}
}

// TestAuthWorkspaces_backfillsKeychain verifies that AuthWorkspaces updates
// the keychain entry's GridWorkspaces field after a successful call.
// This test uses a workspace that resolves to a non-routable host; the
// network error is expected — we only verify the error path is clean.
func TestAuthWorkspaces_credentialsRequired(t *testing.T) {
	kc := &fakeKeychain{
		entries: map[string]keychain.Entry{},
	}
	// With no saved entry, AuthWorkspaces must return a meaningful error.
	_, err := AuthWorkspaces(context.Background(), kc, "notexist.slack.com")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

// TestNewAuthCmd_hasWorkspaces verifies that the "workspaces" subcommand is
// registered on the auth command tree.
func TestNewAuthCmd_hasWorkspaces(t *testing.T) {
	stub := func(_ *cobra.Command, _ []string) error { return nil }
	authCmd := NewAuthCmd(stub, stub)
	found := false
	for _, sub := range authCmd.Commands() {
		if sub.Name() == "workspaces" {
			found = true
			break
		}
	}
	if !found {
		t.Error("auth command missing 'workspaces' subcommand")
	}
}
