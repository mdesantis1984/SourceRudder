# Rollback Procedure — ia-buscar

This document is the single source of truth for reverting an
`ia-buscar` production deployment. Every release is pinned to a
commit SHA so the rollback path is always to a known, reviewed
artifact — never to a floating mutable tag like `:latest` or
`:1.2.0`.

## Preconditions

Before any rollback, confirm:

1. The current commit SHA on the production host matches the SHA
   recorded in the release manifest. The systemd unit's
   `ExecStartPre` `cmp` line enforces this on every start; a
   mismatch means the host is already in a degraded state and the
   rollback target must be chosen carefully.
2. The candidate rollback SHA must be present in the git history
   AND must have a corresponding container image in the registry.
   Both are produced by the `make release-image` target at the
   time of the original merge.
3. The `close-fetch-resilience-and-release-gates` release gate
   (Phase 13) must have PASSED on the rollback SHA. The gate's
   `./scripts/release-gate.sh` exits 0 on a clean checkout
   matching the SHA; running it on the rollback commit before
   promoting is a mandatory sanity check. The gate's step 2 is
   satisfied by the local RDD receipt at
   `docs/release/reviews/review-be4525bc4797e972.md` (Status:
   pass + Candidate Commit equal to HEAD or HEAD~1 of the LOCAL
   checkout, or PR_HEAD_SHA / PR_HEAD_PARENT_SHA of the CI
   merge-checkout + exact Branch: field + free-form Scope +
   Verified Commands section with PASS entries + Unresolved
   Blocker Policy declaration). The previous `Authority: official`
   header has been removed — there is no external review provider
   binding.

   The Candidate Commit check is CI-merge-aware (R4-013): a
   local run uses HEAD/HEAD~1; a CI run on a GitHub pull_request
   event uses the two SHAs the workflow exports
   (`RELEASE_GATE_PR_HEAD_SHA` = PR tip and
   `RELEASE_GATE_PR_HEAD_PARENT_SHA` = PR tip~1). The CI
   merge-checkout's HEAD is the synthetic merge commit and
   cannot represent the receipt's PR-tip context directly,
   so the workflow exports the explicit context. The contract
   is exact-match on the two SHAs in either context — arbitrary
   ancestors are NOT accepted, so the R4-006 rollback safety
   is preserved.

   The gate's MERGE_BASE pre-condition (R4-012) accepts a
   pre-computed value from the workflow
   (`MERGE_BASE=$(git merge-base $BASE_SHA HEAD)`) before
   falling back to the local `git merge-base "$BASE_REF" HEAD`
   re-resolution. The CI PR checkout does NOT fetch a local
   `main` ref, so the in-script re-resolution fails; the
   pre-computed value is the authoritative path under CI.

4. A **rollback forces a fresh RDD receipt**. The receipt's
   `Candidate Commit` field MUST equal HEAD or HEAD~1 of the
   new HEAD (local context) OR the PR_HEAD_SHA / PR_HEAD_PARENT_SHA
   the workflow exports (CI context). After `git revert
   <buggy-sha>`, the buggy SHA is HEAD~2 or deeper of the
   rollback commit, so the existing receipt's `Candidate
   Commit` no longer satisfies the precise contract and the
   gate will fail. The operator MUST re-author the receipt as
   a separate commit (a "recovery receipt") with:
   - `Status: pass` (kept; the receipt is the local
     attestation)
   - `Candidate Commit: <rollback-sha>` (the SHA of the
     rollback commit, which becomes HEAD~1 after the
     receipt is committed — the canonical two-commit flow
     that satisfies the local context; in CI the same value
     satisfies the PR_HEAD_PARENT_SHA half of the CI
     contract)
   - `Branch: <branch>` (unchanged)
   - `Verified Commands:` re-run against the rolled-back
     code (the operator re-executes the `go build / vet /
     test / -race` quartet)
   - `Unresolved Blocker Policy: <status>` (updated to
     describe the rollback)

   The re-authoring commit is the new HEAD and the rollback
   commit is HEAD~1, satisfying the precise contract under
   the local context. In CI, the workflow exports
   PR_HEAD_SHA = the new HEAD (the receipt re-authoring
   commit) and PR_HEAD_PARENT_SHA = the rollback commit; the
   receipt's Candidate Commit = PR_HEAD_PARENT_SHA satisfies
   the CI contract. A `Status: fail` flip is NOT the right
   answer — the gate would still fail because the receipt
   would lack `Verified Commands:` entries, and the contract
   would remain ambiguous about whether the receipt attests
   the rollback or the original buggy SHA.

## Atomic Git Revert (canonical path)

The preferred rollback is an atomic `git revert` of the offending
merge commit, followed by a fast-forward of `main`:

```bash
# 1. Identify the offending merge commit.
git log --first-parent --oneline -20 main

