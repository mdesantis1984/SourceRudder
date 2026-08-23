#!/usr/bin/env bash
# tests/k8s/k8s_deployment_test.sh — RED/GREEN behavior tests for the
# Kubernetes deployment manifest. Pure bash, no external test runner.
#
# Invoked with `bash tests/k8s/k8s_deployment_test.sh` from the repo
# root. Exits 0 only if every assertion passes; otherwise prints the
# failing assertion(s) and exits non-zero.
#
# Background: the application listens on `:8080` (see cmd/ia-buscar/main.go
# `-http-addr` flag default, the Dockerfile `EXPOSE 8080`, and the systemd
# unit's `-http-addr :8080`). The previous deployment manifest declared
# `containerPort: 5000` plus matching probe ports and Service targetPort,
# which would have caused Kubernetes to direct liveness/readiness probes
# at a port the container never bound. The runtime guard for that
# mismatch is this test: the manifest's ports MUST match the actual
# application listen address (8080) so a `kubectl apply` does not put the
# pod into CrashLoopBackoff on the first probe.

set -u
set -o pipefail

# Resolve repo root from this script's path so the test is location-independent.
TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$TEST_DIR/../.." && pwd)"
MANIFEST="$REPO_ROOT/deploy/kubernetes/deployment.yaml"

# ---- Assertion harness ----------------------------------------------------

FAIL_COUNT=0
PASS_COUNT=0
CURRENT_TEST=""

record_pass() {
  PASS_COUNT=$((PASS_COUNT + 1))
  printf '  \033[32mPASS\033[0m %s\n' "$CURRENT_TEST"
}

record_fail() {
  local reason="$1"
  FAIL_COUNT=$((FAIL_COUNT + 1))
  printf '  \033[31mFAIL\033[0m %s — %s\n' "$CURRENT_TEST" "$reason"
}

run_test() {
  CURRENT_TEST="$1"
  if "$1"; then
    record_pass
  fi
}

assert_eq() {
  local actual="$1"
  local expected="$2"
  local label="${3:-value}"
  if [ "$actual" = "$expected" ]; then
    return 0
  fi
  record_fail "$label: expected [$expected] got [$actual]"
  return 1
}

assert_grep() {
  local file="$1"
  local pattern="$2"
  local label="${3:-grep}"
  if [ ! -f "$file" ]; then
    record_fail "$label: file missing ($file)"
    return 1
  fi
  if grep -qE "$pattern" "$file"; then
    return 0
  fi
  record_fail "$label: pattern not found ($pattern) in $file"
  return 1
}

assert_not_grep() {
  local file="$1"
  local pattern="$2"
  local label="${3:-grep}"
  if [ ! -f "$file" ]; then
    record_fail "$label: file missing ($file)"
    return 1
  fi
  if grep -qE "$pattern" "$file"; then
    record_fail "$label: forbidden pattern present ($pattern) in $file"
    return 1
  fi
  return 0
}

# Extract the first scalar value on a line that defines the key `$1`.
# Handles both block-scalar (`key: value`) and list-item
# (`- key: value`) forms. Returns "" if no such line exists.
yaml_scalar_after() {
  local key="$1"
  local file="$2"
  awk -v k="$key" '
    $0 ~ "^[[:space:]]*-?[[:space:]]*" k "[[:space:]]*:" {
      sub("^[[:space:]]*-?[[:space:]]*" k "[[:space:]]*:[[:space:]]*", "")
      sub("[[:space:]]*$", "")
      print
      exit
    }
  ' "$file"
}

# ---- Tests ----------------------------------------------------------------

# Application's listen address is the source of truth for which port
# the containerPort, probe ports, and Service targetPort MUST agree
# with. Pull it from the runtime surface so a future regression in
# either direction trips this test before reaching CI.
app_listen_port() {
  # The default `http-addr` flag in cmd/ia-buscar/main.go is `:8080`
  # per the production unit file, Dockerfile, and configs example.
  # If that default changes, this test must change too.
  printf '8080'
}

test_deployment_manifest_exists() {
  [ -f "$MANIFEST" ] || { record_fail "manifest missing at $MANIFEST"; return 1; }
}

test_container_port_matches_app_listen() {
  # The `containerPort` field tells Kubernetes which port the
  # container exposes. It MUST match the address the binary binds.
  local actual
  actual="$(yaml_scalar_after 'containerPort' "$MANIFEST")"
  local expected
  expected="$(app_listen_port)"
  assert_eq "$actual" "$expected" "containerPort" || return 1
}

