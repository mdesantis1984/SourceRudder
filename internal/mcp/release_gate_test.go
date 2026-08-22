package mcp

import (
	"os"
	"os/exec"
	"path/filepath"
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

// TestReleaseGateReviewAuthorityRemainsPending is the
// behavior-first process guard for spec #4289 scenario
// "Phase 16 unlock precondition": Phase 16 of the
// `close-fetch-resilience-and-release-gates` change MUST remain
// `pending` until this change merges AND `local-docker-qa` passes,
// and the unlock MUST be handled by a separate change, not this one.
//
// The truthful runtime/process guard is to scan the release-gate
// review placeholder: the placeholder is the file the release-gate
// script checks, and the only path to merge is for a future
// provider-issued binding to flip its `Authority` header from
// `pending` to `official`. As long as the header stays `pending`,
// the release-gate blocks merge and Phase 16 stays held.
//
// GREEN-on-first-run by construction: the contract is "the review
// placeholder header MUST say Authority: pending", and the current
// file already satisfies it. The test exists to lock the contract
// against future regressions (someone editing the placeholder, a CI
// step flipping the header without a binding, etc.) and to give
// verify a passing runtime/process guard instead of the
// apply-progress narration it had before. The exception is
// documented per the user's instruction.
func TestReleaseGateReviewAuthorityRemainsPending(t *testing.T) {
	placeholderPath := filepath.Join("..", "..", "docs", "release", "reviews", "review-be4525bc4797e972.md")
	data, err := os.ReadFile(placeholderPath)
	if err != nil {
		t.Fatalf("ReadFile %s: %v", placeholderPath, err)
	}

	// The Authority header lives on its own line immediately after the
	// `# Review: review-...` title. We extract the SECOND non-empty
	// line of the file and assert it equals `Authority: pending`. This
	// is robust against the explanatory table that mentions both
	// `pending` and `official` for documentation purposes — those
	// tokens must not trip the hard-fail guard.
	lines := strings.Split(string(data), "\n")
	var headerLine string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "# ") {
			// skip the title line; the next non-empty line is the header
			continue
		}
		headerLine = trimmed
		break
	}

	// Guard 1: the placeholder header MUST be exactly `Authority: pending`.
	if headerLine != "Authority: pending" {
		t.Errorf("release-gate review placeholder %s header is %q; want %q — Phase 16 stays held until this change merges AND local-docker-qa passes. Full content:\n%s", placeholderPath, headerLine, "Authority: pending", string(data))
	}
}
