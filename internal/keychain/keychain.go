// Package keychain stores and retrieves slackcli credentials in the macOS
// system Keychain. Each workspace gets one generic-password item:
//
//	service  = "slackcli.bound" (signed release) or "slackcli" (dev builds, legacy)
//	account  = workspace domain (e.g. "myorg.slack.com")
//	password = JSON-encoded Entry
//
// A second item tracks the set of known workspaces:
//
//	service  = same as above
//	account  = "__index__"
//	password = JSON-encoded []string of workspace domains
//
// Where the items live is decided once per process by secrets() in store.go:
// signed release builds keep them under a service bound to the slackcli
// binary's code identity (migrating legacy items on first read); development
// builds and tests use the `security` CLI under the legacy service.
// dump-keychain is intentionally avoided: it requires per-item approval
// prompts on modern macOS and its output format is unstable.
package keychain

import (
	"encoding/json"
	"errors"
	"fmt"
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
	TeamID         string    `json:"team_id,omitempty"`         // workspace team ID (T…); used to build slack:// deep links
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

	if err := secrets().set(e.Workspace, string(payload)); err != nil {
		return fmt.Errorf("keychain.Save: %w", err)
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
	out, err := secrets().get(workspace)
	if errors.Is(err, ErrNotFound) {
		return Entry{}, ErrNotFound
	}
	if err != nil {
		return Entry{}, fmt.Errorf("keychain.Load: %w", err)
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
	err := secrets().del(workspace)
	if errors.Is(err, ErrNotFound) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("keychain.Delete: %w", err)
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
	out, err := secrets().get(indexAccount)
	if errors.Is(err, ErrNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("indexLoad: %w", err)
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
	if err := secrets().set(indexAccount, string(payload)); err != nil {
		return fmt.Errorf("indexSave: %w", err)
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
