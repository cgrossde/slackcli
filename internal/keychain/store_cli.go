package keychain

import (
	"encoding/hex"
	"fmt"
	"os/exec"
	"strings"
	"unicode/utf8"
)

// cliStore keeps secrets as generic-password items written by the macOS
// `security` CLI. Those items trust /usr/bin/security, so any process running
// as the user can read them without a prompt; release builds only use this
// store to migrate existing items.
type cliStore struct {
	service string
}

func (c cliStore) get(account string) (string, error) {
	out, err := run("security", "find-generic-password", "-s", c.service, "-a", account, "-w")
	if err != nil {
		if isNotFound(out) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("%w: %s", err, out)
	}
	return decodeSecurityOutput(strings.TrimSuffix(out, "\n")), nil
}

func (c cliStore) set(account, secret string) error {
	// -U: update if already exists (idempotent).
	out, err := run("security", "add-generic-password", "-s", c.service, "-a", account, "-w", secret, "-U")
	if err != nil {
		return fmt.Errorf("%w: %s", err, out)
	}
	return nil
}

func (c cliStore) del(account string) error {
	out, err := run("security", "delete-generic-password", "-s", c.service, "-a", account)
	if err != nil {
		if isNotFound(out) {
			return ErrNotFound
		}
		return fmt.Errorf("%w: %s", err, out)
	}
	return nil
}

// decodeSecurityOutput undoes `security -w`'s habit of printing a secret as
// bare hex when it contains non-ASCII bytes (e.g. a display name "Müller").
// Only hex that decodes to valid UTF-8 containing non-ASCII text is decoded,
// so plain ASCII secrets that happen to look like hex are left alone.
func decodeSecurityOutput(out string) string {
	if len(out) == 0 || len(out)%2 != 0 {
		return out
	}
	raw, err := hex.DecodeString(out)
	if err != nil || !utf8.Valid(raw) {
		return out
	}
	for _, b := range raw {
		if b >= utf8.RuneSelf {
			return string(raw)
		}
	}
	return out
}

// isNotFound reports whether a `security` command output indicates the item
// was not found (exit code 44).
func isNotFound(out string) bool {
	return strings.Contains(out, "could not be found") ||
		strings.Contains(out, "The specified item could not be found")
}

// run executes a command and returns combined stdout+stderr and any error.
func run(name string, args ...string) (string, error) {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return string(out), fmt.Errorf("%s: %w", name, err)
	}
	return string(out), nil
}
