package slack

import (
	"path/filepath"
	"testing"
)

// newCacheAt creates a ChannelCache pointed at the given path.
// This is a test helper that bypasses LoadChannelCache() and the real cache dir.
func newCacheAt(path string) *ChannelCache {
	return &ChannelCache{path: path, channels: make(map[string]string)}
}

// TestChannelCache_GetMiss verifies that Get returns false and empty string on a cache miss.
func TestChannelCache_GetMiss(t *testing.T) {
	tempDir := t.TempDir()
	cc := newCacheAt(filepath.Join(tempDir, "channels.json"))

	ws, ok := cc.Get("CNOBODY")
	if ok {
		t.Errorf("expected ok=false, got ok=true")
	}
	if ws != "" {
		t.Errorf("expected empty workspace on miss, got %q", ws)
	}
}

// TestChannelCache_SetAndGet verifies that Set stores a key and Get retrieves it.
func TestChannelCache_SetAndGet(t *testing.T) {
	tempDir := t.TempDir()
	cc := newCacheAt(filepath.Join(tempDir, "channels.json"))

	cc.Set("C123", "myorg.slack.com")

	ws, ok := cc.Get("C123")
	if !ok {
		t.Errorf("expected ok=true, got ok=false")
	}
	if ws != "myorg.slack.com" {
		t.Errorf("expected workspace=%q, got %q", "myorg.slack.com", ws)
	}
}

// TestChannelCache_PersistsAcrossLoad verifies that data persists on disk
// and is re-loaded when creating a new cache from the same path.
func TestChannelCache_PersistsAcrossLoad(t *testing.T) {
	tempDir := t.TempDir()
	cachePath := filepath.Join(tempDir, "channels.json")

	// Set a key in the first cache instance.
	cc1 := newCacheAt(cachePath)
	cc1.Set("C123", "myorg.slack.com")

	// Create a new cache instance from the same path and verify the key persists.
	cc2 := newCacheAt(cachePath)
	if err := cc2.load(); err != nil {
		t.Fatalf("failed to load cache: %v", err)
	}

	ws, ok := cc2.Get("C123")
	if !ok {
		t.Errorf("expected ok=true on reload, got ok=false")
	}
	if ws != "myorg.slack.com" {
		t.Errorf("expected workspace=%q after reload, got %q", "myorg.slack.com", ws)
	}
}

// TestChannelCache_DifferentKeys verifies that multiple keys can be set and retrieved independently.
func TestChannelCache_DifferentKeys(t *testing.T) {
	tempDir := t.TempDir()
	cc := newCacheAt(filepath.Join(tempDir, "channels.json"))

	cc.Set("C123", "org1.slack.com")
	cc.Set("C456", "org2.slack.com")
	cc.Set("C789", "org3.slack.com")

	tests := []struct {
		channelID string
		expected  string
	}{
		{"C123", "org1.slack.com"},
		{"C456", "org2.slack.com"},
		{"C789", "org3.slack.com"},
	}

	for _, tt := range tests {
		ws, ok := cc.Get(tt.channelID)
		if !ok {
			t.Errorf("expected ok=true for %s, got ok=false", tt.channelID)
		}
		if ws != tt.expected {
			t.Errorf("expected workspace=%q for %s, got %q", tt.expected, tt.channelID, ws)
		}
	}

	// Also verify a cache miss for an unset key.
	_, ok := cc.Get("CNEVER")
	if ok {
		t.Errorf("expected ok=false for unset key, got ok=true")
	}
}
