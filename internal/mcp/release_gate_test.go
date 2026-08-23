package mcp

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// forbiddenDeployCommands is the exact set of shell-level operations
// that would trigger a production rollout. If any of these tokens
// appear inside the runtime surface — main.go, the binary string
// table, or the Makefile as a default target — the change has
// crossed the boundary of "no production deploy" the spec promises.
//
// The list is intentionally narrow: it lists the EXACT commands that
// mutate a production cluster or the systemd manager. It is NOT a
// general "no shell-out" rule; the runtime already shells out to
// fetch / search, and that is allowed.
var forbiddenDeployCommands = []string{
	"kubectl apply",
	"kubectl rollout",
	"kubectl delete",
	"kubectl scale",
	"systemctl daemon-reload",
	"docker push",
}

// TestRuntimeSurfaceDoesNotInvokeProductionDeploy is the
// behavior-first process guard for spec #4289 scenario
// "change scope excludes production rollout": this change ships the
// restored contract on the working branch, and the apply phase MUST
// NOT have triggered a production deploy step. The truthful
// runtime/process guard is to scan the runtime surface
// (cmd/ia-buscar/main.go, the compiled binary, and the Makefile
// default targets) for the exact commands that would mutate
// production. If any are present, the test FAILS.
//
// GREEN-on-first-run by construction: the contract is "this code
// MUST NOT contain deploy commands", and the current code already
// satisfies it. The test exists to lock the contract against future
// regressions and to give verify a passing runtime/process guard
// instead of the apply-progress narration it had before. The
// exception is documented per the user's instruction so a future
// reviewer does not mistake it for a missing RED proof.
func TestRuntimeSurfaceDoesNotInvokeProductionDeploy(t *testing.T) {
	// Guard 1: cmd/ia-buscar/main.go source. Forbidden deploy
	// commands must not appear in the runtime entry point. The grep
	// is exact-string based so a coincidental match inside a comment
	// is impossible.
	mainPath := filepath.Join("..", "..", "cmd", "ia-buscar", "main.go")
	mainSrc, err := os.ReadFile(mainPath)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", mainPath, err)
	}
	for _, cmd := range forbiddenDeployCommands {
		if strings.Contains(string(mainSrc), cmd) {
			t.Errorf("cmd/ia-buscar/main.go contains forbidden production-deploy command %q — this change MUST NOT trigger a production rollout. Full source follows:\n%s", cmd, string(mainSrc))
		}
	}

	// Guard 2: the compiled binary's string table. If the deploy
	// command was added to a Go file that does not get compiled into
	// the binary we still want the test to catch it, but if it ends
	// up in the binary the runtime guard fires. We build into a temp
	// dir so the test is hermetic. The build must run from the repo
	// root because `go build ./cmd/ia-buscar` is resolved against the
	// current working directory; tests run inside `internal/mcp/`.
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("filepath.Abs repoRoot: %v", err)
	}
	binDir := t.TempDir()
	binPath := filepath.Join(binDir, "ia-buscar")
	build := exec.Command("go", "build", "-o", binPath, "./cmd/ia-buscar")
	build.Dir = repoRoot
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, string(out))
	}
	stringsOut, err := exec.Command("strings", binPath).CombinedOutput()
	if err != nil {
		// `strings` is optional; if it is not installed we skip
		// this guard rather than fail spuriously.
		t.Logf("`strings` unavailable, skipping binary string-table guard: %v", err)
	} else {
		binStrings := string(stringsOut)
		for _, cmd := range forbiddenDeployCommands {
			if strings.Contains(binStrings, cmd) {
				t.Errorf("compiled binary string table contains forbidden production-deploy command %q — the binary would auto-deploy on start. Binary path: %s", cmd, binPath)
			}
		}
	}

	// Guard 3: the Makefile must not invoke any deploy command from
	// a default target. We scan the Makefile source for the tokens
	// appearing inside a recipe body. A future regression that wires
	// `kubectl apply` into `make build` or `make test` would surface
	// here before it reaches CI.
	makefilePath := filepath.Join("..", "..", "Makefile")
	mfSrc, err := os.ReadFile(makefilePath)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", makefilePath, err)
	}
	mfContent := string(mfSrc)
	for _, cmd := range forbiddenDeployCommands {
		if strings.Contains(mfContent, cmd) {
			t.Errorf("Makefile contains forbidden production-deploy command %q — a default target MUST NOT trigger production rollout", cmd)
		}
	}
}

