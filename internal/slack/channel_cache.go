// Package slack — channel_cache.go provides a persistent cache that maps
// Slack channel IDs to the workspace domain where they were found.
//
// The cache lives at ~/.cache/slackcli/channels.json and is shared across
// all workspaces: channel IDs are globally unique within an Enterprise Grid,
// so a single flat map is correct.
//
// Reads are safe for concurrent use; writes acquire a mutex before flushing.
package slack

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// ChannelCache maps channel IDs to the workspace domain (e.g. "myorg.slack.com")
// where they were last successfully accessed.
type ChannelCache struct {
	mu       sync.Mutex
	path     string
	channels map[string]string // channelID → workspace domain
}

// LoadChannelCache opens the cache at the default location
// (~/.cache/slackcli/channels.json), creating the file and its parent
// directory if they do not yet exist.  A missing or empty file is treated as
// an empty cache — not an error.
func LoadChannelCache() (*ChannelCache, error) {
	dir, err := cacheDir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "channels.json")
	cc := &ChannelCache{path: path, channels: make(map[string]string)}
	if err := cc.load(); err != nil {
		return nil, err
	}
	return cc, nil
}

// Get returns the workspace domain for channelID and true, or ("", false) on
// a cache miss.  Safe to call concurrently.
func (cc *ChannelCache) Get(channelID string) (workspace string, ok bool) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	ws, ok := cc.channels[channelID]
	return ws, ok
}

// Set records that channelID belongs to workspace and flushes the cache to
// disk.  A flush error is silently ignored — the in-memory entry is still
// usable for the lifetime of the process.
func (cc *ChannelCache) Set(channelID, workspace string) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	cc.channels[channelID] = workspace
	_ = cc.flush()
}

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

func (cc *ChannelCache) load() error {
	data, err := os.ReadFile(cc.path)
	if os.IsNotExist(err) {
		return nil // empty cache is fine
	}
	if err != nil {
		return err
	}
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, &cc.channels)
}

// flush writes cc.channels to disk.  Must be called with cc.mu held.
func (cc *ChannelCache) flush() error {
	data, err := json.Marshal(cc.channels)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cc.path), 0o700); err != nil {
		return err
	}
	return os.WriteFile(cc.path, data, 0o600)
}

// cacheDir returns ~/.cache/slackcli, creating it if needed.
func cacheDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(base, "slackcli")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", err
	}
	return dir, nil
}
