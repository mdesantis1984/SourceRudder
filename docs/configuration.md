# Configuration

[English](configuration.md) | [Español](configuration.es.md)

SourceRudder 3.0.0 runs as `sourcerudder`. Configure it with flags, environment variables, and deployment manifests. Explicit non-empty flags take precedence over environment values, then built-in defaults.

## Quick path

```bash
export SOURCERUDDER_AUTH_KEY='replace-with-a-secret'
sourcerudder -transport http -http-addr :8080
```

Keep secrets out of command arguments in deployed environments. `SOURCERUDDER_AUTH_KEY` is preferred to `-auth-key` because process listings and shell history can expose flags.

## Runtime settings

| Flag | Default | Purpose |
|---|---|---|
| `-transport` | `stdio` | `stdio` or `http`; another value stops startup. |
| `-http-addr` | `:8080` | HTTP listen address. |
| `-searxng-url` | `SEARXNG_URL` or `http://localhost:8888` | Local SearXNG endpoint. |
| `-cache-ttl` | `300` | In-process cache TTL in seconds. |
| `-fetch-timeout-ms` | `FETCH_TIMEOUT_MS` or `30000` | Fetch timeout in milliseconds. |
| `-auth-key` | empty | Overrides `SOURCERUDDER_AUTH_KEY`. |
| `-local-index-path` | empty | Overrides `LOCAL_INDEX_PATH`. |
| `-result-set-ttl` | `RESULT_SET_TTL` or `900` | Seconds a stored search result set stays pageable by `cursor`. |
| `-result-set-max-sets` | `RESULT_SET_MAX_SETS` or `64` | Maximum stored result sets; the oldest are evicted first. |
| `-result-set-max-results` | `RESULT_SET_MAX_RESULTS` or `2000` | Maximum results held across all stored sets; the oldest sets are evicted first. |

## Environment and Compose

| Variable | Default | Effect |
|---|---|---|
| `SOURCERUDDER_AUTH_KEY` | empty | Credential for HTTP `/mcp` and `/metrics`. An empty key fails closed: every protected request is rejected. |
| `SOURCERUDDER_PORT` | `8080` in Compose | Loopback host port published by Compose. It does not replace `-http-addr`. |
| `SEARXNG_URL` | `http://localhost:8888` | SearXNG backend URL. |
| `FETCH_USER_AGENT` | Desktop Chrome User-Agent (`fetch.DefaultUserAgent`) | Fetch User-Agent. Many sites reject self-identified bot agents; an explicit value is used unchanged. |
| `FETCH_TIMEOUT_MS` | `30000` | Fetch timeout; `-fetch-timeout-ms` wins when positive. |
| `FETCH_MAX_REDIRECTS` | `5` | Maximum validated redirects. |
| `FETCH_MAX_ATTEMPTS` | `3` | Maximum fetch attempts. |
| `LOCAL_INDEX_PATH` | empty | Enables `search_local_index`. |
| `RESULT_SET_TTL` | `900` | Result-set lifetime in seconds; `-result-set-ttl` wins when set. Non-positive values fall back to the default. |
| `RESULT_SET_MAX_SETS` | `64` | Maximum stored result sets; `-result-set-max-sets` wins when set. |
| `RESULT_SET_MAX_RESULTS` | `2000` | Maximum stored results in total; `-result-set-max-results` wins when set. |
| `YOUTUBE_TRANSCRIPTS` | enabled | `off` disables `fetch_youtube_transcript`, the YouTube delegation in `fetch_url` / `fetch_and_extract` and the direct yt-dlp search of `search_youtube` (SearXNG only). |
| `YTDLP_PATH` | `yt-dlp` | yt-dlp executable. The systemd unit sets `/opt/yt-dlp/bin/yt-dlp`. |
| `YTDLP_JS_RUNTIMES` | empty | JavaScript runtime passed to yt-dlp as `RUNTIME[:PATH]`. The systemd unit sets `node:/opt/nodejs/bin/node`. |
| `YOUTUBE_POT_BASE_URL` | empty (plugin default `http://127.0.0.1:4416`) | bgutil PO token server URL. |
| `YOUTUBE_FETCH_POT` | `always` | yt-dlp `youtube:fetch_pot` policy (`always`, `auto`, `never`). `always` also sends a PO token with the player request; with `auto`, a flagged IP gets "Sign in to confirm you're not a bot" before yt-dlp asks bgutil for a token. |
| `YOUTUBE_DEFAULT_LANGUAGE` | _(empty)_ | Language code (e.g. `es`) used as the requested transcript language when a call omits it, notably `fetch_url` and `fetch_and_extract`, which have no language argument. An explicit `language` always wins; an invalid value is ignored. Set it because YouTube's declared spoken language can differ from the title's language for auto-dubbed videos (e.g. a Spanish title reported as `ar`). |
| `YOUTUBE_CACHE_TTL` | `168h` | Transcript cache TTL per video and language. |
| `YOUTUBE_BLOCK_COOLDOWN` | `15m` | Global pause on YouTube requests after a 429 or bot check. |
| `YTDLP_TIMEOUT` | `2m` | Timeout of each yt-dlp invocation. |
| `YOUTUBE_WORK_DIR` | empty (`TMPDIR`) | Scratch directory for yt-dlp output. |

