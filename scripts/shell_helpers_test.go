package scripts_test

import (
	"os/exec"
	"testing"
)

// newShellCmd builds a `sh -c <cmd>` invocation for tests that want
// to exercise rendered shell snippets (e.g. the systemd ExecStartPre
// chain after Makefile substitution). Returning the *exec.Cmd keeps
// callers free to set Dir / Env before Run().
func newShellCmd(t *testing.T, snippet string) *exec.Cmd {
	t.Helper()
	return exec.Command("sh", "-c", snippet)
}

// isExitError reports whether err is a non-zero process exit and
// returns the exit code. Tests use this to assert "the rendered
// command failed with code N" without matching on the error string.
func isExitError(err error) (int, bool) {
	if err == nil {
		return 0, false
	}
	if ee, ok := err.(*exec.ExitError); ok {
		return ee.ExitCode(), true
	}
	return -1, false
}