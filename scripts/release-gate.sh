#!/usr/bin/env bash
#
# Release gate. Exits non-zero on any failure.
#
# Required env (auto-detected if unset):
#   BASE_REF                     base ref for diff comparison (default: origin/main)
#   RELEASE_GATE_BRANCH          current branch name (default: git HEAD)
#                                Overridden automatically by the first
#                                non-empty value among GITHUB_HEAD_REF,
#                                GITHUB_REF_NAME, and CI_COMMIT_REF_NAME
#                                so a detached-HEAD CI checkout (typical
#                                for GitHub Actions merge checkouts and
#                                GitLab CI merge request pipelines)
#                                resolves a real branch name. If none
#                                of those are set AND the local git
#                                ref is literal `HEAD`, the gate fails
#                                closed rather than accepting the
#                                literal HEAD as a branch.
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
#                                Forward Reference). The receipt
#                                documents the exception only; it does
#                                NOT substitute for the RDD receipt
#                                required by step 2.
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
#   2. RDD receipt exists at docs/release/reviews/review-be4525bc4797e972.md
#      with all required fields: Status: pass, Candidate Commit: <sha>
#      (must equal HEAD or HEAD~1 — precise contract for rollback
#      safety), Branch: <exact branch name> (exact match; no substring),
#      Scope: <free-form operator context>, Verified Commands: section
#      whose entries all end in ': PASS' (parsed only within the
#      section), Unresolved Blocker Policy: header. The receipt is
#      the local deterministic attestation — there is NO external
#      review provider binding. A legacy `Authority:` line anywhere
#      in the receipt body is rejected, matching the Go process
#      guard in internal/mcp/release_gate_test.go.
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

# ---- structured log helpers ----------------------------------------------
#
# IMPORTANT: `fail` MUST be defined before any caller invokes it.
# The previous layout put these helpers AFTER the branch-resolution
# step, and the branch-resolution step calls `fail` for the
# detached-HEAD-without-trusted-env case. Bash reported
# `fail: command not found` and continued into the next gate
# step because the script does not run with `set -e`. The fix
# defines the helpers BEFORE the first caller and documents the
# precedence explicitly so a future refactor cannot move the
# helpers below a caller again (R4-001 / NEW-001).

log() { printf 'release-gate: %s\n' "$*"; }
# log_failure emits a per-check FAIL line to stderr so the operator
# (and the test harness) sees which specific gate step failed before
# the gate exits. The final summary `fail` line below also goes to
# stderr. Success-path `log` calls still go to stdout.
log_failure() { printf 'release-gate: FAIL %s\n' "$*" >&2; }
fail() { printf 'release-gate: FAIL %s\n' "$*" >&2; exit 1; }

# Resolve the target branch from a trusted source in priority
# order: CI env var (set by GitHub Actions / GitLab CI), operator
# override, then the local git symbolic ref. The local ref is the
# last resort because a detached-HEAD checkout (`git checkout
# <sha>`, the typical CI merge-checkout shape) returns the literal
# string `HEAD`, which would either pass or fail spuriously against
# a substring-based Scope check. The CI env vars are set by the
# runner BEFORE checkout so they always reflect the source branch
# the operator intends. When none of the trusted sources resolve
# to a real branch name, `fail` MUST terminate the gate
# immediately — the previous bug was that `fail` was undefined
# at this call site, so bash printed `command not found` and the
# gate continued into subsequent steps.
CURRENT_BRANCH="${GITHUB_HEAD_REF:-${GITHUB_REF_NAME:-${CI_COMMIT_REF_NAME:-${RELEASE_GATE_BRANCH:-}}}}"
if [[ -z "$CURRENT_BRANCH" ]]; then
  CURRENT_BRANCH="$(git rev-parse --abbrev-ref HEAD 2>/dev/null || true)"
fi
if [[ -z "$CURRENT_BRANCH" || "$CURRENT_BRANCH" == "HEAD" ]]; then
  fail "could not resolve target branch from CI env (GITHUB_HEAD_REF / GITHUB_REF_NAME / CI_COMMIT_REF_NAME), RELEASE_GATE_BRANCH, or git symbolic ref (detached HEAD with no trusted branch env)"
fi
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

