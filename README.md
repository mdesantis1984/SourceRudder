# SourceRudder

[English](README.md) | [Español](README.es.md)

SourceRudder is a Go MCP server for research, retrieval, validation, synthesis,
and citation. **2.0.0 is a pre-release and has not been published yet.** It
supports `stdio` and authenticated HTTP, specialized connectors, SearXNG, and
optional IA_Recuerdo memory delivery.

Repository: <https://github.com/mdesantis1984/SourceRudder> · Module:
`github.com/mdesantis1984/SourceRudder`

## Local quickstart

Requirements: Go 1.26.6+, Docker, and Docker Compose v2.

```bash
git clone https://github.com/mdesantis1984/SourceRudder.git
cd SourceRudder
cp .env.example .env
# Set independent SOURCERUDDER_AUTH_KEY and SEARXNG_SECRET values in .env.
docker compose up --build -d
curl -fsS http://127.0.0.1:8080/healthz
docker compose logs -f sourcerudder
```

Compose runs the `sourcerudder` service and binds the HTTP port to
`127.0.0.1:${SOURCERUDDER_PORT:-8080}`. Stop it with `docker compose down`.

### Build and test

```bash
go build -o bin/sourcerudder ./cmd/sourcerudder
go test ./...
go test -race ./...
go vet ./...
git diff --check
```

### Run locally

```bash
./bin/sourcerudder -transport stdio -searxng-url http://localhost:8888

SOURCERUDDER_AUTH_KEY='<local-secret>' \
SEARXNG_URL='http://localhost:8888' \
./bin/sourcerudder -transport http -http-addr :8080
```

| Endpoint | Access | Purpose |
|---|---|---|
| `GET /healthz` | Public | Minimal liveness response. |
| `POST /mcp` | Bearer token | MCP JSON-RPC boundary. |
| `GET /metrics` | Bearer token | Prometheus metrics. |

## MCP contract

The MCP resource `agent-guide://sourcerudder/wire-contract` is available through
`resources/list` and `resources/read`. Search responses keep JSON arrays for
`results`, `sourcesUsed`, `warnings`, and `errors`; `results` is never `null`.

| Group | Tool names |
|---|---|
| Search | `search_web`, `search_news`, `search_doc_oficial`, `search_local_index`, `search_github`, `search_github_pr`, `search_github_issue`, `search_stackoverflow`, `search_npm`, `search_nuget`, `search_pypi`, `search_docker_hub`, `search_academic`, `search_reddit`, `search_youtube`, `search_images` |
| Fetch and validate | `fetch_url`, `fetch_and_extract`, `extract_structured`, `validate_url`, `check_link_status` |
| Synthesis | `summarize_results`, `deep_research`, `compare_sources` |
| Stateful utilities | `get_cached`, `invalidate_cache`, `get_search_history`, `get_current_date` |

Connectors include SearXNG, GitHub, Stack Overflow, npm, NuGet, PyPI, Docker
Hub, and the official-documentation registry. Relevant strategy IDs remain:
`searxng`, `searxng_reddit_index`, `official_doc_registry_search`,
`official_doc_web_fallback`, `local_index_lexical`, and
`local_index_unavailable`.

`search_local_index` uses one configured, validated, read-only JSON corpus. It
does not discover files, follow symlinks, crawl, make HTTP requests, or fall
back to `search_web`. Without `LOCAL_INDEX_PATH`, it returns
`strategy="local_index_unavailable"` with `results=[]`.

## Configuration and security

| Variable | Purpose |
|---|---|
| `SOURCERUDDER_AUTH_KEY` | Required secret for HTTP MCP and metrics authentication. |
| `SOURCERUDDER_PORT` | Compose host port; defaults to `8080`. |
| `SEARXNG_URL` | SearXNG endpoint; defaults to `http://localhost:8888`. |
| `LOCAL_INDEX_PATH` | Optional read-only local-index corpus. |
| `MEMORY_URL`, `MEMORY_APIKEY` | Optional IA_Recuerdo integration; no memory I/O occurs without `MEMORY_URL`. |
| `FETCH_USER_AGENT`, `FETCH_TIMEOUT_MS`, `FETCH_MAX_REDIRECTS`, `FETCH_MAX_ATTEMPTS` | Fetcher controls. |

Keep secrets out of Git and command-line flags. Do not expose HTTP directly to
the Internet; use a private network or a TLS reverse proxy with request and
access controls. The fetcher validates redirect hops and DNS targets to reduce
SSRF risk. See [SECURITY.md](SECURITY.md).

## Local QA environment

The isolated QA stack is separate from the quickstart Compose stack. Its shell
tests require `QA_AUTH_KEY` (or `AUTH_KEY`, or a dev-only `.env.qa`) and inject
it into the container as `SOURCERUDDER_AUTH_KEY`. Never put production
credentials in `.env.qa`.

```bash
export QA_AUTH_KEY="$(openssl rand -hex 32)"
make qa-build
make qa-up
make qa-smoke
make qa-down
```

Use it only on a protected development machine. The QA service publishes port
`8080` on the loopback interface only.

## Documentation index

- [Migration to 2.0](MIGRATION-TO-2.0.md) · [Spanish migration guide](MIGRATION-TO-2.0.es.md)
- [Changelog](CHANGELOG.md)
- [Architecture](docs/architecture.md) and [deployment](docs/deployment.md)
- [Configuration](docs/configuration.md) and [operations](docs/operations.md)
- [MCP clients](docs/mcp-clients.md) and [development](docs/development.md)
- [Contributing](CONTRIBUTING.md), [security](SECURITY.md), and [acknowledgements](ACKNOWLEDGEMENTS.md)
- [License review gate](docs/legal/README.md)
- [Cutover checklist](docs/release/cutover.md) and [rollback runbook](docs/release/rollback.md)
- [Local-index example](configs/local-index.example.json)

## License

The root [LICENSE](LICENSE) remains MIT pending professional review of the
proposed custom SourceRudder license. No legal approval is claimed. Prior
IA_Buscar releases retain their MIT rights.
