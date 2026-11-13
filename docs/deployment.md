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

The systemd unit reads secrets from `/etc/sourcerudder/sourcerudder.env`, starts `/opt/sourcerudder/bin/sourcerudder` on `127.0.0.1:8080`, and verifies `/opt/sourcerudder/VERSION`, `/opt/sourcerudder/IMAGE`, and the root-owned `/etc/sourcerudder/release/BINARY_SHA256` before start. Linux archives include the architecture-specific `BINARY_SHA256`; each release also includes the rendered unit, `VERSION`, and `IMAGE`. Install them with the matching binary as one atomic release set. The unit makes both release paths read-only to the `sourcerudder` process. An operator-managed bridge may replace the listen address with an explicit private IP; do not use an empty-host wildcard on a dual-stack host.

Before installing a downloaded archive, verify its published checksum and GitHub provenance attestation:

```bash
sha256sum -c checksums.txt --ignore-missing
gh attestation verify sourcerudder_3.0.0_linux_amd64.tar.gz \
  --repo mdesantis1984/SourceRudder
```

The local `BINARY_SHA256` guard detects an accidentally mixed or replaced
binary after installation. The GitHub attestation is the independent provenance
anchor; neither control protects a host after root compromise.

## YouTube transcripts

`fetch_youtube_transcript` (and the YouTube delegation in `fetch_url` / `fetch_and_extract`) runs yt-dlp on the host, because YouTube answers direct page scraping with HTTP 429. Without the pieces below the tool returns `youtube_unavailable`. Out of scope by design: Google account cookies, paid or residential proxies, and downloading audio or video (only one subtitle track is fetched).

### Host requirements

- A recent yt-dlp (`yt-dlp[default]`, installed in a venv at `/opt/yt-dlp`), run with IPv4 forced; SourceRudder already passes `--force-ipv4`.
- A JavaScript runtime for yt-dlp (Node.js 20+ or Deno).
- The bgutil PO token plugin `bgutil-ytdlp-pot-provider` 2.0.2 for yt-dlp, and the bgutil server of the same version listening on `127.0.0.1:4416` only. The server declares Node.js 22+; Debian 13 ships Node.js 20, so the installer resolves the current Node.js LTS from `https://nodejs.org/dist/index.json` at install time, verifies the official tarball against `SHASUMS256.txt`, extracts it to `/opt/nodejs-<version>`, and atomically repoints the `/opt/nodejs` symlink. It never touches apt's `nodejs` or `/usr/bin/node`.

### Install

Run as root on the Debian/Ubuntu host, from a checkout of this repository:

```bash
sudo deploy/youtube/install-bgutil.sh   # /opt/nodejs (current LTS), /opt/bgutil-pot-provider, user "bgutil", bgutil-pot-provider.service
sudo deploy/youtube/install-ytdlp.sh    # /opt/yt-dlp venv, yt-dlp + bgutil plugin 2.0.2, /usr/local/bin/yt-dlp
sudo install -m 0644 deploy/youtube/yt-dlp-update.service deploy/youtube/yt-dlp-update.timer /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now yt-dlp-update.timer
```

Both scripts are idempotent; the unit and `YTDLP_JS_RUNTIMES` use the version-neutral `/opt/nodejs/bin/node` path. The bgutil server runs as the unprivileged `bgutil` user with `--host 127.0.0.1 --port 4416`, basic hardening (`NoNewPrivileges`, `ProtectSystem=strict`, `ProtectHome`, `PrivateTmp`), and `Restart=on-failure`. `MemoryDenyWriteExecute` is intentionally unset because Node.js needs a JIT. The released `sourcerudder.service` already sets `YTDLP_PATH=/opt/yt-dlp/bin/yt-dlp`, `YTDLP_JS_RUNTIMES=node:/opt/nodejs/bin/node`, and `YOUTUBE_POT_BASE_URL=http://127.0.0.1:4416`; yt-dlp runs with `--no-cache-dir`, so the unit needs no extra writable path.

Docker alternative (when you prefer a container over the native server):

```bash
docker run -d --init --restart unless-stopped --name bgutil-pot \
  -p 127.0.0.1:4416:4416 brainicism/bgutil-ytdlp-pot-provider:2.0.2
```

The server listens on all interfaces inside the container; the `127.0.0.1:` prefix in the port mapping is what keeps it off the network. Never publish the port without it: the server is unauthenticated and anyone who reaches it can mint tokens. With Compose, use `ports: ["127.0.0.1:4416:4416"]` and `restart: unless-stopped`. Do not run the Docker and native servers at the same time.

### Verify

```bash
curl -fsS http://127.0.0.1:4416/ping          # JSON with server_uptime and version
/opt/yt-dlp/bin/yt-dlp --version
/opt/yt-dlp/bin/yt-dlp -v --ignore-config --skip-download --force-ipv4 \
  --js-runtimes node:/opt/nodejs/bin/node \
  "https://www.youtube.com/watch?v=<video-id>" 2>&1 | grep -iE 'pot|bgutil'
```

The verbose output should list the `bgutil` PO token provider and a PO token request. Run the last command sparingly: every run is a real request to YouTube from the server IP.

### Update yt-dlp

YouTube changes often and old yt-dlp versions stop working. `yt-dlp-update.timer` runs weekly (with a randomized delay) and executes `pip install --upgrade "yt-dlp[default]" "bgutil-ytdlp-pot-provider==2.0.2"`. Do not use `yt-dlp -U`: it cannot update a pip-installed yt-dlp. The plugin stays pinned to the bgutil server version; when you move the server to another tag, change `BGUTIL_VERSION` and the pin in `yt-dlp-update.service` together. The timer covers yt-dlp only. To upgrade the Node.js LTS runtime or bgutil, re-run `install-bgutil.sh` (it installs a newer LTS when one exists and rebuilds the server when the Node.js version changes). To bump bgutil, change `BGUTIL_VERSION` (the server tag and the pip plugin must match) in `install-bgutil.sh` and `install-ytdlp.sh`, change the pin in `yt-dlp-update.service`, reinstall that unit, and re-run both installers. Manual update:

```bash
sudo systemctl start yt-dlp-update.service
journalctl -u yt-dlp-update.service -n 20
```

### Troubleshooting `youtube_blocked`

YouTube rate-limited or bot-checked the server IP. SourceRudder reports it once, does not retry, does not cache the failure, and pauses all YouTube requests for `YOUTUBE_BLOCK_COOLDOWN` (15 minutes by default). Do not retry in a loop and do not try other fetch tools for the same video. Wait, confirm bgutil answers `/ping` and yt-dlp is current, then try a single video again. If the block persists, the IP is flagged; this deployment does not use cookies or proxies.

## Secrets and release safety

- Keep Compose secrets in an ignored, permission-restricted local environment file; never place credentials in Compose, manifests, or source control.
- Use Kubernetes Secrets or an approved secret manager for cluster credentials.
- Record the reviewed source revision and immutable image digest before an upgrade.
- Verify health, MCP initialization, tool listing, and representative provider calls after a change.
- Roll back by restoring the previous known-good revision or digest. For systemd, restore the binary, root-owned `BINARY_SHA256`, rendered unit, `VERSION`, and `IMAGE` together.
- Protect the GitHub `release` environment with a deployment policy limited to approved release tags. Keep package and release permissions scoped to the workflow job.

For exact runtime settings and recovery commands, see the deployment artifacts and the operations documentation. No release or deployment is implied by this guide.
