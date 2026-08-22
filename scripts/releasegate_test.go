// Package scripts_test runs scripts/release-gate.sh in synthetic git
// repositories so each gate behavior is exercised in isolation without
// touching the worktree the tests are running from.
package scripts_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// harness controls one synthetic gate scenario.
type harness struct {
	t          *testing.T
	repo       string
	gateScript string
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	repo := t.TempDir()

	runGit(t, repo, "init", "--initial-branch=main")
	runGit(t, repo, "config", "user.email", "gate@example.com")
	runGit(t, repo, "config", "user.name", "Gate Test")
	runGit(t, repo, "config", "commit.gpgsign", "false")

	// Minimal Go module so `go build/vet/test` have something to chew.
	mustWrite(t, repo, "go.mod", "module gatemod\n\ngo 1.23\n")
	mustWrite(t, repo, "hello.go", "package gatemod\n\nfunc Hello() string { return \"hi\" }\n")
	mustWrite(t, repo, "hello_test.go", "package gatemod\n\nimport \"testing\"\n\nfunc TestHello(t *testing.T) { if Hello() != \"hi\" { t.Fatal() } }\n")

	// Copy the gate script under test into the synthetic repo and
	// include it in the initial commit so it is tracked and not flagged
	// as an untracked file outside the allow-list.
	// Tests run with cwd = the package directory (scripts/), so the
	// real script lives one directory up.
	src, err := os.ReadFile(filepath.Join("..", "scripts", "release-gate.sh"))
	if err != nil {
		t.Fatalf("read gate script: %v", err)
	}
	gate := filepath.Join(repo, "release-gate.sh")
	mustWrite(t, repo, "release-gate.sh", string(src))
	if err := os.Chmod(gate, 0o755); err != nil {
		t.Fatalf("chmod gate: %v", err)
	}

	runGit(t, repo, "add", ".")
	runGit(t, repo, "commit", "-m", "initial")

	return &harness{t: t, repo: repo, gateScript: gate}
}

func (h *harness) run(env ...string) (int, string, string) {
	h.t.Helper()
	cmd := exec.Command(h.gateScript)
	cmd.Dir = h.repo
	// Strip gate-relevant env vars inherited from the calling shell
	// so each test runs against a clean slate; the explicit env vars
	// passed to run() override.
	filtered := make([]string, 0, len(os.Environ()))
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "RELEASE_GATE_") || strings.HasPrefix(e, "BASE_REF=") {
			continue
		}
		filtered = append(filtered, e)
	}
	cmd.Env = append(filtered, env...)
	var stdout, stderr strings.Builder
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	exit := 0
	if ee, ok := err.(*exec.ExitError); ok {
		exit = ee.ExitCode()
	} else if err != nil {
		h.t.Fatalf("gate exec: %v", err)
	}
	return exit, stdout.String(), stderr.String()
}

func (h *harness) touchFile(rel, content string) {
	h.t.Helper()
	mustWrite(h.t, h.repo, rel, content)
}

func (h *harness) commitFile(rel, content, msg string) {
	h.t.Helper()
	mustWrite(h.t, h.repo, rel, content)
	runGit(h.t, h.repo, "add", rel)
	runGit(h.t, h.repo, "commit", "-m", msg)
}

func mustWrite(t *testing.T, dir, rel, content string) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// ---- tests --------------------------------------------------------------

// TestGateFailsOnMissingReview covers the precondition the gate enforces
// before any Go check: docs/release/reviews/review-be4525bc4797e972.md
// must exist with an Authority header. A clean repo without that file
// must exit non-zero.
func TestGateFailsOnMissingReview(t *testing.T) {
	h := newHarness(t)
	exit, _, stderr := h.run("RELEASE_GATE_BRANCH=main", "BASE_REF=main")
	if exit == 0 {
		t.Fatalf("expected non-zero exit, got 0; stderr=%q", stderr)
	}
	if !strings.Contains(stderr, "review placeholder missing") {
		t.Fatalf("expected 'review placeholder missing' in stderr, got %q", stderr)
	}
}

