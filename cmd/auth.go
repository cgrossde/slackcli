// Package cmd implements slackcli subcommands (Layer 1: execution).
//
// Layer 1 functions return raw (stdout string, error). No truncation, no
// footer, no stderr attachment — that is Layer 2's job in main.go.
//
// Cobra commands in this package are thin adapters that call the Layer 1
// functions. The RunE for browser-dependent commands (login, reauth) is
// injected by main.go so signal handling and Playwright stay out of this
// package.
package cmd

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/cgrossde/slackcli/internal/keychain"
	"github.com/cgrossde/slackcli/internal/slack"
)

// keychainStore is the subset of keychain operations used by the auth command.
// The real keychain package satisfies this implicitly; tests supply a stub.
type keychainStore interface {
	Load(workspace string) (keychain.Entry, error)
	Save(e keychain.Entry) error
	List() ([]keychain.Entry, []string, error)
	Delete(workspace string) error
	SetDefault(workspace string) error
	ResolveDefault() (string, error)
}

// realKeychain is the production implementation that delegates directly to the
// keychain package.
type realKeychain struct{}

func (realKeychain) Load(ws string) (keychain.Entry, error)          { return keychain.Load(ws) }
func (realKeychain) Save(e keychain.Entry) error                     { return keychain.Save(e) }
func (realKeychain) List() ([]keychain.Entry, []string, error)       { return keychain.List() }
func (realKeychain) Delete(ws string) error                          { return keychain.Delete(ws) }
func (realKeychain) SetDefault(ws string) error                      { return keychain.SetDefault(ws) }
func (realKeychain) ResolveDefault() (string, error)                 { return keychain.ResolveDefault() }

// NewAuthCmd builds the "auth" Cobra command tree.
//
// loginRunE and reauthRunE are injected by main.go because they require
// Playwright and OS signal handling that do not belong in this package.
// They must write human-readable output to cmd.OutOrStdout() and return
// a non-nil error on failure (the error message becomes the [stderr] field
// in Layer 2).
func NewAuthCmd(
	loginRunE func(*cobra.Command, []string) error,
	reauthRunE func(*cobra.Command, []string) error,
) *cobra.Command {
	authCmd := &cobra.Command{
		Use:   "auth",
		Short: "Manage Slack authentication",
	}

	// login
	loginCmd := &cobra.Command{
		Use:   "login",
		Short: "Authenticate via browser and save credentials to Keychain",
		RunE:  loginRunE,
	}
	loginCmd.Flags().String("workspace", "", "Slack workspace name or URL (e.g. myorg or myorg.slack.com)")
	_ = loginCmd.MarkFlagRequired("workspace")

	// reauth
	reauthCmd := &cobra.Command{
		Use:   "reauth",
		Short: "Re-authenticate, clearing existing Keychain credentials first",
		RunE:  reauthRunE,
	}
	reauthCmd.Flags().String("workspace", "", "Slack workspace name or URL (e.g. myorg or myorg.slack.com)")
	_ = reauthCmd.MarkFlagRequired("workspace")

	// status
	statusCmd := &cobra.Command{
		Use:   "status",
		Short: "Show saved credentials and verify token validity",
		RunE: func(c *cobra.Command, _ []string) error {
			ws, _ := c.Flags().GetString("workspace")
			out, err := AuthStatus(realKeychain{}, ws)
			if out != "" {
				fmt.Fprint(c.OutOrStdout(), out)
			}
			return err
		},
	}
	statusCmd.Flags().String("workspace", "", "Workspace to check (omit to show all saved workspaces)")

	// logout
	logoutCmd := &cobra.Command{
		Use:   "logout",
		Short: "Remove saved credentials from Keychain",
		RunE: func(c *cobra.Command, _ []string) error {
			ws, _ := c.Flags().GetString("workspace")
			out, err := AuthLogout(realKeychain{}, ws)
			if out != "" {
				fmt.Fprint(c.OutOrStdout(), out)
			}
			return err
		},
	}
	logoutCmd.Flags().String("workspace", "", "Workspace to log out of (required)")
	_ = logoutCmd.MarkFlagRequired("workspace")

	// default
	defaultCmd := &cobra.Command{
		Use:   "default",
		Short: "Get or set the default workspace",
		Long: `Get or set the default workspace used when no --workspace flag is given.

Without --workspace: prints the currently resolved default workspace.
With    --workspace: stores it as the persistent default in the Keychain.

Resolution order (when --workspace is not provided to other commands):
  1. Stored default (set with this command)
  2. Single saved workspace (implicit when only one exists)
  3. Error — ambiguous; use --workspace or set a default`,
		RunE: func(c *cobra.Command, _ []string) error {
			ws, _ := c.Flags().GetString("workspace")
			out, err := AuthDefault(realKeychain{}, ws)
			if out != "" {
				fmt.Fprint(c.OutOrStdout(), out)
			}
			return err
		},
	}
	defaultCmd.Flags().String("workspace", "", "Workspace domain to set as default (e.g. myorg or myorg.slack.com)")

	// workspaces
	workspacesCmd := &cobra.Command{
		Use:   "workspaces",
		Short: "List all Enterprise Grid workspaces accessible with saved credentials",
		Long: `List all Enterprise Grid workspaces accessible with the saved credentials.

Calls client.userBoot to enumerate every workspace visible to the token,
updates the saved keychain entry with the results (backfill), and prints the
list. Useful after initial login to verify that grid metadata was captured.`,
		RunE: func(c *cobra.Command, _ []string) error {
			ws, _ := c.Flags().GetString("workspace")
			out, err := AuthWorkspaces(context.Background(), realKeychain{}, ws)
			if out != "" {
				fmt.Fprint(c.OutOrStdout(), out)
			}
			return err
		},
	}
	workspacesCmd.Flags().String("workspace", "", "Workspace to use for the token (omit to use the default)")

	authCmd.AddCommand(loginCmd, reauthCmd, statusCmd, logoutCmd, defaultCmd, workspacesCmd)
	return authCmd
}

