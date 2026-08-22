#!/usr/bin/env bash
#
# Release gate. Exits non-zero on any failure.
#
# Required env (auto-detected if unset):
#   BASE_REF                     base ref for diff comparison (default: origin/main)
#   RELEASE_GATE_BRANCH          current branch name (default: git HEAD)
#
# Optional env:
#   RELEASE_GATE_SIZE_EXCEPTION  exact branch name permitted to exceed the
#                                400-line authored budget. Matches that
#                                branch ONLY; every other branch falls
#                                back to the strict 400-line gate.
#                                When the carve-out is active, the gate
#                                ALSO validates a tracked size-exception
#                                receipt at
#                                docs/release/size-exceptions/<branch>.md
#                                with required fields (Branch, Commit,
#                                Approval Reference, Scope, Expiration,
#                                Authority Requirement). The receipt
#                                documents the exception; it does NOT
#                                substitute for the final official 4R
#                                review with Authority: official.
#
# Local-QA seam (default OFF; ignored under CI):
#   RELEASE_GATE_ALLOW_DIRTY=1   skips the worktree cleanliness check
#                                so a local operator can run the gate
#                                against uncommitted work. CI runs
#                                must NEVER set this — the gate
#                                ignores it when CI=true.
#
# What it checks (in order):
#   1. Worktree is clean (only docs/release/reviews/, .atl/, .codegraph/,
#      .codebase-memory/ allowed as untracked; seam ignored under CI).
#   2. Review file exists at docs/release/reviews/review-be4525bc4797e972.md
#      and its Authority header is exactly "Authority: official".
#   3. Diff vs merge-base of BASE_REF is below 400 lines, OR the carve-out
#      env var exactly matches the current branch AND the tracked
#      size-exception receipt validates.
#   4. go build ./...  ;  go vet ./...  ;  go test ./...  ;  go test -race ./...
#
# All checks emit structured key=value lines so CI logs and the PR body
# can both surface the same evidence.

set -uo pipefail

REPO_ROOT="$(git rev-parse --show-toplevel)"
cd "$REPO_ROOT"

CURRENT_BRANCH="${RELEASE_GATE_BRANCH:-$(git rev-parse --abbrev-ref HEAD)}"
BASE_REF="${BASE_REF:-origin/main}"

ALLOW_LIST_REGEX='^(docs/release/reviews/|\.atl/|\.codegraph/|\.codebase-memory/)'
REVIEW_FILE="docs/release/reviews/review-be4525bc4797e972.md"
SIZE_EXCEPTIONS_DIR="docs/release/size-exceptions"
# The size-exception receipt is tracked at a fixed path under the
# size-exceptions directory. The change name in the filename matches
# the SDD change identifier so every carve-out has a discoverable,
# reviewable artifact. The receipt's `Branch` field carries the
# actual branch name. Operators can override the path via
# SIZE_EXCEPTIONS_RECEIPT to use a different receipt location.
SIZE_EXCEPTIONS_RECEIPT="${SIZE_EXCEPTIONS_RECEIPT:-${SIZE_EXCEPTIONS_DIR}/close-fetch-resilience-and-release-gates.md}"

# ---- structured log helpers ----------------------------------------------

log() { printf 'release-gate: %s\n' "$*"; }
fail() { printf 'release-gate: FAIL %s\n' "$*" >&2; exit 1; }

# ---- preconditions -------------------------------------------------------

MERGE_BASE="$(git merge-base "$BASE_REF" HEAD 2>/dev/null || true)"
if [[ -z "$MERGE_BASE" ]]; then
  fail "could not resolve merge-base of BASE_REF=$BASE_REF"
fi
log "BASE_REF=$BASE_REF BRANCH=$CURRENT_BRANCH MERGE_BASE=$MERGE_BASE"

# ---- 1. worktree cleanliness ---------------------------------------------

# The local-QA seam is honoured ONLY outside CI. CI runs must enforce
# the committed-clean invariant so a CI build cannot accidentally
# accept uncommitted work. The elif below surfaces the bypass attempt
# in the log so the operator knows the seam was ignored.
if [[ "${CI:-}" == "true" && "${RELEASE_GATE_ALLOW_DIRTY:-}" == "1" ]]; then
  log "RELEASE_GATE_ALLOW_DIRTY=1 ignored under CI=true (gate runs against committed work)"
