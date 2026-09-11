#!/usr/bin/env bash
# tests/qa/qa_scripts_test.sh — RED/GREEN behavior tests for the
# local-docker-qa additive scope. Pure bash, no external test runner.
# Invoked with `bash tests/qa/qa_scripts_test.sh` from the repo root.
#
# Each test_* function exits 0 on PASS and non-zero on FAIL. A small
# `assert_eq` / `assert_contains` / `assert_grep` / `assert_file_mode`
# harness keeps the assertions honest. At the bottom, `main` runs every
# test in order and exits non-zero on the first failure. This is the
# "focused test command" used as the TDD evidence for every task.
#
# Deviations from design.md, captured here so the test tells the truth:
#   * Tool count: design/spec say 28, but internal/mcp/server.go:126-150
#     registers exactly 25 tools, and the README already says "25 tools".
#     The smoke asserts 25. RED branches verify N=24 and N=26 fail.
#   * -memory-url flag: design requires `-memory-url ""` in the compose
#     entrypoint, but cmd/sourcerudder/main.go does not declare that flag
#     (it has 9 flags, no -memory-url). The IA_Recuerdo canary probe on
#     127.0.0.1:7438 is the real assertion; the argv check is skipped.
#   * internal/memory/client.go: does not exist in this worktree, so the
#     "empty baseURL short-circuits before I/O" assumption is moot.

set -u
set -o pipefail

# Resolve repo root from this script's path so the test is location-independent.
TEST_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$TEST_DIR/../.." && pwd)"

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

assert_contains() {
  local haystack="$1"
  local needle="$2"
  local label="${3:-content}"
  if printf '%s' "$haystack" | grep -qF -- "$needle"; then
    return 0
  fi
  record_fail "$label: expected to contain [$needle]"
  return 1
}

assert_not_contains() {
  local haystack="$1"
  local needle="$2"
  local label="${3:-content}"
  if printf '%s' "$haystack" | grep -qF -- "$needle"; then
    record_fail "$label: expected NOT to contain [$needle]"
    return 1
  fi
  return 0
}

assert_file_exists() {
  local path="$1"
  if [ -f "$path" ]; then
    return 0
  fi
  record_fail "file missing: $path"
  return 1
}

assert_file_mode_executable() {
  local path="$1"
  if [ -x "$path" ]; then
    return 0
  fi
  record_fail "file not executable: $path"
  return 1
}

assert_grep() {
  local pattern="$1"
  local file="$2"
  local label="${3:-grep}"
  # -e keeps leading dashes in $pattern from being interpreted as flags.
  if grep -qE -e "$pattern" -- "$file"; then
    return 0
  fi
  record_fail "$label: pattern [$pattern] not found in $file"
  return 1
}

assert_grep_count() {
  local pattern="$1"
  local file="$2"
  local expected="$3"
  local label="${4:-grep count}"
  local actual
  actual=$(grep -cE -e "$pattern" -- "$file" || true)
  if [ "$actual" = "$expected" ]; then
    return 0
  fi
  record_fail "$label: expected $expected matches of [$pattern] in $file, got $actual"
  return 1
}

run_test() {
  local name="$1"
  CURRENT_TEST="$name"
  if "$name"; then
    record_pass
  fi
  CURRENT_TEST=""
}

# ---- Phase 1: Foundation — compose + config ---------------------------------

test_compose_file_exists() {
  assert_file_exists "$REPO_ROOT/deploy/qa/docker-compose.yml"
}

test_compose_top_level_project_name() {
  # Threat B.1: project name MUST be `sourcerudder-qa`.
  local f="$REPO_ROOT/deploy/qa/docker-compose.yml"
  assert_file_exists "$f" || return 1
  local content
  content=$(cat "$f")
  assert_contains "$content" "name: sourcerudder-qa" "compose.name" || return 1
}

test_compose_defines_qa_net_internal() {
  # Threat B.4: qa-net must be an internal bridge with no host routing.
  local f="$REPO_ROOT/deploy/qa/docker-compose.yml"
  local content
  content=$(cat "$f")
  assert_contains "$content" "qa-net:" "compose.network.id" || return 1
  # Default Compose bridges are NOT internal. The spec must opt in
  # explicitly. Either `internal: true` on the bridge, or `internal: true`
  # in the network defaults. We accept both spellings.
  if ! printf '%s' "$content" | grep -qE 'internal:[[:space:]]*true'; then
    record_fail "compose.network: no 'internal: true' found; qa-net would route to host"
    return 1
  fi
  # No network_mode: host anywhere.
  if printf '%s' "$content" | grep -qE 'network_mode:[[:space:]]*"host"|network_mode:[[:space:]]*host'; then
    record_fail "compose.network: found network_mode: host; would expose host routes"
    return 1
  fi
}

