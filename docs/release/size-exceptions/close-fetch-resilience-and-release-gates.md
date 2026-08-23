# Size Exception Receipt — close-fetch-resilience-and-release-gates

**Status**: tracked (in git). This receipt documents the
one-time branch-level size exception for the SDD change
`close-fetch-resilience-and-release-gates`. The receipt is REQUIRED
by the release gate (`scripts/release-gate.sh`) when the carve-out
is active on this branch. It is REVIEWEABLE in the PR diff because
it lives in the tracked file tree.

**It does NOT substitute for the local RDD receipt.** The release
gate's step 2 is satisfied by the RDD receipt at
`docs/release/reviews/review-be4525bc4797e972.md` (Status: pass +
reachable Candidate Commit + branch-mentioning Scope + Verified
Commands with PASS entries + Unresolved Blocker Policy declaration).
This size-exception receipt only documents the line-budget carve-out;
the RDD receipt is the forward authority attestation and is a
separate, mandatory requirement on every merge.

The previous `Authority: official` external-binding header has been
removed entirely. There is NO external review provider binding —
the RDD receipt is the local operator's attestation, and the gate
independently re-runs `go build` / `go vet` / `go test` / `go test
-race` so a forged receipt cannot bypass a real regression.

## Required Fields

| Field | Value |
|-------|-------|
| Branch | `feature/close-fetch-resilience-release-gates-exception` |
| Commit | `2b91adbb652f0944384fd7b41bdd0947b96688aa` (Phase 17 R4-014 fix commit; the receipt re-authoring that follows sits at HEAD) |
| Approval Reference | [#4125](memory://4139) — user-approved baseline size exception |
| Scope | bounded (single-PR exception for Phase 12-17: Phase 12-14 pre-production security, Phase 13 release-gate hardening, Phase 16 PR #2 CI corrective batch, Phase 17 R4-014 Go-guard no-skip + synthetic-merge deterministic fixture + duplicate PR-head test differentiation) |
| Expiration | 2026-12-31 |
| Forward Reference | `docs/release/reviews/review-be4525bc4797e972.md` (the RDD receipt is the local authority attestation; this size-exception receipt NEVER substitutes for it) |

## Parser-Compatible Fields

The release-gate parser at `scripts/release-gate.sh` (in step 3,
the size-budget check) greps for the six required fields as
colon-form lines (`^Field: value`). The table above is the
human-readable summary; this section mirrors those fields in the
parser-accepted shape so the gate validates this receipt after the
local RDD receipt at `docs/release/reviews/review-be4525bc4797e972.md`
declares `Status: pass`. The values are identical to the table; the
duplicate shape exists ONLY so the gate parser can find them.

Branch: feature/close-fetch-resilience-release-gates-exception
Commit: 2b91adbb652f0944384fd7b41bdd0947b96688aa
Approval Reference: #4125 (memory://4139) — user-approved baseline size exception
Scope: bounded (single-PR exception for Phase 12-17: Phase 12-14 pre-production security, Phase 13 release-gate hardening, Phase 16 PR #2 CI corrective batch R3-001 / R4-012 / R4-013, and Phase 17 R4-014 Go-guard no-skip / synthetic-merge deterministic fixture / duplicate PR-head test differentiation)
Expiration: 2026-12-31
Forward Reference: docs/release/reviews/review-be4525bc4797e972.md (RDD receipt is the local authority attestation)

## Why This Exception

The user prioritizes completing and deploying the
`close-fetch-resilience-and-release-gates` change promptly after the
outage. Splitting the change into chained PRs would block
production closure on additional reviewer cycles the operator does
not have. The size-exception applies ONLY to this branch and only
after the receipt validates.

The exception does NOT waive safeguards:

- The local RDD receipt at
  `docs/release/reviews/review-be4525bc4797e972.md` (Status: pass
  + reachable Candidate Commit + Scope mentioning the branch +
  Verified Commands with PASS entries + Unresolved Blocker Policy
  declaration) is mandatory before any merge; the gate's step 2
  enforces it on every merge. The previous `Authority: official`
  external-binding header is no longer used. The two-context
  Candidate Commit contract (R4-013) means CI runs validate
  against PR tip / PR tip~1, not the synthetic merge commit,
  while local runs keep the R4-006 HEAD/HEAD~1 contract —
  rollback safety is preserved on both paths. Phase 17 (R4-014)
  extends the Go receipt guard at
  `internal/mcp/release_gate_test.go` to validate under the
  CI context (not skip), so the receipt contract is exercised
  on every CI run; a missing or malformed `RELEASE_GATE_PR_HEAD_*`
  pair fails closed rather than silently waiving the check.
- All Phase 12-17 fixes are committed to the PR diff, not to
  follow-up branches. Phase 16 is the PR #2 CI corrective batch
  (R3-001 / R4-012 / R4-013) and Phase 17 is the R4-014
  Go-guard no-skip batch (synthetic-merge deterministic fixture,
  duplicate PR-head test differentiation, corrected stale
  HEAD-or-HEAD~1 comment). Both phases are part of the same
  change so the release-gate, the receipt contract, and the
  workflows stay consistent at the PR tip.
- The exception is bounded: it expires 2026-12-31. Any future
  over-budget PR MUST either fit the 400-line budget or split
  into chained PRs.

## Composition of the Over-Budget Diff

This PR contains the work for Phases 1-15 of the SDD change
plus the Phase 16 PR #2 CI corrective batch and the Phase 17
R4-014 Go-guard no-skip batch:

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
  `.github/workflows/release-gate.yml` workflow exports the
  pre-computed MERGE_BASE plus RELEASE_GATE_PR_HEAD_SHA /
  RELEASE_GATE_PR_HEAD_PARENT_SHA; the ci.yml workflow uses
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
  conditional `Capture PR metadata` step (gated on
  `github.event_name == 'pull_request'`) that exports the
  same PR_HEAD env pair as `release-gate.yml`, so the Go
  guard's CI contract is representable in the CI test
  pipeline. The stale `must equal HEAD or HEAD~1` comment in
  the wrapper docstring and the bash gate's step-2 block
  header is corrected to describe the two-context contract.

## Cross-references

- **Approval evidence**: #4125 (user-approved decision recorded
  before this receipt was added).
- **Prior apply-progress**: #4138 (Phases 1-8 evidence preserved).
- **Replan tasks**: #4109 (replan #3 with the tracked-receipt path).
- **RDD receipt**: `docs/release/reviews/review-be4525bc4797e972.md`
  (the local deterministic authority attestation; this size-exception
  receipt is orthogonal — the RDD receipt is required on every merge,
  this size-exception receipt is required only when the diff
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
`RDD_RECEIPT=docs/release/reviews/review-be4525bc4797e972.md validated`
(step 2) AND `SIZE_EXCEPTION_RECEIPT=docs/release/size-exceptions/close-fetch-resilience-and-release-gates.md
validated` (step 3, when the carve-out is active) before exiting 0.
The previous `Authority=official` line is no longer emitted —
there is no external review provider binding.
