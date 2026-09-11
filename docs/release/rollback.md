# SourceRudder Release Rollback

[English](rollback.md) | [Español](rollback.es.md)

This runbook applies to SourceRudder 2.0 releases after the repository and
license gates have been completed. SourceRudder 2.0.0 is not published yet.

## Invariants

- Roll back to a reviewed commit and an immutable image digest, never a mutable
  tag such as `latest` or `2.0.0` alone.
- Keep the previous binary, image digest, configuration, and secret names until
  the replacement is healthy.
- Do not delete volumes, caches, or rollback artifacts during incident response.
- A Kubernetes rollback is only an operational bridge; reconcile Git afterward.

## Preconditions

1. Identify the previous good commit, release tag, and image digest.
2. Verify the image reference has the form
   `ghcr.io/mdesantis1984/sourcerudder:2.0.0@sha256:<digest>`.
3. Run the normal build, vet, test, race, deployment-contract, and Compose
   validation checks on the rollback commit.
4. Confirm the active `SOURCERUDDER_AUTH_KEY` and optional integration secrets
   remain available. Never copy secrets into Git or incident notes.

## Kubernetes Incident Bridge

```bash
kubectl rollout history deployment/sourcerudder -n sourcerudder
kubectl rollout undo deployment/sourcerudder -n sourcerudder
kubectl rollout status deployment/sourcerudder -n sourcerudder
kubectl get deployment/sourcerudder -n sourcerudder \
  -o jsonpath='{.spec.template.spec.containers[0].image}'
```

Confirm `/healthz`, authenticated `/mcp`, and authenticated `/metrics` before
ending the incident bridge. Then revert or restore the corresponding Git change
so the declared manifest matches the running workload.

## systemd Rollback

```bash
sudo systemctl stop sourcerudder
sudo install -d -o root -g root -m 0755 /etc/sourcerudder/release
sudo install -m 0755 /opt/sourcerudder/releases/<previous>/sourcerudder \
  /opt/sourcerudder/bin/sourcerudder
sudo install -m 0644 /opt/sourcerudder/releases/<previous>/BINARY_SHA256 \
  /etc/sourcerudder/release/BINARY_SHA256
sudo install -m 0644 /opt/sourcerudder/releases/<previous>/sourcerudder.service \
  /etc/systemd/system/sourcerudder.service
sudo install -m 0644 /opt/sourcerudder/releases/<previous>/VERSION \
  /opt/sourcerudder/VERSION
sudo install -m 0644 /opt/sourcerudder/releases/<previous>/IMAGE \
  /opt/sourcerudder/IMAGE
sudo systemctl daemon-reload
sudo systemctl start sourcerudder
sudo systemctl status sourcerudder
```

Verify the archive's GitHub attestation before installation. The binary,
root-owned `BINARY_SHA256`, unit, `VERSION`, and `IMAGE` must come from the same
release bundle. A mismatch must fail closed rather than run an unknown binary or
image.

## Git Reconciliation

Prefer a reviewed `git revert` over rewriting published history:

```bash
git log --first-parent --oneline -20 main
git show --stat <offending-merge-sha>
git revert --no-edit -m 1 <offending-merge-sha>
bash scripts/release-gate.sh
```

Follow the repository's normal protected-branch and review process. Do not push
directly to `main` merely because an incident rollback occurred.

## Completion Checklist

- The running binary and image digest match the selected previous release.
- Health, authenticated MCP, metrics, and representative searches pass.
- Kubernetes or systemd state matches the repository declaration.
- The rollback commit and incident record identify the failed and restored
  versions without exposing credentials.
- Monitoring confirms normal error rate, latency, and resource consumption.