// TestGateFailsOnUncommittedTrackedChange ensures the gate refuses dirty
// tracked files (any uncommitted edit) before any other check runs.
func TestGateFailsOnUncommittedTrackedChange(t *testing.T) {
	h := newHarness(t)
	h.commitFile("docs/release/reviews/review-be4525bc4797e972.md",
		"Authority: official\n", "add review placeholder")
	h.commitFile("marker.txt", "clean state\n", "add marker")
	// Uncommitted tracked edit on marker.txt.
	h.touchFile("marker.txt", "dirty state\n")

	exit, _, stderr := h.run("RELEASE_GATE_BRANCH=main", "BASE_REF=main")
	if exit == 0 {
		t.Fatalf("expected non-zero exit, got 0; stderr=%q", stderr)
	}
	if !strings.Contains(stderr, "uncommitted tracked changes") {
		t.Fatalf("expected 'uncommitted tracked changes' in stderr, got %q", stderr)
	}
}

// TestGateFailsOnUntrackedOutsideAllowList enforces the untracked
// allow-list: only docs/release/reviews/, .atl/, .codegraph/, and
// .codebase-memory/ may be untracked at gate time.
func TestGateFailsOnUntrackedOutsideAllowList(t *testing.T) {
	h := newHarness(t)
	h.commitFile("docs/release/reviews/review-be4525bc4797e972.md",
		"Authority: official\n", "add review placeholder")
	h.touchFile("scratch.txt", "leaked untracked file")

	exit, _, stderr := h.run("RELEASE_GATE_BRANCH=main", "BASE_REF=main")
	if exit == 0 {
		t.Fatalf("expected non-zero exit, got 0; stderr=%q", stderr)
	}
	if !strings.Contains(stderr, "untracked files outside allow-list") {
		t.Fatalf("expected 'untracked files outside allow-list' in stderr, got %q", stderr)
	}
}

// TestGateSucceedsOnCleanTrivialRepo exercises the happy path: review
// placeholder present, no untracked leakage, no over-budget diff, and
// the Go checks pass against the trivial synthetic module.
func TestGateSucceedsOnCleanTrivialRepo(t *testing.T) {
	h := newHarness(t)
	h.commitFile("docs/release/reviews/review-be4525bc4797e972.md",
		"Authority: official\n", "add review placeholder")

	exit, stdout, stderr := h.run("RELEASE_GATE_BRANCH=main", "BASE_REF=main")
	if exit != 0 {
		t.Fatalf("expected exit 0, got %d; stderr=%q", exit, stderr)
	}
	if !strings.Contains(stdout, "release-gate: PASS") {
		t.Fatalf("expected PASS line in stdout, got %q", stdout)
	}
}

