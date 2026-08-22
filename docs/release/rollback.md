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
   promoting is a mandatory sanity check.

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

# 4. Localise the rollback commit to a SHA-pinned release-image.
make release-image GHCR=ghcr.io/thiscloud REGISTRY=ia-buscar
# Output: release-image: SHA=<new-sha> IMAGE=ghcr.io/thiscloud/ia-buscar:<new-sha>

# 5. Run the release gate locally so the CI gate cannot disagree.
git diff
bash scripts/release-gate.sh
# Expected: release-gate: PASS

# 6. Push directly to main (the size-exception carve-out is tracked
#    on the feature branch only; the rollback is on main, so the
#    gate's strict 400-line budget applies and the revert must
#    be a clean one-commit change).
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
      with the tracked receipt present and `Authority: official`
      in the review file.
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

## What Rollback Does NOT Do

- It does not erase the `close-fetch-resilience-and-release-gates`
  size-exception receipt. The receipt is a tracking artefact for
  the carve-out the operator approved; it remains in git history.
- It does not flip the `Authority: official` review header back to
  `pending`. The 4R review is the forward reference; the gate
  continues to enforce it on every merge.
- It does not modify the prior apply-progress in Engram. The
  previous progress records remain available for audit.
- It does not silence the `release-gate: FAIL` block if the
  review file is still `Authority: pending`. The gate is the
  final safeguard; the rollback must not bypass it.
