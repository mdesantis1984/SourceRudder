#!/usr/bin/env bash
# Idempotent installer for the native bgutil PO token server.
#
# Run as root on a Debian/Ubuntu host. It:
#   - installs git, curl, xz-utils and the runtime libraries of the `canvas`
#     module via apt, only when missing
#   - installs the current official Node.js LTS tarball into /opt/nodejs-<version>
#     and points the /opt/nodejs symlink at it (the bgutil server declares
#     engines.node >= 22), verifying it against SHASUMS256.txt; re-running
#     upgrades to a newer LTS; apt's nodejs and /usr/bin/node are never touched
#   - clones the bgutil repository at the pinned tag into /opt/bgutil-pot-provider
#   - builds server/ with `npm ci` and `npx tsc`
#   - creates the unprivileged `bgutil` system user and installs the systemd unit
#
# Debian 13 (trixie) ships Node.js 20, below the server's declared minimum,
# which is why a dedicated runtime is used. The systemd unit calls
# /opt/nodejs/bin/node explicitly.
set -euo pipefail
umask 022

# Single place to bump the bgutil version. Server tag and yt-dlp plugin must
# match: also change BGUTIL_VERSION in install-ytdlp.sh and the pin in
# yt-dlp-update.service, then re-run both installers.
BGUTIL_VERSION="${BGUTIL_VERSION:-2.0.2}"
BGUTIL_REPO="${BGUTIL_REPO:-https://github.com/Brainicism/bgutil-ytdlp-pot-provider.git}"
INSTALL_DIR="${INSTALL_DIR:-/opt/bgutil-pot-provider}"
SERVICE_USER="${SERVICE_USER:-bgutil}"
NODE_MIN_MAJOR="${NODE_MIN_MAJOR:-22}"
NODE_LINK="${NODE_LINK:-/opt/nodejs}"
NODE_DIST_URL="${NODE_DIST_URL:-https://nodejs.org/dist}"
UNIT_SRC="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/bgutil-pot-provider.service"
UNIT_DST="/etc/systemd/system/bgutil-pot-provider.service"

if [ "$(id -u)" -ne 0 ]; then
  echo "install-bgutil.sh must run as root" >&2
  exit 1
fi

node_major() {
  if command -v node >/dev/null 2>&1; then
    node --version | sed -E 's/^v([0-9]+).*/\1/'
  else
    echo 0
  fi
}

apt_candidate_major() {
  apt-cache policy "$1" 2>/dev/null | awk '/Candidate:/ {print $2}' |
    sed -E 's/^([0-9]+:)?([0-9]+).*/\2/' | grep -E '^[0-9]+$' || echo 0
}

export DEBIAN_FRONTEND=noninteractive
apt_updated=0
apt_install() {
  if [ "$apt_updated" -eq 0 ]; then
    apt-get update -qq
    apt_updated=1
  fi
  apt-get install -y --no-install-recommends "$@"
}

for tool in git curl; do
  command -v "$tool" >/dev/null 2>&1 || apt_install "$tool" ca-certificates
done

# Dedicated Node.js LTS runtime behind the /opt/nodejs symlink (official
# tarball, checksum verified). The current LTS is resolved at install time;
# re-running this script upgrades to a newer LTS. apt's nodejs and
# /usr/bin/node are never touched.
node_link_version() {
  if [ -x "${NODE_LINK}/bin/node" ]; then
    "${NODE_LINK}/bin/node" --version
  else
    echo none
  fi
}

case "$(uname -m)" in
  x86_64) node_arch=x64 ;;
  aarch64) node_arch=arm64 ;;
  *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

work="$(mktemp -d)"
trap 'rm -f "$work"/* && rmdir "$work"' EXIT
curl -fsSL --max-time 120 -o "${work}/index.json" "${NODE_DIST_URL}/index.json"
lts_version="$(python3 -I -c '
import json, sys
for entry in json.load(open(sys.argv[1])):
    if entry.get("lts"):
        print(entry["version"])
        break
' "${work}/index.json")"
if [ -z "$lts_version" ]; then
  echo "could not resolve the current Node.js LTS from ${NODE_DIST_URL}/index.json" >&2
  exit 1
fi

