# Changelog

[English](CHANGELOG.md) | [Español](CHANGELOG.es.md)

All notable SourceRudder changes are documented here.

## [Unreleased]

### Added

- The shared search `outputSchema` declares the optional result fields
  `publishedAt`, `engine` and `author`, so codemode and other TypeScript clients
  see the date, the producing engine and the channel or author in their types.
- `search_tech_news` MCP tool: technology and software engineering news with no
  API key, from HN Algolia, Google News RSS search and 19 curated RSS/Atom feeds
  (read concurrently, 8 s per feed, 12 s overall, cached 30 minutes per feed).
  Curated items are filtered by the query, results are ranked by match with
  the sources taking turns inside a tier, HN is always bounded by date, `timeRange` filters by publication date, and failed sources give one
  summarized warning. Feed content is sanitized and size-capped.
- `search_news` adds Google News RSS results after the SearXNG ones when SearXNG
  returns fewer than `maxResults` or answers partly (`language` maps to the Google
  News edition, `timeRange` to `when:1d/7d/30d/1y`); an RSS failure keeps the SearXNG results
  and adds a warning.
- `fetch_youtube_transcript` MCP tool (`url`, optional `language`, optional
  `maxChars`) that returns a YouTube video transcript with title, channel, date,
  duration, language, manual/automatic flag, and cache status, through yt-dlp
  with bgutil PO tokens. Transcripts are cached per video and language, and a
  429 or bot check yields one `youtube_blocked` error with a short global
  cooldown, without retries.
- `fetch_url` and `fetch_and_extract` delegate YouTube video URLs to the
  transcript service instead of scraping the watch page.
- `deploy/youtube/` assets: yt-dlp and bgutil installers, a hardened loopback-only
  `bgutil-pot-provider.service`, a weekly `yt-dlp-update` timer, and new
  `YTDLP_PATH`, `YTDLP_JS_RUNTIMES`, `YOUTUBE_POT_BASE_URL`, `YOUTUBE_FETCH_POT`, `YOUTUBE_DEFAULT_LANGUAGE`, `YOUTUBE_CACHE_TTL`,
  `YOUTUBE_BLOCK_COOLDOWN`, `YTDLP_TIMEOUT`, `YOUTUBE_WORK_DIR`, and
  `YOUTUBE_TRANSCRIPTS` settings documented in the deployment and configuration guides.
- Result sets: every search tool keeps the full list of results it fetched in an
  in-process store and returns one page plus `resultSetId`, `total` and, only while
  results remain, an opaque `nextCursor`. Send `cursor` (the previous `nextCursor`)
  to read the next page with no upstream request; a cursor only works with the tool
  that issued it. New flags `-result-set-ttl` (`RESULT_SET_TTL`, default `900`
  seconds), `-result-set-max-sets` (`RESULT_SET_MAX_SETS`, default `64`) and
  `-result-set-max-results` (`RESULT_SET_MAX_RESULTS`, default `2000`) bound the store.
- `summarize_results`, `deep_research` and `compare_sources` accept `resultSetId`
  instead of `results`, so the list is not sent back. If both are given, `results`
  wins with a warning; an unknown or expired ID returns `result_set_not_found`.
- Lazy next-page fetch for `search_web`: paging past the stored set requests the next
  SearXNG page (at most one extra request per call, at most 5 SearXNG pages per set,
  only URLs not already stored are added).
- Search tools accept `format` (`json` default, or `compact`: one `title | url | snippet`
  line per result) and `detail` (`titles`, `snippets` default, or `full`; only with
  `format=compact`) to save tokens.
- Every `tools/call` result carries `structuredContent` (the full typed response) next
  to one text block. Tools now declare a `title` and `annotations`, and the search
  and synthesis tools declare an `outputSchema` that `structuredContent` conforms to.
- Search quality corpus (`scripts/quality/corpus/search_quality.json`) and
  `scripts/quality/measure_corpus.py` (`validate`, `run`, `compare`) to compare
  bytes, estimated tokens, latency and top-result quality before and after a change;
  results are in `docs/quality/search-result-quality-measurement.md`.
- `search_web` returns the SearXNG `answers`, `infoboxes` (`{title, content, url}`),
  `suggestions` and `corrections` of the first page as optional response fields
  (omitted when empty, sanitized to single lines of at most 500 characters,
  cached with the search, declared in the shared `outputSchema` and printed as
  labeled lines with `format=compact`).
