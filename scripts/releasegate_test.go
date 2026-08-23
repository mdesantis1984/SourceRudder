// Package scripts_test runs scripts/release-gate.sh in synthetic git
// repositories so each gate behavior is exercised in isolation without
// touching the worktree the tests are running from.
package scripts_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// headSHA returns the SHA of the synthetic repo's current HEAD.
func (h *harness) headSHA() string {
	h.t.Helper()
	cmd := exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = h.repo
	out, err := cmd.Output()
	if err != nil {
		h.t.Fatalf("git rev-parse HEAD: %v", err)
	}
	return strings.TrimSpace(string(out))
}

// addValidRDDPassReceipt commits a complete RDD receipt with Status: pass
// referencing the SHA of the FIRST commit that introduced the receipt.
// The receipt commit stays reachable from any later HEAD on the same
// branch (parent chain), so the gate's ancestor check passes when the
// gate runs against HEAD after further commits.
//
// The flow is two commits, not an amend, because `git commit --amend`
// replaces the original commit object — the original SHA is no longer
// reachable from any branch. A plain two-commit flow keeps the
// placeholder commit on the history as the parent of the final commit.
func (h *harness) addValidRDDPassReceipt(branch string) {
	h.t.Helper()
	// Step 1: commit a placeholder receipt to lock in a real SHA.
	h.commitFile(
		"docs/release/reviews/review-be4525bc4797e972.md",
		makeValidRDDPassReceipt(branch, "PLACEHOLDER_SHA"),
		"add RDD receipt placeholder",
	)
	// Step 2: resolve the commit SHA, rewrite the receipt with the
	// real value, and commit again as a separate commit so the
	// placeholder commit remains on the history as the receipt
	// commit's parent.
	receiptSHA := h.headSHA()
	mustWrite(h.t, h.repo, "docs/release/reviews/review-be4525bc4797e972.md",
		makeValidRDDPassReceipt(branch, receiptSHA))
	runGit(h.t, h.repo, "add", "docs/release/reviews/review-be4525bc4797e972.md")
	runGit(h.t, h.repo, "commit", "-m", "finalize RDD receipt with real Candidate Commit")
}