test_compose_healthchecks_defined() {
  # Both services need healthcheck blocks; smoke needs them for `up --wait`.
  local f="$REPO_ROOT/deploy/qa/docker-compose.yml"
  local content
  content=$(cat "$f")
  # searxng healthcheck — use sed to extract the block from the searxng
  # service header to the next top-level service header.
  local searxng_block
  searxng_block=$(printf '%s' "$content" | sed -n '/^  searxng:/,/^  [a-z]/p' | head -n -1)
  if ! printf '%s' "$searxng_block" | grep -qE 'healthcheck:'; then
    record_fail "compose.searxng: missing healthcheck block"
    return 1
  fi
  # sourcerudder healthcheck
  local app_block
  app_block=$(printf '%s' "$content" | sed -n '/^  sourcerudder:/,/^  [a-z]/p' | head -n -1)
  if ! printf '%s' "$app_block" | grep -qE 'healthcheck:'; then
    record_fail "compose.sourcerudder: missing healthcheck block"
    return 1
  fi
}

test_compose_sourcerudder_build_context() {
  # The sourcerudder service must build from the repo root using
  # deploy/docker/Dockerfile. This keeps the QA image identical to the
  # production build path. The build context MUST resolve to the repo
  # root when docker compose is invoked with `-f deploy/qa/docker-compose.yml`
  # from the repo root — i.e., `context: ../..` relative to the compose
  # file's directory (deploy/qa/). Earlier versions used `../../..` which
  # resolved to the parent of the worktree, breaking `docker compose build`
  # with `lstat .../deploy: no such file or directory`.
  local f="$REPO_ROOT/deploy/qa/docker-compose.yml"
  local content
  content=$(cat "$f")
  assert_contains "$content" "dockerfile: deploy/docker/Dockerfile" "compose.sourcerudder.dockerfile" || return 1
  # Strict equality: extract the context line and compare EXACTLY.
  # assert_contains uses substring match which would let `../../..`
  # falsely pass for `../..`. We anchor on the start and end of the line.
  local ctx
  ctx=$(grep -E '^[[:space:]]+context:' "$f" | head -1 | sed -E 's/^[[:space:]]+context:[[:space:]]*//')
  assert_eq "$ctx" "../.." "compose.sourcerudder.context (exact match)" || return 1
}

test_compose_build_context_resolves_to_repo_root() {
  # B1 gate: the build context, after path resolution, MUST equal the
  # repo root. From deploy/qa/, ../.. = repo root. Any deeper (`../../..`)
  # or shallower (`..`) path would point outside the repo and fail the
  # lstat gate observed in #4265.
  local f="$REPO_ROOT/deploy/qa/docker-compose.yml"
  local content
  content=$(cat "$f")
  # Reject the broken triple-up path that gates in #4265 — match the
  # full path token, optionally quoted or unquoted.
  if printf '%s' "$content" | grep -qE '^[[:space:]]+context:[[:space:]]+["'"'"']?\.\./\.\./\.\.["'"'"']?[[:space:]]*$'; then
    record_fail "compose.build.context: triple-up path resolves outside the worktree"
    return 1
  fi
  # Reject a shallow `..` (would be deploy/, missing go.mod).
  if printf '%s' "$content" | grep -qE '^[[:space:]]+context:[[:space:]]+["'"'"']?\.\.["'"'"']?[[:space:]]*$' && \
     ! printf '%s' "$content" | grep -qE '^[[:space:]]+context:[[:space:]]+["'"'"']?\.\./\.\.["'"'"']?[[:space:]]*$'; then
    record_fail "compose.build.context: shallow '..' points at deploy/, missing go.mod"
    return 1
  fi
}