// TestGateCarveOutExactBranchOnly proves the size-exception matches the
// current branch ONLY. Two branches are exercised: on the named branch
// the exception unlocks a 500-line diff AND the tracked receipt
// validates; on a different branch the same diff still fails because
// the carve-out env does not match. Each branch is created BEFORE the
// bulk commit so the diff vs main actually diverges.
func TestGateCarveOutExactBranchOnly(t *testing.T) {
	bulk := strings.Repeat("// padding line to inflate diff\n", 500)
	const carveBranch = "feature/close-fetch-resilience-release-gates-exception"
	const receiptPath = "docs/release/size-exceptions/close-fetch-resilience-and-release-gates.md"
	receipt := "# Size Exception Receipt\n\n" +
		"Branch: " + carveBranch + "\n" +
		"Commit: <pr-head-sha>\n" +
		"Approval Reference: #4125\n" +
		"Scope: bounded (single-PR exception for Phase 12-14 pre-production security and Phase 13 release-gate hardening)\n" +
		"Expiration: 2026-12-31\n" +
		"Authority Requirement: official\n"

	// ---- branch A: carve-out matches -> PASS -----------------------
	hA := newHarness(t)
	hA.commitFile("docs/release/reviews/review-be4525bc4797e972.md",
		"Authority: official\n", "add review placeholder")
	hA.commitFile(receiptPath, receipt, "add receipt")
	runGit(t, hA.repo, "checkout", "-b", carveBranch)
	hA.commitFile("bulk.go", "package gatemod\n\n"+bulk, "bulk to exceed 400 lines")

	exitA, _, stderrA := hA.run(
		"RELEASE_GATE_BRANCH="+carveBranch,
		"RELEASE_GATE_SIZE_EXCEPTION="+carveBranch,
		"BASE_REF=main",
	)
	if exitA != 0 {
		t.Fatalf("branch A: expected exit 0 with carve-out + receipt, got %d; stderr=%q", exitA, stderrA)
	}

	// ---- branch B: carve-out does not match -> FAIL ---------------
	hB := newHarness(t)
	hB.commitFile("docs/release/reviews/review-be4525bc4797e972.md",
		"Authority: official\n", "add review placeholder")
	runGit(t, hB.repo, "checkout", "-b", "feature/other-candidate")
	hB.commitFile("bulk.go", "package gatemod\n\n"+bulk, "bulk to exceed 400 lines")

	exitB, _, stderrB := hB.run(
		"RELEASE_GATE_BRANCH=feature/other-candidate",
		"RELEASE_GATE_SIZE_EXCEPTION="+carveBranch,
		"BASE_REF=main",
	)
	if exitB == 0 {
		t.Fatalf("branch B: expected non-zero exit (carve-out must not match other branch), got 0")
	}
	if !strings.Contains(stderrB, "line budget exceeded") {
		t.Fatalf("branch B: expected 'line budget exceeded' in stderr, got %q", stderrB)
	}
}

// TestGateFailsOnBuildError injects a syntax error in the synthetic
// module and confirms the gate fails fast on the go build step rather
// than swallowing the failure. The broken file is committed so the
// worktree stays clean (otherwise the dirty-check fires first and the
// build check never runs).
func TestGateFailsOnBuildError(t *testing.T) {
	h := newHarness(t)
	h.commitFile("docs/release/reviews/review-be4525bc4797e972.md",
		"Authority: official\n", "add review placeholder")
	h.commitFile("hello.go",
		"package gatemod\n\nfunc Hello() string { THIS IS NOT GO }\n",
		"introduce build break")

	exit, _, stderr := h.run("RELEASE_GATE_BRANCH=main", "BASE_REF=main")
	if exit == 0 {
		t.Fatalf("expected non-zero exit, got 0; stderr=%q", stderr)
	}
	if !strings.Contains(stderr, "go build") {
		t.Fatalf("expected 'go build' in stderr, got %q", stderr)
	}
}

// TestGateRequiresExactAuthorityOfficial is the Phase 13.1 RED gate.
// The previous gate regex `^Authority:[[:space:]]*[A-Za-z]+` accepts
// ANY word, so `Authority: pending`, `Authority: foo`, `Authority:
// anything` all pass. The new contract: only the exact value
// "official" is accepted. Anything else MUST fail the gate so a
// partial review cannot unlock a merge.
func TestGateRequiresExactAuthorityOfficial(t *testing.T) {
	cases := []struct {
		name       string
		authority  string
		mustPass   bool
		wantInErr  string
	}{
		{name: "pending-fails", authority: "Authority: pending\n", mustPass: false, wantInErr: "Authority"},
		{name: "foo-fails", authority: "Authority: foo\n", mustPass: false, wantInErr: "Authority"},
		{name: "official-passes", authority: "Authority: official\n", mustPass: true},
		{name: "capital-Approved-fails", authority: "Authority: Approved\n", mustPass: false, wantInErr: "Authority"},
		{name: "official-trailing-fails", authority: "Authority: officialish\n", mustPass: false, wantInErr: "Authority"},
		{name: "missing-keyword-fails", authority: "Status: official\n", mustPass: false, wantInErr: "Authority"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.commitFile("docs/release/reviews/review-be4525bc4797e972.md",
				"# Review\n\n"+tt.authority+"\n", "add review placeholder")

			exit, _, stderr := h.run("RELEASE_GATE_BRANCH=main", "BASE_REF=main")
			if tt.mustPass {
				if exit != 0 {
					t.Fatalf("expected exit 0 (Authority: official), got %d; stderr=%q", exit, stderr)
				}
			} else {
				if exit == 0 {
					t.Fatalf("expected non-zero exit (Authority=%q must not pass), got 0", strings.TrimSpace(tt.authority))
				}
				if !strings.Contains(stderr, tt.wantInErr) {
					t.Fatalf("expected stderr to mention %q, got %q", tt.wantInErr, stderr)
				}
			}
		})
	}
}