// ---------------------------------------------------------------------------
// Layer 1 functions — pure logic, no Cobra, no I/O writers, fully testable.
// ---------------------------------------------------------------------------

// AuthStatus returns a human-readable status string for one workspace (if
// non-empty) or all saved workspaces. A non-nil error means a hard failure
// (keychain unreadable); a missing workspace is reported in the string, not
// as an error.
func AuthStatus(kc keychainStore, workspace string) (string, error) {
	if workspace != "" {
		ws := CanonicalDomain(workspace)
		e, err := kc.Load(ws)
		if errors.Is(err, keychain.ErrNotFound) {
			return fmt.Sprintf("No saved credentials for %q.\nRun: slackcli auth login --workspace %s\n", ws, ws), nil
		}
		if err != nil {
			return "", fmt.Errorf("reading keychain: %w", err)
		}
		return FormatEntryStatus(e), nil
	}

	entries, corrupt, err := kc.List()
	if err != nil {
		return "", fmt.Errorf("reading keychain: %w", err)
	}
	if len(entries) == 0 && len(corrupt) == 0 {
		return "No saved credentials. Run: slackcli auth login --workspace <name>\n", nil
	}

	var b strings.Builder
	for _, e := range entries {
		b.WriteString(FormatEntryStatus(e))
	}
	for _, ws := range corrupt {
		fmt.Fprintf(&b, "  %-40s  [corrupt keychain entry — run: slackcli auth reauth --workspace %s]\n", ws, ws)
	}
	return b.String(), nil
}

// AuthLogout removes saved credentials for the given workspace.
func AuthLogout(kc keychainStore, workspace string) (string, error) {
	if workspace == "" {
		return "", fmt.Errorf("workspace must not be empty")
	}
	ws := CanonicalDomain(workspace)
	err := kc.Delete(ws)
	if errors.Is(err, keychain.ErrNotFound) {
		return fmt.Sprintf("No saved credentials for %q\n", ws), nil
	}
	if err != nil {
		return "", fmt.Errorf("removing credentials: %w", err)
	}
	return fmt.Sprintf("Logged out of %q (credentials removed from Keychain)\n", ws), nil
}

