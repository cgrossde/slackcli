package keychain

import (
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"testing"
)

// secretStore is the seam between the credential logic in keychain.go and
// the place secrets physically live. Each store holds one secret string per
// account under a fixed Keychain service.
type secretStore interface {
	get(account string) (string, error) // ErrNotFound when absent
	set(account, secret string) error
	del(account string) error // ErrNotFound when absent
}

const (
	// legacyService holds items written by /usr/bin/security. Development
	// builds keep using it; release builds only read it to migrate.
	legacyService = serviceName
	// boundService holds items created through Security.framework by the
	// signed release binary, trusted only by that binary's code identity.
	boundService = "slackcli.bound"
)

// teamID is the Apple Developer team whose Developer ID signs release
// builds. goreleaser injects it at link time
// (-X .../internal/keychain.teamID=...); it is empty in every other build,
// which then keeps the `security` CLI store.
var teamID string

// releaseRequirement matches only slackcli binaries signed with the project's
// Developer ID Application certificate. It is the condition under which a
// bound item stays readable without a prompt across updates.
func releaseRequirement() string {
	return `identifier "slackcli" and anchor apple generic` +
		` and certificate 1[field.1.2.840.113635.100.6.2.6]` +
		` and certificate leaf[field.1.2.840.113635.100.6.1.13]` +
		` and certificate leaf[subject.OU] = "` + teamID + `"`
}

// secrets returns the store for this process, chosen once.
var secrets = sync.OnceValue(func() secretStore {
	return selectStore(boundService, legacyService)
})

// selectStore binds items to the binary only for a Developer-ID-signed
// release build. Unsigned, ad-hoc signed (plain `go build`) and test binaries
// keep the `security` CLI so rebuilding never invalidates stored credentials
// and tests never create bound items.
func selectStore(bound, legacy string) secretStore {
	cli := cliStore{service: legacy}
	if !frameworkAvailable || teamID == "" || testing.Testing() || !selfSatisfies(releaseRequirement()) {
		return cli
	}
	return migratingStore{bound: frameworkStore{service: bound}, legacy: cli}
}

// migratingStore reads and writes the bound store, and moves an account's
// legacy item into it the first time that account is missed. Reading a
// legacy item through `security` is silent (it created the item); the copy is
// then created by this binary, and the legacy item is deleted so the secret is
// no longer readable by other programs without a prompt.
type migratingStore struct {
	bound  secretStore
	legacy secretStore
}

func (m migratingStore) get(account string) (string, error) {
	secret, err := m.bound.get(account)
	if !errors.Is(err, ErrNotFound) {
		return secret, err
	}
	secret, err = m.legacy.get(account)
	if err != nil {
		return "", err // ErrNotFound when neither store has it
	}
	if err := m.bound.set(account, secret); err != nil {
		return "", fmt.Errorf("migrating %q to binary-bound keychain item: %w", account, err)
	}
	if err := m.legacy.del(account); err != nil && !errors.Is(err, ErrNotFound) {
		slog.Warn("keychain: migrated item but could not delete legacy copy", "account", account, "err", err)
	}
	return secret, nil
}

func (m migratingStore) set(account, secret string) error {
	if err := m.bound.set(account, secret); err != nil {
		return err
	}
	// A legacy item can reappear after a downgrade; drop it on the next write.
	if err := m.legacy.del(account); err != nil && !errors.Is(err, ErrNotFound) {
		slog.Warn("keychain: could not delete legacy item", "account", account, "err", err)
	}
	return nil
}

func (m migratingStore) del(account string) error {
	boundErr := m.bound.del(account)
	legacyErr := m.legacy.del(account)
	switch {
	case boundErr != nil && !errors.Is(boundErr, ErrNotFound):
		return boundErr
	case legacyErr != nil && !errors.Is(legacyErr, ErrNotFound):
		return legacyErr
	case boundErr != nil && legacyErr != nil:
		return ErrNotFound
	}
	return nil
}