Use a local, ignored `.env` file with restrictive permissions.

## Result sets and paging

Every search tool keeps the full list of results it obtained in an in-process store and returns one page with `resultSetId`, `total` and, only while results remain, `nextCursor`. `maxResults` is the page size chosen by the caller, not a cap on what is kept. Send `cursor` (the `nextCursor` of a previous response, plus the `query` and any `maxResults` you want) to read the next page without any upstream request; a cursor only works with the tool that issued it (another tool returns `invalid_cursor: cursor was issued by <tool>`), a malformed one returns `invalid_cursor: <reason>`, `offset` and `resultSetId` are retired on search tools (the call still runs and the response warns `offset and resultSetId are no longer supported; page with cursor=<nextCursor>`), and an unknown or expired set returns `result_set_not_found`. The store is per process and not durable: a restart or an eviction invalidates IDs. Memory use is bounded by `-result-set-max-results` (2,000 results by default), so size it for the replica.

For `search_web`, paging past the end of the stored set can fetch more: when `cursor` points at the end of a set whose SearXNG source may have more results, the server requests the next SearXNG page (`pageno`), appends only URLs not already stored, and returns them. Each such call makes at most one extra upstream request, never at search time, and a set reads at most 5 SearXNG pages in total (search-time pages included). `nextCursor` is omitted once the 5th page is read, when a page adds no new URLs, or when the result budget is full (a warning says so). The query, `language`, `timeRange` and `safeSearch` of the original search are reused. If the extra page fails, the response is empty with a warning and the same cursor can be retried. Other search tools never fetch lazily: their `nextCursor` is omitted at the end of the stored set.

Search results are filtered before they are stored or returned, on the first page and on pages fetched lazily: results whose URL is not an `http(s)` URL with a host (cloaked engine links such as `http:///go/v1/...`) are always removed, except in `search_local_index`, which keeps its `local-index://` URLs. When `safeSearch` is on, results from adult domains are removed too (a list of well-known domains and subdomains, plus host labels such as `porn`, `xxx`, `nsfw`, `hentai` or `sex` as a whole word; titles and snippets are never inspected). `safeSearch` defaults to `true` (moderate, SearXNG `safesearch=1`) when omitted; `safeSearch: false` turns it off and disables the adult filter. Each removal adds a response warning with the count (`removed N results without a valid URL host (cloaked engine links, searxng#6967)`, `removed N results from adult domains (safe search)`).

A call needs `query` or `cursor`: with only a `cursor` it reads the next page, and with neither it returns `invalid_arguments: provide query or cursor`. A cursor page keeps the page size of the first page unless the call sends `maxResults` (capped at 100).

`summarize_results`, `deep_research` and `compare_sources` accept `resultSetId` (the value a search tool returned) instead of `results`, so the list is not sent back. The stored set is read locally, with no network request. If both are given, `results` wins and the response carries the warning `resultSetId ignored: results were provided in the same call; results win`. An unknown or expired ID returns an error result: `result_set_not_found: <id> (expired or unknown; run the search again)`.

### Result format

Every `tools/call` result carries `structuredContent` (the full typed response, always a JSON object) and exactly one text block. The search tools accept two optional arguments for that text: `format` is `json` (default; the same value as `structuredContent`, minified) or `compact` (one line per result, `title | url | snippet`, with `resultSetId`, `total` and `nextCursor` in a header line), and `detail` (only with `format=compact`) is `titles`, `snippets` (default) or `full`. `structuredContent` is the full response in both formats. Some clients show the model only the text and cut text over about 20 KB, so for large pages use `format=compact` with `detail=titles`. An unknown `format` or `detail` value is rejected with an argument error that lists the allowed values.

