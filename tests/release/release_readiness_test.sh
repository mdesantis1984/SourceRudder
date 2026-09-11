#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
OUTPUT="$(mktemp)"
trap 'rm -f "$OUTPUT"' EXIT

bash "$ROOT_DIR/scripts/release-readiness.sh" --identity-only >/dev/null

if env -u SOURCERUDDER_LEGAL_APPROVAL_REF -u SOURCERUDDER_LEGAL_REVIEW_SHA256 \
  bash "$ROOT_DIR/scripts/release-readiness.sh" v2.0.0 >"$OUTPUT" 2>&1; then
  printf 'full release gate passed without legal approval evidence\n' >&2
  exit 1
fi

if ! grep -qiE 'license|legal' "$OUTPUT"; then
  printf 'full release gate failed for a non-legal reason:\n' >&2
  sed 's/^/  /' "$OUTPUT" >&2
  exit 1
fi

printf 'release_readiness_test: PASS\n'
