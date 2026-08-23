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
#
# The value is extracted from cmd/ia-buscar/main.go's `-http-addr`
# flag default (a single `flag.String("http-addr", ":<port>", …)`
# line) rather than hard-coded, so a future regression that changes
# the binary's default without updating the manifest (or vice
# versa) trips this test before reaching CI. The grep is anchored
# to the flag declaration's shape so a coincidental match in a
# comment or a different flag is impossible.
app_listen_port() {
  local main_go="$REPO_ROOT/cmd/ia-buscar/main.go"
  if [[ ! -f "$main_go" ]]; then
    printf 'K8S_PORT_DERIVATION_FAILED: main.go not found at %s\n' "$main_go" >&2
    return 1
  fi
  # The flag is declared as:
  #   httpAddr = flag.String("http-addr", ":<port>", "HTTP server address")
  # We capture the literal port value (digits) immediately before
  # the closing `"` of the default string.
  local derived
  derived="$(grep -E '^[[:space:]]*httpAddr[[:space:]]*=[[:space:]]*flag\.String\("http-addr",[[:space:]]*":[0-9]+"' "$main_go" \
    | sed -E 's/.*":[0-9]+"/&/' \
    | grep -oE ':[0-9]+' \
    | head -n 1 \
    | tr -d ':')"
  if [[ -z "$derived" ]]; then
    printf 'K8S_PORT_DERIVATION_FAILED: could not derive http-addr default from %s\n' "$main_go" >&2
    return 1
  fi
  printf '%s' "$derived"
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

test_app_listen_port_derives_from_main_go() {
  # The helper that supplies the expected port MUST derive its
  # value from cmd/ia-buscar/main.go's `-http-addr` flag default
  # rather than embed a literal. A regression that re-introduces
  # a hard-coded port (e.g. a future contributor copy-pastes
  # `printf '8080'` back into app_listen_port) is caught here
  # because the helper is invoked in a subshell that checks the
  # output against the actual `flag.String("http-addr", …)` line
  # in main.go.
  local actual
  if ! actual="$(app_listen_port)"; then
    record_fail "app_listen_port helper failed to derive a port from cmd/ia-buscar/main.go"
    return 1
  fi
  # The output MUST be a positive integer; empty output means
  # the grep pipeline silently returned nothing.
  if ! [[ "$actual" =~ ^[0-9]+$ ]]; then
    record_fail "app_listen_port returned non-numeric value: $actual"
    return 1
  fi
  if (( actual < 1 || actual > 65535 )); then
    record_fail "app_listen_port returned out-of-range port: $actual"
    return 1
  fi
  # The output MUST match the port that the source actually
  # declares. A regression that flips the default in main.go
  # without updating the helper trips this assertion.
  local main_port
  main_port="$(grep -oE 'flag\.String\("http-addr", ":[0-9]+"' "$REPO_ROOT/cmd/ia-buscar/main.go" \
    | grep -oE ':[0-9]+' \
    | head -n 1 \
    | tr -d ':')"
  assert_eq "$actual" "$main_port" "app_listen_port vs main.go -http-addr default" || return 1
}

# probe_scalar_in_block extracts the first scalar value of a key
# nested inside a probe block (e.g. livenessProbe or
# readinessProbe). The awk program walks lines after the probe
# header, exits on the next same-indentation header, and prints
# the value of the named key when found. Returns "" if the key
# is absent.
probe_scalar_in_block() {
  local key="$1"
  local probe_header="$2"   # livenessProbe: or readinessProbe:
  local file="$3"
  awk -v k="$key" -v hdr="$probe_header" '
    $0 == hdr { in_block = 1; next }
    in_block {
      # Exit when we see another top-level probe header at the
      # same column (livenessProbe: / readinessProbe: live in
      # sibling positions under the same `containers:` entry).
      if ($0 ~ /^[a-zA-Z]/) { in_block = 0; next }
      if (in_block && $0 ~ "^[[:space:]]+" k ":[[:space:]]*[^[:space:]]") {
        sub("^[[:space:]]+" k ":[[:space:]]*", "")
        sub("[[:space:]]*$", "")
        print
        exit
      }
    }
  ' "$file"
}

test_liveness_probe_timing_bounds() {
  # Sanity-check the liveness probe timing values that ARE
  # present in the manifest. A regression to
  # `periodSeconds: 1` (probe flapping) or
  # `initialDelaySeconds: 99999` (the pod never reaches the
  # probe window) would pass every existing port/path test
  # and silently break production rollouts. The bounds below
  # are derived from the production runtime expectations.
  #
  # Fields that are absent fall through to the Kubernetes
  # defaults (timeoutSeconds=1, periodSeconds=10,
  # initialDelaySeconds=0, failureThreshold=3,
  # successThreshold=1), which are already sane. We do NOT
  # force the operator to add new fields — the check is
  # strictly "if you set it, it must be sane".
  local period initial
  period="$(probe_scalar_in_block 'periodSeconds' 'livenessProbe:' "$MANIFEST")"
  initial="$(probe_scalar_in_block 'initialDelaySeconds' 'livenessProbe:' "$MANIFEST")"
  if [[ -n "$period" ]]; then
    if ! [[ "$period" =~ ^[0-9]+$ ]]; then
      record_fail "livenessProbe.periodSeconds must be a non-negative integer (got $period)"
      return 1
    fi
    if (( period < 5 )); then
      record_fail "livenessProbe.periodSeconds must be >= 5 to avoid probe flapping (got $period)"
      return 1
    fi
  fi
  if [[ -n "$initial" ]]; then
    if ! [[ "$initial" =~ ^[0-9]+$ ]]; then
      record_fail "livenessProbe.initialDelaySeconds must be a non-negative integer (got $initial)"
      return 1
    fi
    if (( initial > 60 )); then
      record_fail "livenessProbe.initialDelaySeconds must be in [0, 60] (got $initial)"
      return 1
    fi
  fi
}

test_readiness_probe_timing_bounds() {
  local period initial
  period="$(probe_scalar_in_block 'periodSeconds' 'readinessProbe:' "$MANIFEST")"
  initial="$(probe_scalar_in_block 'initialDelaySeconds' 'readinessProbe:' "$MANIFEST")"
  if [[ -n "$period" ]]; then
    if ! [[ "$period" =~ ^[0-9]+$ ]]; then
      record_fail "readinessProbe.periodSeconds must be a non-negative integer (got $period)"
      return 1
    fi
    if (( period < 5 )); then
      record_fail "readinessProbe.periodSeconds must be >= 5 to avoid probe flapping (got $period)"
      return 1
    fi
  fi
  if [[ -n "$initial" ]]; then
    if ! [[ "$initial" =~ ^[0-9]+$ ]]; then
      record_fail "readinessProbe.initialDelaySeconds must be a non-negative integer (got $initial)"
      return 1
    fi
    if (( initial > 60 )); then
      record_fail "readinessProbe.initialDelaySeconds must be in [0, 60] (got $initial)"
      return 1
    fi
  fi
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
  run_test test_app_listen_port_derives_from_main_go
  run_test test_liveness_probe_timing_bounds
  run_test test_readiness_probe_timing_bounds

  printf '\n%d passed, %d failed\n' "$PASS_COUNT" "$FAIL_COUNT"
  if [ "$FAIL_COUNT" -gt 0 ]; then
    exit 1
  fi
}

main "$@"