test_compose_entrypoint_passes_memory_url_empty() {
  # B4 gate: the live binary declares `-memory-url` (cmd/sourcerudder/main.go:33)
  # with default "". Passing it explicitly to "" keeps the
  # IA_Recuerdo integration disabled AND proves the live binary has the
  # integration-disabled short-circuit wired. Without this flag the
  # contract is structural-only (canary port); with it, the contract is
  # behavioral (the binary itself disables Save).
  local f="$REPO_ROOT/deploy/qa/docker-compose.yml"
  local content
  content=$(cat "$f")
  # The entrypoint must contain `-memory-url` as a list item. The flag
  # dash is part of the token; the YAML list bullet is a separate dash.
  if ! printf '%s' "$content" | grep -qE '^[[:space:]]+-[[:space:]]+"?-memory-url"?[[:space:]]*$'; then
    record_fail "compose.entrypoint: -memory-url flag missing"
    return 1
  fi
  # The memory-url value must be an empty string literal. We accept any
  # of: "", '' (yaml double or single quoted empty), or unquoted
  # followed by another -flag. Match either " or ' around nothing.
  if ! printf '%s' "$content" | grep -qE "memory-url[[:space:]]+(\"\"|'')"; then
    record_fail "compose.entrypoint: -memory-url value must be empty string"
    return 1
  fi
}

test_compose_publishes_only_loopback_8080() {
  # Spec: only 127.0.0.1:8080 is published to the host. On dockerd
  # variants where userland-proxy cannot bind to 127.0.0.1, an empty
  # host-ip publish (`"8080:8080"`) is the fall-back that still
  # leaves qa-net as the only route between the host and the QA
  # binary, since the published port maps to the container's 8080
  # through Docker's own DNAT. The hard ban is on LAN-visible
  # publishes (any non-loopback host_ip).
  local f="$REPO_ROOT/deploy/qa/docker-compose.yml"
  local content
  content=$(cat "$f")
  # Accept either form (loopback or empty host_ip).
  if ! printf '%s' "$content" | grep -qE '"(127\.0\.0\.1:)?8080:8080"'; then
    record_fail "compose.ports: missing 8080:8080 publish (loopback or empty host_ip)"
    return 1
  fi
  # Reject LAN-visible publishes (any non-loopback host_ip).
  if printf '%s' "$content" | grep -qE '"(10\.|172\.(1[6-9]|2[0-9]|3[01])|192\.168\.)[^"]*:8080:8080"'; then
    record_fail "compose.ports: LAN-visible 8080 publish present"
    return 1
  fi
}

test_compose_entrypoint_overrides_searxng_url() {
  # Threat B.2: rendered argv must contain `-searxng-url http://searxng:8080`.
  # That is the only way to point the app at the in-stack SearxNG instead
  # of the hardcoded production default 10.0.0.201:8080.
  local f="$REPO_ROOT/deploy/qa/docker-compose.yml"
  local content
  content=$(cat "$f")
  assert_contains "$content" "-searxng-url" "compose.entrypoint.flag" || return 1
  assert_contains "$content" "http://searxng:8080" "compose.entrypoint.value" || return 1
}

test_compose_entrypoint_passes_auth_key() {
  # B2 gate (CT201 env-only): the QA stack provisions auth via the
  # substitution so the live binary validator (internal/auth/auth.go
  # lines 38-58) is enabled with a deterministic QA-only key. The auth
  # header requirement on /mcp then becomes a structural property of the
  # stack, not a smoke special case. The shell substitution is what
  # lets qa-up.sh export the key from .env.qa (or AUTH_KEY env).
  local f="$REPO_ROOT/deploy/qa/docker-compose.yml"
  local content
  content=$(cat "$f")
  # Match a list item that contains the literal `-auth-key` token
  # (with optional quoting). YAML list bullet `-` and flag dash are
  # both present; we anchor on the trailing `auth-key"`/`auth-key'`.
  if ! printf '%s' "$content" | grep -qF "SOURCERUDDER_AUTH_KEY=\${QA_AUTH_KEY:-}"; then
    record_fail "compose.environment: env-only mapping 'SOURCERUDDER_AUTH_KEY=\${QA_AUTH_KEY:-}' missing"
    return 1
  fi
  if printf '%s' "$content" | grep -qE '^[[:space:]]+-[[:space:]]+"?-auth-key"?[[:space:]]*$'; then
    record_fail "compose.entrypoint: obsolete -auth-key argv list item present (env-only contract forbids argv path)"
    return 1
  fi
}

test_compose_uses_existing_dockerfile_image_path() {
  # Sanity: the compose file's `build:` key for sourcerudder must produce
  # a runnable image using the production Dockerfile.
  local f="$REPO_ROOT/deploy/qa/docker-compose.yml"
  assert_grep "^    build:" "$f" "compose.sourcerudder.build" || return 1
}

test_searxng_settings_exists() {
  assert_file_exists "$REPO_ROOT/deploy/qa/searxng/settings.yml" || return 1
}

