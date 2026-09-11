#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

fail() {
  printf 'release-readiness: FAIL: %s\n' "$*" >&2
  exit 1
}

expect_fixed() {
  local needle="$1"
  local file="$2"
  grep -qF -- "$needle" "$file" || fail "$file is missing: $needle"
}

mode="${1:-}"
version="$(awk -F= '$1 == "VERSION" { print $2 }' Makefile)"
binary="$(awk -F= '$1 == "BINARY" { print $2 }' Makefile)"

[[ "$version" == "2.0.0" ]] || fail "Makefile VERSION must be 2.0.0"
[[ "$binary" == "sourcerudder" ]] || fail "Makefile BINARY must be sourcerudder"
[[ "$(go list -m)" == "github.com/mdesantis1984/SourceRudder" ]] || fail "unexpected Go module"
[[ -f cmd/sourcerudder/main.go ]] || fail "cmd/sourcerudder/main.go is missing"
[[ ! -e cmd/ia-buscar ]] || fail "legacy cmd/ia-buscar directory still exists"
[[ -f deploy/systemd/sourcerudder.service ]] || fail "sourcerudder.service is missing"
[[ ! -e deploy/systemd/ia-buscar.service ]] || fail "legacy systemd unit still exists"

expect_fixed 'serverName    = "sourcerudder"' internal/mcp/server.go
expect_fixed 'serverVersion = "2.0.0"' internal/mcp/server.go
expect_fixed 'agent-guide://sourcerudder/wire-contract' internal/mcp/resources.go
expect_fixed 'sourcerudder_http_requests_total' internal/observability/metrics.go
expect_fixed 'name: sourcerudder' compose.yaml
expect_fixed 'image: <IMAGE>' deploy/kubernetes/deployment.yaml
expect_fixed 'MemorySwapMax=512M' deploy/systemd/sourcerudder.service
expect_fixed 'sha256sum -c /etc/sourcerudder/release/BINARY_SHA256' deploy/systemd/sourcerudder.service

if grep -RIE 'IA_Buscar|IA_BUSCAR|ia-buscar|github\.com/thiscloud' \
  cmd internal pkg deploy scripts tests configs .github/workflows \
  | grep -vF 'scripts/release-readiness.sh:' >/dev/null; then
  fail "legacy product identifiers remain in runtime, deployment, or automation paths"
fi

while IFS= read -r dockerfile; do
  if grep -E '^FROM[[:space:]]+' "$dockerfile" | grep -vE '^FROM[[:space:]]+scratch([[:space:]]|$)' | grep -vq '@sha256:'; then
    fail "$dockerfile contains an external base image without a digest"
  fi
done < <(git ls-files '*Dockerfile')

if [[ "$mode" == "--identity-only" ]]; then
  printf 'release-readiness: identity checks PASS\n'
  exit 0
fi

expected_license_sha256='f275204b804f9d6649cd29bef3feaabbf985f7a7a33524b8a7ea97c0cd1bde95'
actual_license_sha256="$(sha256sum LICENSE | cut -d ' ' -f 1)"

expect_fixed 'MIT License' LICENSE
expect_fixed 'Permission is hereby granted, free of charge' LICENSE

if [[ "$actual_license_sha256" != "$expected_license_sha256" ]]; then
  fail "LICENSE does not match the approved MIT license for SourceRudder 2.0.0"
fi

if [[ "$mode" == "--license-only" ]]; then
  printf 'release-readiness: MIT license checks PASS\n'
  exit 0
fi

tag="${mode:-${GITHUB_REF_NAME:-}}"
[[ "$tag" == "v$version" ]] || fail "release tag must be v$version"
[[ -z "$(git status --porcelain)" ]] || fail "worktree must be clean"
[[ "$(git rev-parse "${tag}^{commit}" 2>/dev/null)" == "$(git rev-parse HEAD)" ]] || fail "tag $tag must point to HEAD"

repository="${GITHUB_REPOSITORY:-}"
if [[ -z "$repository" ]]; then
  origin="$(git remote get-url origin 2>/dev/null || true)"
  [[ "$origin" =~ github\.com[:/]mdesantis1984/SourceRudder(\.git)?$ ]] || fail "origin must be mdesantis1984/SourceRudder"
else
  [[ "$repository" == "mdesantis1984/SourceRudder" ]] || fail "workflow must run in mdesantis1984/SourceRudder"
fi

printf 'release-readiness: full release checks PASS (%s; MIT license matched)\n' "$tag"
