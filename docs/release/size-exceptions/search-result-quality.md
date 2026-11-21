# Size Exception Receipt: search-result-quality

Branch: feat/search-result-quality
Commit: 2a5b36a172147b45149b895e8b6b0022729cd901
Approval Reference: User decision 2026-11-21 (single PR with size exception, recorded in the session task document; the user asked to deliver everything together in one PR).
Scope: One-time PR for search result quality: result sets with cursor paging, compact output, MCP result envelope, tool metadata and isError, synthesis by resultSetId, lazy web paging, quality corpus and measurement scripts, and docs.
Expiration: 2026-12-31
Forward Reference: https://github.com/mdesantis1984/SourceRudder/issues/28 (Measure and reduce MCP context token usage), closed by this PR; production rollout follows the merge.

Commit above is the last code commit (HEAD 2a5b36a); this receipt sits on top of it.

Size at that commit, `git diff --shortstat origin/develop...HEAD`:
72 files changed, 7413 insertions(+), 240 deletions(-) (7,653 changed lines;
the receipt and workflow edit add a few more).

Verification already done on that commit: gofmt clean, go vet clean,
`go test -race ./...` ok, 62 Python quality tests ok, LabSecurity gate PASS
(new 0, open 0, blocking 0; engagement 95).

The exception does not waive build, vet, test, race, gosec, govulncheck,
Gitleaks, Actionlint, CodeQL, issue approval, or type-label gates. It applies
only to the exact branch above and expires after this PR is merged or closed.
