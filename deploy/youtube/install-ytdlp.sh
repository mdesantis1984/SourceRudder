#!/usr/bin/env bash
# Idempotent installer for the yt-dlp side of SourceRudder YouTube transcripts.
#
# Installs, on a Debian/Ubuntu host (run as root):
#   - python3-venv and a JavaScript runtime (Node.js >= 20) via apt, only if missing
#   - a venv at /opt/yt-dlp with yt-dlp[default] and the bgutil PO token plugin
#   - the /usr/local/bin/yt-dlp symlink
#
# The bgutil plugin version must match the bgutil server installed by
# install-bgutil.sh (BGUTIL_VERSION below and in yt-dlp-update.service).
set -euo pipefail
umask 022

VENV_DIR="${VENV_DIR:-/opt/yt-dlp}"
# Keep in sync with BGUTIL_VERSION in install-bgutil.sh and yt-dlp-update.service.
BGUTIL_VERSION="${BGUTIL_VERSION:-2.0.2}"
NODE_MIN_MAJOR="${NODE_MIN_MAJOR:-20}"
LINK_PATH="${LINK_PATH:-/usr/local/bin/yt-dlp}"

if [ "$(id -u)" -ne 0 ]; then
  echo "install-ytdlp.sh must run as root" >&2
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

# Python venv support (ensurepip is split out on Debian/Ubuntu).
if ! python3 -c 'import ensurepip, venv' >/dev/null 2>&1; then
  apt_install python3 python3-venv
fi

# JavaScript runtime used by yt-dlp to solve YouTube challenges. Prefer the
# dedicated /opt/nodejs runtime from install-bgutil.sh (the SourceRudder unit
# points YTDLP_JS_RUNTIMES at it). Never upgrade an existing runtime blindly:
# only install nodejs from apt when no suitable runtime exists.
NODE_BIN="${NODE_BIN:-/opt/nodejs/bin/node}"
if [ -x "$NODE_BIN" ] && [ "$("$NODE_BIN" --version | sed -E 's/^v([0-9]+).*/\1/')" -ge "$NODE_MIN_MAJOR" ]; then
  js_node="$NODE_BIN"
elif [ "$(node_major)" -ge "$NODE_MIN_MAJOR" ]; then
  js_node="$(command -v node)"
else
  js_node=""
fi
if [ -z "$js_node" ]; then
  if [ "$(apt_candidate_major nodejs)" -ge "$NODE_MIN_MAJOR" ]; then
    apt_install nodejs
    js_node="$(command -v node)"
  else
    echo "Node.js >= ${NODE_MIN_MAJOR} is required and the apt candidate is too old." >&2
    echo "Install a newer Node.js (see docs/deployment.md, 'YouTube transcripts') and re-run." >&2
    exit 1
  fi
fi

if [ ! -x "${VENV_DIR}/bin/python" ]; then
  python3 -m venv "$VENV_DIR"
fi

PIP_DISABLE_PIP_VERSION_CHECK=1 PIP_NO_CACHE_DIR=1 \
  "${VENV_DIR}/bin/pip" install --upgrade \
  "yt-dlp[default]" "bgutil-ytdlp-pot-provider==${BGUTIL_VERSION}"

ln -sfn "${VENV_DIR}/bin/yt-dlp" "$LINK_PATH"

echo "yt-dlp:        $("${VENV_DIR}/bin/yt-dlp" --version)"
echo "bgutil plugin: $("${VENV_DIR}/bin/pip" show bgutil-ytdlp-pot-provider | awk '/^Version:/ {print $2}')"
echo "node:          ${js_node} $("$js_node" --version)"
echo "python:        $("${VENV_DIR}/bin/python" --version 2>&1)"
echo "Next: install the bgutil server (install-bgutil.sh) and the update timer (docs/deployment.md)."