// AuthDefault gets or sets the default workspace.
// If workspace is non-empty, it is canonicalised and stored as the default.
// If workspace is empty, the current resolved default is printed.
func AuthDefault(kc keychainStore, workspace string) (string, error) {
	if workspace != "" {
		ws := CanonicalDomain(workspace)
		if err := kc.SetDefault(ws); err != nil {
			return "", fmt.Errorf("setting default workspace: %w", err)
		}
		return fmt.Sprintf("Default workspace set to %q\n", ws), nil
	}

	// Print the resolved default.
	ws, err := kc.ResolveDefault()
	if err != nil {
		return "", fmt.Errorf("%w\nRun: slackcli auth default --workspace <name>", err)
	}
	return fmt.Sprintf("Default workspace: %s\n", ws), nil
}

// AuthWorkspaces lists all Enterprise Grid workspaces accessible with the
// saved credentials for workspace (or the default workspace if empty). It
// updates the keychain entry with the discovered domains (backfill) and
// returns a human-readable listing.
func AuthWorkspaces(ctx context.Context, kc keychainStore, workspace string) (string, error) {
	ws, err := resolveWorkspace(workspace)
	if err != nil {
		return "", err
	}
	entry, err := kc.Load(ws)
	if err != nil {
		return "", fmt.Errorf("loading credentials for %q: %w", ws, err)
	}

	client := slack.NewClient(entry.Token, entry.Cookie)
	grids, err := client.GridWorkspaces(ctx, ws)
	if err != nil {
		return "", fmt.Errorf("fetching grid workspaces: %w", err)
	}

	// Build the domain list (bare domain, no ".slack.com" suffix).
	domains := make([]string, len(grids))
	for i, g := range grids {
		domains[i] = g.Domain + ".slack.com"
	}

	// Back-fill the keychain entry.
	entry.GridWorkspaces = domains
	if entry.EnterpriseID == "" {
		// Best-effort: populate EnterpriseID from auth.test if not already set.
		if at, authErr := client.AuthTest(); authErr == nil && at.OK {
			entry.EnterpriseID = at.EnterpriseID
		}
	}
	_ = kc.Save(entry) // non-fatal; stale metadata is acceptable

	var b strings.Builder
	fmt.Fprintf(&b, "Grid workspaces for %s (%d total):\n", ws, len(grids))
	for _, g := range grids {
		fmt.Fprintf(&b, "  %s.slack.com  (id: %s)\n", g.Domain, g.ID)
	}
	return b.String(), nil
}

// FormatEntryStatus calls auth.test for one entry and returns a status line.
// Exported so main.go tests can inspect it directly.
func FormatEntryStatus(e keychain.Entry) string {
	client := slack.NewClient(e.Token, e.Cookie)
	result, err := client.AuthTest()
	age := time.Since(e.SavedAt).Round(time.Minute)
	if err != nil {
		return fmt.Sprintf("  %-40s  [network error: %v]\n", e.Workspace, err)
	}
	if !result.OK {
		return fmt.Sprintf(
			"  %-40s  EXPIRED  (error: %s, saved %s ago)\n    Run: slackcli auth reauth --workspace %s\n",
			e.Workspace, result.Error, age, e.Workspace,
		)
	}
	return fmt.Sprintf("  %-40s  OK  (user: %s, team: %s, saved %s ago)\n",
		e.Workspace, result.User, result.Team, age)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

// CanonicalDomain converts a workspace name or URL to a bare domain.
// "myorg" → "myorg.slack.com", "https://myorg.slack.com/..." → "myorg.slack.com"
func CanonicalDomain(workspace string) string {
	for _, pfx := range []string{"https://", "http://"} {
		workspace = strings.TrimPrefix(workspace, pfx)
	}
	if idx := strings.Index(workspace, "/"); idx >= 0 {
		workspace = workspace[:idx]
	}
	if !strings.HasSuffix(workspace, ".slack.com") {
		workspace += ".slack.com"
	}
	return workspace
}
