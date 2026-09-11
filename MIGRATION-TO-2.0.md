# Migrating to SourceRudder 2.0

[English](MIGRATION-TO-2.0.md) | [Español](MIGRATION-TO-2.0.es.md)

SourceRudder 2.0.0 is the first public SourceRudder release. Use this guide to
upgrade an IA_Buscar 1.x deployment safely.

## Breaking identities

| IA_Buscar 1.x | SourceRudder 2.0 |
|---|---|
| Repository `https://github.com/mdesantis1984/IA_Buscar` | `https://github.com/mdesantis1984/SourceRudder` |
| Module `github.com/mdesantis1984/IA_Buscar` | `github.com/mdesantis1984/SourceRudder` |
| Binary/service `ia-buscar` | `sourcerudder` / `sourcerudder.service` |
| Compose service `ia-buscar` | `sourcerudder` |
| Kubernetes workload/secret `ia-buscar` | `sourcerudder` |
| `IA_BUSCAR_AUTH_KEY` | `SOURCERUDDER_AUTH_KEY` |
| `IA_BUSCAR_PORT` | `SOURCERUDDER_PORT` |
| `agent-guide://ia-buscar/wire-contract` | `agent-guide://sourcerudder/wire-contract` |
| Prometheus metrics `ia_buscar_*` | `sourcerudder_*` |
| Image name | `ghcr.io/mdesantis1984/sourcerudder:2.0.0@sha256:<digest>` |

Update client launch commands, Compose overrides, systemd unit references,
Kubernetes names, secret keys, monitoring labels, and the MCP resource URI.
Replace `<digest>` with the immutable image digest published in the 2.0.0
release metadata.

## Unchanged wire contracts

MCP tool names, tool JSON, connector behavior, and strategy IDs remain stable.
This includes SearXNG, MCP, Go, GitHub, and optional IA_Recuerdo integration.
`results` remains a JSON array, never `null`.

The stable tool surface is: `search_web`, `search_news`, `search_doc_oficial`,
`search_local_index`, `search_github`, `search_github_pr`,
`search_github_issue`, `search_stackoverflow`, `search_npm`, `search_nuget`,
`search_pypi`, `search_docker_hub`, `search_academic`, `search_reddit`,
`search_youtube`, `search_images`, `fetch_url`, `fetch_and_extract`,
`extract_structured`, `validate_url`, `check_link_status`, `summarize_results`,
`deep_research`, `compare_sources`, `get_cached`, `invalidate_cache`,
`get_search_history`, and `get_current_date`.

Stable strategy IDs include `searxng`, `searxng_reddit_index`,
`official_doc_registry_search`, `official_doc_web_fallback`,
`local_index_lexical`, and `local_index_unavailable`.

## Upgrade sequence

1. Inventory every identity in the table, including client MCP configuration.
2. Create a new `SOURCERUDDER_AUTH_KEY`; store it in the target secret manager.
3. Update manifests for `sourcerudder`, `sourcerudder.service`, and the
   `sourcerudder` Kubernetes workload; set `SOURCERUDDER_PORT` where Compose
   needs a non-default host port.
4. Deploy the digest-pinned 2.0.0 image after it is published.
5. Verify `/healthz`, then authenticate an MCP `initialize` and `tools/list`.
6. Read `agent-guide://sourcerudder/wire-contract` and smoke-test each
   connector class your deployment uses.
7. Retire 1.x only after monitoring and client traffic confirm the new service.

## Rollback boundary

Rollback is safe only before clients, automation, and deployed secrets are
irreversibly switched to SourceRudder identities. Keep the 1.x image, its
`IA_BUSCAR_AUTH_KEY`, and its manifests intact until the new service is proven.
After clients hardcode the new resource URI or depend on SourceRudder service
names, rollback requires reverting those configurations too. MCP tool payloads
and strategy IDs do not themselves require a wire-format rollback.

## License

SourceRudder 2.0.0 and prior IA_Buscar releases are distributed under the
[MIT License](LICENSE). Recipients may continue using every MIT-licensed copy
under those terms; any future license change can apply only prospectively.

See the [README](README.md) for local operation and QA guidance.