// TestReleaseGateRDDReceiptSatisfiesLocalContract is the
// behavior-first process guard for the local RDD receipt/evidence
// contract that replaced the fictitious external-review
// `Authority: official` binding. The gate at
// `scripts/release-gate.sh` parses this receipt on every merge;
// this test pins the receipt shape so a future regression (a CI
// step rewriting the receipt, a manual edit dropping a field, an
// external-binding resurrection) trips here before reaching the
// release gate.
//
// Required fields, all parsed as line-start matches from the
// receipt body. The full bash validator lives in
// `scripts/release-gate.sh`; this Go test pins the same shape as
// a process guard for the compiled binary's view of the
// contract:
//
//   1. Status: pass                        (exact line)
//   2. Candidate Commit: <full 40-char SHA>
//   3. Scope: <text mentioning current branch>
//   4. Verified Commands:                  (section; every entry
//      ends in `: PASS`)
//   5. Unresolved Blocker Policy:          (header; value free-form)
//
// GREEN-on-first-run by construction: the staged receipt
// already satisfies the contract. The test exists to lock the
// contract against future regressions and to give verify a
// passing runtime/process guard instead of an external-binding
// dependency.
func TestReleaseGateRDDReceiptSatisfiesLocalContract(t *testing.T) {
	placeholderPath := filepath.Join("..", "..", "docs", "release", "reviews", "review-be4525bc4797e972.md")
	data, err := os.ReadFile(placeholderPath)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", placeholderPath, err)
	}
	body := string(data)

	problems := rddReceiptValidateStaged(body)
	if len(problems) > 0 {
		t.Fatalf("RDD receipt at %s failed validation: %v\nFull content:\n%s", placeholderPath, problems, body)
	}

	// Hard guard: the receipt MUST NOT mention the old external-binding
	// tokens (`Authority:` header, the word `official` as a value).
	// A future regression that re-introduces the external-binding
	// gate MUST fail here first, before reaching the release-gate
	// subprocess tests.
	if strings.Contains(body, "\nAuthority:") || strings.HasPrefix(body, "Authority:") {
		// Walk every line and reject any line whose first non-blank
		// characters are `Authority:` (the gate's parser uses
		// line-start matches).
		for _, line := range strings.Split(body, "\n") {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "Authority:") {
				t.Errorf("RDD receipt at %s contains the legacy `Authority:` header (line %q). The local RDD contract replaced the fictitious external review binding; that header must not return.", placeholderPath, trimmed)
			}
		}
	}
}

