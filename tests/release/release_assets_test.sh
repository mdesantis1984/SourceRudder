#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DIST_DIR="$(mktemp -d)"
ARCHIVES_ONE="$(mktemp -d)"
ARCHIVES_TWO="$(mktemp -d)"
trap 'rm -rf "$DIST_DIR" "$ARCHIVES_ONE" "$ARCHIVES_TWO"' EXIT

DIGEST="sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
VERSION="$(awk -F= '$1 == "VERSION" { print $2 }' "$ROOT_DIR/Makefile")"
IMAGE="ghcr.io/mdesantis1984/sourcerudder:$VERSION@$DIGEST"
RELEASE_WORKFLOW="$ROOT_DIR/.github/workflows/release.yml"
RELEASE_CONFIG="$ROOT_DIR/.github/release.yml"
RELEASE_NOTES="$ROOT_DIR/.github/release-notes/v$VERSION.md"

make --no-print-directory -C "$ROOT_DIR" release-image \
  IMAGE_DIGEST="$DIGEST" DIST_DIR="$DIST_DIR" >/dev/null

grep -qF "image: $IMAGE" "$DIST_DIR/sourcerudder-kubernetes.yaml"
grep -qF "$IMAGE" "$DIST_DIR/sourcerudder.service"
grep -qxF "$VERSION" "$DIST_DIR/VERSION"
grep -qxF "$IMAGE" "$DIST_DIR/IMAGE"
grep -qF 'image: <IMAGE>' "$ROOT_DIR/deploy/kubernetes/deployment.yaml"
grep -qF '@VERSION@' "$ROOT_DIR/deploy/systemd/sourcerudder.service"
grep -qF '@IMAGE@' "$ROOT_DIR/deploy/systemd/sourcerudder.service"
grep -qF 'type:breaking-change' "$RELEASE_CONFIG"
grep -qF 'type:feature' "$RELEASE_CONFIG"
grep -qF 'type:bug' "$RELEASE_CONFIG"
grep -qF 'isDraft' "$RELEASE_WORKFLOW"
grep -qF -- '--draft=false' "$RELEASE_WORKFLOW"
grep -qF 'immutable assets remain unchanged' "$RELEASE_WORKFLOW"
grep -qF 'bash scripts/build-release-archives.sh' "$RELEASE_WORKFLOW"
grep -qF "# SourceRudder $VERSION" "$RELEASE_NOTES"
grep -qF 'MIT License' "$RELEASE_NOTES"
grep -qF 'Licencia MIT' "$RELEASE_NOTES"

if grep -Eq '^[[:space:]]*image:[[:space:]]*<IMAGE>' "$DIST_DIR/sourcerudder-kubernetes.yaml" \
  || grep -Eq '<VERSION>|@VERSION@|@IMAGE@' "$DIST_DIR/sourcerudder-kubernetes.yaml" "$DIST_DIR/sourcerudder.service"; then
  printf 'release assets contain unresolved placeholders\n' >&2
  exit 1
fi

TARGETS="linux/amd64 windows/amd64" SOURCE_DATE_EPOCH=1700000000 DIST_DIR="$ARCHIVES_ONE" \
  bash "$ROOT_DIR/scripts/build-release-archives.sh"
TARGETS="linux/amd64 windows/amd64" SOURCE_DATE_EPOCH=1700000000 DIST_DIR="$ARCHIVES_TWO" \
  bash "$ROOT_DIR/scripts/build-release-archives.sh"
cmp "$ARCHIVES_ONE/sourcerudder_${VERSION}_linux_amd64.tar.gz" \
  "$ARCHIVES_TWO/sourcerudder_${VERSION}_linux_amd64.tar.gz"
cmp "$ARCHIVES_ONE/sourcerudder_${VERSION}_windows_amd64.zip" \
  "$ARCHIVES_TWO/sourcerudder_${VERSION}_windows_amd64.zip"

printf 'release_assets_test: PASS\n'
