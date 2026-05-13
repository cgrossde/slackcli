package keychain

import (
	"errors"
	"testing"
)

// ---------------------------------------------------------------------------
// SetDefault / GetDefault round-trip (integration — requires macOS security CLI)
// ---------------------------------------------------------------------------

// TestDefaultRoundTrip exercises SetDefault and GetDefault against the real
// keychain. It saves the prior default (if any) and restores it on cleanup.
func TestDefaultRoundTrip(t *testing.T) {
	const testWS = "slackcli-test-default.slack.com"

	// Save existing default so cleanup can restore it.
	prior, priorErr := GetDefault()

	t.Cleanup(func() {
		if errors.Is(priorErr, ErrNotFound) {
			// Delete the item entirely — no default existed before.
			_, _ = run("security", "delete-generic-password",
				"-s", serviceName, "-a", defaultAccount)
		} else if priorErr == nil {
			// Restore whatever was there.
			_ = SetDefault(prior)
		}
	})

	// Set our test value.
	if err := SetDefault(testWS); err != nil {
		t.Fatalf("SetDefault: %v", err)
	}

	// Read it back.
	got, err := GetDefault()
	if err != nil {
		t.Fatalf("GetDefault: %v", err)
	}
	if got != testWS {
		t.Errorf("GetDefault() = %q, want %q", got, testWS)
	}

	// Overwrite — must be idempotent.
	const testWS2 = "slackcli-test-default2.slack.com"
	if err := SetDefault(testWS2); err != nil {
		t.Fatalf("SetDefault (overwrite): %v", err)
	}
	got2, err := GetDefault()
	if err != nil {
		t.Fatalf("GetDefault after overwrite: %v", err)
	}
	if got2 != testWS2 {
		t.Errorf("GetDefault() after overwrite = %q, want %q", got2, testWS2)
	}
}

// TestGetDefault_notFound verifies that GetDefault returns ErrNotFound when
// no default is stored. We temporarily delete the default item and restore it.
func TestGetDefault_notFound(t *testing.T) {
	prior, priorErr := GetDefault()

	// Remove the default for this test.
	_, _ = run("security", "delete-generic-password",
		"-s", serviceName, "-a", defaultAccount)

	t.Cleanup(func() {
		if priorErr == nil {
			_ = SetDefault(prior)
		}
	})

	_, err := GetDefault()
	if !errors.Is(err, ErrNotFound) {
		t.Errorf("expected ErrNotFound, got %v", err)
	}
}

// ---------------------------------------------------------------------------
// SetDefault validation
// ---------------------------------------------------------------------------

func TestSetDefault_emptyWorkspace(t *testing.T) {
	err := SetDefault("")
	if err == nil {
		t.Error("expected error for empty workspace, got nil")
	}
}
