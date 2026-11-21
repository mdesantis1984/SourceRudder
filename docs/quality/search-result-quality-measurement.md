# Search result quality: before/after measurement (T17, S7-S11)

Date: 2026-11-21. Branch `feat/search-result-quality` at `1282b16` (after) versus `origin/develop` `02848d8` (before).

## Setup

- Stack: the pinned SearXNG from `deploy/quality/docker-compose.yml` (project `sourcerudder-quality`, only the `searxng`
  service), with public egress, as the quality stack documents. No production service was touched.
- Both binaries were built with `CGO_ENABLED=0 go build ./cmd/sourcerudder` (before from `git archive origin/develop`).
  Each ran in turn in a `golang:1.26-alpine` container on the same quality network, bound to `127.0.0.1:18080`, with the
  compose app's limits (read-only, `cap_drop ALL`, 512 MB, 0.5 CPU) and a fresh container (cold cache) per run. The
  compose `app` service was not used because its image needs a `golang:1.27.4` pull; the runtime is the same static binary.
- Corpus: `scripts/quality/corpus/search_quality.json` (20 cases, default JSON format, one pass, `--repeat 1`).
- Commands (key generated per run, never stored):

```bash
python3 scripts/quality/measure_corpus.py run --output before.json     # then after.json
python3 scripts/quality/measure_corpus.py compare before.json after.json
# rerun with the order reversed (after2, then before2) to expose order and upstream variance
```

## Before/after (run 1: before first, then after)

| Metric | Before | After |
| --- | --- | --- |
| Results returned (20 queries) | 182 | 182 |
| Expectation hits (hit@K) | 14/20 (0.70) | 15/20 (0.75) |
| Mean first-hit rank | 1.000 | 1.467 |
| Latency p50 / p95 (ms) | 681 / 1288 | 380 / 1082 |
| Total bytes (model-visible text) | 87,598 | 73,313 (-16%) |
| Median bytes per response | 5,044 | 4,313 |
| Total tokens (estimate, chars/4) | 21,861 | 18,288 |
| Tool errors / transport failures | 0 | 0 |

Run 2 (reversed order, after first):

| Metric | Before | After |
| --- | --- | --- |
| Results returned | 162 | 182 |
| hit@K | 13/20 (0.65) | 15/20 (0.75) |
| Latency p50 / p95 (ms) | 286 / 1002 | 323 / 943 |
| Total bytes | 80,064 | 73,381 (-8%) |
| Tool errors | 0 | 0 |

`compare` exit code was 0 in both runs (no hit@K drop). Bytes fall because the default text is now minified JSON
(`structuredContent` carries the full typed response). Corpus cases use `maxResults=10`, so the per-query result count
does not show S2/S8: `search_web` stores 20 results per query on the after binary (`total` 20).

## Bytes of the new output modes (after binary)

Corpus arguments, default text versus `format=compact`; one cursor page per web query. Web bytes vary a little between
passes because live results change (for example `web-go-context` default: 4,387 and 4,307 bytes).

| Query | Default text | compact snippets | compact titles | Cursor page 2 |
| --- | --- | --- | --- | --- |
| web-go-context | 4,307 | 3,089 | 1,471 | 4,631 B, 10 results |
| web-docker-healthcheck | 4,505 | 3,288 | 1,675 | 4,463 B, 10 results |
| web-python-asyncio | 4,346 | 3,129 | 1,496 | 4,337 B, 10 results |
| github-kubernetes | 2,864 | 1,149 | 750 | none (`total` 10) |
| so-python-yield | 4,331 | 2,129 | 1,892 | none (`total` 10) |

Zero upstream requests for the cursor page: the SearXNG container was paused (`docker pause`) while the cursor call ran.
All three calls succeeded (`isError` false, 10 results), so none needed SearXNG. The cursor page is not smaller than
page 1: it is the same 10-result slice size.

`tools/list` (raw JSON-RPC `result`, minified): before 22,907 bytes (29 tools); after 55,753 bytes. This is the cost of
`outputSchema`, annotations and titles for clients that load every schema up front.

## Variance and caveats

- The upstream engines were degraded all runs: brave suspended, duckduckgo and startpage CAPTCHA, news engines blocked.
  Nine to eleven responses were `partial` in every run, before and after alike, with the same warnings.
- Run 2 `before` returned 0 results for `github-golang-go` and `github-searxng` (`API error: 403`, unauthenticated GitHub
  rate limit), which gave its lower hit rate (13/20) and result count (162). That is upstream variance, not a code
  difference; the after binary in the same window returned 10 each. Do not read it as evidence for S1.
- `news-linux-kernel` missed in `before` run 1 and hit (rank 8) in every other run: volatile news, as the README warns.
- Mean first-hit rank rose in run 1 only because that news case now hits at rank 8; it fell in run 2 (1.538 to 1.4).
- Latency is not comparable between runs: p50 is 286-681 ms for `before`, and upstream caches warm during a pass. The
  first run favored `after` (it ran second), run 2 slightly disfavored it. Treat latency as roughly equal.
- Stable misses in both binaries (`so-exit-vim`, `npm-express`, `npm-react`, `npm-typescript`, `news-kubernetes`) are
  corpus or upstream expectation issues, not regressions.

## Not measured

- Real tokenizer counts (the figures are `ceil(characters / 4)`).
- GitHub/StackOverflow zero-result retry (S1), page-2 fill (S2) and 30 s empty cache (S3) with an upstream that returns
  those conditions; the corpus queries returned results normally.
- Synthesis tools with `resultSetId` (S10), cursor expiry, lazy page fetch beyond the stored set, concurrency.
- Compact format over the whole corpus; only the 5 queries above were measured in compact mode.
- Production, CT201 and any non-loopback endpoint; Pi or codemode token costs.
