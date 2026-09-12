package scripts_test

import (
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// renderSystemdUnit reads deploy/systemd/sourcerudder.service from the
// worktree root, substitutes @VERSION@/@IMAGE@ with the supplied sample
// values, and returns the rendered unit as a string. The function is
// shared by the systemd identity tests so each case exercises the same
// substitution path operators run through `make release-image`.
func renderSystemdUnit(t *testing.T, version, image string) string {
	t.Helper()
	unit, err := os.ReadFile(filepath.Join("..", "deploy", "systemd", "sourcerudder.service"))
	if err != nil {
		t.Fatalf("read systemd unit: %v", err)
	}
	rendered := string(unit)
	rendered = strings.ReplaceAll(rendered, "@VERSION@", version)
	rendered = strings.ReplaceAll(rendered, "@IMAGE@", image)
	return rendered
}

func TestSystemdTemplateBindsHTTPToLoopback(t *testing.T) {
	rendered := renderSystemdUnit(t, "3.0.0", "example.invalid/image@sha256:digest")
	var execStart string
	for _, line := range strings.Split(rendered, "\n") {
		if strings.HasPrefix(line, "ExecStart=") {
			execStart = line
			break
		}
	}
	if execStart == "" {
		t.Fatal("rendered systemd unit has no ExecStart")
	}
	if !strings.Contains(execStart, "-http-addr 127.0.0.1:8080") {
		t.Fatalf("systemd HTTP listener is not loopback-bound: %s", execStart)
	}
}

// extractExecStartPre returns every ExecStartPre=... line in the
// rendered systemd unit so identity assertions target the actual
// pre-flight commands and not unrelated prose.
func extractExecStartPre(t *testing.T, rendered string) []string {
	t.Helper()
	var out []string
	for _, line := range strings.Split(rendered, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "ExecStartPre=") {
			out = append(out, line)
		}
	}
	if len(out) == 0 {
		t.Fatal("rendered systemd unit has no ExecStartPre= lines; identity check is silently absent")
	}
	return out
}

// TestSystemdIdentityValidationComparesContentsNotPaths is the
// Phase 14.2 RED gate. The previous shape was
//
//	cmp -s /opt/sourcerudder/VERSION @VERSION@
//	cmp -s /opt/sourcerudder/IMAGE   @IMAGE@
//
// After substitution the @-tokens become PATH operands, so the
// command tries to `cmp /opt/sourcerudder/VERSION c0c852f`. The second
// operand is treated as a file path, not as the literal expected
// content; the comparison is meaningless. The fix: the rendered
// ExecStartPre MUST verify the on-disk file CONTAINS the expected
// content (e.g. via `grep -qxF 'expected' /opt/sourcerudder/VERSION`),
// not that some file at the path `expected` matches the on-disk
// file. The test asserts the rendered command uses content-matching
// for BOTH the VERSION file and the IMAGE file, and that the
// literal expected value appears INSIDE the command (proving the
// substitution is treated as content, not as a path operand).
func TestSystemdIdentityValidationComparesContentsNotPaths(t *testing.T) {
	const sampleVersion = "2.0.0"
	const sampleImage = "ghcr.io/mdesantis1984/sourcerudder:2.0.0@sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"

	rendered := renderSystemdUnit(t, sampleVersion, sampleImage)
	pre := extractExecStartPre(t, rendered)
	if len(pre) < 2 {
		t.Fatalf("expected at least 2 ExecStartPre lines (VERSION + IMAGE), got %d:\n%s", len(pre), strings.Join(pre, "\n"))
	}

	// Both ExecStartPre commands MUST use a content-matching tool —
	// `grep -qxF` (exact whole-line match, fixed string) or `fgrep
	// -qx`. Plain `cmp -s` is forbidden because cmp compares two
	// FILE PATHS, so the substituted content token becomes a path
	// operand instead of the expected literal.
	joined := strings.Join(pre, "\n")
	if strings.Contains(joined, "/usr/bin/cmp ") || strings.Contains(joined, "cmp -s ") {
		t.Fatalf("ExecStartPre uses `cmp -s` which compares two FILE PATHS; after substitution the expected value becomes a path operand, not a content check:\n%s", joined)
	}

	// The literal expected values MUST appear INSIDE the rendered
	// commands (proving substitution treated them as content), not
	// as standalone path tokens. We accept either shell-quoted
	// ('value' or "value") or unquoted tokens — what matters is
	// that the literal appears as a token immediately after the
	// content-matching tool, not as a second path operand to cmp.
	if !strings.Contains(joined, sampleVersion) {
		t.Fatalf("rendered ExecStartPre must contain the literal expected version %q (treated as content); got:\n%s", sampleVersion, joined)
	}
	if !strings.Contains(joined, sampleImage) {
		t.Fatalf("rendered ExecStartPre must contain the literal expected image %q (treated as content); got:\n%s", sampleImage, joined)
	}

	// Defensive: confirm a `grep -qxF` (or `fgrep -qx`) command is
	// present so we know the content match is enforced — not just
	// that the literal appears in a comment.
	hasContentMatch := false
	for _, line := range pre {
		if strings.Contains(line, "grep -qxF") || strings.Contains(line, "fgrep -qx") || strings.Contains(line, "grep -Fqx") {
			hasContentMatch = true
			break
		}
	}
	if !hasContentMatch {
		t.Fatalf("no ExecStartPre uses a content-matching tool (grep -qxF / fgrep -qx); identity check is still path-based:\n%s", joined)
	}
}