// makeValidRDDPassReceipt builds the deterministic receipt body the
// gate's RDD validator accepts. branch is interpolated into Scope;
// candidateCommit is interpolated into Candidate Commit and must be
// reachable from HEAD for the receipt to validate.
func makeValidRDDPassReceipt(branch, candidateCommit string) string {
	return "# RDD Receipt\n\n" +
		"Status: pass\n" +
		"Candidate Commit: " + candidateCommit + "\n" +
		"Scope: local gate validation (branch=" + branch + ")\n" +
		"Verified Commands:\n" +
		"  - go build ./...: PASS\n" +
		"  - go vet ./...: PASS\n" +
		"  - go test ./...: PASS\n" +
		"  - go test -race ./...: PASS\n" +
		"Unresolved Blocker Policy: none\n"
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
// must exist as a parseable RDD receipt. A clean repo without that file
// must exit non-zero.
func TestGateFailsOnMissingReview(t *testing.T) {
	h := newHarness(t)
	exit, _, stderr := h.run("RELEASE_GATE_BRANCH=main", "BASE_REF=main")
	if exit == 0 {
		t.Fatalf("expected non-zero exit, got 0; stderr=%q", stderr)
	}
	if !strings.Contains(stderr, "RDD receipt missing") &&
		!strings.Contains(stderr, "review placeholder missing") {
		t.Fatalf("expected stderr to mention RDD/review receipt missing, got %q", stderr)
	}
}

// TestGateFailsOnUncommittedTrackedChange ensures the gate refuses dirty
// tracked files (any uncommitted edit) before any other check runs.
func TestGateFailsOnUncommittedTrackedChange(t *testing.T) {
	h := newHarness(t)
	h.addValidRDDPassReceipt("main")
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
	h.addValidRDDPassReceipt("main")
	h.touchFile("scratch.txt", "leaked untracked file")

	exit, _, stderr := h.run("RELEASE_GATE_BRANCH=main", "BASE_REF=main")
	if exit == 0 {
		t.Fatalf("expected non-zero exit, got 0; stderr=%q", stderr)
	}
	if !strings.Contains(stderr, "untracked files outside allow-list") {
		t.Fatalf("expected 'untracked files outside allow-list' in stderr, got %q", stderr)
	}
}

// TestGateSucceedsOnCleanTrivialRepo exercises the happy path: RDD
// receipt present, no untracked leakage, no over-budget diff, and the
// Go checks pass against the trivial synthetic module.
func TestGateSucceedsOnCleanTrivialRepo(t *testing.T) {
	h := newHarness(t)
	h.addValidRDDPassReceipt("main")

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
		"Forward Reference: docs/release/reviews/review-be4525bc4797e972.md (RDD receipt is the local authority attestation)\n"

	// ---- branch A: carve-out matches -> PASS -----------------------
	hA := newHarness(t)
	hA.addValidRDDPassReceipt(carveBranch)
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
	hB.addValidRDDPassReceipt("feature/other-candidate")
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

// TestGateRejectsDirtyBypassUnderCI is the Phase 13.3 RED gate. The
// local-QA seam `RELEASE_GATE_ALLOW_DIRTY=1` MUST be ignored when
// CI=true so a CI run cannot accidentally bypass the worktree
// cleanliness check. The seam exists only for local QA; under CI the
// gate runs against a committed, clean worktree.
func TestGateRejectsDirtyBypassUnderCI(t *testing.T) {
	h := newHarness(t)
	h.addValidRDDPassReceipt("main")
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

// TestGateFailsOnBuildError injects a syntax error in the synthetic
// module and confirms the gate fails fast on the go build step rather
// than swallowing the failure. The broken file is committed so the
// worktree stays clean (otherwise the dirty-check fires first and the
// build check never runs).
func TestGateFailsOnBuildError(t *testing.T) {
	h := newHarness(t)
	h.addValidRDDPassReceipt("main")
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

// TestGateAllowsDirtyBypassOutsideCI is the Phase 13.3 triangulation
// surface. Outside CI, the seam must still work so a local operator
// can run the gate against uncommitted work.
func TestGateAllowsDirtyBypassOutsideCI(t *testing.T) {
	h := newHarness(t)
	h.addValidRDDPassReceipt("main")
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

// TestGateRDDStatusMustBePass is the Phase 16 RED gate (post-refactor).
// The previous gate enforced `^Authority: official`; the new RDD
// contract enforces `Status: pass`. The table below exercises the
// new Status validator with the same shape of edge cases the
// Authority table covered: a forbidden value MUST block, the
// literal `pass` MUST pass, and any partial / mistyped value MUST
// be rejected. The `wantInErr` substring is intentionally generic
// (`Status`) so a future tweak to the exact log message does not
// require updating the test.
func TestGateRDDStatusMustBePass(t *testing.T) {
	cases := []struct {
		name      string
		status    string
		mustPass  bool
		wantInErr string
	}{
		{name: "pending-fails", status: "Status: pending\n", mustPass: false, wantInErr: "Status"},
		{name: "fail-fails", status: "Status: fail\n", mustPass: false, wantInErr: "Status"},
		{name: "foo-fails", status: "Status: foo\n", mustPass: false, wantInErr: "Status"},
		{name: "pass-passes", status: "Status: pass\n", mustPass: true},
		{name: "PASS-uppercase-fails", status: "Status: PASS\n", mustPass: false, wantInErr: "Status"},
		{name: "pass-with-trailing-fails", status: "Status: passing\n", mustPass: false, wantInErr: "Status"},
		{name: "missing-status-block-fails", status: "Status:\n", mustPass: false, wantInErr: "Status"},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			// Build a receipt that contains the test's Status value
			// and every other required field, so a failure can be
			// attributed to Status alone.
			body := "# RDD Receipt\n" +
				tt.status +
				"Candidate Commit: " + h.headSHA() + "\n" +
				"Scope: local gate validation (branch=main)\n" +
				"Verified Commands:\n" +
				"  - go build ./...: PASS\n" +
				"Unresolved Blocker Policy: none\n"
			h.commitFile("docs/release/reviews/review-be4525bc4797e972.md", body, "add receipt")

			exit, _, stderr := h.run("RELEASE_GATE_BRANCH=main", "BASE_REF=main")
			if tt.mustPass {
				if exit != 0 {
					t.Fatalf("expected exit 0 (Status: pass), got %d; stderr=%q", exit, stderr)
				}
			} else {
				if exit == 0 {
					t.Fatalf("expected non-zero exit (Status=%q must not pass), got 0", strings.TrimSpace(tt.status))
				}
				if !strings.Contains(stderr, tt.wantInErr) {
					t.Fatalf("expected stderr to mention %q, got %q", tt.wantInErr, stderr)
				}
			}
		})
	}
}

// TestGateSizeExceptionTiedToTrackedReceipt is the Phase 13.6 RED
// gate. When the carve-out matches the branch AND the diff is over
// budget, the gate MUST validate that a tracked size-exception receipt
// exists at the documented path with the required fields. The
// receipt filename matches the SDD change name so every carve-out
// has a discoverable, reviewable artifact. The size-exception receipt
// NEVER substitutes for the RDD receipt in step 2; it is an
// orthogonal gate on the line-budget carve-out only.
func TestGateSizeExceptionTiedToTrackedReceipt(t *testing.T) {
	bulk := strings.Repeat("// padding line to inflate diff\n", 500)
	const branch = "feature/close-fetch-resilience-release-gates-exception"
	const receiptPath = "docs/release/size-exceptions/close-fetch-resilience-and-release-gates.md"

	t.Run("missing-receipt-fails", func(t *testing.T) {
		h := newHarness(t)
		h.addValidRDDPassReceipt(branch)
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
		h.addValidRDDPassReceipt(branch)
		// Commit the receipt FIRST so the diff is clean.
		receipt := "# Size Exception Receipt\n\n" +
			"Branch: " + branch + "\n" +
			"Commit: <pr-head-sha>\n" +
			"Approval Reference: #4125\n" +
			"Scope: bounded (single-PR exception for Phase 12-14 pre-production security and Phase 13 release-gate hardening)\n" +
			"Expiration: 2026-12-31\n" +
			"Forward Reference: docs/release/reviews/review-be4525bc4797e972.md (RDD receipt is the local authority attestation)\n"
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
		h.addValidRDDPassReceipt(branch)
		// Missing Expiration.
		receipt := "# Size Exception Receipt\n\n" +
			"Branch: " + branch + "\n" +
			"Commit: <pr-head-sha>\n" +
			"Approval Reference: #4125\n" +
			"Scope: bounded\n" +
			"Forward Reference: docs/release/reviews/review-be4525bc4797e972.md (RDD receipt is the local authority attestation)\n"
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
			"| Forward Reference | docs/release/reviews/review-be4525bc4797e972.md |\n"
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
			"Forward Reference: docs/release/reviews/review-be4525bc4797e972.md\n"
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
			"Forward Reference: docs/release/reviews/review-be4525bc4797e972.md\n"
		missing := receiptMissingParserFields(body)
		if len(missing) != 0 {
			t.Fatalf("expected complete colon-form receipt to have 0 missing fields, got %v", missing)
		}
	})
}

// receiptMissingParserFields mirrors the field-presence check the
// release gate performs at scripts/release-gate.sh for the
// size-exception receipt. It scans the receipt body for `^Field:`
// lines (one per required field) and returns the names of any fields
// not found. Returns nil when every required field is present.
//
// The required-field set was updated when the `Authority Requirement`
// field was renamed to `Forward Reference` (the receipt now points
// at the local RDD receipt at
// docs/release/reviews/review-be4525bc4797e972.md rather than at
// the removed `Authority: official` external-binding header).
func receiptMissingParserFields(body string) []string {
	required := []string{
		"Branch: ",
		"Commit: ",
		"Approval Reference: ",
		"Scope: ",
		"Expiration: ",
		"Forward Reference: ",
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

// ---- RDD receipt contract (Phase 16: local deterministic attestation) ----
//
// The release gate used to require `Authority: official` (a header
// flip that gated on a fictitious external review provider binding).
// That contract has been replaced with a deterministic, locally
// verifiable RDD receipt/evidence contract. The new model still
// fail-closes on merge, but it does NOT depend on an external
// provider: every required field is a value the operator can audit
// from the receipt alone.
//
// Required fields, all parsed from the receipt body as line-start
// matches (the gate's parser mirrors the same rules in bash):
//   1. Status: pass                        (exact line; any other value blocks)
//   2. Candidate Commit: <sha>             (sha must be reachable from HEAD)
//   3. Scope: <text mentioning the branch> (branch name must appear)
//   4. Verified Commands:                  (section header, followed by
//      `- <cmd>: PASS` entries — every entry must end with `: PASS`)
//   5. Unresolved Blocker Policy:          (header; value is free-form
//      so the operator can declare or waive)
//
// The receipt file lives at the same path the old Authority header
// occupied (docs/release/reviews/review-be4525bc4797e972.md) so the
// allow-list regex and reviewer diff surface stay unchanged.

// rddReceiptValidate mirrors the gate's bash validator. body is the
// receipt file contents; currentBranch is what RELEASE_GATE_BRANCH
// will be set to at gate time; headSHA is HEAD's SHA in the same
// repo where the receipt will be evaluated. Returns a list of
// validation failures (empty slice == receipt validates).
//
// The helper deliberately avoids shelling out to git: the gate
// subprocess tests cover the real `git merge-base --is-ancestor`
// invocation. The helper exists so the receipt shape can be unit
// tested with deterministic inputs.
func rddReceiptValidate(body, currentBranch, headSHA string) []string {
	var problems []string

	// 1. Status line must be exactly "Status: pass".
	statusLine := firstLineWithPrefix(body, "Status:")
	switch {
	case statusLine == "":
		problems = append(problems, "Status line missing")
	case strings.TrimSpace(statusLine) != "Status: pass":
		problems = append(problems, fmt.Sprintf("Status line must be exactly 'Status: pass' (got %q)", statusLine))
	}

	// 2. Candidate Commit line must reference a reachable SHA. The
	//    helper accepts ANY 40-char lowercase hex SHA — the gate
	//    performs the real ancestor check via git.
	commitLine := firstLineWithPrefix(body, "Candidate Commit:")
	switch {
	case commitLine == "":
		problems = append(problems, "Candidate Commit line missing")
	default:
		fields := strings.Fields(strings.TrimPrefix(commitLine, "Candidate Commit:"))
		if len(fields) == 0 {
			problems = append(problems, "Candidate Commit value missing")
		} else {
			sha := fields[0]
			if !looksLikeFullSHA(sha) {
				problems = append(problems, fmt.Sprintf("Candidate Commit %q is not a full 40-char SHA", sha))
			} else if sha != headSHA && !isReachabilityPlaceholder(sha) {
				// The shape helper cannot run git, so we only fail
				// the shape when the SHA is obviously wrong
				// (mismatched full-length SHA). The gate's bash
				// ancestor check is the source of truth for
				// reachability; the helper defers to that.
				problems = append(problems, fmt.Sprintf("Candidate Commit %q does not match expected HEAD %q (gate will additionally verify reachability)", sha, headSHA))
			}
		}
	}

	// 3. Scope line must mention the current branch.
	scopeLine := firstLineWithPrefix(body, "Scope:")
	switch {
	case scopeLine == "":
		problems = append(problems, "Scope line missing")
	case !strings.Contains(scopeLine, currentBranch):
		problems = append(problems, fmt.Sprintf("Scope line does not mention branch %q (got %q)", currentBranch, scopeLine))
	}

	// 4. Verified Commands section must exist with at least one
	//    entry, and every entry must end in ': PASS'.
	vcLine := firstLineWithPrefix(body, "Verified Commands:")
	switch {
	case vcLine == "":
		problems = append(problems, "Verified Commands section missing")
	default:
		entries := linesStartingWith(body, "  - ")
		if len(entries) == 0 {
			problems = append(problems, "Verified Commands section has no entries")
		}
		passRe := regexp.MustCompile(`^  - .*:\s*PASS\s*$`)
		failRe := regexp.MustCompile(`^  - .*:\s*FAIL`)
		for _, e := range entries {
			if failRe.MatchString(e) {
				problems = append(problems, fmt.Sprintf("Verified Commands entry reports FAIL (gate is fail-closed): %q", e))
			} else if !passRe.MatchString(e) {
				problems = append(problems, fmt.Sprintf("Verified Commands entry must end with ': PASS' (got %q)", e))
			}
		}
	}

	// 5. Unresolved Blocker Policy header must be present.
	if firstLineWithPrefix(body, "Unresolved Blocker Policy:") == "" {
		problems = append(problems, "Unresolved Blocker Policy line missing")
	}

	return problems
}

// firstLineWithPrefix returns the first line that begins with prefix
// (after stripping trailing \r). Returns "" if no such line exists.
func firstLineWithPrefix(body, prefix string) string {
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	return ""
}

// linesStartingWith returns every line whose first non-empty column
// matches prefix (used to enumerate Verified Commands entries).
func linesStartingWith(body, prefix string) []string {
	var out []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, prefix) {
			out = append(out, line)
		}
	}
	return out
}

// looksLikeFullSHA returns true iff s is exactly 40 lowercase hex
// characters, matching the gate's bash `git rev-parse` output shape.
func looksLikeFullSHA(s string) bool {
	if len(s) != 40 {
		return false
	}
	for _, c := range s {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// isReachabilityPlaceholder is true for SHA-like placeholders the
// test harness may substitute before the receipt is amended with
// the real commit SHA. The gate does not see these values because
// the helper rewrites the file before commit.
func isReachabilityPlaceholder(s string) bool {
	return s == "PLACEHOLDER_SHA"
}

// TestRDDReceiptShapeHelper is the fast unit-level test for the
// receipt shape. It does NOT exercise the gate subprocess; that
// coverage lives in TestGateValidatesRDDReceipt below. This test
// pins the helper so a future parser regression surfaces here.
func TestRDDReceiptShapeHelper(t *testing.T) {
	const branch = "feature/close-fetch-resilience-release-gates-exception"
	const head = "0123456789abcdef0123456789abcdef01234567"

	cases := []struct {
		name       string
		body       string
		wantErrors []string // substrings; missing means "no problem reported"
	}{
		{
			name: "complete-pass-receipt",
			body: makeValidRDDPassReceipt(branch, head),
		},
		{
			name: "missing-status",
			body: "# RDD Receipt\n" +
				"Candidate Commit: " + head + "\n" +
				"Scope: " + branch + "\n" +
				"Verified Commands:\n  - go build ./...: PASS\n" +
				"Unresolved Blocker Policy: none\n",
			wantErrors: []string{"Status line missing"},
		},
		{
			name: "status-pending-blocks",
			body: "# RDD Receipt\n" +
				"Status: pending\n" +
				"Candidate Commit: " + head + "\n" +
				"Scope: " + branch + "\n" +
				"Verified Commands:\n  - go build ./...: PASS\n" +
				"Unresolved Blocker Policy: none\n",
			wantErrors: []string{"Status line must be exactly"},
		},
		{
			name: "status-fail-blocks",
			body: "# RDD Receipt\n" +
				"Status: fail\n" +
				"Candidate Commit: " + head + "\n" +
				"Scope: " + branch + "\n" +
				"Verified Commands:\n  - go build ./...: PASS\n" +
				"Unresolved Blocker Policy: none\n",
			wantErrors: []string{"Status line must be exactly"},
		},
		{
			name: "candidate-commit-mismatches-head",
			body: "# RDD Receipt\n" +
				"Status: pass\n" +
				"Candidate Commit: ffffffffffffffffffffffffffffffffffffffff\n" +
				"Scope: " + branch + "\n" +
				"Verified Commands:\n  - go build ./...: PASS\n" +
				"Unresolved Blocker Policy: none\n",
			wantErrors: []string{"ffffffffffffffffffffffffffffffffffffffff"},
		},
		{
			name: "candidate-commit-not-full-sha",
			body: "# RDD Receipt\n" +
				"Status: pass\n" +
				"Candidate Commit: HEAD\n" +
				"Scope: " + branch + "\n" +
				"Verified Commands:\n  - go build ./...: PASS\n" +
				"Unresolved Blocker Policy: none\n",
			wantErrors: []string{"not a full 40-char SHA"},
		},
		{
			name: "scope-omits-branch",
			body: "# RDD Receipt\n" +
				"Status: pass\n" +
				"Candidate Commit: " + head + "\n" +
				"Scope: release-note (no branch token)\n" +
				"Verified Commands:\n  - go build ./...: PASS\n" +
				"Unresolved Blocker Policy: none\n",
			wantErrors: []string{"Scope line does not mention branch"},
		},
		{
			name: "missing-verified-commands-section",
			body: "# RDD Receipt\n" +
				"Status: pass\n" +
				"Candidate Commit: " + head + "\n" +
				"Scope: " + branch + "\n" +
				"Unresolved Blocker Policy: none\n",
			wantErrors: []string{"Verified Commands section missing"},
		},
		{
			name: "verified-commands-empty-section",
			body: "# RDD Receipt\n" +
				"Status: pass\n" +
				"Candidate Commit: " + head + "\n" +
				"Scope: " + branch + "\n" +
				"Verified Commands:\n" +
				"Unresolved Blocker Policy: none\n",
			wantErrors: []string{"Verified Commands section has no entries"},
		},
		{
			name: "verified-commands-with-fail-block",
			body: "# RDD Receipt\n" +
				"Status: pass\n" +
				"Candidate Commit: " + head + "\n" +
				"Scope: " + branch + "\n" +
				"Verified Commands:\n" +
				"  - go build ./...: PASS\n" +
				"  - go test -race ./...: FAIL\n" +
				"Unresolved Blocker Policy: none\n",
			wantErrors: []string{"FAIL (gate is fail-closed)"},
		},
		{
			name: "verified-commands-without-pass-result",
			body: "# RDD Receipt\n" +
				"Status: pass\n" +
				"Candidate Commit: " + head + "\n" +
				"Scope: " + branch + "\n" +
				"Verified Commands:\n" +
				"  - go build ./...: ok\n" +
				"Unresolved Blocker Policy: none\n",
			wantErrors: []string{"must end with ': PASS'"},
		},
		{
			name: "missing-unresolved-blocker-policy",
			body: "# RDD Receipt\n" +
				"Status: pass\n" +
				"Candidate Commit: " + head + "\n" +
				"Scope: " + branch + "\n" +
				"Verified Commands:\n  - go build ./...: PASS\n",
			wantErrors: []string{"Unresolved Blocker Policy line missing"},
		},
	}

	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			got := rddReceiptValidate(tt.body, branch, head)
			if len(tt.wantErrors) == 0 {
				if len(got) != 0 {
					t.Fatalf("expected receipt to validate, got problems: %v\nbody:\n%s", got, tt.body)
				}
				return
			}
			if len(got) == 0 {
				t.Fatalf("expected problems containing %v, got none\nbody:\n%s", tt.wantErrors, tt.body)
			}
			for _, want := range tt.wantErrors {
				found := false
				for _, g := range got {
					if strings.Contains(g, want) {
						found = true
						break
					}
				}
				if !found {
					t.Fatalf("expected problem containing %q, got %v\nbody:\n%s", want, got, tt.body)
				}
			}
		})
	}
}

// TestGateValidatesRDDReceipt drives the release-gate subprocess
// against the new RDD receipt contract. Each subtest exercises one
// failure mode or the happy path end-to-end through the bash
// validator. The receipt file path is fixed (the same one the old
// Authority header used) so the worktree allow-list and reviewer
// diff surface stay unchanged.
func TestGateValidatesRDDReceipt(t *testing.T) {
	const branch = "feature/close-fetch-resilience-release-gates-exception"

	t.Run("missing-receipt-file-fails", func(t *testing.T) {
		h := newHarness(t)
		// No receipt committed.
		exit, _, stderr := h.run("RELEASE_GATE_BRANCH=main", "BASE_REF=main")
		if exit == 0 {
			t.Fatalf("expected non-zero exit (receipt missing), got 0; stderr=%q", stderr)
		}
		if !strings.Contains(stderr, "receipt missing") &&
			!strings.Contains(stderr, "review placeholder missing") &&
			!strings.Contains(stderr, "review file missing") {
			t.Fatalf("expected stderr to mention missing receipt, got %q", stderr)
		}
	})

	t.Run("complete-pass-receipt-passes", func(t *testing.T) {
		h := newHarness(t)
		h.addValidRDDPassReceipt(branch)
		// Pin the branch env to match the receipt's Scope so the
		// branch-mention check finds it.
		exit, stdout, stderr := h.run(
			"RELEASE_GATE_BRANCH="+branch,
			"BASE_REF=main",
		)
		if exit != 0 {
			t.Fatalf("expected exit 0 (valid RDD receipt), got %d; stderr=%q", exit, stderr)
		}
		if !strings.Contains(stdout, "release-gate: PASS") {
			t.Fatalf("expected PASS line, got stdout=%q", stdout)
		}
	})

	t.Run("status-pending-fails", func(t *testing.T) {
		h := newHarness(t)
		h.commitFile("docs/release/reviews/review-be4525bc4797e972.md",
			"# RDD Receipt\n"+
				"Status: pending\n"+
				"Candidate Commit: ffffffffffffffffffffffffffffffffffffffff\n"+
				"Scope: "+branch+"\n"+
				"Verified Commands:\n"+
				"  - go build ./...: PASS\n"+
				"Unresolved Blocker Policy: none\n",
			"add pending receipt")
		exit, _, stderr := h.run(
			"RELEASE_GATE_BRANCH="+branch,
			"BASE_REF=main",
		)
		if exit == 0 {
			t.Fatal("expected non-zero exit (Status: pending blocks merge)")
		}
		if !strings.Contains(stderr, "Status") {
			t.Fatalf("expected stderr to mention Status, got %q", stderr)
		}
	})

	t.Run("verified-commands-with-fail-blocks", func(t *testing.T) {
		h := newHarness(t)
		h.commitFile("docs/release/reviews/review-be4525bc4797e972.md",
			"# RDD Receipt\n"+
				"Status: pass\n"+
				"Candidate Commit: ffffffffffffffffffffffffffffffffffffffff\n"+
				"Scope: "+branch+"\n"+
				"Verified Commands:\n"+
				"  - go build ./...: PASS\n"+
				"  - go test -race ./...: FAIL\n"+
				"Unresolved Blocker Policy: none\n",
			"add receipt with FAIL")
		exit, _, stderr := h.run(
			"RELEASE_GATE_BRANCH="+branch,
			"BASE_REF=main",
		)
		if exit == 0 {
			t.Fatal("expected non-zero exit (Verified Commands FAIL blocks merge)")
		}
		if !strings.Contains(stderr, "PASS") {
			t.Fatalf("expected stderr to mention PASS, got %q", stderr)
		}
	})

	t.Run("missing-unresolved-blocker-policy-fails", func(t *testing.T) {
		h := newHarness(t)
		h.commitFile("docs/release/reviews/review-be4525bc4797e972.md",
			"# RDD Receipt\n"+
				"Status: pass\n"+
				"Candidate Commit: ffffffffffffffffffffffffffffffffffffffff\n"+
				"Scope: "+branch+"\n"+
				"Verified Commands:\n"+
				"  - go build ./...: PASS\n",
			"add receipt without blocker policy")
		exit, _, stderr := h.run(
			"RELEASE_GATE_BRANCH="+branch,
			"BASE_REF=main",
		)
		if exit == 0 {
			t.Fatal("expected non-zero exit (Unresolved Blocker Policy missing)")
		}
		if !strings.Contains(stderr, "Blocker Policy") && !strings.Contains(stderr, "blocker") {
			t.Fatalf("expected stderr to mention Blocker Policy, got %q", stderr)
		}
	})

	t.Run("scope-does-not-mention-branch-fails", func(t *testing.T) {
		h := newHarness(t)
		h.commitFile("docs/release/reviews/review-be4525bc4797e972.md",
			"# RDD Receipt\n"+
				"Status: pass\n"+
				"Candidate Commit: ffffffffffffffffffffffffffffffffffffffff\n"+
				"Scope: some-other-candidate\n"+
				"Verified Commands:\n"+
				"  - go build ./...: PASS\n"+
				"Unresolved Blocker Policy: none\n",
			"add receipt with wrong scope")
		exit, _, stderr := h.run(
			"RELEASE_GATE_BRANCH="+branch,
			"BASE_REF=main",
		)
		if exit == 0 {
			t.Fatal("expected non-zero exit (Scope does not mention branch)")
		}
		if !strings.Contains(stderr, "Scope") && !strings.Contains(stderr, "scope") {
			t.Fatalf("expected stderr to mention Scope, got %q", stderr)
		}
	})
}

// TestGateRDDReceiptDoesNotRequireExternalAuthority verifies the
// Phase 16 RED gate: the previous release gate enforced
// `Authority: official`, a header that could only be flipped by a
// fictitious external review provider. The new RDD contract is
// satisfied entirely by a tracked receipt the operator authors in
// the repo. There is NO external binding — the receipt alone is the
// authority, and the gate parses it deterministically.
//
// This test pins that contract by asserting the gate PASSES on a
// minimal receipt that mentions neither "Authority" nor "official":
// the receipt's only authority surface is its Status, Candidate
// Commit, Scope, Verified Commands, and Unresolved Blocker Policy.
func TestGateRDDReceiptDoesNotRequireExternalAuthority(t *testing.T) {
	h := newHarness(t)
	const branch = "feature/close-fetch-resilience-release-gates-exception"
	receipt := "# RDD Receipt\n" +
		"Status: pass\n" +
		"Candidate Commit: PLACEHOLDER_SHA\n" +
		"Scope: local candidate (" + branch + ")\n" +
		"Verified Commands:\n" +
		"  - go build ./...: PASS\n" +
		"  - go test ./...: PASS\n" +
		"Unresolved Blocker Policy: none\n"
	h.commitFile("docs/release/reviews/review-be4525bc4797e972.md", receipt, "add receipt")
	// Two-commit flow (not amend) so the placeholder commit stays
	// reachable from HEAD — the gate's ancestor check requires it.
	receiptSHA := h.headSHA()
	mustWrite(h.t, h.repo, "docs/release/reviews/review-be4525bc4797e972.md",
		strings.Replace(receipt, "PLACEHOLDER_SHA", receiptSHA, 1))
	runGit(h.t, h.repo, "add", "docs/release/reviews/review-be4525bc4797e972.md")
	runGit(h.t, h.repo, "commit", "-m", "finalize RDD receipt with real Candidate Commit")

	// Hard assertion: the receipt body does NOT mention the old
	// external-authority header anywhere. If a future regression
	// silently re-introduces the external binding, this assertion
	// fails first.
	body, err := os.ReadFile(filepath.Join(h.repo, "docs/release/reviews/review-be4525bc4797e972.md"))
	if err != nil {
		t.Fatalf("read receipt: %v", err)
	}
	if strings.Contains(strings.ToLower(string(body)), "authority") {
		t.Fatalf("RDD receipt must not mention 'authority' — the local RDD contract replaced the fictitious external review binding. Body:\n%s", string(body))
	}
	if strings.Contains(strings.ToLower(string(body)), "official") {
		t.Fatalf("RDD receipt must not mention 'official' — that token belongs to the old external-binding model. Body:\n%s", string(body))
	}

	exit, stdout, stderr := h.run(
		"RELEASE_GATE_BRANCH="+branch,
		"BASE_REF=main",
	)
	if exit != 0 {
		t.Fatalf("expected exit 0 with locally-authored receipt, got %d; stderr=%q", exit, stderr)
	}
	if !strings.Contains(stdout, "release-gate: PASS") {
		t.Fatalf("expected PASS line, got stdout=%q", stdout)
	}
}