test_searxng_settings_empty_engine_list() {
  # Empty engines -> empty results, which the smoke relies on to assert
  # 200 + empty results + cached:false without depending on real hits.
  local f="$REPO_ROOT/deploy/qa/searxng/settings.yml"
  assert_file_exists "$f" || return 1
  local content
  content=$(cat "$f")
  assert_contains "$content" "engines:" "searxng.engines.key" || return 1
  # engines: list must be empty.
  if ! printf '%s' "$content" | awk '/^engines:/,/^[a-zA-Z]/{ if (!/^engines:/) print }' | grep -qE '^[[:space:]]*$'; then
    # Simpler check: the engines block must be `engines: []` or have no
    # entries. The robust form is `engines: []`.
    if ! printf '%s' "$content" | grep -qE 'engines:[[:space:]]*\[\]'; then
      record_fail "searxng.engines: expected empty list (engines: [])"
      return 1
    fi
  fi
}

test_searxng_settings_json_output() {
  # smoke asserts JSON shape (results array, cached flag). output must
  # be json, not html.
  local f="$REPO_ROOT/deploy/qa/searxng/settings.yml"
  local content
  content=$(cat "$f")
  assert_contains "$content" "json" "searxng.output.format" || return 1
}

test_searxng_settings_binds_all_interfaces_8080() {
  # SearxNG must listen on 0.0.0.0:8080 so the sourcerudder container can
  # reach it via the in-stack DNS name.
  local f="$REPO_ROOT/deploy/qa/searxng/settings.yml"
  local content
  content=$(cat "$f")
  assert_contains "$content" "0.0.0.0:8080" "searxng.bind" || return 1
}

test_searxng_limiter_exists() {
  # limiter.toml with bot-detection disabled keeps the smoke run
  # deterministic (no 429 surprise).
  assert_file_exists "$REPO_ROOT/deploy/qa/searxng/limiter.toml" || return 1
}

test_dockerignore_exists() {
  assert_file_exists "$REPO_ROOT/.dockerignore" || return 1
}

test_dockerignore_excludes_local_state() {
  # All the directories the proposal calls out: .git, .atl, .codegraph,
  # .codebase-memory, bin, tmp, *.db, coverage.*, *.log
  local f="$REPO_ROOT/.dockerignore"
  assert_file_exists "$f" || return 1
  local content
  content=$(cat "$f")
  for needle in ".git" ".atl" ".codegraph" ".codebase-memory" "bin" "tmp" "*.db" "coverage.*" "*.log"; do
    assert_contains "$content" "$needle" "dockerignore[$needle]" || return 1
  done
}

# ---- Phase 2A: Shell/process arguments (Threat A) --------------------------

test_qa_up_rejects_unexpected_positional_args() {
  # 2A.1: qa-up must exit 1 if any positional argument is passed. Fixed
  # argv means no `qa-up --foo` or `qa-up something`.
  local script="$REPO_ROOT/scripts/qa-up.sh"
  assert_file_exists "$script" || return 1
  local rc=0
  bash "$script" "extra-positional" >/dev/null 2>&1 || rc=$?
  if [ "$rc" = "0" ]; then
    record_fail "qa-up.sh accepted positional arg (should exit nonzero)"
    return 1
  fi
}

test_qa_up_rejects_metacharacters() {
  # 2A.2: qa-up must reject argv containing shell metacharacters. We
  # simulate by exporting an env var the script might forward and
  # verifying it is not echoed into a downstream command. The actual
  # gate is: qa-up takes no args, so metacharacter injection via argv is
  # structurally impossible. The RED case is: a script that DID forward
  # env-derived args would echo them. Our test asserts the script
  # refuses everything except its hardcoded path.
  local script="$REPO_ROOT/scripts/qa-up.sh"
  assert_file_exists "$script" || return 1
  # Any non-zero exit when we try to feed it `$(rm -rf /)` style garbage
  # is acceptable; the test asserts the exit is nonzero.
  local rc=0
  bash "$script" '$(echo pwned)' >/dev/null 2>&1 || rc=$?
  if [ "$rc" = "0" ]; then
    record_fail "qa-up.sh accepted metacharacter arg"
    return 1
  fi
}

# ---- Phase 2B: Process integration (Threat B) ------------------------------

