package cmd

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/cgrossde/slackcli/internal/keychain"
	"github.com/cgrossde/slackcli/internal/slack"
)

// LoginFunc performs a browser-based Slack login for the given workspace and
// saves the credentials to the Keychain. An empty workspace is valid — the
// browser will navigate to the generic Slack login page and detect the
// workspace automatically.
type LoginFunc func(ctx context.Context, workspace string) error

type setupFlags struct {
	InstallSkill   bool
	UninstallSkill bool
	NoSkill        bool
}

// NewSetupCmd returns the `slackcli setup` command.
// out is real stdout — wizard output is visible immediately.
// loginFn is injected by main.go; it handles browser CDP and signal handling.
// skillContent is the //go:embed bytes from main.go.
func NewSetupCmd(out io.Writer, loginFn LoginFunc, skillContent []byte) *cobra.Command {
	var flags setupFlags

	c := &cobra.Command{
		Use:   "setup",
		Short: "First-time setup wizard (auth + Claude skill install)",
		Long: `Guided setup in two steps:
  1. Log in to Slack (skipped if credentials are already valid)
  2. Install the Claude/OpenCode skill`,
		Example: `  slackcli setup                   # full wizard
  slackcli setup --install-skill    # install (or update) the skill only
  slackcli setup --uninstall-skill  # remove the skill`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			ctx := context.Background()

			// Standalone skill flags short-circuit the wizard.
			if flags.UninstallSkill {
				return UninstallSkill(out)
			}
			if flags.InstallSkill {
				if len(skillContent) == 0 {
					return fmt.Errorf("no embedded skill content available")
				}
				return InstallSkill(out, skillContent)
			}

			fmt.Fprintln(out, "Welcome to slackcli — let's get you set up.")

			// ── Step 1: Auth ─────────────────────────────────────────────────
			setupDivider(out, "Step 1 of 2 — Slack Login")

			// Check for existing valid credentials.
			entries, _, _ := keychain.List()
			alreadyLoggedIn := false
			for _, e := range entries {
				if at, err := slack.NewClient(e.Token, e.Cookie).AuthTest(); err == nil && at.OK {
					fmt.Fprintf(out, "✓ Already logged in (%s, user: %s)\n", e.Workspace, at.User)
					alreadyLoggedIn = true
					break
				}
			}

			if !alreadyLoggedIn {
				fmt.Fprintln(out, "")
				fmt.Fprintln(out, "Enter your Slack workspace (e.g. myorg or myorg.slack.com):")
				workspace := setupPromptString(out, "> ")
				if workspace == "" {
					return fmt.Errorf("workspace is required")
				}
				fmt.Fprintln(out, "Opening browser for Slack login…")
				if err := loginFn(ctx, workspace); err != nil {
					return fmt.Errorf("login failed: %w", err)
				}
			}

			// ── Step 2: Skill install ─────────────────────────────────────────
			setupDivider(out, "Step 2 of 2 — Claude / OpenCode Skill")

			if flags.NoSkill || len(skillContent) == 0 {
				fmt.Fprintln(out, "Skill step skipped.")
			} else {
				if err := setupSkillInteractive(out, skillContent); err != nil {
					return err
				}
			}

			// ── Done ──────────────────────────────────────────────────────────
			setupDivider(out, "Done!")
			fmt.Fprintln(out, "Try:")
			fmt.Fprintln(out, "  slackcli auth status")
			fmt.Fprintln(out, "  slackcli activity")
			fmt.Fprintln(out, "  slackcli search \"hello\"")
			return nil
		},
	}

	c.Flags().BoolVar(&flags.InstallSkill, "install-skill", false, "Install the embedded skill to ~/.claude/skills/slackcli/SKILL.md")
	c.Flags().BoolVar(&flags.UninstallSkill, "uninstall-skill", false, "Remove the installed skill")
	c.Flags().BoolVar(&flags.NoSkill, "no-skill", false, "Skip skill installation")

	return c
}

// setupDivider prints a visual section divider.
func setupDivider(out io.Writer, title string) {
	fmt.Fprintf(out, "\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n%s\n━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━\n\n", title)
}

// setupSkillInteractive installs the embedded SKILL.md, prompting the user
// when the file is missing or outdated. Already-current installs are silent.
func setupSkillInteractive(out io.Writer, content []byte) error {
	home, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("cannot determine home directory: %w", err)
	}
	skillDir := filepath.Join(home, ".claude", "skills", "slackcli")
	skillPath := filepath.Join(skillDir, "SKILL.md")

	if existing, rerr := os.ReadFile(skillPath); rerr == nil {
		if bytes.Equal(existing, content) {
			fmt.Fprintf(out, "✓ Skill already up to date at %s\n", skillPath)
			return nil
		}
		fmt.Fprintf(out, "Skill exists at %s but is outdated.\n", skillPath)
	} else {
		fmt.Fprintf(out, "Install the slackcli skill for Claude/OpenCode?\nTarget: %s\n\n", skillPath)
	}

	if !setupPromptYesNo(out, "Install? [Y/n]: ", true) {
		fmt.Fprintln(out, "Skipped. Run: slackcli setup --install-skill   to install later.")
		return nil
	}

	if merr := os.MkdirAll(skillDir, 0o755); merr != nil {
		return fmt.Errorf("creating skill directory: %w", merr)
	}
	if werr := os.WriteFile(skillPath, content, 0o644); werr != nil {
		return fmt.Errorf("writing skill file: %w", werr)
	}
	fmt.Fprintln(out, "✓ Skill installed.")
	return nil
}

// setupPromptYesNo prints prompt to out and reads a y/n answer from stdin.
// Returns defaultVal when the user presses Enter without typing.
func setupPromptYesNo(out io.Writer, prompt string, defaultVal bool) bool {
	fmt.Fprint(out, prompt)
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return defaultVal
	}
	ans := strings.TrimSpace(strings.ToLower(scanner.Text()))
	switch ans {
	case "y", "yes":
		return true
	case "n", "no":
		return false
	default:
		return defaultVal
	}
}

// setupPromptString prints prompt to out and reads a line from stdin.
func setupPromptString(out io.Writer, prompt string) string {
	fmt.Fprint(out, prompt)
	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		return ""
	}
	return strings.TrimSpace(scanner.Text())
}