fi
if [[ "${RELEASE_GATE_ALLOW_DIRTY:-}" == "1" && "${CI:-}" != "true" ]]; then
  log "WARN RELEASE_GATE_ALLOW_DIRTY=1: skipping worktree cleanliness check (local QA only)"
else
  if ! git diff --quiet HEAD -- .; then
    fail "worktree has uncommitted tracked changes"
  fi
  untracked_outside="$(git ls-files --others --exclude-standard \
    | grep -vE "$ALLOW_LIST_REGEX" || true)"
  if [[ -n "$untracked_outside" ]]; then
    fail "untracked files outside allow-list:
$untracked_outside"
  fi
fi

# ---- 2. review placeholder (Authority gate) -----------------------------

if [[ ! -f "$REVIEW_FILE" ]]; then
  fail "review placeholder missing at $REVIEW_FILE"
fi
# Exact-match only: the previous regex `^Authority:[[:space:]]*[A-Za-z]+`
# accepted ANY word; the gate now requires the exact literal
# `Authority: official`. Anything else (pending, foo, Approved,
# official-something) is rejected so a partial review cannot unlock
# a merge.
if ! grep -qxE '^Authority:[[:space:]]*official' "$REVIEW_FILE"; then
  observed="$(grep -E '^Authority:' "$REVIEW_FILE" | head -n 1 || true)"
  fail "review file does not declare Authority: official (found: ${observed:-<missing>}) at $REVIEW_FILE"
fi
log "REVIEW_FILE=$REVIEW_FILE Authority=official"

# ---- 3. line-budget (with exact-branch carve-out + tracked receipt) ----

NUMSTAT="$(git diff --numstat "$MERGE_BASE..HEAD" || true)"
ADDED="$(printf '%s\n' "$NUMSTAT"   | awk '$1 != "-" {a += $1} END {print a+0}')"
REMOVED="$(printf '%s\n' "$NUMSTAT" | awk '$2 != "-" {r += $2} END {print r+0}')"
TOTAL=$((ADDED + REMOVED))
log "DIFF_RANGE=$MERGE_BASE..HEAD ADDED=$ADDED REMOVED=$REMOVED TOTAL=$TOTAL"

if (( TOTAL >= 400 )); then
  if [[ "${RELEASE_GATE_SIZE_EXCEPTION:-}" != "$CURRENT_BRANCH" ]]; then
    fail "line budget exceeded TOTAL=$TOTAL (>= 400) and RELEASE_GATE_SIZE_EXCEPTION!=$CURRENT_BRANCH"
  fi
  # Carve-out active: validate the tracked receipt.
  log "SIZE_EXCEPTION=active branch=$CURRENT_BRANCH total=$TOTAL (>= 400, validating tracked receipt)"
  RECEIPT_FILE="$SIZE_EXCEPTIONS_RECEIPT"
  if [[ ! -f "$RECEIPT_FILE" ]]; then
    fail "size-exception receipt missing at $RECEIPT_FILE (required when carve-out is active)"
  fi
  if ! git ls-files --error-unmatch "$RECEIPT_FILE" >/dev/null 2>&1; then
    fail "size-exception receipt $RECEIPT_FILE is not tracked in git (untracked receipt cannot be reviewed on the PR diff)"
  fi
  missing_field=""
  for field in "Branch" "Commit" "Approval Reference" "Scope" "Expiration" "Authority Requirement"; do
    if ! grep -qE "^${field}:" "$RECEIPT_FILE"; then
      missing_field="$missing_field $field"
    fi
  done
  if [[ -n "$missing_field" ]]; then
    fail "size-exception receipt missing required field(s):$missing_field (see $RECEIPT_FILE)"
  fi
  log "SIZE_EXCEPTION_RECEIPT=$RECEIPT_FILE validated"
else
  log "SIZE_EXCEPTION=inactive total=$TOTAL (< 400, no carve-out required)"
fi

# ---- 4. Go checks --------------------------------------------------------

log "RUN go build"
go build ./... || fail "go build"
log "RUN go vet"
go vet ./...   || fail "go vet"
log "RUN go test"
go test ./...  || fail "go test"
log "RUN go test -race"
go test -race ./... || fail "go test -race"

log "PASS"
