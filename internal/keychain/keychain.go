// Package keychain stores and retrieves slackcli credentials in the macOS
// system Keychain. Each workspace gets one generic-password item:
//
//	service  = "slackcli"
//	account  = workspace domain (e.g. "myorg.slack.com")
//	password = JSON-encoded Entry
//
// A second item tracks the set of known workspaces:
//
//	service  = "slackcli"
//	account  = "__index__"
//	password = JSON-encoded []string of workspace domains
//
// The macOS `security` CLI is used directly — no external Go dependencies.
// dump-keychain is intentionally avoided: it requires per-item approval
// prompts on modern macOS and its output format is unstable.
package keychain

import (
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

const (
	serviceName  = "slackcli"
	indexAccount = "__index__"
)

// Entry is the credential record stored for one workspace.
type Entry struct {
	Workspace      string    `json:"workspace"`
	Token          string    `json:"token"`
	Cookie         string    `json:"cookie"`
	SavedAt        time.Time `json:"saved_at"`
	EnterpriseID   string    `json:"enterprise_id,omitempty"`
	GridWorkspaces []string  `json:"grid_workspaces,omitempty"` // domains of sibling workspaces on Enterprise Grid
}

// ErrNotFound is returned when no credential exists for the requested workspace.
var ErrNotFound = errors.New("no credentials found for workspace")

// Save writes (or overwrites) the credential entry for the given workspace
// and registers it in the workspace index.
// workspace should be the canonical domain, e.g. "myorg.slack.com".
func Save(e Entry) error {
	if e.Workspace == "" {
		return errors.New("keychain.Save: workspace must not be empty")
	}
	if e.Token == "" || e.Cookie == "" {
		return errors.New("keychain.Save: token and cookie must not be empty")
	}
	if e.SavedAt.IsZero() {
		e.SavedAt = time.Now().UTC()
	}

	payload, err := json.Marshal(e)
	if err != nil {
		return fmt.Errorf("keychain.Save: marshal: %w", err)
	}

	// -U: update if already exists (idempotent).
	out, err := run("security", "add-generic-password",
		"-s", serviceName,
		"-a", e.Workspace,
		"-w", string(payload),
		"-U",
	)
	if err != nil {
		return fmt.Errorf("keychain.Save: %w: %s", err, out)
	}

	if err := indexAdd(e.Workspace); err != nil {
		return fmt.Errorf("keychain.Save: updating index: %w", err)
	}
	return nil
}

// Load retrieves the credential entry for the given workspace.
// Returns ErrNotFound if no item exists.
func Load(workspace string) (Entry, error) {
	if workspace == "" {
		return Entry{}, errors.New("keychain.Load: workspace must not be empty")
	}
	out, err := run("security", "find-generic-password",
		"-s", serviceName,
		"-a", workspace,
		"-w",
	)
	if err != nil {
		if isNotFound(out) {
			return Entry{}, ErrNotFound
		}
		return Entry{}, fmt.Errorf("keychain.Load: %w: %s", err, out)
	}

	var e Entry
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &e); err != nil {
		return Entry{}, fmt.Errorf("keychain.Load: corrupt entry for %q: %w", workspace, err)
	}
	return e, nil
}

// Delete removes the credential entry for the given workspace and removes it
// from the workspace index.
// Returns ErrNotFound if no item exists.
func Delete(workspace string) error {
	if workspace == "" {
		return errors.New("keychain.Delete: workspace must not be empty")
	}
	out, err := run("security", "delete-generic-password",
		"-s", serviceName,
		"-a", workspace,
	)
	if err != nil {
		if isNotFound(out) {
			return ErrNotFound
		}
		return fmt.Errorf("keychain.Delete: %w: %s", err, out)
	}

	if err := indexRemove(workspace); err != nil {
		return fmt.Errorf("keychain.Delete: updating index: %w", err)
	}
	return nil
}

// List returns all workspace entries saved under the slackcli service.
// Workspaces whose keychain item is missing or corrupt are returned in the
// second slice so callers can surface the issue.
//
// List reads the workspace index (a single keychain item) then calls Load
// for each workspace — no dump-keychain required.
func List() (entries []Entry, corrupt []string, err error) {
	workspaces, err := indexLoad()
	if err != nil {
		return nil, nil, fmt.Errorf("keychain.List: reading index: %w", err)
	}

	for _, ws := range workspaces {
		e, err := Load(ws)
		if errors.Is(err, ErrNotFound) {
			// Entry disappeared after being indexed — treat as corrupt so the
			// caller can tell the user to re-authenticate.
			corrupt = append(corrupt, ws)
			continue
		}
		if err != nil {
			corrupt = append(corrupt, ws)
			continue
		}
		entries = append(entries, e)
	}
	return entries, corrupt, nil
}

