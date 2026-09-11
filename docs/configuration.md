# Configuration

[English](configuration.md) | [Español](configuration.es.md)

SourceRudder 2.0.0 runs as `sourcerudder`. Configure it with flags, environment variables, and deployment manifests. Explicit non-empty flags take precedence over environment values, then built-in defaults.

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
| `-memory-url` / `-memory-apikey` | empty | Override `MEMORY_URL` / `MEMORY_APIKEY`. |
| `-local-index-path` | empty | Overrides `LOCAL_INDEX_PATH`. |

## Environment and Compose

| Variable | Default | Effect |
|---|---|---|
| `SOURCERUDDER_AUTH_KEY` | empty | Credential for HTTP `/mcp` and `/metrics`. An empty key fails closed: every protected request is rejected. |
| `SOURCERUDDER_PORT` | `8080` in Compose | Loopback host port published by Compose. It does not replace `-http-addr`. |
| `SEARXNG_URL` | `http://localhost:8888` | SearXNG backend URL. |
| `FETCH_USER_AGENT` | `SourceRudder/2.0.0 (anonymous-only)` | Fetch User-Agent. |
| `FETCH_TIMEOUT_MS` | `30000` | Fetch timeout; `-fetch-timeout-ms` wins when positive. |
| `FETCH_MAX_REDIRECTS` | `5` | Maximum validated redirects. |
| `FETCH_MAX_ATTEMPTS` | `3` | Maximum fetch attempts. |
| `MEMORY_URL` | empty | Optional IA_Recuerdo observation endpoint. |
| `MEMORY_APIKEY` | empty | Optional Bearer token for that endpoint. |
| `LOCAL_INDEX_PATH` | empty | Enables `search_local_index`. |

Use a local, ignored `.env` file with restrictive permissions. `MEMORY_*` is best effort: an empty `MEMORY_URL` disables outbound memory requests; a configured endpoint receives `POST` JSON and may add up to its 10-second client timeout without changing an otherwise successful search result.

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

SearXNG serves `search_web`, `search_news`, `search_academic`, `search_reddit`, `search_youtube`, `search_images`, and the web fallback for `search_doc_oficial`. Reddit uses the local SearXNG index filtered to public `reddit.com` content; it does not require direct Reddit credentials.

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

Cache entries have a default 300-second TTL; search history retains the newest 100 searches. Both are in-process only, are not shared by replicas, and disappear on restart. Do not treat either as durable audit storage.