test_liveness_probe_port_matches_app_listen() {
  # livenessProbe.httpGet.port is the port Kubernetes polls to decide
  # whether to restart the pod. If it does not match the binary's
  # listen address, the probe fails and the pod is killed on every
  # startup. The /healthz handler in cmd/ia-buscar binds to the
  # same port the binary listens on.
  local actual
  # Take the line that follows the `livenessProbe:` header and
  # contains a `port:` key. A 6-line window is enough to span the
  # httpGet.path/port block in this manifest.
  actual="$(awk '
    /livenessProbe:/ { in_liveness = 1; lines = 0; next }
    in_liveness {
      lines++
      if (lines > 6) { in_liveness = 0; next }
      if (/^[[:space:]]+port:[[:space:]]*[0-9]+/) {
        sub("^[[:space:]]+port:[[:space:]]*", "")
        sub("[[:space:]]*$", "")
        print
        exit
      }
    }
  ' "$MANIFEST")"
  local expected
  expected="$(app_listen_port)"
  assert_eq "$actual" "$expected" "livenessProbe.httpGet.port" || return 1
}

test_readiness_probe_port_matches_app_listen() {
  local actual
  actual="$(awk '
    /readinessProbe:/ { in_readiness = 1; lines = 0; next }
    in_readiness {
      lines++
      if (lines > 6) { in_readiness = 0; next }
      if (/^[[:space:]]+port:[[:space:]]*[0-9]+/) {
        sub("^[[:space:]]+port:[[:space:]]*", "")
        sub("[[:space:]]*$", "")
        print
        exit
      }
    }
  ' "$MANIFEST")"
  local expected
  expected="$(app_listen_port)"
  assert_eq "$actual" "$expected" "readinessProbe.httpGet.port" || return 1
}

test_service_port_matches_app_listen() {
  # Service.spec.ports[0].port is what `kubectl port-forward` and
  # in-cluster callers dial. targetPort must point at the container
  # port the binary actually binds, otherwise the Service routes
  # traffic to a port nothing is listening on.
  local actual_port actual_target
  actual_port="$(yaml_scalar_after 'port' "$MANIFEST")"
  actual_target="$(yaml_scalar_after 'targetPort' "$MANIFEST")"
  local expected
  expected="$(app_listen_port)"
  assert_eq "$actual_port" "$expected" "Service.spec.ports[0].port" || return 1
  assert_eq "$actual_target" "$expected" "Service.spec.ports[0].targetPort" || return 1
}

test_no_legacy_5000_port_remain() {
  # The inherited deployment manifest declared port 5000 because the
  # proof-of-concept container originally listened on 5000. After the
  # refactor the binary binds 8080, so the legacy value must not
  # reappear anywhere in the manifest. This is a regression guard
  # against a future "backport the old PoC default" change.
  assert_not_grep "$MANIFEST" '(:[[:space:]]*5000[[:space:]]*$|port:[[:space:]]*5000[[:space:]]*$|containerPort:[[:space:]]*5000[[:space:]]*$|targetPort:[[:space:]]*5000[[:space:]]*$)' \
    "legacy 5000 port reference" || return 1
}

test_healthz_path_is_configured() {
  # The probe path must be /healthz (the handler the binary registers).
  # A misconfigured path turns every probe into a 404 → CrashLoopBackoff.
  assert_grep "$MANIFEST" 'path:[[:space:]]*/healthz' "probe path /healthz" || return 1
}

# ---- Driver ----------------------------------------------------------------

main() {
  printf 'Running k8s_deployment_test.sh — Kubernetes manifest port contract\n'
  printf 'Manifest: %s\n' "$MANIFEST"
  printf 'App listen port: %s\n\n' "$(app_listen_port)"

  run_test test_deployment_manifest_exists
  run_test test_container_port_matches_app_listen
  run_test test_liveness_probe_port_matches_app_listen
  run_test test_readiness_probe_port_matches_app_listen
  run_test test_service_port_matches_app_listen
  run_test test_no_legacy_5000_port_remain
  run_test test_healthz_path_is_configured

  printf '\n%d passed, %d failed\n' "$PASS_COUNT" "$FAIL_COUNT"
  if [ "$FAIL_COUNT" -gt 0 ]; then
    exit 1
  fi
}

main "$@"
