[English](deployment.md) | [Español](deployment.es.md)

# SourceRudder deployment model

This document describes SourceRudder deployment artifacts and operational expectations; it does not authorize or perform a deployment.

## Supported artifact paths

| Target | Artifact | Intended boundary |
|---|---|---|
| Local Compose | `compose.yaml` | SourceRudder and SearXNG, published only on loopback ports. |
| Isolated quality baseline | `deploy/quality/docker-compose.yml` | A separate `sourcerudder-quality` project and network for live-baseline evidence. |
| Kubernetes | `deploy/kubernetes/deployment.yaml` | A `ClusterIP` service with an immutable image-digest placeholder. |
| systemd | `deploy/systemd/sourcerudder.service` | Host-managed endpoint using `/opt/sourcerudder` and `/etc/sourcerudder/sourcerudder.env`. |

## Container controls

The Compose artifacts pin the external SearXNG image by digest. They use `restart: "no"`, hard memory limits with swap equal to memory, CPU and PID limits, and `json-file` log rotation. Published ports bind to `127.0.0.1`; expose an endpoint externally only through a reviewed reverse proxy or network policy.

The quality stack is intentionally isolated from the root Compose project. It has its own network and temporary credentials, and is for evidence collection rather than production probing.

## Kubernetes and systemd

Kubernetes uses the `sourcerudder` namespace and an `<IMAGE>` placeholder that the release pipeline renders into a separate release asset containing `@sha256:...`; do not commit or deploy a mutable tag. In that namespace, the `sourcerudder` Secret supplies `auth-key`, and the separately managed `sourcerudder` ConfigMap must supply a reachable `searxng-url`. The manifest deliberately fails to start when either dependency is absent. Resource limits, disabled service-account token mounting, a read-only root filesystem, a default seccomp profile, and health probes protect the running pod.

The systemd unit reads secrets from `/etc/sourcerudder/sourcerudder.env`, starts `/opt/sourcerudder/bin/sourcerudder`, and verifies `/opt/sourcerudder/VERSION`, `/opt/sourcerudder/IMAGE`, and the root-owned `/etc/sourcerudder/release/BINARY_SHA256` before start. Linux archives include the architecture-specific `BINARY_SHA256`; each release also includes the rendered unit, `VERSION`, and `IMAGE`. Install them with the matching binary as one atomic release set. The unit makes both release paths read-only to the `sourcerudder` process.

Before installing a downloaded archive, verify its published checksum and GitHub provenance attestation:

```bash
sha256sum -c checksums.txt --ignore-missing
gh attestation verify sourcerudder_3.0.0_linux_amd64.tar.gz \
  --repo mdesantis1984/SourceRudder
```

The local `BINARY_SHA256` guard detects an accidentally mixed or replaced
binary after installation. The GitHub attestation is the independent provenance
anchor; neither control protects a host after root compromise.

## Secrets and release safety

- Keep Compose secrets in an ignored, permission-restricted local environment file; never place credentials in Compose, manifests, or source control.
- Use Kubernetes Secrets or an approved secret manager for cluster credentials.
- Record the reviewed source revision and immutable image digest before an upgrade.
- Verify health, MCP initialization, tool listing, and representative provider calls after a change.
- Roll back by restoring the previous known-good revision or digest. For systemd, restore the binary, root-owned `BINARY_SHA256`, rendered unit, `VERSION`, and `IMAGE` together.
- Protect the GitHub `release` environment with a deployment policy limited to approved release tags. Keep package and release permissions scoped to the workflow job.

For exact runtime settings and recovery commands, see the deployment artifacts and the operations documentation. No release or deployment is implied by this guide.