// TestSystemdIdentityValidationRejectsMismatchedContent is the
// triangulation surface: the rendered command MUST exit non-zero
// when the on-disk file's contents do NOT match the expected
// value. The test stages a temp file with the WRONG contents and
// invokes the rendered command in a shell so we exercise the real
// exit code. This proves the comparison is real (not a no-op that
// always returns 0).
func TestSystemdIdentityValidationRejectsMismatchedContent(t *testing.T) {
	const sampleVersion = "expected-version-token"
	const sampleImage = "expected-image-token"

	rendered := renderSystemdUnit(t, sampleVersion, sampleImage)
	pre := extractExecStartPre(t, rendered)

	// Set up a temp dir with two files whose contents DO NOT match
	// the expected values.
	dir := t.TempDir()
	versionFile := filepath.Join(dir, "VERSION")
	imageFile := filepath.Join(dir, "IMAGE")
	if err := os.WriteFile(versionFile, []byte("something-else-entirely\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	if err := os.WriteFile(imageFile, []byte("another-mismatch\n"), 0o644); err != nil {
		t.Fatalf("write IMAGE: %v", err)
	}

	// Rewrite the rendered commands so they reference our temp
	// files instead of /opt/sourcerudder. The shape must still be a
	// content-matching command — otherwise the test would silently
	// pass on a cmp-based version that errors out for the wrong
	// reason.
	for i, line := range pre {
		line = strings.ReplaceAll(line, "/opt/sourcerudder/VERSION", versionFile)
		line = strings.ReplaceAll(line, "/opt/sourcerudder/IMAGE", imageFile)
		pre[i] = line
	}

	// Run each ExecStartPre as its own /bin/sh -c invocation in
	// sequence. The chain is expected to fail fast — the first
	// mismatch aborts before the second command runs.
	for _, line := range pre {
		cmd := newShellCmd(t, extractShellCmd(line))
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err == nil {
			t.Fatalf("expected the ExecStartPre to FAIL when on-disk contents mismatch expected tokens; got success.\ncmd: %s\noutput: %s", line, out)
		}
		// Stop at the first failure — subsequent lines are not run.
		return
	}
}

// TestSystemdIdentityValidationAcceptsMatchingContent is the
// triangulation surface for the GREEN path: when the on-disk file
// DOES contain the expected token (and only that token), the
// rendered ExecStartPre chain MUST exit 0 so the unit is allowed
// to start. Without this case a regression that turns the check
// into "always fails" or "always succeeds" would still pass the
// mismatch test below.
func TestSystemdIdentityValidationAcceptsMatchingContent(t *testing.T) {
	const sampleVersion = "expected-version-token"
	const sampleImage = "expected-image-token"

	rendered := renderSystemdUnit(t, sampleVersion, sampleImage)
	pre := extractExecStartPre(t, rendered)

	// Stage temp files whose contents ARE the expected tokens
	// verbatim — `grep -qxF` requires an EXACT whole-line match.
	dir := t.TempDir()
	versionFile := filepath.Join(dir, "VERSION")
	imageFile := filepath.Join(dir, "IMAGE")
	binaryFile := filepath.Join(dir, "sourcerudder")
	checksumFile := filepath.Join(dir, "BINARY_SHA256")
	if err := os.WriteFile(versionFile, []byte(sampleVersion+"\n"), 0o644); err != nil {
		t.Fatalf("write VERSION: %v", err)
	}
	if err := os.WriteFile(imageFile, []byte(sampleImage+"\n"), 0o644); err != nil {
		t.Fatalf("write IMAGE: %v", err)
	}
	binary := []byte("verified sourcerudder binary")
	if err := os.WriteFile(binaryFile, binary, 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	checksum := fmt.Sprintf("%x  %s\n", sha256.Sum256(binary), binaryFile)
	if err := os.WriteFile(checksumFile, []byte(checksum), 0o644); err != nil {
		t.Fatalf("write BINARY_SHA256: %v", err)
	}

	for i, line := range pre {
		line = strings.ReplaceAll(line, "/opt/sourcerudder/VERSION", versionFile)
		line = strings.ReplaceAll(line, "/opt/sourcerudder/IMAGE", imageFile)
		line = strings.ReplaceAll(line, "/etc/sourcerudder/release/BINARY_SHA256", checksumFile)
		line = strings.ReplaceAll(line, "/opt/sourcerudder/bin/sourcerudder", binaryFile)
		pre[i] = line
	}

	for _, line := range pre {
		cmd := newShellCmd(t, extractShellCmd(line))
		cmd.Dir = dir
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("expected ExecStartPre to SUCCEED when on-disk contents match; got err=%v\ncmd: %s\noutput: %s", err, line, out)
		}
	}
}

func TestSystemdUnitBoundsResourcesAndPrivileges(t *testing.T) {
	rendered := renderSystemdUnit(t, "2.0.0", "example.invalid/image@sha256:digest")
	for _, directive := range []string{
		"MemoryMax=512M",
		"MemorySwapMax=512M",
		"CPUQuota=50%",
		"TasksMax=128",
		"NoNewPrivileges=true",
		"ProtectSystem=strict",
		"ProtectHome=true",
		"ReadOnlyPaths=/opt/sourcerudder /etc/sourcerudder/release",
		"CapabilityBoundingSet=",
	} {
		if !strings.Contains(rendered, directive) {
			t.Errorf("systemd unit missing hardening directive %q", directive)
		}
	}
}

func TestSystemdUnitVerifiesBinaryChecksum(t *testing.T) {
	rendered := renderSystemdUnit(t, "2.0.0", "example.invalid/image@sha256:digest")
	if !strings.Contains(rendered, "sha256sum -c /etc/sourcerudder/release/BINARY_SHA256") {
		t.Fatal("systemd unit must verify the installed binary against BINARY_SHA256")
	}
}

func TestSystemdUnitRejectsBinaryChecksumMismatch(t *testing.T) {
	rendered := renderSystemdUnit(t, "2.0.0", "example.invalid/image@sha256:digest")
	pre := extractExecStartPre(t, rendered)
	checksumCommand := pre[len(pre)-1]
	dir := t.TempDir()
	binaryFile := filepath.Join(dir, "sourcerudder")
	checksumFile := filepath.Join(dir, "BINARY_SHA256")
	if err := os.WriteFile(binaryFile, []byte("modified binary"), 0o755); err != nil {
		t.Fatalf("write binary: %v", err)
	}
	if err := os.WriteFile(checksumFile, []byte(strings.Repeat("0", 64)+"  "+binaryFile+"\n"), 0o644); err != nil {
		t.Fatalf("write checksum: %v", err)
	}
	checksumCommand = strings.ReplaceAll(checksumCommand, "/etc/sourcerudder/release/BINARY_SHA256", checksumFile)
	checksumCommand = strings.ReplaceAll(checksumCommand, "/opt/sourcerudder/bin/sourcerudder", binaryFile)
	cmd := newShellCmd(t, extractShellCmd(checksumCommand))
	if output, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("checksum guard accepted modified binary; output=%s", output)
	}
}

// extractShellCmd strips the systemd-specific `ExecStartPre=` prefix
// from a unit line so the remaining value can be fed to `sh -c`. The
// unit file already encodes the executor (/bin/sh -c '<cmd>'), so we
// hand the prefix and the single-quoted argument straight through.
func extractShellCmd(line string) string {
	trimmed := strings.TrimSpace(strings.TrimPrefix(line, "ExecStartPre="))
	return trimmed
}
