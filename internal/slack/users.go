// Package slack — users.go implements users.info with a persistent filesystem
// cache so each user ID is fetched at most once per workspace across runs.
//
// Cache location: ~/.cache/slackcli/users-<workspace>.json
// Format: JSON object mapping user ID → CachedUser.
package slack

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	slackgo "github.com/slack-go/slack"
)

// CachedUser holds the subset of Slack user fields we care about for display.
type CachedUser struct {
	ID          string `json:"id"`
	Name        string `json:"name"`         // Slack handle / employee ID, e.g. "u123456"
	DisplayName string `json:"display_name"` // human name shown in the UI, e.g. "Alice Johnson"
	Email       string `json:"email"`        // profile email, e.g. "alice.johnson@example.com"
}

// Label returns "DisplayName (Name)" when DisplayName is set, else falls back
// through Name then ID so callers always get a non-empty string.
func (u CachedUser) Label() string {
	switch {
	case u.DisplayName != "":
		return fmt.Sprintf("%s (%s)", u.DisplayName, u.Name)
	case u.Name != "":
		return u.Name
	default:
		return u.ID
	}
}
// ShortLabel returns just the display name for compact contexts (mentions).
// Falls back through Name then ID so callers always get a non-empty string.
func (u CachedUser) ShortLabel() string {
	switch {
	case u.DisplayName != "":
		return u.DisplayName
	case u.Name != "":
		return u.Name
	default:
		return u.ID
	}
}

// UserCache is a per-workspace, filesystem-backed cache of Slack user info.
// Load it once per command invocation with NewUserCache; it reads the on-disk
// JSON and flushes any new entries back on the first call to GetUser that
// triggers a network fetch.
type UserCache struct {
	workspace string // e.g. "myorg.slack.com"
	path      string // resolved cache file path
	users     map[string]CachedUser
	client    *Client
}

// NewUserCache loads (or creates) the cache file for workspace and returns a
// ready-to-use UserCache. client is used for cache misses.
func NewUserCache(workspace string, client *Client) (*UserCache, error) {
	p, err := cacheFilePath(workspace)
	if err != nil {
		return nil, err
	}
	uc := &UserCache{
		workspace: workspace,
		path:      p,
		users:     make(map[string]CachedUser),
		client:    client,
	}
	if err := uc.load(); err != nil {
		return nil, err
	}
	return uc, nil
}

// GetUser returns display info for id. On a cache miss it fetches users.info,
// stores the result, and flushes the cache file.
func (uc *UserCache) GetUser(id string) (CachedUser, error) {
	if u, ok := uc.users[id]; ok {
		return u, nil
	}
	u, err := uc.fetch(id)
	if err != nil {
		return CachedUser{ID: id}, err
	}
	uc.users[id] = u
	// Best-effort flush; a write failure is not fatal for the caller.
	_ = uc.flush()
	return u, nil
}

// ResolveUserMentions replaces all <@WXXXX> and <@UXXXX> patterns in text
// with <@DisplayName (Name)>. Unknown IDs are left unchanged.
func (uc *UserCache) ResolveUserMentions(text string) string {
	return mentionRe.ReplaceAllStringFunc(text, func(match string) string {
		id := match[2 : len(match)-1] // strip <@ and >
		u, err := uc.GetUser(id)
		if err != nil {
			return match
		}
		return "<@" + u.Label() + ">"
	})
}

// FindByName returns the CachedUser whose Name (Slack handle / employee ID)
// matches name case-insensitively. Only the in-memory cache is searched — no
// network call is made. Returns false when not found.
func (uc *UserCache) FindByName(name string) (CachedUser, bool) {
	lower := strings.ToLower(name)
	for _, u := range uc.users {
		if strings.ToLower(u.Name) == lower {
			return u, true
		}
	}
	return CachedUser{}, false
}

// Search returns all cached users whose Name, DisplayName, or Email contain
// query as a case-insensitive substring. Only the in-memory cache is searched
// — no network call is made. The slice is sorted by DisplayName then Name.
func (uc *UserCache) Search(query string) []CachedUser {
	lower := strings.ToLower(query)
	var matches []CachedUser
	for _, u := range uc.users {
		if strings.Contains(strings.ToLower(u.Name), lower) ||
			strings.Contains(strings.ToLower(u.DisplayName), lower) ||
			strings.Contains(strings.ToLower(u.Email), lower) {
			matches = append(matches, u)
		}
	}
	sort.Slice(matches, func(i, j int) bool {
		di, dj := matches[i].DisplayName, matches[j].DisplayName
		if di != dj {
			return di < dj
		}
		return matches[i].Name < matches[j].Name
	})
	return matches
}

// mentionRe matches bare user-ID mentions produced by Slack: <@W…> or <@U…>.
var mentionRe = regexp.MustCompile(`<@[WU][A-Z0-9]+>`)

// -------------------------------------------------------------------
// internal helpers
// -------------------------------------------------------------------

func (uc *UserCache) fetch(id string) (CachedUser, error) {
	if uc.client == nil {
		return CachedUser{}, fmt.Errorf("users.info %s: no client available", id)
	}
	info, err := uc.client.api.GetUserInfo(id)
	if err != nil {
		return CachedUser{}, fmt.Errorf("users.info %s: %w", id, err)
	}
	return fromSlackUser(info), nil
}

func fromSlackUser(u *slackgo.User) CachedUser {
	dn := u.Profile.DisplayName
	if dn == "" {
		dn = u.Profile.RealName
	}
	return CachedUser{
		ID:          u.ID,
		Name:        u.Name,
		DisplayName: dn,
		Email:       u.Profile.Email,
	}
}

func (uc *UserCache) load() error {
	data, err := os.ReadFile(uc.path)
	if os.IsNotExist(err) {
		return nil // empty cache is fine
	}
	if err != nil {
		return fmt.Errorf("read user cache %s: %w", uc.path, err)
	}
	if err := json.Unmarshal(data, &uc.users); err != nil {
		// Corrupt cache — start fresh rather than hard-failing.
		uc.users = make(map[string]CachedUser)
	}
	return nil
}

func (uc *UserCache) flush() error {
	data, err := json.MarshalIndent(uc.users, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(uc.path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(uc.path, data, 0o600)
}

func cacheFilePath(workspace string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home dir: %w", err)
	}
	// Sanitise workspace into a safe filename component.
	safe := strings.NewReplacer("/", "_", ":", "_", " ", "_").Replace(workspace)
	return filepath.Join(home, ".cache", "slackcli", "users-"+safe+".json"), nil
}

// NewUserCacheFromMap constructs a UserCache pre-populated with the given
// entries. No file I/O is performed and cache misses return an error rather
// than hitting the network (client is nil). Intended for tests.
func NewUserCacheFromMap(workspace string, users map[string]CachedUser) *UserCache {
	return &UserCache{
		workspace: workspace,
		users:     users,
	}
}