# ---- 2. RDD receipt (deterministic local attestation) --------------------
#
# The previous contract required an `Authority: official` header
# whose only source was a fictitious external review provider
# binding. That gate could not be satisfied from inside the repo.
# The new contract is a deterministic, locally verifiable RDD
# receipt/evidence contract:
#
#   1. Status: pass                  (exact line; anything else blocks)
#   2. Candidate Commit: <sha>       (precise contract: must equal HEAD
#                                    or HEAD~1 — forces a new receipt
#                                    after any rollback or code change)
#   3. Branch: <exact branch name>   (exact match, no substring; the
#                                    authoritative source for the
#                                    target branch the receipt attests)
#   4. Scope: <free-form text>       (operator context; presence only)
#   5. Verified Commands:            (section header; every `- cmd:`
#                                    entry must end in `: PASS` AND
#                                    must fall within the section —
#                                    bullets outside the section are
#                                    ignored so a forged entry cannot
#                                    inflate the count)
#   6. Unresolved Blocker Policy:    (header; value is free-form so
#                                    the operator can declare or waive)
#
# A legacy `Authority:` line anywhere in the receipt body is
# rejected to keep the bash validator symmetric with the Go
# process guard at internal/mcp/release_gate_test.go; a future
# regression that re-introduces the external-binding header MUST
# fail here first.
#
# The receipt is fail-closed: any missing or malformed field blocks
# the merge, and the gate independently re-runs `go build` / `go vet`
# / `go test` / `go test -race` in step 4 so a forged receipt cannot
# bypass a real regression.

if [[ ! -f "$REVIEW_FILE" ]]; then
  fail "RDD receipt missing at $REVIEW_FILE (expected deterministic local attestation)"
fi

rdd_fail=0

# 0. Hard guard: reject any line beginning with the legacy
#    `Authority:` header anywhere in the receipt body. The Go
#    process guard in internal/mcp/release_gate_test.go performs
#    the same check via `strings.HasPrefix(strings.TrimSpace(line),
#    "Authority:")`; both validators must stay symmetric so a
#    future regression cannot smuggle the external-binding
#    header back through only one of them. The regex below
#    mirrors the Go guard exactly: a line is rejected iff its
#    trimmed first column starts with `Authority:` — including
#    a bare `Authority:` header with no value and no trailing
#    whitespace (which the previous `^[[:space:]]*Authority:[[:space:]]`
#    regex incorrectly accepted because it required whitespace
#    after the colon).
if grep -qE '^[[:space:]]*Authority:' "$REVIEW_FILE"; then
  log_failure "RDD receipt at $REVIEW_FILE contains a legacy 'Authority:' line; the local RDD contract replaced the external-binding header and it MUST NOT return"
  rdd_fail=1
fi

# 1. Status: pass (exact line).
rdd_status="$(grep -E '^Status:' "$REVIEW_FILE" | head -n 1 || true)"
if [[ "$rdd_status" != "Status: pass" ]]; then
  log_failure "RDD receipt Status line must be exactly 'Status: pass' (found: ${rdd_status:-<missing>}) at $REVIEW_FILE"
  rdd_fail=1
fi

# 2. Candidate Commit: <sha>. The precise contract requires the
#    SHA to equal HEAD or HEAD~1. This forces a new receipt after
#    a rollback (the buggy SHA is no longer HEAD~1) and after any
#    code change (the receipt's candidate SHA is now HEAD~2 or
#    deeper). The two-commit code-then-receipt workflow still
#    works because the receipt commit lands at HEAD and the code
#    commit it attests sits at HEAD~1.
rdd_commit_line="$(grep -E '^Candidate Commit:' "$REVIEW_FILE" | head -n 1 || true)"
if [[ -z "$rdd_commit_line" ]]; then
  log_failure "RDD receipt Candidate Commit line missing at $REVIEW_FILE"
  rdd_fail=1
else
  rdd_commit_sha="$(printf '%s' "$rdd_commit_line" | sed -E 's/^Candidate Commit:[[:space:]]*//' | awk '{print $1}')"
  if [[ -z "$rdd_commit_sha" ]]; then
    log_failure "RDD receipt Candidate Commit value empty at $REVIEW_FILE"
    rdd_fail=1
  elif ! [[ "$rdd_commit_sha" =~ ^[0-9a-f]{40}$ ]]; then
    log_failure "RDD receipt Candidate Commit $rdd_commit_sha is not a full 40-char SHA at $REVIEW_FILE"
    rdd_fail=1
  else
    rdd_head_sha="$(git rev-parse HEAD)"
    rdd_head_parent_sha="$(git rev-parse HEAD~1 2>/dev/null || true)"
    if [[ "$rdd_commit_sha" != "$rdd_head_sha" && "$rdd_commit_sha" != "$rdd_head_parent_sha" ]]; then
      log_failure "RDD receipt Candidate Commit $rdd_commit_sha must equal HEAD ($rdd_head_sha) or HEAD~1 (${rdd_head_parent_sha:-<none>}) — arbitrary ancestors are not accepted; rollback or code change requires a new receipt at $REVIEW_FILE"
      rdd_fail=1
    else
      log "RDD_CANDIDATE_COMMIT=$rdd_commit_sha matches HEAD_or_HEAD~1"
    fi
  fi
fi

