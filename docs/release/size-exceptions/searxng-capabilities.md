# Size Exception Receipt: searxng-capabilities

Branch: feat/searxng-capabilities
Commit: 102f6755ec07cd8f36701f83bed90d90fc49dfc4
Approval Reference: User decision 2026-11-24 ("No importa el límite, vamos a hacer una excepción, necesito que pase directamente"), recorded in the session task document; one PR instead of the chained split.
Scope: One-time PR for SearXNG capabilities: proven science engines and the timeRange empty-result warning, SearXNG answers, infoboxes, suggestions and corrections, search_youtube through yt-dlp with SearXNG fallback, a shared RSS/Atom parser, Google News RSS fallback in search_news, the new search_tech_news tool, the result fields publishedAt, engine and author in the search outputSchema, and docs.
Expiration: 2026-12-31
Forward Reference: https://github.com/mdesantis1984/SourceRudder/issues/55 (Use more SearXNG capabilities and keyless news and video sources), closed by this PR; production rollout follows the merge.

Commit above is the last code commit; this receipt sits on top of it.

Size at that commit, `git diff --shortstat origin/develop...HEAD`:
 57 files changed, 4668 insertions(+), 112 deletions(-)
(the receipt and workflow edit add a few more).

Verification already done on that commit: gofmt clean, go vet clean,
`go test -race -count=1 ./...` ok, QA script tests 39/0, MCP end-to-end runs
of search_youtube, search_news and search_tech_news with jsonschema validation
of structuredContent against outputSchema, LabSecurity gate PASS (new 0,
open 0, blocking 0).

The exception does not waive build, vet, test, race, gosec, govulncheck,
Gitleaks, Actionlint, CodeQL, issue approval, or type-label gates. It applies
only to the exact branch above and expires after this PR is merged or closed.
