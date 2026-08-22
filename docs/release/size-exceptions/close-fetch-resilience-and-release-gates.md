# Size Exception Receipt — close-fetch-resilience-and-release-gates

**Status**: tracked (in git). This receipt documents the
one-time branch-level size exception for the SDD change
`close-fetch-resilience-and-release-gates`. The receipt is REQUIRED
by the release gate (`scripts/release-gate.sh`) when the carve-out
is active on this branch. It is REVIEWEABLE in the PR diff because
it lives in the tracked file tree.

**It does NOT substitute for the final official 4R review.** The
release gate still refuses merge while the review file at
`docs/release/reviews/review-be4525bc4797e972.md` does not declare
`Authority: official`. This receipt only documents the exception
for the 400-line authored budget; the official 4R review with
provider-issued binding is a separate, mandatory requirement
(Phase 16).

## Required Fields

| Field | Value |
|-------|-------|
| Branch | `feature/close-fetch-resilience-release-gates-exception` |
| Commit | `<pr-head-sha>` (filled at PR head by the orchestrator) |
| Approval Reference | [#4125](memory://4139) — user-approved baseline size exception |
| Scope | bounded (single-PR exception for Phase 12-14 pre-production security and Phase 13 release-gate hardening) |
| Expiration | 2026-12-31 |
| Authority Requirement | official |

## Parser-Compatible Fields

The release-gate parser at `scripts/release-gate.sh` (lines 138-145)
greps for the six required fields as colon-form lines
(`^Field: value`). The table above is the human-readable summary;
this section mirrors those fields in the parser-accepted shape so the
gate validates this receipt after Phase 16 flips Authority to
`official`. The values are identical to the table; the duplicate
shape exists ONLY so the gate parser can find them.

Branch: feature/close-fetch-resilience-release-gates-exception
Commit: <pr-head-sha>
Approval Reference: #4125 (memory://4139) — user-approved baseline size exception
Scope: bounded (single-PR exception for Phase 12-14 pre-production security and Phase 13 release-gate hardening)
Expiration: 2026-12-31
Authority Requirement: official

## Why This Exception

The user prioritizes completing and deploying the
`close-fetch-resilience-and-release-gates` change promptly after the
outage. Splitting the change into chained PRs would block
production closure on additional reviewer cycles the operator does
not have. The size-exception applies ONLY to this branch and only
after the receipt validates.

The exception does NOT waive safeguards:

- A fresh official 4R review with `Authority: official` (Phase 16)
  is mandatory before production merge.
- All Phase 12-14 fixes are committed to the PR diff, not to
  follow-up branches.
- The exception is bounded: it expires 2026-12-31. Any future
  over-budget PR MUST either fit the 400-line budget or split
  into chained PRs.

## Composition of the Over-Budget Diff

This PR contains the work for Phases 1-15 of the SDD change:

- **Phases 1-8** (preserved from the prior apply batch):
  release gate, fetch resilience, connector reliability, anonymous
  Reddit, MCP 1.2.0, documentation parity, full QA.
- **Phase 12** (this batch): pre-production security fixes
  (auth nil-validator, DNS pinning, cancellable retry, server
  Start/Stop, FETCH env vars).
- **Phase 13** (this batch): release-gate hardening (exact
  Authority: official, tracked receipt validation, CI-only dirty
  bypass).
- **Phase 14** (this batch): deployment identity & rollback honesty
  (SHA-pinned image placeholder, systemd identity check, atomic
  rollback doc).
- **Phase 15** (this batch): final QA + receipt creation.

## Cross-references

- **Approval evidence**: #4125 (user-approved decision recorded
  before this receipt was added).
- **Prior apply-progress**: #4138 (Phases 1-8 evidence preserved).
- **Replan tasks**: #4109 (replan #3 with the tracked-receipt path).
- **Review placeholder**: `docs/release/reviews/review-be4525bc4797e972.md`
  (Authority: pending — flips to `official` only after the final
  official 4R review).
- **Closed lineage note**: #4123 (previous review-be4525bc4797e972
  lineage closed through `review/abandon`).

## Verification Receipt

To re-verify the carve-out is still bound to this branch:

```bash
RELEASE_GATE_BRANCH=feature/close-fetch-resilience-release-gates-exception \
RELEASE_GATE_SIZE_EXCEPTION=feature/close-fetch-resilience-release-gates-exception \
  bash scripts/release-gate.sh
```

The gate MUST log `Authority=official` (after Phase 16) AND
`SIZE_EXCEPTION_RECEIPT=docs/release/size-exceptions/close-fetch-resilience-and-release-gates.md
validated` before exiting 0.