// TestGateRejectsDirtyBypassUnderCI is the Phase 13.3 RED gate. The
// local-QA seam `RELEASE_GATE_ALLOW_DIRTY=1` MUST be ignored when
// CI=true so a CI run cannot accidentally bypass the worktree
// cleanliness check. The seam exists only for local QA; under CI the
// gate runs against a committed, clean worktree.
func TestGateRejectsDirtyBypassUnderCI(t *testing.T) {
	h := newHarness(t)
	h.commitFile("docs/release/reviews/review-be4525bc4797e972.md",
		"Authority: official\n", "add review placeholder")
	// Commit a marker file so we can dirty it without breaking the
	// synthetic Go module's existing tests.
	h.commitFile("marker.txt", "clean state\n", "add marker")
	// Uncommitted tracked edit on marker.txt.
	h.touchFile("marker.txt", "dirty state\n")

	// CI=true + RELEASE_GATE_ALLOW_DIRTY=1 must STILL fail.
	exit, _, stderr := h.run(
		"RELEASE_GATE_BRANCH=main",
		"BASE_REF=main",
		"CI=true",
		"RELEASE_GATE_ALLOW_DIRTY=1",
	)
	if exit == 0 {
		t.Fatal("expected non-zero exit under CI even with RELEASE_GATE_ALLOW_DIRTY=1")
	}
	if !strings.Contains(stderr, "uncommitted tracked changes") &&
		!strings.Contains(stderr, "CI") {
		t.Fatalf("expected stderr to mention worktree cleanliness or CI, got %q", stderr)
	}
}

// TestGateAllowsDirtyBypassOutsideCI is the Phase 13.3 triangulation
// surface. Outside CI, the seam must still work so a local operator
// can run the gate against uncommitted work.
func TestGateAllowsDirtyBypassOutsideCI(t *testing.T) {
	h := newHarness(t)
	h.commitFile("docs/release/reviews/review-be4525bc4797e972.md",
		"Authority: official\n", "add review placeholder")
	h.commitFile("marker.txt", "clean state\n", "add marker")
	h.touchFile("marker.txt", "dirty state\n")

	exit, _, stderr := h.run(
		"RELEASE_GATE_BRANCH=main",
		"BASE_REF=main",
		"RELEASE_GATE_ALLOW_DIRTY=1",
	)
	if exit != 0 {
		t.Fatalf("expected exit 0 outside CI with bypass, got %d; stderr=%q", exit, stderr)
	}
}

