package output

import (
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestFormat_successNoStderr(t *testing.T) {
	t.Parallel()
	r := Result{Stdout: "hello\n", ExitCode: 0, Elapsed: 12 * time.Millisecond}
	got := Format(r)
	if !strings.Contains(got, "hello") {
		t.Errorf("missing stdout content: %q", got)
	}
	if !strings.Contains(got, "[exit:0 | 12ms]") {
		t.Errorf("missing footer: %q", got)
	}
	if strings.Contains(got, "[stderr]") {
		t.Errorf("unexpected stderr on success: %q", got)
	}
}

func TestFormat_failureNoStderrInStdout(t *testing.T) {
	t.Parallel()
	r := Result{
		Stdout:   "partial output\n",
		Stderr:   "bash: pip: command not found",
		ExitCode: 127,
		Elapsed:  3 * time.Millisecond,
	}
	got := Format(r)
	if !strings.Contains(got, "partial output") {
		t.Errorf("missing stdout: %q", got)
	}
	if strings.Contains(got, "bash: pip") {
		t.Errorf("stderr must not appear in Format stdout: %q", got)
	}
	if !strings.Contains(got, "[exit:127 | 3ms]") {
		t.Errorf("missing footer: %q", got)
	}
}

func TestPrint_failureWritesStderrToErrWriter(t *testing.T) {
	t.Parallel()
	r := Result{
		Stdout:   "partial output\n",
		Stderr:   "bash: pip: command not found",
		ExitCode: 127,
		Elapsed:  3 * time.Millisecond,
	}
	var outBuf, errBuf strings.Builder
	Print(&outBuf, &errBuf, r)
	if !strings.Contains(outBuf.String(), "partial output") {
		t.Errorf("missing stdout: %q", outBuf.String())
	}
	if strings.Contains(outBuf.String(), "bash: pip") {
		t.Errorf("stderr must not appear on stdout: %q", outBuf.String())
	}
	if !strings.Contains(errBuf.String(), "bash: pip: command not found") {
		t.Errorf("stderr must appear on errWriter: %q", errBuf.String())
	}
	if !strings.Contains(outBuf.String(), "[exit:127 | 3ms]") {
		t.Errorf("missing footer: %q", outBuf.String())
	}
}

func TestPrint_failureSilentStderrOmitted(t *testing.T) {
	t.Parallel()
	// Whitespace-only stderr must not be written to errWriter.
	r := Result{Stdout: "out\n", Stderr: "   \n", ExitCode: 1, Elapsed: time.Millisecond}
	var outBuf, errBuf strings.Builder
	Print(&outBuf, &errBuf, r)
	if errBuf.Len() != 0 {
		t.Errorf("unexpected stderr output for whitespace-only stderr: %q", errBuf.String())
	}
}

func TestFormat_footerDurations(t *testing.T) {
	t.Parallel()
	cases := []struct {
		d    time.Duration
		want string
	}{
		{500 * time.Microsecond, "500µs"},
		{12 * time.Millisecond, "12ms"},
		{1500 * time.Millisecond, "1.5s"},
		{62 * time.Second, "62.0s"},
	}
	for _, c := range cases {
		r := Result{ExitCode: 0, Elapsed: c.d}
		got := Format(r)
		if !strings.Contains(got, c.want) {
			t.Errorf("duration %v: want %q in output, got %q", c.d, c.want, got)
		}
	}
}

func TestOverflow_shortOutputPassedThrough(t *testing.T) {
	t.Parallel()
	stdout := strings.Repeat("line\n", 10)
	got := overflow(stdout)
	if got != stdout {
		t.Errorf("short output should pass through unchanged, got %q", got)
	}
}

func TestOverflow_longOutputTruncated(t *testing.T) {
	t.Parallel()
	// Build output with 500 lines.
	var sb strings.Builder
	for i := 0; i < 500; i++ {
		fmt.Fprintf(&sb, "line %d\n", i)
	}
	stdout := sb.String()

	got := overflow(stdout)

	// Should contain the truncation notice.
	if !strings.Contains(got, "--- output truncated") {
		t.Errorf("expected truncation notice, got: %q", got[:200])
	}
	// Should contain the first line but not line 201.
	if !strings.Contains(got, "line 0") {
		t.Errorf("expected first line in output")
	}
	if strings.Contains(got, "line 200\n") {
		t.Errorf("line 201 (0-indexed 200) should not appear in truncated output")
	}
	// Should include grep/tail hints.
	if !strings.Contains(got, "grep") {
		t.Errorf("expected grep hint in overflow notice")
	}
}