# 2. Inspect the change to confirm it is the right candidate.
git show --stat <merge-sha>

# 3. Atomic revert. `--no-edit` keeps the commit message tight.
git checkout main
git pull --ff-only
git revert --no-edit -m 1 <merge-sha>

# 4. Re-author the RDD receipt as a SEPARATE commit on top of
#    the revert. The precise Candidate Commit contract
#    (HEAD or HEAD~1) requires a fresh receipt — the
#    original receipt's Candidate Commit is now HEAD~2
#    of the new HEAD and the gate will fail until the
#    receipt is re-anchored.
#
#    Author the receipt via the two-commit flow (placeholder
#    + finalize) so the placeholder SHA stays reachable
#    from HEAD; the final receipt's Candidate Commit MUST
#    be the SHA of the revert commit you just made.
ROLLBACK_SHA="$(git rev-parse HEAD)"
# (edit docs/release/reviews/review-be4525bc4797e972.md:
#   Candidate Commit: $ROLLBACK_SHA
#   Branch: <unchanged>
#   Verified Commands: re-run go build/vet/test/-race
#   Unresolved Blocker Policy: rollback for <original-sha>)
git add docs/release/reviews/review-be4525bc4797e972.md
git commit -m "chore: re-author RDD receipt for rollback $ROLLBACK_SHA"

# 5. Localise the rollback commit to a SHA-pinned release-image.
make release-image GHCR=ghcr.io/thiscloud REGISTRY=ia-buscar
# Output: release-image: SHA=<new-sha> IMAGE=ghcr.io/thiscloud/ia-buscar:<new-sha>

# 6. Run the release gate locally so the CI gate cannot disagree.
git diff
bash scripts/release-gate.sh
# Expected: release-gate: PASS
#   (the receipt's Candidate Commit equals HEAD~1 of HEAD,
#   the receipt's Branch field exactly matches the resolved
#   branch, the Verified Commands section re-attests the
#   rolled-back code, and step 4 re-runs Go against the
#   rolled-back code)

# 7. Push directly to main (the size-exception carve-out is tracked
#    on the feature branch only; the rollback is on main, so the
#    gate's strict 400-line budget applies and the revert must
#    be a clean one-commit change plus the receipt commit).
git push origin main
```

The push to `main` triggers the release pipeline which builds the
new image and tags staging. Once staging is green, the same
image is promoted to production.

## Kubernetes Rollback (incident bridge)

For an in-flight incident where `git revert` is too slow, use the
blue/green rollback the deployment already supports:

```bash
# 1. Identify the previous good revision.
kubectl rollout history deployment/ia-buscar -n ia-buscar

# 2. Roll back to the previous revision. Kubernetes records the
#    previous ReplicaSet and switches back.
kubectl rollout undo deployment/ia-buscar -n ia-buscar

# 3. Watch the rollout complete.
kubectl rollout status deployment/ia-buscar -n ia-buscar

# 4. Confirm the image matches the SHA you expected.
kubectl get deployment/ia-buscar -n ia-buscar -o jsonpath='{.spec.template.spec.containers[0].image}'
# Expected: ghcr.io/thiscloud/ia-buscar:<previous-sha>
```

`kubectl rollout undo` is a bridge only — it does NOT create a
git commit. After the rollback halts the bleeding, run the
atomic `git revert` path so the rollback is reflected in the
git history and the release artefacts.

## systemd Rollback (single-host)

The systemd unit's `ExecStartPre` lines check the `VERSION` and
`IMAGE` files on disk against the values baked into the unit. To
roll back:

```bash
# 1. Stop the service.
sudo systemctl stop ia-buscar

# 2. Restore the previous binary, VERSION, and IMAGE files. The
#    previous image corresponds to the previous SHA-pinned
#    release; the artefacts are kept in /opt/ia-buscar/<sha>/
#    alongside the active symlink.
sudo /opt/ia-buscar/scripts/restore-previous \
  --sha <previous-sha> \
  --image <previous-image>

# 3. Refresh the systemd unit so the new SHA is in the
#    ExecStartPre placeholders.
sudo cp /opt/ia-buscar/<previous-sha>/ia-buscar.service \
  /etc/systemd/system/ia-buscar.service
sudo systemctl daemon-reload