Tool metadata and errors follow MCP 2025-11-25. `tools/list` gives every tool a short `title` and `annotations` (`readOnlyHint`, `destructiveHint`, `idempotentHint`, `openWorldHint`; clients treat them as hints, not guarantees). Search and fetch tools are read-only and open-world; the synthesis tools (`summarize_results`, `deep_research`, `compare_sources`), `search_local_index` and the local state tools (`get_cached`, `invalidate_cache`, `get_search_history`, `get_current_date`) are closed-world. The 17 search tools share one `outputSchema` (the search response: `query`, `results[]` with required `title`, `url` and `source` plus `snippet`, `total`, and the optional `resultSetId`, `nextCursor`, `partial`, `warnings`, `errors`, `answers`, `infoboxes`, `suggestions`, `corrections`; it is deliberately slim to save tokens in `tools/list`, and `structuredContent` keeps its other fields) and the three synthesis tools share another; every `structuredContent` they return conforms to it, so clients can type it (Pi codemode scripts can filter it before anything reaches the model). The other tools declare no `outputSchema`.

### SearXNG extras and `timeRange`

`search_web` also returns what SearXNG sends next to the results on its first page: `answers` (strings, for example a hash or a definition), `infoboxes` (`[{title, content, url}]`, a knowledge panel such as a Wikipedia summary), `suggestions` (related queries) and `corrections` (spelling fixes). Each field is omitted when SearXNG sent nothing. Every string is one trimmed line (CR, LF, U+2028 and U+2029 become spaces, whitespace is collapsed, at most 500 characters), and empty or duplicate entries are dropped. They are part of the search, so they are cached with it and repeat on every page read with `cursor`. With `format=compact` they are one labeled line each (`answer:`, `infobox: title | content | url`, `suggestions:`, `corrections:`) after the results and before the warnings, in every `detail`.

When a SearXNG-backed search (`search_web`, `search_news`, `search_images`, `search_academic`, `search_youtube`, `search_reddit`) is called with `timeRange` and returns no results, the response carries the warning `timeRange: SearXNG skips engines without date filtering; retry without timeRange for undated results`. SearXNG silently drops every engine that cannot filter by date, so an empty answer can mean "no dated results", not "no results". The server does not retry on its own. The warning is not added when the call failed outright; it does not mark the response `partial`.

`search_youtube` searches YouTube directly with the same yt-dlp the transcripts use (`yt-dlp --flat-playlist --dump-single-json ytsearchN:<query>`, one argv element after `--`, no shell; N is `maxResults`, at most 50; at most 30 s per search, `YTDLP_TIMEOUT` if shorter; stdout capped at 1 MiB). Results carry `engine`/`source` `yt-dlp`, `author` (channel) and a `snippet` of channel, duration, view count and description, plus `publishedAt` when yt-dlp reports a date (flat search results usually do not). Channels, playlists, live and upcoming entries and entries without id or title are skipped. SearXNG (`engines=youtube,brave`) is the fallback when yt-dlp is missing, fails, times out, is blocked by YouTube (429 or bot check; the transcript cooldown applies) or returns nothing: the response then carries the warning `yt-dlp search failed, used SearXNG fallback: <reason>` or `yt-dlp search returned no results, used SearXNG fallback`, is not `partial`, and is cached for 30 s only. `ytsearch` has no date filter, so a call with `timeRange` skips yt-dlp and goes straight to SearXNG, with the `timeRange` warning above when it returns nothing. With `YOUTUBE_TRANSCRIPTS=off` the tool is SearXNG-only, as before.

`search_academic` asks SearXNG for the engines `arxiv`, `crossref`, `openalex`, `pubmed`, `semantic scholar` and `openairepublications` (no category), the six that answered on the production instance; engines that are blocked or time out there are not requested.

A failed tool call is a normal result, `{"content":[{"type":"text","text":"<message>"}],"isError":true}` with no `structuredContent`, so the model can read the message and retry. This covers argument validation (unknown `format` or `detail`, `invalid_cursor: <reason>`, wrong argument types), business errors (`result_set_not_found: <id> ...`) and upstream or handler failures. JSON-RPC errors remain only for protocol failures: an unknown tool (`-32602`), malformed JSON or malformed `tools/call` params, and an unsupported protocol version.

### News: Google News RSS and `search_tech_news`

