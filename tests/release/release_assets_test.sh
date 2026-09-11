#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
DIST_DIR="$(mktemp -d)"
trap 'rm -rf "$DIST_DIR"' EXIT

DIGEST="sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
IMAGE="ghcr.io/mdesantis1984/sourcerudder:2.0.0@$DIGEST"

make --no-print-directory -C "$ROOT_DIR" release-image \
  IMAGE_DIGEST="$DIGEST" DIST_DIR="$DIST_DIR" >/dev/null

grep -qF "image: $IMAGE" "$DIST_DIR/sourcerudder-kubernetes.yaml"
grep -qF "$IMAGE" "$DIST_DIR/sourcerudder.service"
grep -qxF '2.0.0' "$DIST_DIR/VERSION"
grep -qxF "$IMAGE" "$DIST_DIR/IMAGE"
grep -qF 'image: <IMAGE>' "$ROOT_DIR/deploy/kubernetes/deployment.yaml"
grep -qF '@VERSION@' "$ROOT_DIR/deploy/systemd/sourcerudder.service"
grep -qF '@IMAGE@' "$ROOT_DIR/deploy/systemd/sourcerudder.service"

if grep -Eq '^[[:space:]]*image:[[:space:]]*<IMAGE>' "$DIST_DIR/sourcerudder-kubernetes.yaml" \
  || grep -Eq '@VERSION@|@IMAGE@' "$DIST_DIR/sourcerudder.service"; then
  printf 'release assets contain unresolved placeholders\n' >&2
  exit 1
fi

printf 'release_assets_test: PASS\n'
