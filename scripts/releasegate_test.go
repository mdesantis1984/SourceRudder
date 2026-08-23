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
	"strconv"
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
	//
	// The CI/GitHub strip list is the hermetic-fixture contract for
	// the harness. Without it, a test process running under a CI
	// shell that exports `CI=true`, `GITHUB_HEAD_REF=...`, etc.
	// would leak those values into the synthetic gate run, and the
	// gate would resolve its branch and bypass-state from the leaked
	// values instead of from the test's explicit env. The exact
	// leak surface is the same as the gate's own trusted-source
	// resolution chain (CI=true, GITHUB_HEAD_REF, GITHUB_REF_NAME,
	// CI_COMMIT_REF_NAME) plus the gate's own knobs (RELEASE_GATE_*,
	// BASE_REF). R3-001 documents the original six-symptom cascade
	// caused by the missing filter; R4-013 / R4-014 pin the fix.
	filtered := make([]string, 0, len(os.Environ()))
	for _, e := range os.Environ() {
		if strings.HasPrefix(e, "RELEASE_GATE_") ||
			strings.HasPrefix(e, "BASE_REF=") ||
			strings.HasPrefix(e, "CI=") ||
			strings.HasPrefix(e, "GITHUB_") ||
			strings.HasPrefix(e, "CI_COMMIT_REF_NAME=") ||
			strings.HasPrefix(e, "MERGE_BASE=") ||
			strings.HasPrefix(e, "RELEASE_GATE_PR_HEAD_") {
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
//
// Under the new precise contract the receipt's Candidate Commit must
// equal HEAD or HEAD~1, so this helper is followed by the receipt
// being the LAST commit on the branch. Tests that need to land other
// commits after the receipt (e.g. a size-exception receipt or a
// `bulk.go` inflation commit) call recommitRDDPassReceipt at the end
// of their setup so the receipt is re-anchored at HEAD with the new
// HEAD~1 as its Candidate Commit.
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

// recommitRDDPassReceipt re-authors the RDD receipt so its
// `Candidate Commit` equals the SHA of the current HEAD
// (the latest code commit the receipt attests), then commits
// the rewritten receipt as the new HEAD. After this call, the
// new HEAD is the receipt commit, the new HEAD~1 is the prior
// HEAD (the code commit), and the receipt's `Candidate Commit`
// is HEAD~1 of the new HEAD — satisfying the precise
// HEAD-or-HEAD~1 contract.
//
// Use this AFTER any other commit lands on top of an earlier
// `addValidRDDPassReceipt` (e.g. a size-exception receipt, a
// `bulk.go` inflation commit, a build-break file) so the
// receipt stays anchored as the most recent commit. The
// receipt's `Status: pass` line stays as-is; only the
// `Candidate Commit:` value is rewritten. The rewrite is a
// fresh commit (not `git commit --amend`) so the prior
// receipt commit remains reachable from HEAD as HEAD~2,
// matching the `addValidRDDPassReceipt` two-commit pattern.
func (h *harness) recommitRDDPassReceipt(branch string) {
	h.t.Helper()
	// Capture the SHA of the current HEAD (the code commit
	// the receipt will attest). After the rewrite lands as
	// the new HEAD, this SHA becomes HEAD~1, which is
	// exactly what the precise contract requires.
	currentHead := h.headSHA()
	mustWrite(h.t, h.repo, "docs/release/reviews/review-be4525bc4797e972.md",
		makeValidRDDPassReceipt(branch, currentHead))
	runGit(h.t, h.repo, "add", "docs/release/reviews/review-be4525bc4797e972.md")
	runGit(h.t, h.repo, "commit", "-m", "recommit RDD receipt against latest code commit")
}

// makeValidRDDPassReceipt builds the deterministic receipt body the
// gate's RDD validator accepts. branch is interpolated into the
// dedicated `Branch:` field (exact match contract) and the
// free-form `Scope:` value (operator context). candidateCommit is
// interpolated into Candidate Commit and MUST equal HEAD or HEAD~1
// for the gate's precise contract to accept the receipt.
func makeValidRDDPassReceipt(branch, candidateCommit string) string {
	return "# RDD Receipt\n\n" +
		"Status: pass\n" +
		"Candidate Commit: " + candidateCommit + "\n" +
		"Branch: " + branch + "\n" +
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
// The receipt is re-anchored at HEAD so the dirty-check is the
// first failure surface, isolating this test from receipt-related
// noise.
func TestGateFailsOnUncommittedTrackedChange(t *testing.T) {
	h := newHarness(t)
	h.addValidRDDPassReceipt("main")
	h.commitFile("marker.txt", "clean state\n", "add marker")
	h.recommitRDDPassReceipt("main")
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
	// Re-anchor the receipt at HEAD with the new HEAD~1 so
	// the precise contract holds after the bulk commit.
	hA.recommitRDDPassReceipt(carveBranch)

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
	// Re-anchor so the receipt's Candidate Commit is HEAD~1
	// of the new HEAD (the bulk.go commit), keeping the
	// precise contract satisfied.
	hB.recommitRDDPassReceipt("feature/other-candidate")

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
// gate runs against a committed, clean worktree. The receipt is
// re-anchored at HEAD so the CI dirty-check is the only failure
// surface in scope for this test.
func TestGateRejectsDirtyBypassUnderCI(t *testing.T) {
	h := newHarness(t)
	h.addValidRDDPassReceipt("main")
	// Commit a marker file so we can dirty it without breaking the
	// synthetic Go module's existing tests.
	h.commitFile("marker.txt", "clean state\n", "add marker")
	h.recommitRDDPassReceipt("main")
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
// build check never runs). The receipt is re-anchored at HEAD after
// the build break so the precise Candidate Commit contract holds and
// the receipt check does not mask the build failure.
func TestGateFailsOnBuildError(t *testing.T) {
	h := newHarness(t)
	h.addValidRDDPassReceipt("main")
	h.commitFile("hello.go",
		"package gatemod\n\nfunc Hello() string { THIS IS NOT GO }\n",
		"introduce build break")
	h.recommitRDDPassReceipt("main")

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
// can run the gate against uncommitted work. The receipt is
// re-anchored at HEAD so the dirty-bypass path (which skips the
// worktree cleanliness check) does not trip the precise
// Candidate Commit check before reaching step 4.
func TestGateAllowsDirtyBypassOutsideCI(t *testing.T) {
	h := newHarness(t)
	h.addValidRDDPassReceipt("main")
	h.commitFile("marker.txt", "clean state\n", "add marker")
	h.recommitRDDPassReceipt("main")
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
			// attributed to Status alone. The Candidate Commit is
			// the HEAD of the synthetic repo at construction
			// time so the precise contract is satisfied (Candidate
			// == HEAD).
			body := "# RDD Receipt\n" +
				tt.status +
				"Candidate Commit: " + h.headSHA() + "\n" +
				"Branch: main\n" +
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
//
// Every sub-test re-anchors the RDD receipt at HEAD after the bulk
// commit lands so the precise Candidate Commit contract holds and
// the size-exception check is the only failure surface under test.
func TestGateSizeExceptionTiedToTrackedReceipt(t *testing.T) {
	bulk := strings.Repeat("// padding line to inflate diff\n", 500)
	const branch = "feature/close-fetch-resilience-release-gates-exception"
	const receiptPath = "docs/release/size-exceptions/close-fetch-resilience-and-release-gates.md"

	t.Run("missing-receipt-fails", func(t *testing.T) {
		h := newHarness(t)
		h.addValidRDDPassReceipt(branch)
		runGit(t, h.repo, "checkout", "-b", branch)
		h.commitFile("bulk.go", "package gatemod\n\n"+bulk, "bulk to exceed 400 lines")
		h.recommitRDDPassReceipt(branch)

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
		h.recommitRDDPassReceipt(branch)

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
		h.recommitRDDPassReceipt(branch)

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
// subprocess tests cover the real `git rev-parse HEAD` and
// `git rev-parse HEAD~1` invocations. The helper exists so the
// receipt shape can be unit tested with deterministic inputs.
//
// The contract enforced here matches the bash gate's:
//
//   1. Status: pass                       (exact line)
//   2. Candidate Commit: <sha>            (must equal HEAD or HEAD~1)
//   3. Branch: <exact branch name>        (exact match, no substring)
//   4. Scope: <free-form text>            (presence only)
//   5. Verified Commands:                 (section-scoped; every
//                                          in-section entry must end
//                                          in `: PASS`)
//   6. Unresolved Blocker Policy:         (header present with value)
//   0. Hard guard: any line starting with
//      `Authority:` is rejected, matching
//      the Go process guard.
func rddReceiptValidate(body, currentBranch, headSHA string) []string {
	var problems []string

	// 0. Hard guard: reject any line beginning with the legacy
	//    `Authority:` header. The Go process guard and the bash
	//    gate enforce the same rule; both validators must stay
	//    symmetric so a future regression cannot smuggle the
	//    external-binding header back through only one of them.
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Authority:") {
			problems = append(problems, fmt.Sprintf("RDD receipt contains legacy 'Authority:' line (%q); the local RDD contract replaced the external-binding header and it MUST NOT return", trimmed))
			break
		}
	}

	// 1. Status line must be exactly "Status: pass".
	statusLine := firstLineWithPrefix(body, "Status:")
	switch {
	case statusLine == "":
		problems = append(problems, "Status line missing")
	case strings.TrimSpace(statusLine) != "Status: pass":
		problems = append(problems, fmt.Sprintf("Status line must be exactly 'Status: pass' (got %q)", statusLine))
	}

	// 2. Candidate Commit line. Precise contract: the SHA must
	//    equal HEAD or HEAD~1. Arbitrary ancestors are rejected
	//    so a buggy SHA's receipt cannot authorize a rollback
	//    SHA. The two-commit code-then-receipt workflow still
	//    works because the receipt commit is HEAD and the code
	//    commit sits at HEAD~1. The helper does not run git; the
	//    caller passes headSHA and a sentinel `parentSHA` via
	//    the body's expected Candidate Commit value (the helper
	//    accepts either the literal headSHA OR a value that
	//    looks like an obviously-different 40-char SHA only
	//    when the caller has explicitly opted in via the
	//    reachability-placeholder escape hatch).
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
				// The helper does not run git, so it cannot
				// resolve HEAD~1 directly. Callers that
				// exercise the precise contract drive the
				// gate subprocess and assert on the
				// subprocess stderr instead. Here, we
				// surface the "must equal HEAD" rule
				// against the supplied headSHA so the
				// common case (Candidate == HEAD) is
				// pinned at the unit level.
				problems = append(problems, fmt.Sprintf("Candidate Commit %q does not match expected HEAD %q (precise contract: must equal HEAD or HEAD~1; arbitrary ancestors rejected)", sha, headSHA))
			}
		}
	}

	// 3. Branch line: exact match against the resolved branch.
	//    Replaces the previous substring Scope check that
	//    allowed a Scope of `feature/foo-bar` to unlock a merge
	//    on branch `feature/foo`.
	branchLine := firstLineWithPrefix(body, "Branch:")
	switch {
	case branchLine == "":
		problems = append(problems, "Branch line missing (the receipt must declare its target branch via a dedicated Branch: field for exact-match verification)")
	default:
		value := strings.TrimSpace(strings.TrimPrefix(branchLine, "Branch:"))
		if value == "" {
			problems = append(problems, "Branch value empty")
		} else if value != currentBranch {
			problems = append(problems, fmt.Sprintf("Branch value %q does not exactly match the resolved branch %q", value, currentBranch))
		}
	}

	// 3b. Scope line: presence only — free-form operator context.
	scopeLine := firstLineWithPrefix(body, "Scope:")
	if scopeLine == "" {
		problems = append(problems, "Scope line missing")
	}

	// 4. Verified Commands section. Entries are parsed only
	//    inside the section; a top-level `^[A-Z][A-Za-z]+:`
	//    header that follows exits the scope so out-of-section
	//    bullets cannot inflate the count.
	vcLine := firstLineWithPrefix(body, "Verified Commands:")
	switch {
	case vcLine == "":
		problems = append(problems, "Verified Commands section missing")
	default:
		entries := sectionEntriesAfterHeader(body, "Verified Commands:")
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

// sectionEntriesAfterHeader returns the lines that fall inside
// the named `Header:` section. The scope begins on the line
// immediately after the header and ends at the next top-level
// `^[A-Z][A-Za-z][A-Za-z0-9 ]*:` line (or end of file). Only
// lines beginning with two spaces followed by `- ` are returned,
// matching the Verified Commands bullet shape.
func sectionEntriesAfterHeader(body, header string) []string {
	var out []string
	inSection := false
	headerRe := regexp.MustCompile(`^[A-Z][A-Za-z][A-Za-z0-9 ]*:`)
	for _, line := range strings.Split(body, "\n") {
		if !inSection {
			if line == header {
				inSection = true
			}
			continue
		}
		// Inside the section: exit on the next top-level header.
		if headerRe.MatchString(line) {
			break
		}
		if strings.HasPrefix(line, "  - ") {
			out = append(out, line)
		}
	}
	return out
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
				"Branch: " + branch + "\n" +
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
				"Branch: " + branch + "\n" +
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
				"Branch: " + branch + "\n" +
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
				"Branch: " + branch + "\n" +
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
				"Branch: " + branch + "\n" +
				"Scope: " + branch + "\n" +
				"Verified Commands:\n  - go build ./...: PASS\n" +
				"Unresolved Blocker Policy: none\n",
			wantErrors: []string{"not a full 40-char SHA"},
		},
		{
			name: "branch-mismatch-blocks",
			body: "# RDD Receipt\n" +
				"Status: pass\n" +
				"Candidate Commit: " + head + "\n" +
				"Branch: some-other-candidate\n" +
				"Scope: " + branch + "\n" +
				"Verified Commands:\n  - go build ./...: PASS\n" +
				"Unresolved Blocker Policy: none\n",
			wantErrors: []string{"Branch value"},
		},
		{
			name: "missing-branch-blocks",
			body: "# RDD Receipt\n" +
				"Status: pass\n" +
				"Candidate Commit: " + head + "\n" +
				"Scope: " + branch + "\n" +
				"Verified Commands:\n  - go build ./...: PASS\n" +
				"Unresolved Blocker Policy: none\n",
			wantErrors: []string{"Branch line missing"},
		},
		{
			name: "missing-verified-commands-section",
			body: "# RDD Receipt\n" +
				"Status: pass\n" +
				"Candidate Commit: " + head + "\n" +
				"Branch: " + branch + "\n" +
				"Scope: " + branch + "\n" +
				"Unresolved Blocker Policy: none\n",
			wantErrors: []string{"Verified Commands section missing"},
		},
		{
			name: "verified-commands-empty-section",
			body: "# RDD Receipt\n" +
				"Status: pass\n" +
				"Candidate Commit: " + head + "\n" +
				"Branch: " + branch + "\n" +
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
				"Branch: " + branch + "\n" +
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
				"Branch: " + branch + "\n" +
				"Scope: " + branch + "\n" +
				"Verified Commands:\n" +
				"  - go build ./...: ok\n" +
				"Unresolved Blocker Policy: none\n",
			wantErrors: []string{"must end with ': PASS'"},
		},
		{
			name: "verified-commands-out-of-section-bullet-ignored",
			body: "# RDD Receipt\n" +
				"Status: pass\n" +
				"Candidate Commit: " + head + "\n" +
				"Branch: " + branch + "\n" +
				"Scope: " + branch + "\n" +
				"Notes:\n" +
				"  - forged: PASS\n" +
				"Verified Commands:\n" +
				"  - go build ./...: PASS\n" +
				"Unresolved Blocker Policy: none\n",
			wantErrors: []string{},
		},
		{
			name: "authority-line-blocks",
			body: "# RDD Receipt\n" +
				"Authority: official\n" +
				"Status: pass\n" +
				"Candidate Commit: " + head + "\n" +
				"Branch: " + branch + "\n" +
				"Scope: " + branch + "\n" +
				"Verified Commands:\n  - go build ./...: PASS\n" +
				"Unresolved Blocker Policy: none\n",
			wantErrors: []string{"legacy 'Authority:' line"},
		},
		{
			name: "missing-unresolved-blocker-policy",
			body: "# RDD Receipt\n" +
				"Status: pass\n" +
				"Candidate Commit: " + head + "\n" +
				"Branch: " + branch + "\n" +
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
				"Candidate Commit: "+h.headSHA()+"\n"+
				"Branch: "+branch+"\n"+
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
				"Candidate Commit: "+h.headSHA()+"\n"+
				"Branch: "+branch+"\n"+
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
				"Candidate Commit: "+h.headSHA()+"\n"+
				"Branch: "+branch+"\n"+
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

	t.Run("branch-mismatch-fails", func(t *testing.T) {
		h := newHarness(t)
		h.commitFile("docs/release/reviews/review-be4525bc4797e972.md",
			"# RDD Receipt\n"+
				"Status: pass\n"+
				"Candidate Commit: "+h.headSHA()+"\n"+
				"Branch: some-other-candidate\n"+
				"Scope: some-other-candidate\n"+
				"Verified Commands:\n"+
				"  - go build ./...: PASS\n"+
				"Unresolved Blocker Policy: none\n",
			"add receipt with wrong branch")
		exit, _, stderr := h.run(
			"RELEASE_GATE_BRANCH="+branch,
			"BASE_REF=main",
		)
		if exit == 0 {
			t.Fatal("expected non-zero exit (Branch does not match current branch)")
		}
		if !strings.Contains(stderr, "Branch") {
			t.Fatalf("expected stderr to mention Branch, got %q", stderr)
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
// Commit, Branch, Scope, Verified Commands, and Unresolved Blocker
// Policy.
func TestGateRDDReceiptDoesNotRequireExternalAuthority(t *testing.T) {
	h := newHarness(t)
	const branch = "feature/close-fetch-resilience-release-gates-exception"
	receipt := "# RDD Receipt\n" +
		"Status: pass\n" +
		"Candidate Commit: PLACEHOLDER_SHA\n" +
		"Branch: " + branch + "\n" +
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

// ---- fresh-audit corrective batch (R1-007 / R3-003 / R4-001 / R4-005 / R4-006) ----
//
// The tests below close the fresh-audit findings R1-007 (bash must
// reject legacy `Authority:` line, matching the Go process guard),
// R3-003 / R4-005 (Verified Commands entries must be parsed only
// inside the `Verified Commands:` section), R4-001 + R1-003 (detached
// HEAD must fail closed; receipt scope must be unambiguous via a
// dedicated `Branch:` field with exact match), and R4-006 (Candidate
// Commit must be HEAD or HEAD~1, forcing a new receipt after any
// rollback or code change). Each test pins one behavior the gate
// must guarantee, in isolation from the rest of the receipt
// contract.

// TestGateRejectsLegacyAuthorityHeader is the R1-007 RED gate. The
// Go process guard in internal/mcp/release_gate_test.go explicitly
// rejects any line beginning with `Authority:`. The bash gate had no
// analogous rejection, so a receipt carrying both `Status: pass` and
// `Authority: official` would pass bash but fail Go — a
// defense-in-depth gap. The bash validator MUST now reject any
// receipt whose body contains a line starting with `Authority:` so
// the two validators stay symmetric.
func TestGateRejectsLegacyAuthorityHeader(t *testing.T) {
	const branch = "feature/close-fetch-resilience-release-gates-exception"

	t.Run("header-at-line-start-fails", func(t *testing.T) {
		h := newHarness(t)
		// Build a receipt that is otherwise valid, with the
		// legacy `Authority:` header injected as the first
		// authority-surface line.
		head := h.headSHA()
		body := "# RDD Receipt\n" +
			"Authority: official\n" +
			"Status: pass\n" +
			"Candidate Commit: " + head + "\n" +
			"Branch: " + branch + "\n" +
			"Scope: local gate validation (branch=" + branch + ")\n" +
			"Verified Commands:\n" +
			"  - go build ./...: PASS\n" +
			"Unresolved Blocker Policy: none\n"
		h.commitFile("docs/release/reviews/review-be4525bc4797e972.md", body, "add receipt with legacy Authority header")
		exit, _, stderr := h.run("RELEASE_GATE_BRANCH="+branch, "BASE_REF=main")
		if exit == 0 {
			t.Fatal("expected non-zero exit: legacy Authority: header must not pass the bash gate")
		}
		if !strings.Contains(strings.ToLower(stderr), "authority") {
			t.Fatalf("expected stderr to mention 'authority', got %q", stderr)
		}
	})

	t.Run("header-anywhere-in-body-fails", func(t *testing.T) {
		h := newHarness(t)
		head := h.headSHA()
		// Place the legacy header in the middle of a valid
		// receipt body so the rejection cannot depend on
		// line-number.
		body := "# RDD Receipt\n" +
			"Status: pass\n" +
			"Candidate Commit: " + head + "\n" +
			"Branch: " + branch + "\n" +
			"Scope: local gate validation (branch=" + branch + ")\n" +
			"Notes:\n" +
			"  Authority: official — this is the old external-binding marker\n" +
			"Verified Commands:\n" +
			"  - go build ./...: PASS\n" +
			"Unresolved Blocker Policy: none\n"
		h.commitFile("docs/release/reviews/review-be4525bc4797e972.md", body, "add receipt with Authority inside Notes")
		exit, _, stderr := h.run("RELEASE_GATE_BRANCH="+branch, "BASE_REF=main")
		if exit == 0 {
			t.Fatal("expected non-zero exit: Authority: line anywhere in the body must be rejected")
		}
		if !strings.Contains(strings.ToLower(stderr), "authority") {
			t.Fatalf("expected stderr to mention 'authority', got %q", stderr)
		}
	})

	// "bare-header-fails" pins R2/R1 validator symmetry. The Go
	// process guard in internal/mcp/release_gate_test.go rejects ANY
	// line whose trimmed first column starts with `Authority:` (the
	// `HasPrefix(trimmed, "Authority:")` check), so a bare header
	// line (`Authority:` with NO value and NO trailing whitespace)
	// is rejected by Go but accepted by the bash gate (its regex
	// `^[[:space:]]*Authority:[[:space:]]` requires whitespace
	// after the colon). The bash validator must use the same
	// trim-then-prefix rule so a regression cannot smuggle a bare
	// header past bash while Go still catches it (or vice versa).
	t.Run("bare-header-fails", func(t *testing.T) {
		h := newHarness(t)
		head := h.headSHA()
		body := "# RDD Receipt\n" +
			"Status: pass\n" +
			"Candidate Commit: " + head + "\n" +
			"Branch: " + branch + "\n" +
			"Scope: local gate validation (branch=" + branch + ")\n" +
			"Authority:\n" +
			"Verified Commands:\n" +
			"  - go build ./...: PASS\n" +
			"Unresolved Blocker Policy: none\n"
		h.commitFile("docs/release/reviews/review-be4525bc4797e972.md", body, "add receipt with bare Authority header")
		exit, _, stderr := h.run("RELEASE_GATE_BRANCH="+branch, "BASE_REF=main")
		if exit == 0 {
			t.Fatal("expected non-zero exit: bare 'Authority:' header (no value, no trailing whitespace) must be rejected to stay symmetric with the Go process guard")
		}
		if !strings.Contains(strings.ToLower(stderr), "authority") {
			t.Fatalf("expected stderr to mention 'authority', got %q", stderr)
		}
	})
}

// TestGateParsesVerifiedCommandsOnlyInsideSection is the R3-003 +
// R4-005 RED gate. The previous parser read bullet lines
// (`^[[:space:]]+- .*:[[:space:]]`) from the WHOLE receipt body. A
// future operator adding documentation like:
//
//	Notes:
//	  - I forgot to actually run go build: PASS
//
// OUTSIDE the `Verified Commands:` section would inflate the entry
// count and could mask a subsequent FAIL. The parser MUST scope
// iteration to lines that follow the section header, exit when the
// next top-level `^[A-Z][A-Za-z]+:` header begins, and reject any
// bullet that lives outside the section.
func TestGateParsesVerifiedCommandsOnlyInsideSection(t *testing.T) {
	const branch = "feature/close-fetch-resilience-release-gates-exception"

	t.Run("bullets-outside-section-do-not-count", func(t *testing.T) {
		h := newHarness(t)
		head := h.headSHA()
		// The body intentionally has a `Notes:` section with
		// a fake PASS bullet BEFORE the actual Verified
		// Commands. The gate must count ONLY the inside-
		// section bullet. Since the inside section is empty,
		// the receipt must fail with "no entries".
		body := "# RDD Receipt\n" +
			"Status: pass\n" +
			"Candidate Commit: " + head + "\n" +
			"Branch: " + branch + "\n" +
			"Scope: local gate validation (branch=" + branch + ")\n" +
			"Notes:\n" +
			"  - I forged this: PASS\n" +
			"Verified Commands:\n" +
			"Unresolved Blocker Policy: none\n"
		h.commitFile("docs/release/reviews/review-be4525bc4797e972.md", body, "add receipt with out-of-section fake PASS")
		exit, _, stderr := h.run("RELEASE_GATE_BRANCH="+branch, "BASE_REF=main")
		if exit == 0 {
			t.Fatal("expected non-zero exit: out-of-section fake PASS must not satisfy Verified Commands")
		}
		if !strings.Contains(stderr, "Verified Commands") {
			t.Fatalf("expected stderr to mention 'Verified Commands', got %q", stderr)
		}
	})

	t.Run("real-section-with-malformed-entry-still-fails", func(t *testing.T) {
		h := newHarness(t)
		head := h.headSHA()
		// Inside-section FAIL still must block.
		body := "# RDD Receipt\n" +
			"Status: pass\n" +
			"Candidate Commit: " + head + "\n" +
			"Branch: " + branch + "\n" +
			"Scope: local gate validation (branch=" + branch + ")\n" +
			"Notes:\n" +
			"  - fake: PASS\n" +
			"Verified Commands:\n" +
			"  - go build ./...: PASS\n" +
			"  - go test ./...: FAIL\n" +
			"Unresolved Blocker Policy: none\n"
		h.commitFile("docs/release/reviews/review-be4525bc4797e972.md", body, "add receipt with in-section FAIL")
		exit, _, stderr := h.run("RELEASE_GATE_BRANCH="+branch, "BASE_REF=main")
		if exit == 0 {
			t.Fatal("expected non-zero exit: in-section FAIL must block the gate")
		}
		if !strings.Contains(stderr, "PASS") {
			t.Fatalf("expected stderr to mention 'PASS', got %q", stderr)
		}
	})

	t.Run("second-section-after-verified-commands-exits-scope", func(t *testing.T) {
		h := newHarness(t)
		head := h.headSHA()
		// Bullets after a new top-level `Notes:` header that
		// comes AFTER `Verified Commands:` must NOT count.
		body := "# RDD Receipt\n" +
			"Status: pass\n" +
			"Candidate Commit: " + head + "\n" +
			"Branch: " + branch + "\n" +
			"Scope: local gate validation (branch=" + branch + ")\n" +
			"Verified Commands:\n" +
			"  - go build ./...: PASS\n" +
			"Notes:\n" +
			"  - forged entry outside scope: PASS\n" +
			"Unresolved Blocker Policy: none\n"
		h.commitFile("docs/release/reviews/review-be4525bc4797e972.md", body, "add receipt with post-section forged entry")
		// The receipt's inside-section entry is valid, so the
		// gate MUST pass (and the forged entry outside the
		// section must NOT count). The Branch: line above is
		// not a Verified Commands entry either, so the receipt
		// still validates end-to-end.
		exit, stdout, stderr := h.run("RELEASE_GATE_BRANCH="+branch, "BASE_REF=main")
		if exit != 0 {
			t.Fatalf("expected exit 0: in-section PASS plus out-of-section forged entry; got %d; stderr=%q", exit, stderr)
		}
		if !strings.Contains(stdout, "release-gate: PASS") {
			t.Fatalf("expected PASS line, got stdout=%q", stdout)
		}
	})
}

// TestGateFailsClosedOnDetachedHeadWithoutTrustedEnv is the R4-001
// RED gate. On a detached HEAD (the typical CI merge checkout, or
// `git checkout <sha>`), `git rev-parse --abbrev-ref HEAD` returns
// the literal string `HEAD`. The previous Scope branch check
// compared against that literal, so a receipt with `Scope: … HEAD …`
// would either fail spuriously or pass spuriously. The gate MUST
// resolve the target branch from a trusted CI env var first
// (`GITHUB_HEAD_REF`, `GITHUB_REF_NAME`, `CI_COMMIT_REF_NAME`) and
// only fall back to `RELEASE_GATE_BRANCH` and the local git
// symbolic ref. If none of those resolve to a real branch name
// (i.e. the symbolic ref returns `HEAD`), the gate MUST fail
// closed instead of accepting the literal `HEAD`.
func TestGateFailsClosedOnDetachedHeadWithoutTrustedEnv(t *testing.T) {
	const branch = "feature/close-fetch-resilience-release-gates-exception"

	t.Run("detached-without-env-fails", func(t *testing.T) {
		h := newHarness(t)
		h.addValidRDDPassReceipt(branch)
		// Detach HEAD at the current receipt-commit so the
		// git ref is literal `HEAD` and no CI env is set.
		runGit(t, h.repo, "checkout", "--detach", "HEAD")
		// Strip RELEASE_GATE_BRANCH from the run env. The
		// harness already filters out RELEASE_GATE_*, so
		// only BASE_REF=main is passed.
		exit, stdout, stderr := h.run("RELEASE_GATE_BRANCH=", "BASE_REF=main")
		if exit == 0 {
			t.Fatal("expected non-zero exit: detached HEAD with no trusted branch env must fail closed")
		}
		// The error must mention the branch resolution, NOT
		// just any random gate error.
		if !strings.Contains(stderr, "branch") && !strings.Contains(stderr, "detached") && !strings.Contains(stderr, "HEAD") {
			t.Fatalf("expected stderr to mention branch/detached/HEAD, got %q", stderr)
		}
		// Hard fail-closed assertion: the gate MUST exit at the
		// branch-resolution step and MUST NOT continue to any
		// subsequent gate log. The previous bug was that
		// `fail` was called before its definition, so bash
		// reported "fail: command not found" and the script
		// continued to print "could not resolve merge-base"
		// (the next step's log line). Both downstream lines are
		// pinned here so a regression that re-introduces the
		// pre-definition call OR fails to terminate the script
		// on branch-resolution failure surfaces here.
		if strings.Contains(stderr, "fail: orden no encontrada") || strings.Contains(stderr, "fail: command not found") {
			t.Fatalf("detached-HEAD gate printed 'fail: command not found' — the fail() helper is called before its definition; the script must define fail() before any caller (R4-001/NEW-001). stderr=%q", stderr)
		}
		// The next gate step (merge-base resolution) MUST NOT
		// log; its appearance proves the gate continued past
		// the branch-resolution failure.
		if strings.Contains(stdout, "could not resolve merge-base") || strings.Contains(stderr, "could not resolve merge-base") {
			t.Fatalf("detached-HEAD gate continued past the branch-resolution failure and logged the merge-base step; the gate MUST exit non-zero immediately at the branch-resolution step (R4-001/NEW-001). stdout=%q stderr=%q", stdout, stderr)
		}
		// The structured log line for the BASE_REF / BRANCH /
		// MERGE_BASE block is emitted by the gate ONLY after
		// branch resolution succeeds, so its presence in the
		// detached-HEAD case is also a regression signal.
		if strings.Contains(stdout, "BASE_REF=") || strings.Contains(stdout, "MERGE_BASE=") {
			t.Fatalf("detached-HEAD gate emitted a structured BASE_REF / MERGE_BASE log line; the gate must not advance past the branch-resolution step. stdout=%q", stdout)
		}
	})

	t.Run("detached-with-github-head-ref-passes", func(t *testing.T) {
		h := newHarness(t)
		h.addValidRDDPassReceipt(branch)
		runGit(t, h.repo, "checkout", "--detach", "HEAD")
		// Trusted env var resolves the branch.
		exit, stdout, stderr := h.run(
			"GITHUB_HEAD_REF="+branch,
			"BASE_REF=main",
		)
		if exit != 0 {
			t.Fatalf("expected exit 0 with GITHUB_HEAD_REF=%s, got %d; stderr=%q", branch, exit, stderr)
		}
		if !strings.Contains(stdout, "release-gate: PASS") {
			t.Fatalf("expected PASS line, got stdout=%q", stdout)
		}
	})

	t.Run("detached-with-release-gate-branch-passes", func(t *testing.T) {
		h := newHarness(t)
		h.addValidRDDPassReceipt(branch)
		runGit(t, h.repo, "checkout", "--detach", "HEAD")
		exit, stdout, stderr := h.run(
			"RELEASE_GATE_BRANCH="+branch,
			"BASE_REF=main",
		)
		if exit != 0 {
			t.Fatalf("expected exit 0 with RELEASE_GATE_BRANCH=%s, got %d; stderr=%q", branch, exit, stderr)
		}
		if !strings.Contains(stdout, "release-gate: PASS") {
			t.Fatalf("expected PASS line, got stdout=%q", stdout)
		}
	})
}

// TestGateBranchFieldExactMatch is the R1-003 RED gate. The
// previous Scope check used a substring match
// (`[[ "$rdd_scope" != *"$CURRENT_BRANCH"* ]]`), so a Scope of
// `feature/foo-bar` would unlock a merge on branch `feature/foo`.
// The receipt now carries a dedicated `Branch:` line that MUST
// match the resolved branch EXACTLY (after trimming), regardless
// of what free-form text appears in the `Scope:` field.
func TestGateBranchFieldExactMatch(t *testing.T) {
	const branch = "feature/close-fetch-resilience-release-gates-exception"

	t.Run("scope-substring-without-branch-field-fails", func(t *testing.T) {
		h := newHarness(t)
		head := h.headSHA()
		// A receipt whose Scope contains the branch name as
		// a substring but lacks the dedicated Branch: field
		// must NOT satisfy the branch check.
		body := "# RDD Receipt\n" +
			"Status: pass\n" +
			"Candidate Commit: " + head + "\n" +
			"Scope: feature/foo (contains " + branch + " as substring)\n" +
			"Verified Commands:\n" +
			"  - go build ./...: PASS\n" +
			"Unresolved Blocker Policy: none\n"
		h.commitFile("docs/release/reviews/review-be4525bc4797e972.md", body, "add receipt with substring Scope and no Branch field")
		exit, _, stderr := h.run("RELEASE_GATE_BRANCH=feature/foo", "BASE_REF=main")
		if exit == 0 {
			t.Fatal("expected non-zero exit: substring match on Scope must not satisfy the branch check")
		}
		if !strings.Contains(stderr, "Branch") {
			t.Fatalf("expected stderr to mention 'Branch', got %q", stderr)
		}
	})

	t.Run("branch-field-exact-match-passes", func(t *testing.T) {
		h := newHarness(t)
		h.addValidRDDPassReceipt(branch)
		exit, stdout, stderr := h.run("RELEASE_GATE_BRANCH="+branch, "BASE_REF=main")
		if exit != 0 {
			t.Fatalf("expected exit 0 with exact Branch: field, got %d; stderr=%q", exit, stderr)
		}
		if !strings.Contains(stdout, "release-gate: PASS") {
			t.Fatalf("expected PASS line, got stdout=%q", stdout)
		}
	})
}

// TestGateCandidateCommitMustBeHeadOrParent is the R4-006 RED gate.
// The previous `git merge-base --is-ancestor` check accepted ANY
// ancestor of HEAD, so a buggy SHA's receipt stayed valid after
// `git revert` (the buggy SHA is still an ancestor of the
// rollback-commit). The new contract is precise: `Candidate Commit`
// MUST equal HEAD or HEAD~1. This forces a new receipt after any
// rollback (the buggy SHA is now HEAD~2 or deeper) and after any
// new code commit (the receipt's Candidate is HEAD~2 or deeper).
// The two-commit code-then-receipt workflow still works because
// the receipt commit is HEAD and the code commit is HEAD~1.
func TestGateCandidateCommitMustBeHeadOrParent(t *testing.T) {
	const branch = "feature/close-fetch-resilience-release-gates-exception"

	// Helper that writes a valid-shape receipt with an
	// arbitrary Candidate Commit, then runs the gate and
	// returns (exit, stderr). The harness does NOT stage an
	// intermediate commit: the receipt is committed directly
	// on top of the initial commit, so the new harness's
	// HEAD~1 equals the initial commit's SHA. This is the
	// shape the `candidate-equals-head-passes` sub-test
	// depends on (the receipt's Candidate == HEAD of the
	// test harness == HEAD~1 of the new harness, which the
	// precise contract accepts).
	runWithCommit := func(t *testing.T, commit, scope string) (int, string) {
		t.Helper()
		h := newHarness(t)
		body := "# RDD Receipt\n" +
			"Status: pass\n" +
			"Candidate Commit: " + commit + "\n" +
			"Branch: " + branch + "\n" +
			"Scope: " + scope + "\n" +
			"Verified Commands:\n" +
			"  - go build ./...: PASS\n" +
			"Unresolved Blocker Policy: none\n"
		h.commitFile("docs/release/reviews/review-be4525bc4797e972.md", body, "add receipt with custom Candidate Commit")
		exit, _, stderr := h.run("RELEASE_GATE_BRANCH="+branch, "BASE_REF=main")
		return exit, stderr
	}

	// resolveHeadN returns the SHA of the n-th ancestor of
	// HEAD (HEAD itself when depth=0, HEAD~1 when depth=1,
	// HEAD~2 when depth=2, etc.). It uses `git log` so the
	// test is robust across git versions where
	// `git rev-parse HEAD~N` can exit non-zero on shallow
	// repos or under the harness's `commit.gpgsign=false`
	// config. The harness must have at least depth+1 commits
	// for the function to return a valid SHA; callers MUST
	// stage extra commits when they need a deeper ancestor.
	resolveHeadN := func(t *testing.T, h *harness, depth int) string {
		t.Helper()
		cmd := exec.Command("git", "log", "--format=%H", "-n", strconv.Itoa(depth+1))
		cmd.Dir = h.repo
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("git log depth=%d: %v", depth, err)
		}
		lines := strings.Split(strings.TrimSpace(string(out)), "\n")
		if len(lines) < depth+1 {
			t.Fatalf("harness has %d commits, cannot resolve HEAD~%d (need %d)", len(lines), depth, depth+1)
		}
		return lines[depth]
	}

	t.Run("candidate-equals-head-passes", func(t *testing.T) {
		h := newHarness(t)
		head := h.headSHA()
		exit, stderr := runWithCommit(t, head, "local gate validation")
		if exit != 0 {
			t.Fatalf("expected exit 0 (Candidate == HEAD), got %d; stderr=%q", exit, stderr)
		}
	})

	t.Run("candidate-equals-head-parent-passes", func(t *testing.T) {
		// The previous skip-when-shallow pattern
		// retired by R2-NEW-001 used a single-commit
		// harness whose HEAD~1 didn't exist. The
		// `addValidRDDPassReceipt` helper builds the
		// two-commit receipt workflow (placeholder
		// commit then final receipt commit), so HEAD~1
		// resolves to the placeholder receipt and the
		// receipt's Candidate Commit is HEAD~1 by
		// construction. The gate MUST accept this
		// shape because it is the canonical two-commit
		// code-then-receipt workflow documented in
		// scripts/release-gate.sh. This sub-test
		// replaces the skip-when-shallow skip with an
		// actual assertion.
		h := newHarness(t)
		h.addValidRDDPassReceipt(branch)
		exit, stdout, stderr := h.run("RELEASE_GATE_BRANCH="+branch, "BASE_REF=main")
		if exit != 0 {
			t.Fatalf("expected exit 0 (Candidate == HEAD~1), got %d; stderr=%q", exit, stderr)
		}
		if !strings.Contains(stdout, "release-gate: PASS") {
			t.Fatalf("expected PASS line, got stdout=%q", stdout)
		}
	})

	t.Run("candidate-grandparent-fails", func(t *testing.T) {
		// Stage three intermediate commits on this test
		// harness so HEAD~2 resolves to a stable SHA that
		// is neither HEAD nor HEAD~1 of this harness.
		// `runWithCommit` builds a FRESH harness (its own
		// `t.TempDir()` + `git init`), so the new harness's
		// HEAD and HEAD~1 are unrelated to anything in this
		// test harness. Passing this harness's HEAD~2 as the
		// new receipt's Candidate Commit therefore forces
		// the precise contract to reject: the new harness's
		// HEAD or HEAD~1 cannot equal any SHA from this
		// harness. No tie-breaking is required.
		h := newHarness(t)
		h.commitFile("intermediate_b1.txt", "b1\n", "stage intermediate b1")
		h.commitFile("intermediate_b2.txt", "b2\n", "stage intermediate b2 (HEAD~2 of test harness resolves to this commit)")
		h.commitFile("intermediate_b3.txt", "b3\n", "stage intermediate b3")
		grandparent := resolveHeadN(t, h, 2)
		exit, stderr := runWithCommit(t, grandparent, "local gate validation")
		if exit == 0 {
			t.Fatalf("expected non-zero exit (Candidate == HEAD~2 of this harness cannot equal HEAD or HEAD~1 of the new harness), got 0; stderr=%q", stderr)
		}
		if !strings.Contains(stderr, "Candidate Commit") {
			t.Fatalf("expected stderr to mention 'Candidate Commit', got %q", stderr)
		}
	})

	t.Run("candidate-future-sha-fails", func(t *testing.T) {
		// A 40-char SHA that is neither HEAD nor HEAD~1.
		// The synthetic harness repo has only one commit so
		// HEAD~1 doesn't exist either; a clearly-not-HEAD
		// SHA proves the precise check.
		exit, stderr := runWithCommit(t, "ffffffffffffffffffffffffffffffffffffffff", "local gate validation")
		if exit == 0 {
			t.Fatalf("expected non-zero exit (Candidate == arbitrary SHA), got 0; stderr=%q", stderr)
		}
		if !strings.Contains(stderr, "Candidate Commit") {
			t.Fatalf("expected stderr to mention 'Candidate Commit', got %q", stderr)
		}
	})
}

// ---- R3-001 / R4-013 / R4-012 corrective batch (PR #2 CI) ---------------
//
// The tests below pin the three fixes that close the PR #2 CI red
// lights: the harness env-filter (R3-001), the release-gate MERGE_BASE
// env contract (R4-012), and the receipt CI-merge-aware Candidate Commit
// contract (R4-013). Each test exercises one behavior the gate must
// guarantee, in isolation from the rest of the contract.

// TestHarnessStripsCIEnvFromSubprocess is the R3-001 RED gate. The
// harness.run() env filter MUST strip `CI`, `GITHUB_*`, and
// `CI_COMMIT_REF_NAME` so a CI shell that exports these cannot
// corrupt the synthetic fixtures. The proof is the dirty-bypass
// path: outside CI (in the gate's view) the seam must still work,
// but if the filter is missing the gate would see CI=true and
// refuse the bypass. The test runs with `t.Setenv("CI", "true")`
// in the calling test process; the harness MUST drop the leak.
func TestHarnessStripsCIEnvFromSubprocess(t *testing.T) {
	t.Setenv("CI", "true")
	t.Setenv("GITHUB_HEAD_REF", "feature/leaked")
	t.Setenv("GITHUB_REF_NAME", "leaked-ref")
	t.Setenv("CI_COMMIT_REF_NAME", "leaked-branch")
	t.Setenv("GITHUB_EVENT_NAME", "pull_request")

	h := newHarness(t)
	h.addValidRDDPassReceipt("main")
	h.commitFile("marker.txt", "clean state\n", "add marker")
	h.recommitRDDPassReceipt("main")
	h.touchFile("marker.txt", "dirty state\n")

	// With dirty worktree AND dirty-bypass set, the gate must pass
	// IF the filter stripped CI. If the filter is missing, the gate
	// sees CI=true and refuses the bypass — the test fails.
	exit, stdout, stderr := h.run(
		"RELEASE_GATE_BRANCH=main",
		"BASE_REF=main",
		"RELEASE_GATE_ALLOW_DIRTY=1",
	)
	if exit != 0 {
		t.Fatalf("expected exit 0 (filter must strip CI/GITHUB_*), got %d; stderr=%q", exit, stderr)
	}
	if !strings.Contains(stdout, "release-gate: PASS") {
		t.Fatalf("expected PASS line, got stdout=%q", stdout)
	}
}

// TestHarnessStripsGitHubBranchEnv is the R3-001 RED gate for the
// branch-resolution path. The gate's CURRENT_BRANCH is
// `${GITHUB_HEAD_REF:-...}` first, so a leaked GITHUB_HEAD_REF
// would override the test's explicit RELEASE_GATE_BRANCH=main and
// the receipt's Branch=main would mismatch. The test sets a
// deliberately-different leaked branch; if the filter is missing
// the gate resolves to the leaked value and the receipt blocks.
func TestHarnessStripsGitHubBranchEnv(t *testing.T) {
	t.Setenv("GITHUB_HEAD_REF", "feature/leaked")
	t.Setenv("GITHUB_REF_NAME", "leaked-ref")
	t.Setenv("CI_COMMIT_REF_NAME", "leaked-branch")
	t.Setenv("GITHUB_EVENT_NAME", "pull_request")

	h := newHarness(t)
	h.addValidRDDPassReceipt("main")
	h.commitFile("marker.txt", "clean state\n", "add marker")
	h.recommitRDDPassReceipt("main")

	// RELEASE_GATE_BRANCH=main must win because the filter strips
	// the leaked GITHUB_HEAD_REF before the gate sees it. The receipt
	// declares Branch=main; if the gate sees the leaked value, the
	// receipt blocks the merge.
	exit, stdout, stderr := h.run(
		"RELEASE_GATE_BRANCH=main",
		"BASE_REF=main",
	)
	if exit != 0 {
		t.Fatalf("expected exit 0 (filter must strip GITHUB_*), got %d; stderr=%q", exit, stderr)
	}
	if !strings.Contains(stdout, "BRANCH=main") {
		t.Fatalf("expected stdout to show BRANCH=main, got %q", stdout)
	}
}

// TestHarnessStripsMergeBaseAndPRHeadEnv is the R4-012 / R4-013
// filter-coverage guard. The harness env-filter MUST also strip
// MERGE_BASE and RELEASE_GATE_PR_HEAD_* env vars, otherwise a
// parent process that exports them (e.g. the release-gate script
// calling `go test ./...` with the same env it received from the
// workflow) would leak them into the synthetic gate runs. A
// leaked MERGE_BASE would short-circuit the local
// `git merge-base "$BASE_REF" HEAD` re-resolution; a leaked
// PR_HEAD env would widen the receipt Candidate Commit check to
// the CI context. Both would make existing tests that depend on
// the local contract pass or fail for the wrong reason.
//
// The test exercises the carve-out path because it is the most
// sensitive: the size-exception path requires the line-budget
// check to use the synthetic repo's local merge-base, not a
// leaked value from the parent shell. If the filter is missing
// MERGE_BASE, the gate would see the leaked value and could
// either inflate or shrink the diff range, masking or inventing
// a line-budget failure.
func TestHarnessStripsMergeBaseAndPRHeadEnv(t *testing.T) {
	// Set the new env vars via t.Setenv so they appear in the
	// test process's os.Environ() (which is what h.run() reads).
	t.Setenv("MERGE_BASE", strings.Repeat("a", 40))
	t.Setenv("RELEASE_GATE_PR_HEAD_SHA", strings.Repeat("b", 40))
	t.Setenv("RELEASE_GATE_PR_HEAD_PARENT_SHA", strings.Repeat("c", 40))

	const carveBranch = "feature/close-fetch-resilience-release-gates-exception"
	h := newHarness(t)
	h.addValidRDDPassReceipt("main")
	// Create a feature branch so the merge-base resolution finds
	// a real diff range (the same pattern the existing
	// TestGateCarveOutExactBranchOnly test uses). Without this
	// branch, `git merge-base main HEAD` returns HEAD (HEAD is
	// on main), and the line budget is 0.
	runGit(t, h.repo, "checkout", "-b", carveBranch)
	h.commitFile("bulk.go", "package gatemod\n\n"+strings.Repeat("// padding\n", 500), "bulk to exceed 400 lines")
	h.recommitRDDPassReceipt(carveBranch)

	// The size-exception path is the canary. The carve-out matches
	// the branch, but there is no size-exception receipt in the
	// synthetic repo so the gate must fail-closed with
	// "size-exception receipt missing". If the filter is missing
	// MERGE_BASE, the gate would see the leaked value and could
	// either inflate or shrink the diff range, masking or
	// inventing a line-budget failure.
	exit, stdout, stderr := h.run(
		"RELEASE_GATE_BRANCH="+carveBranch,
		"RELEASE_GATE_SIZE_EXCEPTION="+carveBranch,
		"BASE_REF=main",
	)
	if exit == 0 {
		t.Fatalf("expected non-zero exit (size-exception receipt missing in synthetic repo). stdout=%q stderr=%q", stdout, stderr)
	}
	if !strings.Contains(stderr, "size-exception") {
		t.Fatalf("expected stderr to mention 'size-exception' (the carve-out path), got %q\nstdout=%q", stderr, stdout)
	}
}

// TestGateHonorsValidMergeBaseEnv is the R4-012 RED gate. The
// release-gate workflow pre-computes MERGE_BASE via
// `git merge-base $BASE_SHA HEAD` because the PR checkout does NOT
// fetch a local `main` ref. The gate MUST honor that pre-computed
// value (validated as a 40-char hex SHA) instead of re-resolving
// `git merge-base main HEAD`, which fails in a shallow CI
// merge-checkout. The test exercises the env path end-to-end:
// synthetic repo, two commits, MERGE_BASE env pointing at the
// initial commit, gate must pass and log the env source.
// BASE_REF is set to a non-existent branch so the local fallback
// would also fail — proving the env path is what succeeded.
func TestGateHonorsValidMergeBaseEnv(t *testing.T) {
	h := newHarness(t)
	// Snapshot the initial commit SHA so we can pass it as MERGE_BASE.
	initialSHA := h.headSHA()
	h.addValidRDDPassReceipt("main")
	h.commitFile("marker.txt", "hello\n", "add marker")
	h.recommitRDDPassReceipt("main")

	exit, stdout, stderr := h.run(
		"RELEASE_GATE_BRANCH=main",
		"BASE_REF=__no_such_branch__",
		"MERGE_BASE="+initialSHA,
	)
	if exit != 0 {
		t.Fatalf("expected exit 0 with valid MERGE_BASE env, got %d; stderr=%q", exit, stderr)
	}
	if !strings.Contains(stdout, "release-gate: PASS") {
		t.Fatalf("expected PASS line, got stdout=%q", stdout)
	}
	// Hard assertion: the gate MUST log that MERGE_BASE came from env
	// (proves the env path is taken, not the local re-resolution).
	if !strings.Contains(stdout, "(from env)") {
		t.Fatalf("expected stdout to mention '(from env)' source, got %q", stdout)
	}
}

// TestGateRejectsInvalidMergeBaseEnv is the R4-012 fail-closed
// surface. The env path MUST validate the supplied MERGE_BASE as
// a 40-char hex SHA. An invalid value (non-hex, wrong length, or
// empty when explicitly set) MUST fail closed rather than be
// silently re-resolved.
func TestGateRejectsInvalidMergeBaseEnv(t *testing.T) {
	cases := []struct {
		name string
		val  string
	}{
		{name: "non-hex", val: "not-a-sha"},
		{name: "too-short", val: "abc123"},
		{name: "too-long", val: strings.Repeat("a", 41)},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.addValidRDDPassReceipt("main")
			// Use BASE_REF=__no_such_branch__ so the fallback
			// `git merge-base` also fails — proving the env path
			// is what would block (the failure mode must mention
			// MERGE_BASE, not the BASE_REF fallback).
			exit, _, stderr := h.run(
				"RELEASE_GATE_BRANCH=main",
				"BASE_REF=__no_such_branch__",
				"MERGE_BASE="+tt.val,
			)
			if exit == 0 {
				t.Fatalf("expected non-zero exit for invalid MERGE_BASE=%q, got 0", tt.val)
			}
			if !strings.Contains(stderr, "MERGE_BASE") {
				t.Fatalf("expected stderr to mention MERGE_BASE, got %q", stderr)
			}
		})
	}
}

// TestGateFallsBackToMergeBaseWhenEnvMissing is the R4-012
// triangulation: when MERGE_BASE is unset, the gate MUST fall
// back to `git merge-base "$BASE_REF" HEAD`. The synthetic
// harness creates a clean repo where the local merge-base
// resolution works, so the gate passes without any env.
// This proves the env path is purely additive, not a silent
// breaking change for the local-only flow.
func TestGateFallsBackToMergeBaseWhenEnvMissing(t *testing.T) {
	h := newHarness(t)
	h.addValidRDDPassReceipt("main")

	// No MERGE_BASE env: gate must use the local `git merge-base
	// "$BASE_REF" HEAD` path and pass.
	exit, stdout, stderr := h.run(
		"RELEASE_GATE_BRANCH=main",
		"BASE_REF=main",
	)
	if exit != 0 {
		t.Fatalf("expected exit 0 with fallback to git merge-base, got %d; stderr=%q", exit, stderr)
	}
	if !strings.Contains(stdout, "release-gate: PASS") {
		t.Fatalf("expected PASS line, got stdout=%q", stdout)
	}
}

// TestGateAcceptsPRHeadContext is the R4-013 RED gate. When the
// release-gate workflow sets RELEASE_GATE_PR_HEAD_SHA and
// RELEASE_GATE_PR_HEAD_PARENT_SHA (the explicit PR-head context),
// the receipt's Candidate Commit MUST be allowed to match one of
// those two SHAs (PR tip or its direct parent). This is the
// CI-merge-aware half of the two-context contract: locally the
// contract is still HEAD/HEAD~1, but in CI the contract widens to
// PR tip / PR tip~1 to accommodate the synthetic merge commit
// that `actions/checkout@v4` produces on a pull_request event.
// The test exercises the full env path: PR_HEAD env set, receipt
// committed with Candidate matching the env, gate must pass.
func TestGateAcceptsPRHeadContext(t *testing.T) {
	h := newHarness(t)
	h.addValidRDDPassReceipt("main")
	// Re-anchor the receipt at HEAD so its Candidate equals HEAD
	// of the new HEAD (= the previous HEAD's SHA, which is the
	// placeholder). Under the CI contract, the new HEAD is
	// PR_HEAD_SHA and the previous HEAD (= placeholder) is
	// PR_HEAD_PARENT_SHA; Candidate == PR_HEAD_PARENT_SHA
	// satisfies the CI contract.
	h.recommitRDDPassReceipt("main")
	newHead := h.headSHA()
	parentCmd := exec.Command("git", "rev-parse", "HEAD~1")
	parentCmd.Dir = h.repo
	parentOut, err := parentCmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD~1: %v", err)
	}
	newParent := strings.TrimSpace(string(parentOut))

	exit, stdout, stderr := h.run(
		"RELEASE_GATE_BRANCH=main",
		"BASE_REF=main",
		"RELEASE_GATE_PR_HEAD_SHA="+newHead,
		"RELEASE_GATE_PR_HEAD_PARENT_SHA="+newParent,
	)
	if exit != 0 {
		t.Fatalf("expected exit 0 with PR_HEAD context, got %d; stderr=%q", exit, stderr)
	}
	if !strings.Contains(stdout, "release-gate: PASS") {
		t.Fatalf("expected PASS line, got stdout=%q", stdout)
	}
	// Hard assertion: the gate MUST log the CI context label,
	// not the local one.
	if !strings.Contains(stdout, "PR_HEAD_or_PR_HEAD~1") {
		t.Fatalf("expected stdout to mention 'PR_HEAD_or_PR_HEAD~1' label, got %q", stdout)
	}
}

// TestGateRejectsCandidateNotInPRHeadContext is the R4-013
// not-broader-than-allowed surface. When PR_HEAD env is set, the
// contract is EXACTLY PR_HEAD_SHA or PR_HEAD_PARENT_SHA — not
// arbitrary ancestors. A receipt whose Candidate matches neither
// (e.g. matches HEAD or HEAD~1 of the local checkout, which is a
// different SHA in the synthetic harness) MUST be rejected. This
// is the seam that prevents accidentally broadening the contract
// to "any ancestor" and undoing the R4-006 rollback safety.
func TestGateRejectsCandidateNotInPRHeadContext(t *testing.T) {
	h := newHarness(t)
	h.addValidRDDPassReceipt("main")
	// Receipt's Candidate == HEAD. Now set PR_HEAD env to two
	// SHAs that are deliberately NOT HEAD. The receipt's
	// Candidate does not match either, so the gate must fail.
	fakePRHead := strings.Repeat("1", 40)
	fakePRHeadParent := strings.Repeat("2", 40)

	exit, _, stderr := h.run(
		"RELEASE_GATE_BRANCH=main",
		"BASE_REF=main",
		"RELEASE_GATE_PR_HEAD_SHA="+fakePRHead,
		"RELEASE_GATE_PR_HEAD_PARENT_SHA="+fakePRHeadParent,
	)
	if exit == 0 {
		t.Fatal("expected non-zero exit: Candidate must match PR_HEAD or PR_HEAD~1")
	}
	if !strings.Contains(stderr, "Candidate Commit") {
		t.Fatalf("expected stderr to mention 'Candidate Commit', got %q", stderr)
	}
}

// TestGateRejectsInvalidPRHeadEnv is the R4-013 fail-closed
// surface. When the PR_HEAD env is partially set or contains
// non-hex values, the gate MUST fail closed rather than
// silently fall back to the local HEAD/HEAD~1 path. The
// local fallback would mask the CI context and let a receipt
// authored against the PR tip pass a CI merge-checkout whose
// HEAD is the synthetic merge commit.
func TestGateRejectsInvalidPRHeadEnv(t *testing.T) {
	cases := []struct {
		name         string
		prHead       string
		prHeadParent string
	}{
		{name: "non-hex-prhead", prHead: "not-a-sha", prHeadParent: strings.Repeat("2", 40)},
		{name: "non-hex-prparent", prHead: strings.Repeat("1", 40), prHeadParent: "not-a-sha"},
		{name: "prhead-set-parent-empty", prHead: strings.Repeat("1", 40), prHeadParent: ""},
		{name: "prhead-empty-parent-set", prHead: "", prHeadParent: strings.Repeat("2", 40)},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			h := newHarness(t)
			h.addValidRDDPassReceipt("main")
			exit, _, stderr := h.run(
				"RELEASE_GATE_BRANCH=main",
				"BASE_REF=main",
				"RELEASE_GATE_PR_HEAD_SHA="+tt.prHead,
				"RELEASE_GATE_PR_HEAD_PARENT_SHA="+tt.prHeadParent,
			)
			if exit == 0 {
				t.Fatal("expected non-zero exit for invalid PR_HEAD env")
			}
			if !strings.Contains(stderr, "RELEASE_GATE_PR_HEAD") {
				t.Fatalf("expected stderr to mention 'RELEASE_GATE_PR_HEAD', got %q", stderr)
			}
		})
	}
}

// TestGatePRHeadContextNotLocalFallback is the R4-013
// triangulation: when PR_HEAD env is set AND valid AND
// matches the receipt's Candidate, the gate must use the
// CI context (log PR_HEAD_or_PR_HEAD~1) — not the local
// HEAD/HEAD~1 path. This proves the env path is taken
// (not silently ignored) so a future regression that
// drops the env branch fails here.
func TestGatePRHeadContextNotLocalFallback(t *testing.T) {
	h := newHarness(t)
	h.addValidRDDPassReceipt("main")
	// Re-anchor so the receipt's Candidate == HEAD~1 of the new
	// HEAD (the placeholder). Then set PR_HEAD env to match.
	h.recommitRDDPassReceipt("main")
	newHead := h.headSHA()
	parentCmd := exec.Command("git", "rev-parse", "HEAD~1")
	parentCmd.Dir = h.repo
	parentOut, err := parentCmd.Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD~1: %v", err)
	}
	newParent := strings.TrimSpace(string(parentOut))

	exit, stdout, stderr := h.run(
		"RELEASE_GATE_BRANCH=main",
		"BASE_REF=main",
		"RELEASE_GATE_PR_HEAD_SHA="+newHead,
		"RELEASE_GATE_PR_HEAD_PARENT_SHA="+newParent,
	)
	if exit != 0 {
		t.Fatalf("expected exit 0 with PR_HEAD context, got %d; stderr=%q", exit, stderr)
	}
	// The success-path log must include the CI context label.
	if !strings.Contains(stdout, "PR_HEAD_or_PR_HEAD~1") {
		t.Fatalf("expected stdout to mention CI context label, got %q", stdout)
	}
}