- SearXNG-backed searches called with `timeRange` that return no results add the
  warning `timeRange: SearXNG skips engines without date filtering; retry without
  timeRange for undated results`; there is no automatic retry.

### Changed

- `search_youtube` searches YouTube directly with yt-dlp
  (`--flat-playlist --dump-single-json ytsearchN:<query>`, the way
  music.thiscloud.com.ar does) and adds channel (`author`), duration, view count
  and description to the `snippet`, plus `publishedAt` when yt-dlp reports one.
  It reuses the transcript service's yt-dlp binary, timeout and block cooldown.
  SearXNG (`youtube,brave`) stays as the fallback when yt-dlp is missing, fails,
  times out or finds nothing, with a warning naming the reason. A call with
  `timeRange` goes straight to SearXNG (`ytsearch` has no date filter), and
  `YOUTUBE_TRANSCRIPTS=off` keeps the old SearXNG-only behavior.
- `search_academic` requests the SearXNG engines `arxiv`, `crossref`, `openalex`,
  `pubmed`, `semantic scholar` and `openairepublications` with no category (it
  was `categories=science` plus `arxiv,duckduckgo`), and accepts openalex.org,
  europepmc.org, ncbi.nlm.nih.gov, openaire.eu and crossref.org result URLs.
- The server now registers 30 MCP tools (was 28).
- The default fetch User-Agent is now a desktop Chrome string
  (`fetch.DefaultUserAgent`) instead of `SourceRudder/3.0.0`, because many sites
  reject self-identified bot agents. An explicit `FETCH_USER_AGENT` is used unchanged.
- The systemd and Kubernetes deployment templates no longer pin `FETCH_USER_AGENT`,
  so the browser default applies; `scripts/release-readiness.sh` now fails if a
  template sets it. Operators can still set `FETCH_USER_AGENT` explicitly.
- Tool descriptions no longer mislead callers: `search_news` does not claim a
  `timeRange=week` filter, `deep_research`, `summarize_results`, and
  `compare_sources` state that they only synthesize the `results` passed in, and
  `maxResults` states its real default and cap.
- GitHub (repositories, pull requests, issues) and StackOverflow searches that
  succeed with zero results retry once with a shortened query: qualifiers such as
  `repo:` are kept, English and Spanish stopwords are dropped, and the first four
  terms remain. The response keeps the original `query` and adds the warning
  `query relaxed: <shortened query>`. Errors (429, 4xx, 5xx, transport) are never
  retried. Empty or partial GitHub and StackOverflow responses are cached for at
  most 30 seconds.
- `search_web` returns up to 20 results when `maxResults` is omitted (was 10;
  explicit values are honored, capped at 100), removes duplicate URLs, and
  requests SearXNG page 2 once when page 1 has fewer results than requested. A
  failed page 2 keeps the page-1 results with a warning.
- SearXNG-backed searches cache zero-result responses for at most 30 seconds,
  like degraded ones, so a transient empty answer is not served for the full TTL.
- Search tools no longer truncate on the caller's behalf: `maxResults` is the page
  size, and connectors keep every unique result they fetched. `academic` and
  `youtube` now fill up to `maxResults` after filtering. Result order is unchanged.
- The default text of a result is minified JSON (the same value as before, without
  indentation), about 13% smaller on a 20-result `search_web` response.
- **Breaking:** tool execution, argument and business errors (for example
  `invalid_cursor: ...`, `result_set_not_found: ...`, unknown `format`, upstream
  failures) now return a normal result with `isError: true` and an actionable text
  message instead of JSON-RPC error `-32603`. JSON-RPC errors remain only for unknown
  tools and malformed requests.
- **Breaking:** `offset` and `resultSetId` on search tools are retired in favor of
  `cursor` and `nextCursor`; `offset` and `hasMore` are not returned. The call still
  runs as a new search and the response warns
  `offset and resultSetId are no longer supported; page with cursor=<nextCursor>`.
  `resultSetId` stays valid on the synthesis tools. `SearchRequest.Offset` was removed
  from `pkg/types`.
- Paging a `search_web` set to its end may return one extra empty page, because the
  last SearXNG page is only known to be empty after it is fetched.
- `tools/list` is larger (about 22.9 KB to 43.1 KB) because of `title`, `annotations`
  and `outputSchema`; clients that load every schema up front pay for it once.

