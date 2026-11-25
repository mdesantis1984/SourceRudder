# Size Exception Receipt: search-hardening

Branch: fix/search-hardening
Commit: 01dc4f18532883cf0726c6d5c176d14bd8a64139
Approval Reference: User decision 2026-11-25 ("Si hay alguna excepción de PR por cantidad de líneas de código... Lo apruebo como excepción."), recorded in the session task document; one PR instead of a chained split.
Scope: One-time PR for search hardening: host-less and adult result filtering with safe search on by default, cursor-only search calls and the first page size kept on cursor pages, synthesis confidence capped by distinct hosts, one retry on transient failures for HN Algolia, Google News RSS, npm, PyPI, NuGet and Docker Hub, registry links for NuGet and PyPI packages without a URL, tests and docs.
Expiration: 2026-12-31
Forward Reference: https://github.com/mdesantis1984/SourceRudder/issues/57 (Search results carry cloaked URLs and adult content, cursor-only calls fail, transient source failures empty results), closed by this PR; production rollout follows the merge.

Commit above is the last code commit; this receipt sits on top of it.

Size at that commit, `git diff --shortstat origin/develop...HEAD`:
 27 files changed, 1267 insertions(+), 67 deletions(-)
(the receipt and workflow edit add a few more).

Verification already done on that commit: gofmt clean, go vet clean,
`go test -race -count=1 ./...` ok, Python quality tests 62/0, production
end-to-end on CT201 of every tool (search, paging, cursor-only, fetch,
transcript, synthesis), LabSecurity gate PASS (new 0, open 0, blocking 0).

The exception does not waive build, vet, test, race, gosec, govulncheck,
Gitleaks, Actionlint, CodeQL, issue approval, or type-label gates. It applies
only to the exact branch above and expires after this PR is merged or closed.