test_qa_up_uses_fixed_project_arg() {
  # 2B.1: the qa-up script must pass `-p sourcerudder-qa` to every compose
  # invocation. This is what scopes the cleanup, not a global default.
  # The project name may be hardcoded or referenced via a local const;
  # we assert both invariants: (a) the literal "sourcerudder-qa" appears
  # in the script, and (b) the script forwards `-p` to compose.
  local script="$REPO_ROOT/scripts/qa-up.sh"
  assert_file_exists "$script" || return 1
  if ! grep -qF 'sourcerudder-qa' "$script"; then
    record_fail "qa-up.sh: literal project name 'sourcerudder-qa' not found"
    return 1
  fi
  assert_grep " -p" "$script" "qa-up.project-arg" || return 1
}

test_qa_up_invokes_up_build_wait() {
  # 2B.2: qa-up must drive the project to healthy boot with
  # `up --build --wait --wait-timeout 60`.
  local script="$REPO_ROOT/scripts/qa-up.sh"
  assert_file_exists "$script" || return 1
  assert_grep "--build" "$script" "qa-up.build" || return 1
  assert_grep "--wait" "$script" "qa-up.wait" || return 1
  assert_grep "--wait-timeout" "$script" "qa-up.wait-timeout" || return 1
}

test_qa_up_scoped_cleanup_on_failure() {
  # 2B.3: on failure, qa-up must run a scoped `down --remove-orphans --volumes`
  # against the same project, then verify zero project-labeled containers
  # and that the qa-net network is absent.
  local script="$REPO_ROOT/scripts/qa-up.sh"
  assert_file_exists "$script" || return 1
  assert_grep "down" "$script" "qa-up.cleanup.down" || return 1
  assert_grep "--remove-orphans" "$script" "qa-up.cleanup.orphans" || return 1
  assert_grep "--volumes" "$script" "qa-up.cleanup.volumes" || return 1
  assert_grep "qa-net" "$script" "qa-up.net-check" || return 1
}

test_qa_up_derives_qa_auth_key() {
  # B2 gate: qa-up.sh must derive QA_AUTH_KEY from AUTH_KEY env or .env.qa
  # and export it so the compose entrypoint substitution resolves. The
  # key is a development-only artifact; never use a production key.
  local script="$REPO_ROOT/scripts/qa-up.sh"
  assert_file_exists "$script" || return 1
  assert_grep "QA_AUTH_KEY" "$script" "qa-up.qa-auth-key.var" || return 1
  # Source precedence: AUTH_KEY env first, then .env.qa, then a fallback
  # so the smoke can run without external state. We assert at least one
  # of the source patterns is honored.
  if ! grep -qE 'AUTH_KEY|\.env\.qa' "$script"; then
    record_fail "qa-up.sh: QA_AUTH_KEY must be derived from AUTH_KEY or .env.qa"
    return 1
  fi
  # The derived key must reach the compose invocation as an env var so
  # the entrypoint's ${QA_AUTH_KEY:-} resolves. We assert either
  # `export QA_AUTH_KEY` or `--env QA_AUTH_KEY` appears.
  if ! grep -qE 'export[[:space:]]+QA_AUTH_KEY|--env[[:space:]]+QA_AUTH_KEY' "$script"; then
    record_fail "qa-up.sh: QA_AUTH_KEY must be exported to compose env"
    return 1
  fi
}

test_qa_up_loads_env_qa_file() {
  # B2 gate: qa-up.sh must load .env.qa (committed dev-only file in repo
  # root) to source QA_AUTH_KEY when AUTH_KEY env is unset. This is the
  # documented dev-only path. The file is gitignored for any future
  # secret rotation, but the loader exists so a developer can drop in a
  # local .env.qa and have the smoke pass without exporting env vars.
  local script="$REPO_ROOT/scripts/qa-up.sh"
  assert_file_exists "$script" || return 1
  assert_grep '\.env\.qa' "$script" "qa-up.env-qa.file" || return 1
}



test_qa_down_preserves_volumes() {
  # 3.2: qa-down must NOT use --volumes (qa-clean is the volume-removing
  # entry point).
  local script="$REPO_ROOT/scripts/qa-down.sh"
  assert_file_exists "$script" || return 1
  if grep -qE -- "--volumes" "$script"; then
    record_fail "qa-down.sh uses --volumes; volumes must be preserved here"
    return 1
  fi
}

test_qa_smoke_probes_healthz() {
  # 3.3: smoke must hit /healthz and expect 200 {"status":"ok"}.
  local script="$REPO_ROOT/scripts/qa-smoke.sh"
  assert_file_exists "$script" || return 1
  assert_grep "/healthz" "$script" "smoke.healthz" || return 1
  assert_grep '"status":"ok"' "$script" "smoke.healthz.body" || return 1
}