// TestGateSizeExceptionTiedToTrackedReceipt is the Phase 13.6 RED
// gate. When the carve-out matches the branch AND the diff is over
// budget, the gate MUST validate that a tracked size-exception receipt
// exists at the documented path with the required fields. The
// receipt filename matches the SDD change name so every carve-out
// has a discoverable, reviewable artifact. The receipt NEVER
// substitutes for the final official 4R review (Phase 16) — the
// review file with `Authority: official` is the final authority.
func TestGateSizeExceptionTiedToTrackedReceipt(t *testing.T) {
	bulk := strings.Repeat("// padding line to inflate diff\n", 500)
	const branch = "feature/close-fetch-resilience-release-gates-exception"
	const receiptPath = "docs/release/size-exceptions/close-fetch-resilience-and-release-gates.md"

	t.Run("missing-receipt-fails", func(t *testing.T) {
		h := newHarness(t)
		h.commitFile("docs/release/reviews/review-be4525bc4797e972.md",
			"Authority: official\n", "add review placeholder")
		runGit(t, h.repo, "checkout", "-b", branch)
		h.commitFile("bulk.go", "package gatemod\n\n"+bulk, "bulk to exceed 400 lines")

		exit, _, stderr := h.run(
			"RELEASE_GATE_BRANCH="+branch,
			"RELEASE_GATE_SIZE_EXCEPTION="+branch,
			"BASE_REF=main",
		)
		if exit == 0 {
			t.Fatal("expected non-zero exit: tracked receipt missing; gate MUST refuse")
		}
		if !strings.Contains(stderr, "size-exception") && !strings.Contains(stderr, "receipt") {
			t.Fatalf("expected stderr to mention size-exception receipt, got %q", stderr)
		}
	})

	t.Run("receipt-with-all-fields-passes", func(t *testing.T) {
		h := newHarness(t)
		h.commitFile("docs/release/reviews/review-be4525bc4797e972.md",
			"Authority: official\n", "add review placeholder")
		// Commit the receipt FIRST so the diff is clean.
		receipt := "# Size Exception Receipt\n\n" +
			"Branch: " + branch + "\n" +
			"Commit: <pr-head-sha>\n" +
			"Approval Reference: #4125\n" +
			"Scope: bounded (single-PR exception for Phase 12-14 pre-production security and Phase 13 release-gate hardening)\n" +
			"Expiration: 2026-12-31\n" +
			"Authority Requirement: official\n"
		h.commitFile(receiptPath, receipt, "add size-exception receipt")
		runGit(t, h.repo, "checkout", "-b", branch)
		h.commitFile("bulk.go", "package gatemod\n\n"+bulk, "bulk to exceed 400 lines")

		exit, _, stderr := h.run(
			"RELEASE_GATE_BRANCH="+branch,
			"RELEASE_GATE_SIZE_EXCEPTION="+branch,
			"BASE_REF=main",
		)
		if exit != 0 {
			t.Fatalf("expected exit 0 with complete receipt, got %d; stderr=%q", exit, stderr)
		}
	})

	t.Run("receipt-missing-required-field-fails", func(t *testing.T) {
		h := newHarness(t)
		h.commitFile("docs/release/reviews/review-be4525bc4797e972.md",
			"Authority: official\n", "add review placeholder")
		// Missing Expiration.
		receipt := "# Size Exception Receipt\n\n" +
			"Branch: " + branch + "\n" +
			"Commit: <pr-head-sha>\n" +
			"Approval Reference: #4125\n" +
			"Scope: bounded\n" +
			"Authority Requirement: official\n"
		h.commitFile(receiptPath, receipt, "add size-exception receipt (missing Expiration)")
		runGit(t, h.repo, "checkout", "-b", branch)
		h.commitFile("bulk.go", "package gatemod\n\n"+bulk, "bulk to exceed 400 lines")

		exit, _, stderr := h.run(
			"RELEASE_GATE_BRANCH="+branch,
			"RELEASE_GATE_SIZE_EXCEPTION="+branch,
			"BASE_REF=main",
		)
		if exit == 0 {
			t.Fatal("expected non-zero exit: receipt missing Expiration field")
		}
		if !strings.Contains(stderr, "Expiration") && !strings.Contains(stderr, "size-exception") {
			t.Fatalf("expected stderr to mention missing Expiration, got %q", stderr)
		}
	})
}

