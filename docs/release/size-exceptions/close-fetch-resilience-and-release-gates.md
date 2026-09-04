# Size Exception Receipt — close-fetch-resilience-and-release-gates

> **RDD deprecation:** Ordinary CI/default release-gate runs do not consume
> or require the legacy RDD receipt. Validation is opt-in only via
> `RELEASE_GATE_ENABLE_LEGACY_RDD_RECEIPT=1` and never grants approval.

**Status**: tracked (in git). This receipt documents the
one-time branch-level size exception for the SDD change
`close-fetch-resilience-and-release-gates`. The receipt is REQUIRED
by the release gate (`scripts/release-gate.sh`) when the carve-out
is active on this branch. It is REVIEWEABLE in the PR diff because
it lives in the tracked file tree.

**It does NOT substitute for the historical RDD receipt.** The default
gate does not consume that receipt; its step 2 is satisfied by it only
during explicit compatibility validation at
`docs/release/reviews/review-be4525bc4797e972.md` (Status: pass +
reachable Candidate Commit + branch-mentioning Scope + Verified
Commands with PASS entries + Unresolved Blocker Policy declaration).
This size-exception receipt only documents the line-budget carve-out;
the RDD receipt is historical/opt-in compatibility evidence only, never
default authority or an approval signal.

The previous `Authority: official` external-binding header has been
removed entirely. There is NO external review provider binding —
the historical RDD receipt is compatibility evidence only, and the gate
independently re-runs `go build` / `go vet` / `go test` / `go test
-race` so a forged receipt cannot bypass a real regression.

## Required Fields