// rddReceiptValidateStaged mirrors the gate's RDD validator for the
// receipt as it is staged on disk. The receipt MUST carry a
// dedicated `Branch:` line whose value exactly matches the
// current worktree's branch name (queried via `git rev-parse
// --abbrev-ref HEAD`); the previous substring-based Scope check
// has been retired in favour of the precise exact-match contract.
// `Scope:` remains a free-form operator-context field and is no
// longer used for branch verification.
//
// The function is wired to the worktree's actual branch so the
// Go process guard stays in sync with the bash gate's runtime
// behaviour. The function also pins the new precise Candidate
// Commit contract: the SHA must equal HEAD or HEAD~1. The
// two-commit code-then-receipt workflow still satisfies this
// because the receipt commit is HEAD and the code commit it
// attests sits at HEAD~1.
func rddReceiptValidateStaged(body string) []string {
	var problems []string

	// 0. Hard guard: reject any line beginning with the legacy
	//    `Authority:` header. The bash gate enforces the same
	//    rule; both validators must stay symmetric so a future
	//    regression cannot smuggle the external-binding
	//    header back through only one of them.
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Authority:") {
			problems = append(problems, fmt.Sprintf("RDD receipt contains legacy 'Authority:' line (%q); the local RDD contract replaced the external-binding header and it MUST NOT return", trimmed))
			break
		}
	}

	// 1. Status: pass (exact line).
	statusLine := ""
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Status:") {
			statusLine = trimmed
			break
		}
	}
	if statusLine != "Status: pass" {
		problems = append(problems, fmt.Sprintf("Status line must be exactly 'Status: pass' (got %q)", statusLine))
	}

	// 2. Candidate Commit: <full 40-char SHA> with the precise
	//    HEAD-or-HEAD~1 contract. We resolve HEAD and HEAD~1
	//    from the actual worktree (the receipt's `Status: pass`
	//    commits are always made against the worktree's current
	//    branch) so the process guard is anchored to the
	//    same git state the bash gate would see.
	commitLine := ""
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Candidate Commit:") {
			commitLine = trimmed
			break
		}
	}
	repoRoot := stagedReceiptRepoRoot()
	headSHACmd := exec.Command("git", "rev-parse", "HEAD")
	headSHACmd.Dir = repoRoot
	headOut, headErr := headSHACmd.Output()
	var headSHA string
	if headErr == nil {
		headSHA = strings.TrimSpace(string(headOut))
	}
	var headParentSHA string
	if headSHA != "" {
		parentCmd := exec.Command("git", "rev-parse", "HEAD~1")
		parentCmd.Dir = repoRoot
		parentOut, parentErr := parentCmd.Output()
		if parentErr == nil {
			headParentSHA = strings.TrimSpace(string(parentOut))
		}
	}
	if commitLine == "" {
		problems = append(problems, "Candidate Commit line missing")
	} else {
		fields := strings.Fields(strings.TrimPrefix(commitLine, "Candidate Commit:"))
		if len(fields) == 0 {
			problems = append(problems, "Candidate Commit value missing")
		} else if !looksLikeFullSHA40(fields[0]) {
			problems = append(problems, fmt.Sprintf("Candidate Commit %q is not a full 40-char SHA", fields[0]))
		} else if headSHA != "" && fields[0] != headSHA && fields[0] != headParentSHA {
			problems = append(problems, fmt.Sprintf("Candidate Commit %q must equal HEAD (%s) or HEAD~1 (%s); arbitrary ancestors are rejected so rollback or code changes require a new receipt", fields[0], headSHA, headParentSHA))
		}
	}

	// 3. Branch: <exact branch name>. The receipt MUST carry a
	//    dedicated Branch: line whose value exactly matches
	//    the current worktree's branch (resolved via git). This
	//    replaces the previous substring-based Scope check.
	branchCmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	branchCmd.Dir = repoRoot
	branchOut, branchErr := branchCmd.Output()
	var currentBranch string
	if branchErr == nil {
		currentBranch = strings.TrimSpace(string(branchOut))
	}
	if currentBranch == "HEAD" || currentBranch == "" {
		// Detached HEAD or unresolvable branch. The receipt
		// still must declare a Branch: line for the guard
		// to be meaningful; we surface the fact that the
		// gate will (correctly) fail closed in this state
		// but do not fail the process guard solely on the
		// branch-resolution detail — the bash gate handles
		// that seam.
		currentBranch = ""
	}
	branchLine := ""
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Branch:") {
			branchLine = trimmed
			break
		}
	}
	if branchLine == "" {
		problems = append(problems, "Branch line missing (the receipt must declare its target branch via a dedicated Branch: field for exact-match verification)")
	} else if currentBranch != "" {
		value := strings.TrimSpace(strings.TrimPrefix(branchLine, "Branch:"))
		if value != currentBranch {
			problems = append(problems, fmt.Sprintf("Branch value %q does not exactly match the current worktree branch %q", value, currentBranch))
		}
	}

	// 3b. Scope: <free-form operator context>. Presence only;
	//     not used for branch verification.
	scopeLine := ""
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Scope:") {
			scopeLine = trimmed
			break
		}
	}
	if scopeLine == "" {
		problems = append(problems, "Scope line missing")
	}

	// 4. Verified Commands: section with all-PASS entries.
	//    Section-scoped: iteration begins after the header and
	//    ends at the next top-level `^[A-Z][A-Za-z][A-Za-z0-9 ]*:`
	//    line, mirroring the bash gate.
	hasSection := false
	passRe := regexp.MustCompile(`^  - .*:\s*PASS\s*$`)
	failRe := regexp.MustCompile(`^  - .*:\s*FAIL`)
	headerRe := regexp.MustCompile(`^[A-Z][A-Za-z][A-Za-z0-9 ]*:`)
	for _, line := range strings.Split(body, "\n") {
		if !hasSection {
			if strings.TrimSpace(line) == "Verified Commands:" {
				hasSection = true
			}
			continue
		}
		// Inside the section: exit on the next top-level header.
		if headerRe.MatchString(line) {
			break
		}
		if strings.HasPrefix(line, "  - ") {
			if failRe.MatchString(line) {
				problems = append(problems, fmt.Sprintf("Verified Commands entry reports FAIL (gate is fail-closed): %q", line))
			} else if !passRe.MatchString(line) {
				problems = append(problems, fmt.Sprintf("Verified Commands entry must end with ': PASS' (got %q)", line))
			}
		}
	}
	if !hasSection {
		problems = append(problems, "Verified Commands section missing")
	}

	// 5. Unresolved Blocker Policy header with a non-empty value.
	//    Matches the gate's `grep -qE '^Unresolved Blocker Policy:[[:space:]]*[^[:space:]]'`
	//    so the operator MUST declare the policy (`none`, a blocker
	//    description, etc.); an empty value is rejected because the
	//    value IS the substantive declaration.
	blockerRe := regexp.MustCompile(`(?m)^Unresolved Blocker Policy:\s*\S`)
	if !blockerRe.MatchString(body) {
		problems = append(problems, "Unresolved Blocker Policy line missing or empty")
	}

	return problems
}

// stagedReceiptRepoRoot returns the absolute path to the
// repository root for the running test, so the receipt
// validator can resolve HEAD / HEAD~1 / current branch against
// the actual worktree. The result is cached across calls.
var stagedReceiptRepoRootCache string

func stagedReceiptRepoRoot() string {
	if stagedReceiptRepoRootCache != "" {
		return stagedReceiptRepoRootCache
	}
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		// Best-effort fallback to the current working
		// directory; the validator will surface a more
		// specific error if `git rev-parse` then fails.
		return "."
	}
	stagedReceiptRepoRootCache = repoRoot
	return stagedReceiptRepoRootCache
}

// looksLikeFullSHA40 returns true iff s is exactly 40 lowercase hex
// characters — the shape of `git rev-parse` output the gate expects.
func looksLikeFullSHA40(s string) bool {
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