# 3. Branch: <exact branch name> — exact match (no substring).
#    The previous substring check was ambiguous (`feature/foo`
#    matched a Scope of `feature/foo-bar`). The dedicated
#    `Branch:` field is the authoritative source for the
#    target branch the receipt attests; `Scope:` is now
#    free-form operator context whose value is not used for
#    branch verification.
rdd_branch_line="$(grep -E '^Branch:' "$REVIEW_FILE" | head -n 1 || true)"
if [[ -z "$rdd_branch_line" ]]; then
  log_failure "RDD receipt Branch line missing at $REVIEW_FILE (the receipt must declare its target branch via a dedicated Branch: field for exact-match verification)"
  rdd_fail=1
else
  rdd_branch_value="$(printf '%s' "$rdd_branch_line" | sed -E 's/^Branch:[[:space:]]*//')"
  if [[ -z "$rdd_branch_value" ]]; then
    log_failure "RDD receipt Branch value empty at $REVIEW_FILE"
    rdd_fail=1
  elif [[ "$rdd_branch_value" != "$CURRENT_BRANCH" ]]; then
    log_failure "RDD receipt Branch value '$rdd_branch_value' must exactly match the resolved branch '$CURRENT_BRANCH' at $REVIEW_FILE"
    rdd_fail=1
  fi
fi

# 3b. Scope: <free-form text>. Required for operator context
#     but no longer used for branch verification.
rdd_scope_line="$(grep -E '^Scope:' "$REVIEW_FILE" | head -n 1 || true)"
if [[ -z "$rdd_scope_line" ]]; then
  log_failure "RDD receipt Scope line missing at $REVIEW_FILE"
  rdd_fail=1
fi

# 4. Verified Commands section with all-PASS entries. The
#    parser is section-scoped: iteration begins on the line
#    immediately after `Verified Commands:` and ends on the
#    next top-level `^[A-Z][A-Za-z]+:` header (or end of
#    file). A bullet outside the section is ignored, so a
#    forged entry in a Notes: section cannot inflate the
#    count.
if ! grep -qxE '^Verified Commands:' "$REVIEW_FILE"; then
  log_failure "RDD receipt Verified Commands: section missing at $REVIEW_FILE"
  rdd_fail=1
else
  rdd_entry_count=0
  rdd_bad_entry=""
  rdd_pass_re='^[[:space:]]+- .*:[[:space:]]*PASS[[:space:]]*$'
  # Top-level header pattern: an unindented capitalized word
  # followed by a colon. Used to exit the section.
  rdd_header_re='^[A-Z][A-Za-z][A-Za-z0-9 ]*:[[:space:]]*[^[:space:]]|^[A-Z][A-Za-z][A-Za-z0-9 ]*:[[:space:]]*$'
  rdd_in_section=0
  while IFS= read -r rdd_line; do
    if (( rdd_in_section == 0 )); then
      if [[ "$rdd_line" == "Verified Commands:" ]]; then
        rdd_in_section=1
      fi
      continue
    fi
    # Inside the section: exit on the next top-level header.
    if [[ "$rdd_line" =~ $rdd_header_re ]]; then
      break
    fi
    # Skip blank lines.
    [[ -z "$rdd_line" ]] && continue
    rdd_entry_count=$((rdd_entry_count + 1))
    if ! [[ "$rdd_line" =~ $rdd_pass_re ]]; then
      rdd_bad_entry="$rdd_line"
      break
    fi
  done < "$REVIEW_FILE"
  if (( rdd_entry_count == 0 )); then
    log_failure "RDD receipt Verified Commands section has no entries at $REVIEW_FILE"
    rdd_fail=1
  fi
  if [[ -n "$rdd_bad_entry" ]]; then
    log_failure "RDD receipt Verified Commands entry must end with ': PASS' (got: $rdd_bad_entry)"
    rdd_fail=1
  fi
fi

# 5. Unresolved Blocker Policy header must be present with a value.
#    The operator MUST declare the actual policy (`none`, or a
#    description of any open blocker). A line that is exactly
#    `Unresolved Blocker Policy:` with nothing after the colon is
#    rejected because the value is the substantive declaration; the
#    header alone is a placeholder.
if ! grep -qE '^Unresolved Blocker Policy:[[:space:]]*[^[:space:]]' "$REVIEW_FILE"; then
  log_failure "RDD receipt Unresolved Blocker Policy line missing or empty at $REVIEW_FILE"
  rdd_fail=1
fi

if (( rdd_fail == 1 )); then
  fail "RDD receipt at $REVIEW_FILE did not validate (see FAIL lines above)"
fi
log "RDD_RECEIPT=$REVIEW_FILE validated status=pass branch=$CURRENT_BRANCH candidate=HEAD_or_HEAD~1"

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
  for field in "Branch" "Commit" "Approval Reference" "Scope" "Expiration" "Forward Reference"; do
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
