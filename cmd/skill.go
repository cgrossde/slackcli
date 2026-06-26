package cmd

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
)

// skillTargetPath returns the canonical ~/.claude/skills/slackcli/SKILL.md path.
func skillTargetPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}
	return filepath.Join(home, ".claude", "skills", "slackcli", "SKILL.md"), nil
}

// InstallSkill writes the embedded SKILL.md to its target path,
// reporting whether the install was new, an update, or already current.
func InstallSkill(out io.Writer, content []byte) error {
	path, err := skillTargetPath()
	if err != nil {
		return err
	}
	if existing, rerr := os.ReadFile(path); rerr == nil {
		if bytes.Equal(existing, content) {
			fmt.Fprintf(out, "✓ skill already up to date at %s\n", path)
			return nil
		}
		if err := os.WriteFile(path, content, 0o644); err != nil {
			return fmt.Errorf("updating skill: %w", err)
		}
		fmt.Fprintf(out, "✓ skill updated at %s\n", path)
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("creating skill directory: %w", err)
	}
	if err := os.WriteFile(path, content, 0o644); err != nil {
		return fmt.Errorf("writing skill: %w", err)
	}
	fmt.Fprintf(out, "✓ skill installed at %s\n", path)
	return nil
}

// UninstallSkill removes the installed SKILL.md, reporting whether it existed.
func UninstallSkill(out io.Writer) error {
	path, err := skillTargetPath()
	if err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		if os.IsNotExist(err) {
			fmt.Fprintf(out, "skill not installed (no file at %s)\n", path)
			return nil
		}
		return fmt.Errorf("removing skill: %w", err)
	}
	fmt.Fprintf(out, "✓ skill removed from %s\n", path)
	return nil
}
