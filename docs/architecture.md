[English](architecture.md) | [Español](architecture.es.md)

# SourceRudder 2.0 architecture

SourceRudder 2.0 is **unreleased**. It is an MCP research service that keeps its external wire contracts stable while routing requests to bounded, observable research capabilities.

## Request path

1. An MCP client connects through `stdio` or authenticated HTTP/SSE transport.
2. The server validates the stable JSON-RPC/MCP contract and sends the request to the planner.
3. The planner selects a connector through the connector manager; connector results may use the in-process cache.
4. Results are normalized to stable result envelopes, recorded in bounded in-process history, and optionally passed to synthesis.
5. The search handler stores the connector's full result list in the result-set store and returns the requested page (`maxResults`, chosen by the caller) plus `resultSetId`, `total` and, only while results remain, an opaque `nextCursor`. A call that carries `cursor` skips steps 2-4 and reads the stored set locally. Every `tools/call` result is built by one helper: `structuredContent` holds the full typed response (a JSON object) and `content` holds one text block, minified JSON by default or compact text with `format=compact`.

## Components

| Component | Responsibility |
|---|---|
| MCP transports | `stdio` for local clients; HTTP/SSE for network clients. HTTP exposes health and metrics separately from authenticated MCP calls. |
| Planner and connector manager | Choose the requested or inferred research path, run connectors, and preserve source, strategy, warnings, and partial-result information. |
| Connectors | Reach SearXNG and direct package, code-hosting, community, and registry sources without changing the caller-facing result shape. |
| Result-set store | `internal/resultset` keeps the full result list of each search in memory under an opaque ID (`rs_` + 16 base32 characters from `crypto/rand`) so callers can page it with `cursor` without a new upstream request. It has its own lock, a 15-minute TTL, at most 64 sets and 2,000 results in total, and evicts the oldest sets first. Per process, not durable, not shared across replicas. |
| Cache and history | Maintain per-process, in-memory cache entries and a bounded newest-first history. Neither is durable or shared across replicas. |
| Fetch and extraction | Retrieve public URLs only after SSRF controls validate destinations, redirects, and response limits; extraction returns bounded content. |
| Synthesis | Summarize or compare normalized result sets rather than replacing source evidence. |
| Local index | Optionally searches an immutable, operator-curated JSON corpus with deterministic lexical ranking and no network or filesystem crawl. |
| Metrics | Exposes Prometheus-compatible process and service metrics for operational observation. |

## Contract and safety boundaries

- MCP tools use stable wire envelopes; clients must inspect `strategy`, `partial`, `warnings`, and `errors` instead of treating an empty result as a transport failure.
- SearXNG-backed results expose an optional `engine` field containing the upstream engine name when provided. This adds provenance without changing existing `source` values; absent engine names are omitted.
- Cache and history are in-process operational aids, not a distributed audit system. SearXNG response caching uses versioned request-aware keys and caps degraded and empty response lifetimes at 30 seconds or the configured TTL, whichever is shorter.
- Connectors do not truncate on the caller's behalf: they keep every unique result they already fetched and the handler applies the page size. Result order is never changed by paging. A search makes the same upstream requests as before; `search_web` keeps its single page-2 fill, evaluated against `maxResults`.
- Fetching is deliberately separate from search. SSRF protections apply before network access and across redirects.
- Local-index input is validated at startup and remains operator-owned data.

## Operational implication

Health proves that the SourceRudder process is reachable. It does not prove that SearXNG, direct providers, or a local corpus are available. Monitor transport health, degraded-result signals, and provider-specific evidence independently.