### Security

- Upgraded the Go toolchain to 1.27.4 and `github.com/klauspost/compress` to
  v1.20.4, fixing GO-2026-6809 (crypto/x509), GO-2026-6859 (net/http), and
  GO-2026-6865 (klauspost/compress) reported by govulncheck.
- CI, the release gate, and PR policy run on the private Proxmox VM102
  runner (`private-sourcerudder`); pull requests from forks stay on
  GitHub-hosted runners so untrusted code never reaches it. CodeQL (memory)
  and releases (Docker daemon access) stay GitHub-hosted.

### Fixed

- SearXNG-backed web, news, YouTube, images, academic, and Reddit searches now
  cap degraded-response caching at 30 seconds (or the configured TTL if shorter),
  while healthy responses retain the configured TTL. Versioned cache keys include
  request parameters such as language, safe search, time range, and effective limit.
- SearXNG results now retain upstream provenance in an optional `engine` field;
  existing `source`, partial-result warnings, and metrics remain unchanged.
- `initialize` now negotiates the MCP protocol version instead of always
  returning `2024-11-05`: it echoes supported requests (`2025-11-25`,
  `2025-06-18`, `2024-11-05`) and otherwise returns `2025-11-25`. HTTP GET/POST
  requests with an unsupported `MCP-Protocol-Version` header receive `400`.
- HTTP shutdown now ends open `GET /mcp` Server-Sent Events streams immediately
  instead of waiting for the shutdown timeout, while in-flight POST requests
  still finish.
- Stored result sets no longer share mutable slices with callers (a data race when
  concurrent paging with a different query appended warnings into the same array),
  and the `intent:` warning no longer aliases the connector response's warnings.

### Security

- Build with Go 1.26.9 to pick up standard library fixes in `crypto/tls`,
  `net/http`, `net/textproto`, and `os` reported by `govulncheck`
  (GO-2026-6603 to GO-2026-6617).

### Documentation

- Added Pi native HTTP and local stdio configuration to the MCP client guide,
  including tool exposure and connection verification.

## [3.0.0] - 2026-09-12

### Breaking changes

### Removed

- Retired the optional IA_Recuerdo observation forwarder, including its CLI
  flags, `MEMORY_*` environment variables, deployment settings, and synchronous
  outbound requests. The obsolete integration was removed so any future
  persistence capability can be designed deliberately; no replacement is part
  of this change. The 28 MCP tools and in-process search history are unchanged.

### Fixed

- Wired HTTP request counters and per-source search latency histograms into the
  production MCP request path so `/metrics` reflects real traffic.
- Bound the distributable systemd unit to loopback by default so dual-stack
  hosts do not expose the unauthenticated health endpoint on every interface.

### Distribution and documentation

- Added the complete SourceRudder visual identity and indigo campaign asset set.
- Made release automation derive artifact, image, and deployment versions from
  the reviewed `Makefile` version while release readiness rejects mismatched
  tags and missing release notes.

## [2.0.0] - 2026-09-11

### Breaking identity changes

- Renamed the product, command, service, Compose project, Kubernetes resources,
  Go module, repository target, MCP guide URI, environment prefix, User-Agent,
  and Prometheus metric prefix to SourceRudder identities.
- Replaced `IA_BUSCAR_AUTH_KEY` and `IA_BUSCAR_PORT` with
  `SOURCERUDDER_AUTH_KEY` and `SOURCERUDDER_PORT`.
- See [Migration to 2.0](MIGRATION-TO-2.0.md) for upgrade and rollback steps.

### Preserved contracts

- Kept MCP tool names, JSON fields, connector names, and strategy identifiers
  stable across the major-version migration.
- Kept historical IA_Buscar releases under the rights already granted by MIT.

### Operations and supply chain

- Added digest-pinned external container images, bounded Compose/systemd
  resources, loopback development ports, non-root runtime execution, and
  deployment contract checks.
- Added a gated release workflow for cross-platform archives, checksums,
  provenance attestations, multi-platform GHCR images, SBOM, and immutable
  deployment assets.
- Confirmed MIT as the active SourceRudder 2.0.0 license and made release
  readiness verify the exact approved license before publication.

### Documentation

- Added canonical English documentation with Spanish companion files for
  setup, migration, security, development, configuration, operations,
  deployment, architecture, MCP clients, quality, acknowledgements, and
  rollback.