`search_news` asks SearXNG first. When SearXNG returns fewer results than `maxResults`, or answers only partly, the server adds Google News RSS search results after them (`https://news.google.com/rss/search?q=<query>&hl=..&gl=..&ceid=..`). `language` starting with `es` uses `es-419` / `AR`; anything else uses `en-US` / `US`. `timeRange` becomes `when:1d`, `when:7d`, `when:30d` or `when:1y` (`when:1m` returns nothing on Google News) in the query. Results are deduplicated by URL and by normalized title, and their scores keep decreasing after the SearXNG ones. If the RSS request fails, the SearXNG results are kept and the response carries the warning `google news rss supplement failed: <reason>`. The result `url` of Google News items is a `news.google.com` redirect link (the publisher URL is not in the feed); `source` is the publisher.

`search_tech_news` covers technology and software engineering news with no API key. It reads three kinds of source in parallel:

- HN Algolia (`https://hn.algolia.com/api/v1/search`, stories only). The result `url` is the story link, or the HN item page when the story has none; the `snippet` carries points and comments and the HN discussion link.
- Google News RSS search, as above.
- 19 curated RSS/Atom feeds (InfoQ, Martin Fowler, The Pragmatic Engineer, ByteByteGo, Netflix TechBlog, Cloudflare, GitHub Engineering, Stack Overflow Blog, AWS Architecture, Google Developers, The New Stack, Go Blog, Ars Technica, TechCrunch, The Verge, Wired, The Register, Xataka, Genbeta), kept in one table in `internal/connectors/technews.go`.

At most 6 feeds are fetched at once, each with an 8 s timeout and 12 s for the whole search; each parsed feed is cached for 30 minutes. Curated items are filtered by the query: accent- and case-insensitive words in title and summary, all significant words required; if nothing matches all of them, items matching any word are returned with a warning. Results are ranked by match strength; inside the same strength the sources take turns (each keeps its own order: HN its relevance order, the others newest first), so no single source fills the page. HN requests always carry a date floor: the `timeRange` window, or the last 365 days without `timeRange`, because HN ranks by relevance and points and its top hits would otherwise be years old. `timeRange` filters by publication date and drops undated items. Failed sources produce one summarized warning, for example `tech news: 2 of 21 sources failed: hn algolia, cloudflare blog`; the response is `partial` only when both HN and Google News fail. Each result has `engine` and `source` set to the source name, `type` `news`, `publishedAt` and, when the feed gives one, `author`. Feed content is untrusted: HTML is stripped, whitespace collapsed and every field capped, and a feed body is limited to 2 MiB.

## HTTP authentication

`/healthz` is public. `/mcp` and `/metrics` require either header:

```http
X-Api-Key: <key>
```

```http
Authorization: Bearer <key>
```

`X-Api-Key` takes precedence when both are sent. Missing, empty, or invalid credentials return `401`; HTTP is intentionally fail-closed. `stdio` does not use HTTP authentication.

## Search backends and local index

SearXNG serves `search_web`, `search_news`, `search_academic`, `search_reddit`, `search_images`, `search_youtube` (as the fallback of its direct yt-dlp search), and the web fallback for `search_doc_oficial`. Reddit uses the local SearXNG index filtered to public `reddit.com` content; it does not require direct Reddit credentials.

The local index is optional. When disabled, `search_local_index` returns `strategy="local_index_unavailable"` without web fallback. When enabled, it reads a strict, read-only JSON corpus and returns `strategy="local_index_lexical"`:

```json
{
  "version": 1,
  "documents": [{
    "id": "getting-started",
    "title": "Getting started",
    "url": "https://example.com/docs/getting-started",
    "snippet": "Install and configure the service.",
    "author": "Documentation team",
    "tags": ["setup", "configuration"]
  }]
}
```

Use a regular, non-symlink file that is not group- or world-writable. The loader accepts at most 4 MiB and 10,000 documents; invalid corpus data stops startup. See [`configs/local-index.example.json`](../configs/local-index.example.json).

## State

Cache entries have a default 300-second TTL. SearXNG-backed, GitHub, and StackOverflow searches cap the TTL of degraded (`partial: true`, including unresponsive engines) and empty (zero-result) responses at 30 seconds, without extending a shorter configured TTL; healthy non-empty responses keep the configured TTL. `search_web` returns up to 20 results when `maxResults` is omitted (explicit values are honored, capped at 100), removes duplicate URLs, and requests SearXNG page 2 once when page 1 has fewer results than requested. Their versioned keys include request parameters, including language, safe search, time range, and effective result limit, and cannot reuse legacy keys. Search history retains the newest 100 searches. Both are in-process only, are not shared by replicas, and disappear on restart. Do not treat either as durable audit storage.