// TestStagedSizeExceptionReceiptMatchesGateParser is the
// Phase 13.6/15.b alignment RED gate. The release-gate parser at
// scripts/release-gate.sh:138-145 looks for colon-form lines
// (`^Field: value`) on the receipt. The staged receipt at
// docs/release/size-exceptions/close-fetch-resilience-and-release-gates.md
// MUST include those lines for the gate to validate it after Phase 16
// flips Authority to official. This test reads the actual receipt
// from the repo and asserts the six required colon-form lines are
// present (one per required field). Without these lines the gate
// would block the merge even with Authority=official.
//
// The test skips if the receipt is not present in the checkout (so
// it does not fail on CI before the receipt is staged). When the
// receipt is present but missing the parser lines, the test FAILS
// and names the missing field so the operator can fix the receipt.
//
// The helper receiptHasAllParserFields extracts the field-presence
// check so the triangulation sub-tests can drive it with synthetic
// receipt shapes (table-only, partial-fields, complete).
func TestStagedSizeExceptionReceiptMatchesGateParser(t *testing.T) {
	t.Run("actual-receipt-has-parser-fields", func(t *testing.T) {
		receiptPath := filepath.Join("..", "docs", "release", "size-exceptions", "close-fetch-resilience-and-release-gates.md")
		data, err := os.ReadFile(receiptPath)
		if err != nil {
			t.Skipf("receipt not present in this checkout (path=%s): %v", receiptPath, err)
		}
		missing := receiptMissingParserFields(string(data))
		if len(missing) > 0 {
			t.Fatalf("staged size-exception receipt at %s is missing parser-compatible colon-form lines for: %v. The release gate at scripts/release-gate.sh greps for `^Field: value` lines and will refuse to validate this receipt when Authority=official. Either add the colon-form lines to the receipt or update the parser to accept markdown-table rows.", receiptPath, missing)
		}
	})

	// Triangulation: drive the same check with synthetic receipt
	// shapes. The first two sub-tests reproduce the original bug
	// (the staged receipt used a markdown table that the parser
	// could not see). The third sub-test confirms a partial receipt
	// is rejected with the right field name.
	t.Run("table-only-receipt-is-incomplete", func(t *testing.T) {
		body := "# Size Exception Receipt\n\n" +
			"| Field | Value |\n" +
			"|-------|-------|\n" +
			"| Branch | foo |\n" +
			"| Commit | bar |\n" +
			"| Approval Reference | baz |\n" +
			"| Scope | qux |\n" +
			"| Expiration | 2026-12-31 |\n" +
			"| Authority Requirement | official |\n"
		missing := receiptMissingParserFields(body)
		if len(missing) == 0 {
			t.Fatal("expected markdown-table-only receipt to be reported incomplete; got 0 missing fields")
		}
	})

	t.Run("partial-colon-form-receipt-names-missing-field", func(t *testing.T) {
		body := "# Size Exception Receipt\n\n" +
			"Branch: foo\n" +
			"Commit: bar\n" +
			"Approval Reference: baz\n" +
			"Scope: qux\n" +
			"Authority Requirement: official\n"
		missing := receiptMissingParserFields(body)
		if len(missing) != 1 || missing[0] != "Expiration" {
			t.Fatalf("expected only Expiration to be reported missing, got %v", missing)
		}
	})

	t.Run("complete-colon-form-receipt-is-complete", func(t *testing.T) {
		body := "# Size Exception Receipt\n\n" +
			"Branch: foo\n" +
			"Commit: bar\n" +
			"Approval Reference: baz\n" +
			"Scope: qux\n" +
			"Expiration: 2026-12-31\n" +
			"Authority Requirement: official\n"
		missing := receiptMissingParserFields(body)
		if len(missing) != 0 {
			t.Fatalf("expected complete colon-form receipt to have 0 missing fields, got %v", missing)
		}
	})
}

// receiptMissingParserFields mirrors the field-presence check the
// release gate performs at scripts/release-gate.sh:138-145. It scans
// the receipt body for `^Field:` lines (one per required field) and
// returns the names of any fields not found. Returns nil when every
// required field is present.
func receiptMissingParserFields(body string) []string {
	required := []string{
		"Branch: ",
		"Commit: ",
		"Approval Reference: ",
		"Scope: ",
		"Expiration: ",
		"Authority Requirement: ",
	}
	var missing []string
	for _, prefix := range required {
		// Match at the start of any line: simulate the gate's `grep -qE "^${field}:"`.
		// The leading `\n` ensures we only match column zero of a line.
		needle := "\n" + prefix
		if !strings.Contains(body, needle) {
			fieldName := strings.TrimRight(strings.TrimSuffix(prefix, ": "), ":")
			missing = append(missing, fieldName)
		}
	}
	return missing
}