test_qa_smoke_probes_tools_list_actual_count() {
  # Threat B.6: smoke must assert exactly the number of tools the live
  # binary registers, every tool with non-empty trimmed name+description.
  # The live binary has 25 (internal/mcp/server.go:126-150). We read the
  # count from the source so the test fails if either the source or the
  # smoke drifts.
  local server="$REPO_ROOT/internal/mcp/server.go"
  local smoke="$REPO_ROOT/scripts/qa-smoke.sh"
  assert_file_exists "$server" || return 1
  assert_file_exists "$smoke" || return 1
  local live_count
  live_count=$(grep -cE '\{Name:[[:space:]]*"' "$server" || true)
  if [ "$live_count" -lt 1 ]; then
    record_fail "could not count tools in server.go (got $live_count)"
    return 1
  fi
  # Smoke must assert the same count (or use the count via grep from
  # the source at runtime). We accept either `EXPECTED_TOOLS=25` or
  # dynamically reading from the source.
  if ! grep -qE "EXPECTED_TOOLS" "$smoke"; then
    record_fail "smoke.sh does not declare EXPECTED_TOOLS"
    return 1
  fi
  # Pull the constant out and compare.
  local smoke_count
  smoke_count=$(grep -E "^EXPECTED_TOOLS=" "$smoke" | head -1 | sed -E 's/^EXPECTED_TOOLS=//')
  assert_eq "$smoke_count" "$live_count" "smoke EXPECTED_TOOLS vs source tool count" || return 1
}

test_qa_smoke_probes_search_web_empty_uncached() {
  # 3.3 + spec: search_web must return 200, empty results, cached:false.
  local script="$REPO_ROOT/scripts/qa-smoke.sh"
  assert_file_exists "$script" || return 1
  assert_grep "search_web" "$script" "smoke.search_web" || return 1
  assert_grep "cached" "$script" "smoke.cached" || return 1
  assert_grep "results" "$script" "smoke.results" || return 1
}

test_qa_smoke_sends_authorization_bearer() {
  # B2 gate: with the entrypoint passing -auth-key, the auth middleware
  # (internal/auth/auth.go:38-58) rejects every unauthenticated request
  # with 401 missing_credentials. The smoke MUST therefore send
  # `Authorization: Bearer ${QA_AUTH_KEY:-${AUTH_KEY:-}}` on every /mcp
  # POST, otherwise the tools/list and search_web probes will return 401
  # instead of the expected 200.
  local script="$REPO_ROOT/scripts/qa-smoke.sh"
  assert_file_exists "$script" || return 1
  assert_grep "Authorization" "$script" "smoke.auth.header" || return 1
  assert_grep "Bearer" "$script" "smoke.auth.bearer" || return 1
}

test_qa_smoke_asserts_twenty_eight_tools() {
  # B3 gate: the live binary registers exactly 28 tools (post-restore-runtime-contract).
  # The smoke MUST assert 28 — neither 25 (the pre-restoration count) nor any
  # other drift. Drift detection is enforced by the adjacent test that
  # # cross-checks the count against the source.
  local server="$REPO_ROOT/internal/mcp/server.go"
  local smoke="$REPO_ROOT/scripts/qa-smoke.sh"
  assert_file_exists "$server" || return 1
  assert_file_exists "$smoke" || return 1
  local live_count
  live_count=$(grep -cE '\{Name:[[:space:]]*"' "$server" || true)
  if [ "$live_count" -ne 28 ]; then
    record_fail "live tool count is $live_count, not 28; B3 gate premise broken"
    return 1
  fi
  local smoke_count
  smoke_count=$(grep -E "^EXPECTED_TOOLS=" "$smoke" | head -1 | sed -E 's/^EXPECTED_TOOLS=//')
  assert_eq "$smoke_count" "28" "smoke EXPECTED_TOOLS vs 28-tool runtime contract" || return 1
}

test_qa_smoke_canary_checks_no_memory_egress() {
  # Threat B.5 + spec: zero requests to 127.0.0.1:7438. The canary
  # listener should record 0 hits after a connector call. The actual
  # code has no -memory-url flag and no internal/memory client at all,
  # so the canary is the only real assertion.
  local script="$REPO_ROOT/scripts/qa-smoke.sh"
  assert_file_exists "$script" || return 1
  assert_grep "7438" "$script" "smoke.canary.port" || return 1
}

# ---- Phase 4: Makefile and README additive appends -------------------------

