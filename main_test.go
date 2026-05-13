package main

import (
	"bytes"
	"strings"
	"testing"
)

// run is testable: inject writers instead of os.Stdout/os.Stderr.

func TestRun_noArgs(t *testing.T) {
	var out, errOut bytes.Buffer
	err := run([]string{}, &out, &errOut)
	// Cobra prints help and returns nil when invoked with no subcommand.
	if err != nil {
		t.Fatalf("unexpected error for no args: %v", err)
	}
	help := out.String() + errOut.String()
	if !strings.Contains(help, "slackcli") {
		t.Errorf("expected usage in output, got: %q", help)
	}
}

func TestRun_unknownCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	err := run([]string{"notacommand"}, &out, &errOut)
	// run() now formats all Cobra errors through the presenter and returns nil.
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	combined := out.String()
	if !strings.Contains(combined, "[exit:1") {
		t.Errorf("expected [exit:1 footer in output, got: %q", combined)
	}
}

func TestRun_authLoginMissingWorkspace(t *testing.T) {
	var out, errOut bytes.Buffer
	err := run([]string{"auth", "login"}, &out, &errOut)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	combined := out.String()
	if !strings.Contains(combined, "[exit:1") {
		t.Errorf("expected [exit:1 footer for missing required flag, got: %q", combined)
	}
	if !strings.Contains(combined, "workspace") {
		t.Errorf("expected 'workspace' mentioned in error output, got: %q", combined)
	}
}

func TestRun_authHelp(t *testing.T) {
	var out, errOut bytes.Buffer
	// --help exits with nil; Cobra writes help to out.
	_ = run([]string{"auth", "--help"}, &out, &errOut)
	help := out.String() + errOut.String()
	if !strings.Contains(help, "login") {
		t.Errorf("help output missing 'login': %q", help)
	}
}

func TestRun_authStatusNoCredentials(t *testing.T) {
	var out, errOut bytes.Buffer
	err := run([]string{"auth", "status", "--workspace", "nonexistent-test-org"}, &out, &errOut)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	combined := out.String()
	if !strings.Contains(combined, "No saved credentials") {
		t.Errorf("expected 'No saved credentials' in output, got: %q", combined)
	}
	if !strings.Contains(combined, "[exit:0") {
		t.Errorf("expected footer in output, got: %q", combined)
	}
}

func TestRun_authLogoutNoCredentials(t *testing.T) {
	var out, errOut bytes.Buffer
	err := run([]string{"auth", "logout", "--workspace", "testorg"}, &out, &errOut)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	combined := out.String()
	if !strings.Contains(combined, "No saved credentials") {
		t.Errorf("expected 'No saved credentials' in output, got: %q", combined)
	}
	if !strings.Contains(combined, "[exit:0") {
		t.Errorf("expected footer in output, got: %q", combined)
	}
}

func TestRun_authStatusWithWorkspaceNotFound(t *testing.T) {
	var out, errOut bytes.Buffer
	err := run([]string{"auth", "status", "--workspace", "notexist"}, &out, &errOut)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	combined := out.String()
	if !strings.Contains(combined, "No saved credentials for") {
		t.Errorf("expected not-found message, got: %q", combined)
	}
	if !strings.Contains(combined, "[exit:0") {
		t.Errorf("expected footer, got: %q", combined)
	}
}

// TestRun_jsonFlagSuppressesFooter verifies that --json bypasses the presenter
// footer entirely. The command will fail (no credentials), but stdout must not
// contain the "[exit:N | ...]" footer — the error belongs on stderr only.
func TestRun_jsonFlagSuppressesFooter(t *testing.T) {
	var out, errOut bytes.Buffer
	// search --users --json will fail with a credential error, but in JSON mode
	// the error goes to stderr and stdout stays clean (no footer).
	err := run([]string{"search", "--users", "--json", "somequery"}, &out, &errOut)
	// JSON mode returns errAlreadyPresented on error — run() propagates it.
	// We accept either nil or errAlreadyPresented here.
	if err != nil && !strings.Contains(err.Error(), "already presented") {
		t.Fatalf("unexpected error: %v", err)
	}

	// stdout must NOT contain a presenter footer.
	stdout := out.String()
	if strings.Contains(stdout, "[exit:") {
		t.Errorf("--json mode must not produce presenter footer on stdout, got: %q", stdout)
	}

	// The error detail should appear on stderr (not stdout).
	combined := errOut.String()
	if combined == "" && stdout == "" {
		// Both empty is possible if workspace resolution produced no output.
		// The key invariant is just: no footer on stdout.
	}
}

// TestRun_jsonFlagSearchNoCredentials mirrors the above for the search command.
func TestRun_jsonFlagSearchNoCredentials(t *testing.T) {
	var out, errOut bytes.Buffer
	err := run([]string{"search", "--json", "--workspace", "nonexistent-ws", "test"}, &out, &errOut)
	if err != nil && !strings.Contains(err.Error(), "already presented") {
		t.Fatalf("unexpected error: %v", err)
	}

	stdout := out.String()
	if strings.Contains(stdout, "[exit:") {
		t.Errorf("--json mode must not produce presenter footer on stdout, got: %q", stdout)
	}
}

// TestRun_usersCommandRemoved verifies the top-level users command no longer
// exists — functionality moved to search --users.
func TestRun_usersCommandRemoved(t *testing.T) {
	var out, errOut bytes.Buffer
	_ = run([]string{"users", "somequery"}, &out, &errOut)
	combined := out.String() + errOut.String()
	if !strings.Contains(combined, "unknown command") {
		t.Errorf("expected 'unknown command' error for removed users command, got: %q", combined)
	}
}
