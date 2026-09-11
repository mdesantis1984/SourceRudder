# Operations

[English](operations.md) | [Español](operations.es.md)

Operate SourceRudder as three separate layers: the MCP process, local SearXNG, and external providers. A healthy process does not prove every provider is healthy.

## Daily verification

```bash
curl --fail http://127.0.0.1:8080/healthz
curl --fail --silent \
  -H "Authorization: Bearer ${SOURCERUDDER_AUTH_KEY}" \
  http://127.0.0.1:8080/metrics
```

Then initialize an MCP client, run `tools/list` (expect 28 tools), and test one SearXNG-backed search, one direct-provider search such as `search_stackoverflow`, and `search_local_index` when it is required. Review `partial`, `warnings`, `errors`, and `strategy` before treating an empty result as an incident.

## HTTP surface

| Route | Authentication | Use |
|---|---:|---|
| `GET /healthz` | No | Process liveness/readiness. |
| `POST /mcp` | Yes | MCP JSON-RPC. |
| `GET /mcp` | Yes | SSE stream with 15-second keepalives. |
| `GET /metrics` | Yes | Prometheus metrics. |

`POST /mcp` accepts at most 1 MiB. HTTP shutdown allows 10 seconds after `SIGINT` or `SIGTERM`. Protected routes fail closed when `SOURCERUDDER_AUTH_KEY` is missing or wrong.

## Metrics and alerts

Prometheus also exposes standard Go and process collectors. SourceRudder metrics are:

| Metric | Labels | Meaning |
|---|---|---|
| `sourcerudder_http_requests_total` | `method`, `path`, `status` | HTTP request count. |
| `sourcerudder_search_latency_seconds` | `source` | Search latency histogram. |
| `sourcerudder_search_degraded_total` | `source`, `kind` | Upstream degradation surfaced by a search response. |

Alert on unavailable probes, sustained increases in degraded searches, proxy-observed latency/errors, repeated restarts, and local-index load failures. Set thresholds from your own traffic; this repository does not define universal SLOs.

## Logs

```bash
docker compose logs --tail=200 -f sourcerudder searxng
journalctl -u sourcerudder --since '30 minutes ago' -f
kubectl logs deployment/sourcerudder -n sourcerudder --tail=200 -f
```

Logs may contain queries and URLs. Treat them as sensitive operational data and apply appropriate access, retention, and redaction controls.

## Update and rollback

```bash
git fetch --all --prune
git checkout '<reviewed-sha>'
docker compose build --pull sourcerudder
docker compose up -d
curl --fail http://127.0.0.1:8080/healthz
```

After an update, verify `initialize`, `tools/list`, and representative searches. To roll back, check out the previous known-good SHA, rebuild `sourcerudder`, start Compose, and repeat those checks. For Kubernetes, restore the prior immutable image reference; for systemd, restore the binary, `BINARY_SHA256`, rendered unit, `VERSION`, and `IMAGE` from the same release bundle.

## Incident triage

1. Preserve relevant logs and deployment identity (SHA, image, configuration, UTC time).
2. Rotate `SOURCERUDDER_AUTH_KEY` and any exposed credentials.
3. Separate MCP-process, SearXNG, and provider failures.
4. Check `strategy`, `partial`, `warnings`, and `errors`; Reddit degradation comes from local SearXNG's public index path.
5. Roll back only to a verified revision when that reduces risk.

The cache and search history are per-process and non-durable. `MEMORY_*` delivery is optional and best effort; it is not an audit trail. Back up deployment references, proxy/firewall configuration, managed secrets, local-index corpus and checksum, and any customized SearXNG configuration—not the in-memory cache.

See [Configuration](configuration.md) and [Security](../SECURITY.md).
