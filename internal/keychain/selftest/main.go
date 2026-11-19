//go:build keychainselftest

// Command selftest exercises internal/keychain against throwaway Keychain
// services. Build it only for manual signing experiments:
//
//	go build -tags keychainselftest -o <dir>/slackcli ./internal/keychain/selftest
//
// The output must be named slackcli so its code-signing identifier matches
// the release requirement.
package main

import (
	"fmt"
	"os"

	"github.com/cgrossde/slackcli/internal/keychain"
)

func main() {
	if err := keychain.SelfTest(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
