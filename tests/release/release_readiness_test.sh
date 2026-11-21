#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

bash "$ROOT_DIR/scripts/release-readiness.sh" --identity-only >/dev/null
bash "$ROOT_DIR/scripts/release-readiness.sh" --license-only >/dev/null

if grep -q 'SOURCERUDDER_LEGAL_' "$ROOT_DIR/.github/workflows/release.yml"; then
  printf 'release workflow still depends on obsolete legal-review secrets\n' >&2
  exit 1
fi

# Fetch User-Agent contract: the browser default (fetch.DefaultUserAgent) must
# apply in deployments, so the templates must not pin FETCH_USER_AGENT.
scratch="$(mktemp -d)"
trap 'rm -rf -- "$scratch"' EXIT

make_fixture() {
  local dir="$1"
  mkdir -p "$dir"
  tar -C "$ROOT_DIR" --exclude=.git --exclude=.codegraph -cf - . | tar -C "$dir" -xf -
  git -C "$dir" init -q
}

expect_identity_failure() {
  local name="$1"
  local dir="$2"
  local needle="$3"
  local out status=0
  out="$(bash "$dir/scripts/release-readiness.sh" --identity-only 2>&1)" || status=$?
  if [[ "$status" -eq 0 ]]; then
    printf 'release_readiness_test: %s: readiness unexpectedly passed\n' "$name" >&2
    exit 1
  fi
  if [[ "$out" != *"$needle"* ]]; then
    printf 'release_readiness_test: %s: expected failure containing %q, got: %s\n' "$name" "$needle" "$out" >&2
    exit 1
  fi
}

fixture="$scratch/systemd-pin"
make_fixture "$fixture"
printf 'Environment=FETCH_USER_AGENT=SourceRudder/1.0.0\n' >>"$fixture/deploy/systemd/sourcerudder.service"
expect_identity_failure 'systemd FETCH_USER_AGENT pin' "$fixture" 'must not set FETCH_USER_AGENT'

fixture="$scratch/k8s-pin"
make_fixture "$fixture"
printf '            - name: FETCH_USER_AGENT\n              value: "SourceRudder/1.0.0"\n' >>"$fixture/deploy/kubernetes/deployment.yaml"
expect_identity_failure 'kubernetes FETCH_USER_AGENT pin' "$fixture" 'must not set FETCH_USER_AGENT'

fixture="$scratch/main-default"
make_fixture "$fixture"
sed -i 's/fetch\.DefaultUserAgent/fetch.OtherUserAgent/g' "$fixture/cmd/sourcerudder/main.go"
expect_identity_failure 'main.go without fetch.DefaultUserAgent' "$fixture" 'cmd/sourcerudder/main.go is missing: fetch.DefaultUserAgent'

fixture="$scratch/fetcher-default"
make_fixture "$fixture"
sed -i 's/const DefaultUserAgent =/const OtherUserAgent =/' "$fixture/internal/fetch/fetcher.go"
expect_identity_failure 'fetcher.go without DefaultUserAgent' "$fixture" 'internal/fetch/fetcher.go is missing: const DefaultUserAgent ='

fixture="$scratch/pypi-pin"
make_fixture "$fixture"
sed -i 's#SourceRudder/[0-9.]*#SourceRudder/0.0.0#' "$fixture/internal/connectors/pypi.go"
expect_identity_failure 'pypi.go version pin' "$fixture" 'internal/connectors/pypi.go is missing: SourceRudder/'

printf 'release_readiness_test: PASS\n'