| Field | Value |
|-------|-------|
| Branch | `feature/close-fetch-resilience-release-gates-exception` |
| Commit | `537f9fb3bb70991b2dc5ce8f9adf478264b71d2b` (Phase 19 R6-NEW-001 fix commit; the receipt re-authoring that follows sits at HEAD) |
| Approval Reference | [#4125](memory://4139) — user-approved baseline size exception |
| Scope | bounded (single-PR exception for Phase 12-19: Phase 12-14 pre-production security, Phase 13 release-gate hardening, Phase 16 PR #2 CI corrective batch, Phase 17 R4-014 Go-guard no-skip + synthetic-merge deterministic fixture + duplicate PR-head test differentiation, Phase 18 R5-NEW-001/002/003 PR_HEAD fail-closed contract tightening on Go + bash, Phase 19 R6-NEW-001 release-gate workflow branch-exact size-exception activation replacing the repo-variable dependency with a deterministic ${{ github.head_ref == '...' && '...' || '' }} expression) |
| Expiration | 2026-12-31 |
| Forward Reference | `docs/release/reviews/review-be4525bc4797e972.md` (the RDD receipt is the local authority attestation; this size-exception receipt NEVER substitutes for it) |

## Parser-Compatible Fields

The release-gate parser at `scripts/release-gate.sh` (in step 3,
the size-budget check) greps for the six required fields as
colon-form lines (`^Field: value`). The table above is the
human-readable summary; this section mirrors those fields in the
parser-accepted shape so an explicit compatibility run can validate this
receipt independently. The values are identical to the table; the
duplicate shape exists ONLY so the gate parser can find them.

Branch: feature/close-fetch-resilience-release-gates-exception
Commit: 537f9fb3bb70991b2dc5ce8f9adf478264b71d2b
Approval Reference: #4125 (memory://4139) — user-approved baseline size exception
Scope: bounded (single-PR exception for Phase 12-19: Phase 12-14 pre-production security, Phase 13 release-gate hardening, Phase 16 PR #2 CI corrective batch R3-001 / R4-012 / R4-013, Phase 17 R4-014 Go-guard no-skip / synthetic-merge deterministic fixture / duplicate PR-head test differentiation, Phase 18 R5-NEW-001/002/003 PR_HEAD fail-closed contract tightening on Go + bash, and Phase 19 R6-NEW-001 release-gate workflow branch-exact size-exception activation)
Expiration: 2026-12-31
Forward Reference: docs/release/reviews/review-be4525bc4797e972.md (historical compatibility receipt)

## Why This Exception

The user prioritizes completing and deploying the
`close-fetch-resilience-and-release-gates` change promptly after the
outage. Splitting the change into chained PRs would block
production closure on additional reviewer cycles the operator does
not have. The size-exception applies ONLY to this branch and only
after the size-exception receipt validates.

The exception does NOT waive safeguards:

- Historically, the local RDD receipt at
  `docs/release/reviews/review-be4525bc4797e972.md` (Status: pass
  + reachable Candidate Commit + Scope mentioning the branch +
  Verified Commands with PASS entries + Unresolved Blocker Policy
  declaration) was mandatory before merges; the historical gate's step 2
  enforced it. This is retained only for explicit compatibility validation;
  ordinary CI does not consume it. The previous `Authority: official`
  external-binding header is no longer used. The two-context
  Candidate Commit contract (R4-013) means CI runs validate
  against PR tip / PR tip~1, not the synthetic merge commit,
  while local runs keep the R4-006 HEAD/HEAD~1 contract —
  rollback safety is preserved on both paths. Phase 17 (R4-014)
  extends the Go receipt guard at
  `internal/mcp/release_gate_test.go` to validate under the
  CI context (not skip), so the receipt contract is exercised
  in historical CI; a missing or malformed `RELEASE_GATE_PR_HEAD_*`
  pair fails closed rather than silently waiving the check.
  Historical Phase 18 (R5-NEW-001/002/003) further tightened the contract:
  the wrapper-level integration test now exercises the three
  fail-closed classes (missing / partial / invalid) end-to-end
  on a real synthetic two-parent PR merge checkout (not just
  the pure helper); the workflow contract assertion is
  step-scoped so a comment elsewhere cannot satisfy the
  `if: github.event_name == 'pull_request'` check; and the
  bash gate now emits the same three fail-closed class labels
  the Go guard emits, so a CI operator reading the log can
  act on a specific class.
  Historical Phase 19 (R6-NEW-001) replaced the workflow's repo-variable
  dependency with a branch-exact expression: the
  `RELEASE_GATE_SIZE_EXCEPTION` env is set to the literal branch
  name ONLY when `github.head_ref` equals
  `feature/close-fetch-resilience-release-gates-exception`,
  and to `''` otherwise. The bash validator's fail-closed check
  at `scripts/release-gate.sh:496` stays the authoritative seam;
  the empty-string fallback keeps it active for every other
  branch. The activation contract is pinned by the workflow-contract
  test
  `TestReleaseGateWorkflowSetsSizeExceptionForExactBranchOnly`
  plus the `TestReleaseGateSizeExceptionEnvRegexContract`
  negative-control subtests (comment prefix, wrong branch
  literals, missing empty fallback, the old `vars.` form, and
  the negation operator are all rejected by the anchored
  regex). Broadening the carve-out is a two-edit change
  because the branch literal appears in BOTH the equality
  position and the value position of the conditional.
- All Phase 12-19 fixes are committed to the PR diff, not to
  follow-up branches. Phase 16 is the PR #2 CI corrective batch
  (R3-001 / R4-012 / R4-013), Phase 17 is the R4-014
  Go-guard no-skip batch (synthetic-merge deterministic fixture,
  duplicate PR-head test differentiation, corrected stale
  HEAD-or-HEAD~1 comment), Phase 18 is the R5-NEW-001/002/003
  PR_HEAD fail-closed contract tightening batch (wrapper
  refactor + integration test, step-scoped workflow assertion,
  bash three-class fail-closed alignment with Go), and Phase 19
  is the R6-NEW-001 release-gate workflow branch-exact
  size-exception activation batch (workflow env expression +
  workflow-contract test). All four phases are part of the same
  change so the release-gate, the receipt contract, and the
  workflows stay consistent at the PR tip.
- The exception is bounded: it expires 2026-12-31. Any future
  over-budget PR MUST either fit the 400-line budget or split
  into chained PRs.

## Composition of the Over-Budget Diff

This PR contains the work for Phases 1-15 of the SDD change
plus the Phase 16 PR #2 CI corrective batch, the Phase 17
R4-014 Go-guard no-skip batch, the Phase 18 R5-NEW-001/002/003
PR_HEAD fail-closed contract tightening batch, and the
Phase 19 R6-NEW-001 release-gate workflow branch-exact
size-exception activation batch:

- **Phases 1-8** (preserved from the prior apply batch):
  release gate, fetch resilience, connector reliability, anonymous
  Reddit, MCP 1.2.0, documentation parity, full QA.
- **Phase 12** (this batch): pre-production security fixes
  (auth nil-validator, DNS pinning, cancellable retry, server
  Start/Stop, FETCH env vars).
- **Phase 13** (this batch): release-gate hardening (local RDD
  receipt/evidence contract, tracked receipt validation, CI-only
  dirty bypass).
- **Phase 14** (this batch): deployment identity & rollback honesty
  (SHA-pinned image placeholder, systemd identity check, atomic
  rollback doc).
- **Phase 15** (this batch): final QA + receipt creation.
- **Phase 16** (PR #2 CI corrective batch, R3-001 / R4-012 /
  R4-013): the harness env-filter (R3-001) closes six
  test symptoms caused by CI/GitHub env leakage; the gate's
  MERGE_BASE env contract (R4-012) honors the workflow's
  pre-computed value before the local fallback; the two-context
  Candidate Commit contract (R4-013) makes the receipt
  validation representable on a CI PR merge-checkout without
  broadening the R4-006 rollback safety. The
  the historical `.github/workflows/release-gate.yml` workflow exported
  pre-computed MERGE_BASE plus compatibility-only PR-head context; the
  historical ci.yml workflow used
  `fetch-depth: 0` so the receipt validator can resolve
  HEAD~1. The Go receipt-shape guard at
  `internal/mcp/release_gate_test.go` originally skipped on a
  GitHub PR synthetic merge commit (the merge-checkout cannot
  represent the receipt's PR-tip context; the release-gate
  workflow is the authoritative enforcer on CI) — see
  Phase 17 for R4-014 which closed that defense-in-depth
  reduction.
- **Phase 17** (R4-014 Go-guard no-skip batch): the Go
  receipt-shape guard at `internal/mcp/release_gate_test.go`
  no longer `t.Skipf`s on a GitHub PR synthetic merge commit;
  it now reads `RELEASE_GATE_PR_HEAD_SHA` /
  `RELEASE_GATE_PR_HEAD_PARENT_SHA` and validates the receipt
  against the CI contract (R4-014). The synthetic 2-parent
  commit detection helper is now testable via an isolated
  temp-repo fixture (`buildSyntheticTwoParentCommit`,
  `git commit-tree -p <a> -p <b>`) so the true branch of the
  detection helper executes deterministically on every test
  run. The duplicate PR-head tests in
  `scripts/releasegate_test.go` are differentiated so the
  triangulation proves the CI path is taken when the local
  fallback would have failed. The `ci.yml` workflow gains a
  historical conditional `Capture PR metadata` step (gated on
  `github.event_name == 'pull_request'`) that exports the
  same PR_HEAD env pair as `release-gate.yml`, so the historical Go
  guard's CI contract is representable in the CI test
  pipeline. The stale `must equal HEAD or HEAD~1` comment in
  the wrapper docstring and the bash gate's step-2 block
  header is corrected to describe the two-context contract.
- **Phase 18** (R5-NEW-001/002/003 PR_HEAD fail-closed
  contract tightening): the wrapper at
  `internal/mcp/release_gate_test.go` is refactored from
  `rddReceiptValidateStaged(body)` to the parameterized
  `rddReceiptValidateStagedIn(body, repoDir)` so tests can
  drive the wrapper end-to-end on a real synthetic two-parent
  PR merge checkout (`TestRDDReceiptValidateStagedFailClosedOnPRHeadContext`)
  — exercising the three fail-closed classes (missing /
  partial / invalid) the wrapper MUST surface, not just the
  pure helper. The workflow contract test
  `TestCIWorkflowExportsPRHeadContext` is strengthened with
  a step-scoped assertion
  (`findCIWorkflowStepContaining` + `ciWorkflowStepPRHeadIfRegex`)
  that locates the exact YAML step containing BOTH PR_HEAD
  exports and verifies it carries a properly-formatted
  `if: github.event_name == 'pull_request'` line — replacing
  the previous 500-char lookback that was satisfied by any
  `pull_request` mention anywhere near the export. The bash
  gate at `scripts/release-gate.sh` detects the GitHub PR
  merge-checkout shape (CI=true AND event=pull_request AND
  HEAD has 2+ parents via `git cat-file -p HEAD`) and emits
  three distinct fail-closed class labels (`CI PR context
  missing` / `partially set` / `invalid`) that match the Go
  process guard's labels exactly — so a CI operator reading
  the log can act on a specific class instead of the previous
  single generic message. The docstring claiming "four
  KEY= exports" when the test verifies exactly two is
  corrected to match the test's actual required list
  (RELEASE_GATE_PR_HEAD_SHA= and RELEASE_GATE_PR_HEAD_PARENT_SHA=).
- **Phase 19** (R6-NEW-001 release-gate workflow
  branch-exact size-exception activation): the
  `.github/workflows/release-gate.yml` workflow replaces
  `${{ vars.RELEASE_GATE_SIZE_EXCEPTION }}` (the previous
  repo-variable dependency, which left the env unset in this
  repo and caused the `RELEASE_GATE_SIZE_EXCEPTION != CURRENT_BRANCH`
  fail-closed at `scripts/release-gate.sh:496`) with a
  branch-exact expression
  `${{ github.head_ref == 'feature/close-fetch-resilience-release-gates-exception' && 'feature/close-fetch-resilience-release-gates-exception' || '' }}`.
  The expression activates the carve-out ONLY when
  `github.head_ref` equals the canonical branch name; on every
  other branch the empty-string fallback keeps the bash
  validator's fail-closed check authoritative. Broadening the
  carve-out is a two-edit change because the branch literal
  appears in BOTH the equality and the value positions. The
  activation contract is pinned by
  `TestReleaseGateWorkflowSetsSizeExceptionForExactBranchOnly`
  (production check against the workflow file) and the
  `releaseGateSizeExceptionEnvRegex` regex with its
  `TestReleaseGateSizeExceptionEnvRegexContract` subtests
  (nine negative controls: comment prefix, wrong branch
  equality, wrong branch value, missing empty fallback, the
  old `vars.` form, the negation operator, missing env prefix,
  plus the positive single-quote and double-quote canonical
  forms). The workflow-contract test mirrors the R5-NEW-002
  step-scoped discipline so a future regression that smuggles
  a comment or unrelated substring past the assertion surfaces
  here before reaching the runtime gate.

## Cross-references

- **Approval evidence**: #4125 (user-approved decision recorded
  before this receipt was added).
- **Prior apply-progress**: #4138 (Phases 1-8 evidence preserved).
- **Replan tasks**: #4109 (replan #3 with the tracked-receipt path).
- **Historical RDD receipt**: `docs/release/reviews/review-be4525bc4797e972.md`
  (opt-in compatibility evidence only; this size-exception receipt is
  orthogonal and is required only when the diff
  exceeds 400 lines AND `RELEASE_GATE_SIZE_EXCEPTION` matches
  the current branch).
- **Closed lineage note**: #4123 (previous review-be4525bc4797e972
  lineage closed through `review/abandon`; the file path was
  re-purposed for the local RDD receipt).

## Verification Receipt

To re-verify the carve-out is still bound to this branch:

```bash
RELEASE_GATE_BRANCH=feature/close-fetch-resilience-release-gates-exception \
RELEASE_GATE_SIZE_EXCEPTION=feature/close-fetch-resilience-release-gates-exception \
  bash scripts/release-gate.sh
```

The gate MUST log
`SIZE_EXCEPTION_RECEIPT=docs/release/size-exceptions/close-fetch-resilience-and-release-gates.md
validated` (step 3, when the carve-out is active) before exiting 0; an RDD
receipt line is emitted only during explicit compatibility validation.
The previous `Authority=official` line is no longer emitted —
there is no external review provider binding.
