// Package keychain — default.go manages the "default workspace" keychain item.
//
// The default is stored as a separate generic-password item:
//
//	service  = "slackcli"
//	account  = "__default__"
//	password = workspace domain (e.g. "myorg.slack.com")
//
// This is a plain string value, not JSON, matching the simplicity of the use
// case — one workspace domain, no metadata.
package keychain

import (
	"errors"
	"fmt"
	"strings"
)

const defaultAccount = "__default__"

// SetDefault stores workspace as the default workspace domain.
// workspace must be a canonical domain, e.g. "myorg.slack.com".
// Overwrites any previously stored default.
func SetDefault(workspace string) error {
	if workspace == "" {
		return errors.New("keychain.SetDefault: workspace must not be empty")
	}
	out, err := run("security", "add-generic-password",
		"-s", serviceName,
		"-a", defaultAccount,
		"-w", workspace,
		"-U",
	)
	if err != nil {
		return fmt.Errorf("keychain.SetDefault: %w: %s", err, out)
	}
	return nil
}

// DeleteDefault removes the stored default workspace. No-op if none is set.
func DeleteDefault() error {
	out, err := run("security", "delete-generic-password",
		"-s", serviceName,
		"-a", defaultAccount,
	)
	if err != nil {
		if isNotFound(out) {
			return nil
		}
		return fmt.Errorf("keychain.DeleteDefault: %w: %s", err, out)
	}
	return nil
}

// GetDefault returns the stored default workspace domain.
// Returns ErrNotFound if no default has been set.
func GetDefault() (string, error) {
	out, err := run("security", "find-generic-password",
		"-s", serviceName,
		"-a", defaultAccount,
		"-w",
	)
	if err != nil {
		if isNotFound(out) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("keychain.GetDefault: %w: %s", err, out)
	}
	return strings.TrimSpace(out), nil
}

// ResolveDefault implements the full default-workspace resolution order:
//  1. Stored default (GetDefault).
//  2. If exactly one workspace is saved, return it implicitly.
//  3. Otherwise error — ambiguous or empty.
//
// Callers that accept an explicit --workspace flag should check it first and
// only call ResolveDefault when the flag was not provided.
func ResolveDefault() (string, error) {
	// 1. Stored default.
	ws, err := GetDefault()
	if err == nil {
		return ws, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return "", err
	}

	// 2. Single saved workspace.
	entries, _, listErr := List()
	if listErr != nil {
		return "", fmt.Errorf("listing saved workspaces: %w", listErr)
	}
	switch len(entries) {
	case 0:
		return "", fmt.Errorf("no saved workspaces; run: slackcli auth login --workspace <name>")
	case 1:
		return entries[0].Workspace, nil
	default:
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Workspace
		}
		return "", fmt.Errorf(
			"multiple workspaces saved (%s); set a default with: slackcli auth default --workspace <name>",
			strings.Join(names, ", "),
		)
	}
}
