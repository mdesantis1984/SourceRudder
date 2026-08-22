#!/usr/bin/env bash
# scripts/qa-down.sh — stop the QA stack WITHOUT removing volumes.
# Use `make qa-clean` to drop volumes and the network.

set -u
set -o pipefail

PROJECT="ia-buscar-qa"
COMPOSE_FILE="deploy/qa/docker-compose.yml"

# Same argv discipline as qa-up.sh.
if [ "$#" -ne 0 ]; then
  printf 'qa-down: refusing to run with %d unexpected arg(s)\n' "$#" >&2
  exit 1
fi

if ! command -v docker >/dev/null 2>&1; then
  printf 'qa-down: docker CLI not found\n' >&2
  exit 1
fi
if docker compose version >/dev/null 2>&1; then
  DC=(docker compose)
else
  printf 'qa-down: docker compose v2 plugin not found\n' >&2
  exit 1
fi

"${DC[@]}" -p "$PROJECT" -f "$COMPOSE_FILE" down --remove-orphans
