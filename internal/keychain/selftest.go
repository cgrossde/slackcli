//go:build keychainselftest

package keychain

import (
	"fmt"
	"io"
)

// Throwaway services for manual signing experiments. They never match the
// real slackcli services, so the self-test cannot touch real credentials.
const (
	selfTestBound  = "slackcli-selftest.bound"
	selfTestLegacy = "slackcli-selftest"
)

// SelfTest drives the store selection and migration against throwaway
// services. It exists only in binaries built with -tags keychainselftest;
// release builds (goreleaser) never compile it.
//
//	mode                 report whether this binary counts as a signed release
//	put <account> <val>  store via the selected store
//	get <account>        read via the selected store (migrates legacy items)
//	bound-get <account>  read the bound item only, no migration
//	del <account>        delete via the selected store
func SelfTest(args []string, w io.Writer) error {
	s := selectStore(selfTestBound, selfTestLegacy)
	if len(args) == 0 {
		return fmt.Errorf("usage: mode | put <account> <value> | get <account> | bound-get <account> | del <account>")
	}
	switch {
	case args[0] == "mode":
		_, migrating := s.(migratingStore)
		_, err := fmt.Fprintf(w, "framework=%v release=%v store=%T\n", frameworkAvailable, migrating, s)
		return err
	case args[0] == "put" && len(args) == 3:
		return s.set(args[1], args[2])
	case args[0] == "get" && len(args) == 2:
		v, err := s.get(args[1])
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w, v)
		return err
	case args[0] == "bound-get" && len(args) == 2:
		v, err := frameworkStore{service: selfTestBound}.get(args[1])
		if err != nil {
			return err
		}
		_, err = fmt.Fprintln(w, v)
		return err
	case args[0] == "del" && len(args) == 2:
		return s.del(args[1])
	}
	return fmt.Errorf("unknown or malformed command %q", args)
}