// ---------------------------------------------------------------------------
// Index helpers
// ---------------------------------------------------------------------------

// indexLoad reads the workspace index. Returns an empty slice if the index
// item does not yet exist.
func indexLoad() ([]string, error) {
	out, err := run("security", "find-generic-password",
		"-s", serviceName,
		"-a", indexAccount,
		"-w",
	)
	if err != nil {
		if isNotFound(out) {
			return nil, nil
		}
		return nil, fmt.Errorf("indexLoad: %w: %s", err, out)
	}

	var workspaces []string
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &workspaces); err != nil {
		// Corrupt index — return empty rather than failing List entirely.
		return nil, nil
	}
	return workspaces, nil
}

// indexSave writes the full workspace list back to the index item.
func indexSave(workspaces []string) error {
	payload, err := json.Marshal(workspaces)
	if err != nil {
		return fmt.Errorf("indexSave: marshal: %w", err)
	}
	out, err := run("security", "add-generic-password",
		"-s", serviceName,
		"-a", indexAccount,
		"-w", string(payload),
		"-U",
	)
	if err != nil {
		return fmt.Errorf("indexSave: %w: %s", err, out)
	}
	return nil
}

// indexAdd adds a workspace to the index if not already present.
func indexAdd(workspace string) error {
	workspaces, err := indexLoad()
	if err != nil {
		return err
	}
	for _, w := range workspaces {
		if w == workspace {
			return nil // already present
		}
	}
	return indexSave(append(workspaces, workspace))
}

// indexRemove removes a workspace from the index. No-op if not present.
func indexRemove(workspace string) error {
	workspaces, err := indexLoad()
	if err != nil {
		return err
	}
	filtered := workspaces[:0]
	for _, w := range workspaces {
		if w != workspace {
			filtered = append(filtered, w)
		}
	}
	if len(filtered) == len(workspaces) {
		return nil // not in index, nothing to do
	}
	return indexSave(filtered)
}

// ---------------------------------------------------------------------------
// Helpers
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// Preserved for test compatibility — no longer used in production code.
// ---------------------------------------------------------------------------

// parseKeychainDump is retained only to avoid breaking existing tests that
// exercise the parser directly. It is no longer called by List().
func parseKeychainDump(dump string) (entries []Entry, corrupt []string, err error) {
	var (
		inOurService bool
		currentAcct  string
	)

	for _, line := range strings.Split(dump, "\n") {
		line = strings.TrimSpace(line)

		if strings.HasPrefix(line, `"svce"`) {
			val := extractKeychainValue(line)
			inOurService = (val == serviceName)
			if !inOurService {
				currentAcct = ""
			}
			continue
		}
		if strings.HasPrefix(line, `"acct"`) {
			currentAcct = extractKeychainValue(line)
			continue
		}
		if strings.HasPrefix(line, "password:") && inOurService && currentAcct != "" {
			raw := extractPasswordValue(line)
			var e Entry
			if jErr := json.Unmarshal([]byte(raw), &e); jErr != nil {
				corrupt = append(corrupt, currentAcct)
			} else {
				entries = append(entries, e)
			}
			inOurService = false
			currentAcct = ""
		}
	}
	return entries, corrupt, nil
}

// extractKeychainValue pulls the value from a line like:
//
//	"svce"<blob>="slackcli"
func extractKeychainValue(line string) string {
	idx := strings.Index(line, `="`)
	if idx < 0 {
		return ""
	}
	val := line[idx+2:]
	val = strings.TrimSuffix(val, `"`)
	return val
}

// extractPasswordValue pulls the JSON from a password line like:
//
//	password: "{\"token\":\"…\"}"
func extractPasswordValue(line string) string {
	idx := strings.Index(line, `"`)
	if idx < 0 {
		return ""
	}
	val := line[idx+1:]
	val = strings.TrimSuffix(val, `"`)
	val = strings.ReplaceAll(val, `\"`, `"`)
	return val
}