if [ "$(node_link_version)" != "$lts_version" ]; then
  command -v xz >/dev/null 2>&1 || apt_install xz-utils
  tarball="node-${lts_version}-linux-${node_arch}.tar.xz"
  curl -fsSL --max-time 120 -o "${work}/SHASUMS256.txt" "${NODE_DIST_URL}/${lts_version}/SHASUMS256.txt"
  curl -fsSL --max-time 600 -o "${work}/${tarball}" "${NODE_DIST_URL}/${lts_version}/${tarball}"
  (cd "$work" && grep -F " ${tarball}" SHASUMS256.txt | sha256sum -c -)
  mkdir -p "${NODE_LINK}-${lts_version}"
  tar -xJf "${work}/${tarball}" --no-same-owner -C "${NODE_LINK}-${lts_version}" --strip-components=1
  # Atomically repoint the symlink (a real directory at NODE_LINK is not replaced).
  ln -sfn "${NODE_LINK}-${lts_version}" "${NODE_LINK}.new"
  mv -T "${NODE_LINK}.new" "$NODE_LINK"
fi

node_version="$(node_link_version)"
if [ "$node_version" = none ] || [ "$(printf '%s' "$node_version" | sed -E 's/^v([0-9]+).*/\1/')" -lt "$NODE_MIN_MAJOR" ]; then
  echo "Node.js >= ${NODE_MIN_MAJOR} is required; found ${node_version} in ${NODE_LINK}" >&2
  exit 1
fi
export PATH="${NODE_LINK}/bin:${PATH}"

# Runtime libraries for the prebuilt `canvas` binary (package names to be
# verified per distro; a failure here is reported but not fatal).
apt_install libcairo2 libpango-1.0-0 libpangocairo-1.0-0 libgif7 librsvg2-2 libpixman-1-0 ||
  echo "warning: could not install canvas runtime libraries; check /ping after install" >&2

if ! id -u "$SERVICE_USER" >/dev/null 2>&1; then
  useradd --system --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin "$SERVICE_USER"
fi

need_build=0
if [ ! -d "${INSTALL_DIR}/.git" ]; then
  git clone --quiet --single-branch --branch "$BGUTIL_VERSION" --depth 1 "$BGUTIL_REPO" "$INSTALL_DIR"
  need_build=1
fi

current_tag="$(git -C "$INSTALL_DIR" describe --tags --exact-match 2>/dev/null || true)"
if [ "$current_tag" != "$BGUTIL_VERSION" ]; then
  git -C "$INSTALL_DIR" fetch --quiet --depth 1 origin "refs/tags/${BGUTIL_VERSION}:refs/tags/${BGUTIL_VERSION}"
  git -C "$INSTALL_DIR" checkout --quiet "$BGUTIL_VERSION"
  need_build=1
fi

if [ ! -f "${INSTALL_DIR}/server/build/main.js" ] || [ ! -d "${INSTALL_DIR}/server/node_modules" ]; then
  need_build=1
fi
# The native `canvas` module is built per Node.js version: rebuild on change.
build_marker="${INSTALL_DIR}/server/.built-with-node"
if [ "$(cat "$build_marker" 2>/dev/null || true)" != "$node_version" ]; then
  need_build=1
fi
if [ "$need_build" -eq 1 ]; then
  (cd "${INSTALL_DIR}/server" && npm ci && npx tsc)
  printf '%s\n' "$node_version" >"$build_marker"
fi

# Code is cloned and built by root with umask 022: root-owned, world-readable,
# and the unit makes it read-only for the service user.

install -m 0644 "$UNIT_SRC" "$UNIT_DST"
systemctl daemon-reload
systemctl enable bgutil-pot-provider.service >/dev/null
systemctl restart bgutil-pot-provider.service

# Wait for the server to answer on loopback.
for _ in $(seq 1 20); do
  if curl -fsS --max-time 2 http://127.0.0.1:4416/ping >/dev/null 2>&1; then
    break
  fi
  sleep 1
done

echo "node:  $("${NODE_LINK}/bin/node" --version)"
echo "bgutil server ${BGUTIL_VERSION}: $(curl -fsS --max-time 5 http://127.0.0.1:4416/ping || echo 'NOT RESPONDING; check journalctl -u bgutil-pot-provider')"
