#!/usr/bin/env bash
# scripts/qa-up.sh — boot the local Docker Compose QA stack with
# scoped cleanup on failure. Refuses ANY argv (Threat A).
#
# Auth: live auth.Validator (internal/auth/auth.go:38-58) rejects
# every /mcp request when IA_BUSCAR_AUTH_KEY is set but no matching
# credential is presented. QA_AUTH_KEY source precedence:
#   1. $AUTH_KEY (operator override, dev-only)
#   2. .env.qa (dev-only; gitignored)  3. "" (fail-closed)
# The key is then injected into the container as IA_BUSCAR_AUTH_KEY
# (env path, NOT -auth-key argv) so the secret never appears in
# `ps aux` — see CT201 fix in cmd/ia-buscar/main.go.
set -u
set -o pipefail

PROJECT="ia-buscar-qa"
COMPOSE_FILE="deploy/qa/docker-compose.yml"

if [ "$#" -ne 0 ]; then
  printf 'qa-up: refusing %d unexpected arg(s)\n' "$#" >&2
  exit 1
fi
[ -n "${QA_INJECT:-}" ] && { printf 'qa-up: refusing QA_INJECT\n' >&2; exit 1; }

log() { printf 'qa-up: %s\n' "$*" >&2; }

# Derive QA_AUTH_KEY. Source precedence (first non-empty wins):
# $AUTH_KEY → .env.qa → "" (fail-closed).
if [ -z "${AUTH_KEY:-}" ] && [ -f ".env.qa" ]; then
  _qa_key=$(awk -F= '/^[[:space:]]*QA_AUTH_KEY[[:space:]]*=/ {sub(/^[[:space:]]*QA_AUTH_KEY[[:space:]]*=/, ""); print; exit}' .env.qa || true)
  if [ -n "$_qa_key" ]; then
    export QA_AUTH_KEY="$_qa_key"
    log "loaded QA_AUTH_KEY from .env.qa (dev-only)"
  fi
fi
if [ -n "${AUTH_KEY:-}" ]; then
  export QA_AUTH_KEY="$AUTH_KEY"
  log "loaded QA_AUTH_KEY from \$AUTH_KEY"
fi
export QA_AUTH_KEY="${QA_AUTH_KEY:-}"

if ! command -v docker >/dev/null 2>&1; then log "docker missing"; exit 1; fi
if ! docker compose version >/dev/null 2>&1; then log "compose v2 missing"; exit 1; fi
DC=(docker compose)

# Scoped cleanup: same project label, drop orphans+volumes, verify zero
# project containers and absent qa-net.
cleanup() {
  local rc="$1"
  log "up failed (rc=$rc); scoped cleanup"
  "${DC[@]}" -p "$PROJECT" -f "$COMPOSE_FILE" down --remove-orphans --volumes >/dev/null 2>&1 || true
  local leftover
  leftover=$(docker ps -a --filter "label=com.docker.compose.project=$PROJECT" --format '{{.ID}}' | wc -l)
  [ "$leftover" -eq 0 ] || log "post-cleanup: $leftover project containers remain"
  if docker network inspect "${PROJECT}_qa-net" >/dev/null 2>&1; then
    log "post-cleanup: qa-net still present"
  fi
  exit "$rc"
}

log "booting [$PROJECT]"
if ! "${DC[@]}" -p "$PROJECT" -f "$COMPOSE_FILE" up --build --wait --wait-timeout 60; then
  "${DC[@]}" -p "$PROJECT" -f "$COMPOSE_FILE" ps -a >&2 || true
  "${DC[@]}" -p "$PROJECT" -f "$COMPOSE_FILE" logs --tail=200 >&2 || true
  cleanup 1
fi
log "stack is healthy"
exit 0
