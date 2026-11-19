//go:build !darwin || !cgo

package keychain

import "errors"

// Without cgo there is no Security.framework binding: every build behaves
// like a development build and keeps using the `security` CLI.

const frameworkAvailable = false

var errNoFramework = errors.New("Security.framework binding not compiled in (build with CGO_ENABLED=1)")

type frameworkStore struct {
	service string
}

func (frameworkStore) get(string) (string, error) { return "", errNoFramework }
func (frameworkStore) set(string, string) error   { return errNoFramework }
func (frameworkStore) del(string) error           { return errNoFramework }

func selfSatisfies(string) bool { return false }