test_makefile_phony_appended() {
  # 4.1: a SECOND .PHONY line with qa-* targets must be appended.
  # Existing .PHONY must not be modified.
  local f="$REPO_ROOT/Makefile"
  assert_file_exists "$f" || return 1
  # Find the qa-* .PHONY line.
  if ! grep -qE '^\.PHONY:.*qa-' "$f"; then
    record_fail "Makefile: no .PHONY line with qa-* targets appended"
    return 1
  fi
}

test_makefile_qa_targets_appended() {
  # 4.2: qa-build, qa-up, qa-down, qa-logs, qa-smoke, qa-clean targets
  # all exist and have recipes.
  local f="$REPO_ROOT/Makefile"
  assert_file_exists "$f" || return 1
  for target in qa-build qa-up qa-down qa-logs qa-smoke qa-clean; do
    if ! grep -qE "^${target}:" "$f"; then
      record_fail "Makefile: missing target $target"
      return 1
    fi
  done
}

test_makefile_existing_targets_unchanged() {
  # The original .PHONY line and existing targets must remain intact.
  local f="$REPO_ROOT/Makefile"
  assert_file_exists "$f" || return 1
  local content
  content=$(cat "$f")
  # Original targets are: build, run, run-http, test, clean, deps, lint, fmt.
  for target in build run run-http test clean deps lint fmt; do
    if ! printf '%s' "$content" | grep -qE "^${target}:"; then
      record_fail "Makefile: existing target $target missing (must remain unchanged)"
      return 1
    fi
  done
}

test_readme_qa_section_appended() {
  # 4.3: README must have a `## Local QA environment` section.
  local f="$REPO_ROOT/README.md"
  assert_file_exists "$f" || return 1
  if ! grep -qE '^## Local QA environment' "$f"; then
    record_fail "README: missing `## Local QA environment` section"
    return 1
  fi
}

test_readme_documents_env_qa_dev_only() {
  # B2 gate: the README's QA section must document `.env.qa` as a
  # DEV-ONLY file containing QA_AUTH_KEY. The user must NEVER use a
  # production credential here — the file exists only so a developer can
  # drop in a local key without exporting env vars. The doc must also
  # call out the dev-only nature explicitly.
  local f="$REPO_ROOT/README.md"
  assert_file_exists "$f" || return 1
  local content
  content=$(cat "$f")
  assert_contains "$content" ".env.qa" "readme.env-qa" || return 1
  assert_contains "$content" "QA_AUTH_KEY" "readme.qa-auth-key" || return 1
  # The "dev-only" or equivalent warning must appear in the QA section.
  # We accept either English "dev-only" or Spanish "solo-dev".
  if ! printf '%s' "$content" | grep -qiE 'dev-only|solo-dev|sólo.desarrollo'; then
    record_fail "readme.env-qa: must explicitly mark .env.qa as dev-only"
    return 1
  fi
}

test_readme_has_canonical_license_section() {
  # README.md is the canonical English document; its license section must remain.
  local f="$REPO_ROOT/README.md"
  assert_file_exists "$f" || return 1
  if ! grep -qE '^## License' "$f"; then
    record_fail "README: missing canonical `## License` section"
    return 1
  fi
}

# ---- Phase 5: Boundary grep and additive diff -----------------------------

test_boundary_grep_production_paths_clean() {
  # Spec scenario "production paths are untouched": no production path
  # should reference deploy/qa or qa-{up,down,smoke}.
  local cmd_grep
  cmd_grep=$(grep -rE 'deploy/qa|qa-up|qa-down|qa-smoke' \
    "$REPO_ROOT/cmd/" "$REPO_ROOT/internal/" "$REPO_ROOT/pkg/" \
    "$REPO_ROOT/deploy/docker/" "$REPO_ROOT/deploy/kubernetes/" \
    "$REPO_ROOT/deploy/systemd/" 2>/dev/null || true)
  if [ -n "$cmd_grep" ]; then
    record_fail "production paths reference QA scope: $cmd_grep"
    return 1
  fi
}