# 4. Start the service. The ExecStartPre cmp will fail loudly if
#    the binary's VERSION/IMAGE do not match the unit.
sudo systemctl start ia-buscar
sudo systemctl status ia-buscar
```

## Dry-Run Checklist

Before any rollback reaches production, run through this checklist
on a clean checkout of the rollback SHA:

- [ ] `git log --first-parent -1` shows the revert commit (or the
      previous-good SHA) as HEAD.
- [ ] `bash scripts/release-gate.sh` returns `release-gate: PASS`
      with the RDD receipt at
      `docs/release/reviews/review-be4525bc4797e972.md` declaring
      `Status: pass` and every other required field.
- [ ] `go build ./...` and `go vet ./...` exit 0.
- [ ] `go test ./... -race` exits 0 with no data races.
- [ ] `make release-image` writes the expected SHA-pinned image
      into `deploy/kubernetes/deployment.yaml`.
- [ ] The deployment manifest's `image:` field matches the
      `release-image` output.
- [ ] The systemd unit's `ExecStartPre` lines reference the same
      `@IMAGE@` and `@VERSION@` that `release-image` resolved.
- [ ] A staging rollout of the SHA-pinned image succeeds
      (smoke: anonymous Reddit, fetch timeout, degradation metric,
      redirect cap, DNS-rebinding blocked).
- [ ] The cancellation tests in `internal/fetch/fetcher_test.go`
      pass on the rollback SHA (they cover the Phase 12.5
      cancellable retry behaviour).

## Two-Context Candidate Commit Contract (R4-006 + R4-013)

The release gate's `Candidate Commit` check operates in two
contexts:

| Context | Trigger | Allowed SHAs |
|---------|---------|--------------|
| Local (operator run, push event, local checkout) | Neither `RELEASE_GATE_PR_HEAD_SHA` nor `RELEASE_GATE_PR_HEAD_PARENT_SHA` is set | HEAD or HEAD~1 |
| CI (GitHub Actions pull_request job) | Both `RELEASE_GATE_PR_HEAD_SHA` and `RELEASE_GATE_PR_HEAD_PARENT_SHA` are set by the workflow's `Capture PR metadata` step | PR_HEAD_SHA (PR tip) or PR_HEAD_PARENT_SHA (PR tip~1) |

The two contexts are mutually exclusive: a CI run ALWAYS sets
both PR_HEAD env vars; a local run NEVER sets them. The gate
selects the context from env presence alone — no additional
heuristic. A partially-set PR_HEAD env (one var set, the other
empty) fails closed.

The CI context is required because `actions/checkout@v4` on a
pull_request event produces a synthetic merge commit whose
HEAD~1 is the PR tip and HEAD~2 is the receipt's actual code
commit. None of those three SHAs match the receipt's `Candidate
Commit` field by the local contract (HEAD or HEAD~1), so without
the PR_HEAD env the gate would refuse a receipt authored against
the PR tip. The explicit env export makes the CI path
representable.

The CI contract is NOT a broadening of the rollback safety. The
allowed set in CI is exactly two SHAs (PR tip and PR tip~1);
arbitrary ancestors are still rejected. A receipt attesting a
SHAs deeper than the PR tip~1 will fail the gate with "RDD
receipt Candidate Commit <sha> must equal PR_HEAD_SHA or
PR_HEAD_PARENT_SHA" — the same fail-closed behavior the local
contract enforces.

The corresponding Go test guard at
`internal/mcp/release_gate_test.go`
(`TestReleaseGateRDDReceiptSatisfiesLocalContract`) skips on a
GitHub PR synthetic merge commit via the
`isGitHubPRMergeCheckout` helper (CI=true AND
GITHUB_EVENT_NAME=pull_request AND HEAD has 2+ parents). The
helper exists so the receipt-shape guard is not masked by the
fail-closed "branch context unresolvable" error on a
merge-checkout whose HEAD is the synthetic merge commit. The
release-gate workflow is the authoritative enforcer of the
receipt contract on CI; the Go guard exists to pin the receipt
shape for the PR-tip checkout shape only.

## What Rollback Does NOT Do

- It does not erase the `close-fetch-resilience-and-release-gates`
  size-exception receipt. The receipt is a tracking artefact for
  the carve-out the operator approved; it remains in git history.
- It does not flip the RDD receipt's `Status: pass` line back to
  `pending`. The receipt is the local deterministic attestation;
  the gate continues to enforce its field contract on every merge.
  The previous `Authority: official` external-binding header has
  been removed entirely; rollback does not re-introduce it.
  Rollback DOES require a fresh receipt (a separate commit with
  `Candidate Commit: <rollback-sha>`); see precondition 4 above
  and the canonical path step 4.
- It does not modify the prior apply-progress in Engram. The
  previous progress records remain available for audit.
- It does not silence the `release-gate: FAIL` block if the
  RDD receipt does not validate. The gate is the final safeguard;
  the rollback must not bypass it. A rollback that ships without
  a fresh receipt will fail the gate with "RDD receipt Candidate
  Commit <old-sha> must equal HEAD or HEAD~1" — this is the
  correct, fail-closed behaviour and the operator MUST author a
  fresh receipt before the rollback can be promoted.
