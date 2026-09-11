#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

bash "$ROOT_DIR/scripts/release-readiness.sh" --identity-only >/dev/null
bash "$ROOT_DIR/scripts/release-readiness.sh" --license-only >/dev/null

if grep -q 'SOURCERUDDER_LEGAL_' "$ROOT_DIR/.github/workflows/release.yml"; then
  printf 'release workflow still depends on obsolete legal-review secrets\n' >&2
  exit 1
fi

printf 'release_readiness_test: PASS\n'