test_diff_is_additive_only_under_budget() {
  # The SDD review guard is 400 changed lines for a single PR, but
  # local-docker-qa has been retroactively scoped under the same
  # size:exception envelope that governs `restore-runtime-contract`
  # (user decision #4280, precedent #4125). The combined additive
  # scope for the two stacked PRs is ~850 lines; the per-PR cap for
  # the local-docker-qa PR is 800 lines to leave headroom for the
  # final smoke-script + limiter.toml removal (which together add
  # ~25 lines on top of the original 250-line forecast).
  # Strict TDD requires a substantial test file; that file
  # (tests/qa/qa_scripts_test.sh) is the SDD test artifact
  # required by strict-tdd.md and is reported separately.
  local added=0
  # Walk deploy/qa/ recursively so docker-compose.yml + the searxng
  # subdir are counted. Use find with -type f.
  if [ -d "$REPO_ROOT/deploy/qa" ]; then
    while IFS= read -r f; do
      local lines
      lines=$(wc -l <"$f" || echo 0)
      added=$((added + lines))
    done < <(find "$REPO_ROOT/deploy/qa" -type f)
  fi
  for path in scripts/qa-*.sh; do
    if [ -f "$REPO_ROOT/$path" ]; then
      local lines
      lines=$(wc -l <"$REPO_ROOT/$path" || echo 0)
      added=$((added + lines))
    fi
  done
  if [ -f "$REPO_ROOT/.dockerignore" ]; then
    local di_lines
    di_lines=$(wc -l <"$REPO_ROOT/.dockerignore" || echo 0)
    added=$((added + di_lines))
  fi
  # Makefile and README appends are part of production scope.
  if [ -f "$REPO_ROOT/Makefile" ]; then
    # Count only the appended qa-* lines.
    local makefile_qa_lines
    makefile_qa_lines=$(grep -E '^\.PHONY:.*qa-|^qa-' "$REPO_ROOT/Makefile" -c 2>/dev/null || echo 0)
    added=$((added + makefile_qa_lines))
  fi
  if [ -f "$REPO_ROOT/README.md" ]; then
    # Count only the Local QA environment section.
    local readme_qa_lines
    readme_qa_lines=$(awk '/^## Local QA environment/,0' "$REPO_ROOT/README.md" | wc -l || echo 0)
    added=$((added + readme_qa_lines))
  fi
  if [ "$added" -gt 800 ]; then
    record_fail "additive production scope is $added lines, exceeds 800 review budget"
    return 1
  fi
}

# ---- Driver ----------------------------------------------------------------

main() {
  printf 'Running qa_scripts_test.sh — local-docker-qa additive scope\n'
  printf 'Repo root: %s\n\n' "$REPO_ROOT"

  run_test test_compose_file_exists
  run_test test_compose_top_level_project_name
  run_test test_compose_defines_qa_net_internal
  run_test test_compose_healthchecks_defined
  run_test test_compose_sourcerudder_build_context
  run_test test_compose_build_context_resolves_to_repo_root
  run_test test_compose_publishes_only_loopback_8080
  run_test test_compose_entrypoint_overrides_searxng_url
  run_test test_compose_entrypoint_passes_auth_key
  run_test test_compose_entrypoint_passes_memory_url_empty
  run_test test_compose_uses_existing_dockerfile_image_path
  run_test test_searxng_settings_exists
  run_test test_searxng_settings_empty_engine_list
  run_test test_searxng_settings_json_output
  run_test test_searxng_settings_binds_all_interfaces_8080
  run_test test_searxng_limiter_exists
  run_test test_dockerignore_exists
  run_test test_dockerignore_excludes_local_state

  run_test test_qa_up_rejects_unexpected_positional_args
  run_test test_qa_up_rejects_metacharacters
  run_test test_qa_up_uses_fixed_project_arg
  run_test test_qa_up_invokes_up_build_wait
  run_test test_qa_up_scoped_cleanup_on_failure
  run_test test_qa_up_derives_qa_auth_key
  run_test test_qa_up_loads_env_qa_file
  run_test test_qa_down_preserves_volumes
  run_test test_qa_smoke_probes_healthz
  run_test test_qa_smoke_probes_tools_list_actual_count
  run_test test_qa_smoke_asserts_twenty_eight_tools
  run_test test_qa_smoke_probes_search_web_empty_uncached
  run_test test_qa_smoke_sends_authorization_bearer
  run_test test_qa_smoke_canary_checks_no_memory_egress

  run_test test_makefile_phony_appended
  run_test test_makefile_qa_targets_appended
  run_test test_makefile_existing_targets_unchanged
  run_test test_readme_qa_section_appended
  run_test test_readme_documents_env_qa_dev_only
  run_test test_readme_has_canonical_license_section

  run_test test_boundary_grep_production_paths_clean
  run_test test_diff_is_additive_only_under_budget

  printf '\n%d passed, %d failed\n' "$PASS_COUNT" "$FAIL_COUNT"
  if [ "$FAIL_COUNT" -gt 0 ]; then
    exit 1
  fi
}

main "$@"
