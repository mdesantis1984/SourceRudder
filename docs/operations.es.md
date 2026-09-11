# Operaciones

[English](operations.md) | [Español](operations.es.md)

Opere SourceRudder como tres capas separadas: proceso MCP, SearXNG local y proveedores externos. Un proceso sano no demuestra que cada proveedor esté sano.

## Verificación diaria

```bash
curl --fail http://127.0.0.1:8080/healthz
curl --fail --silent \
  -H "Authorization: Bearer ${SOURCERUDDER_AUTH_KEY}" \
  http://127.0.0.1:8080/metrics
```

Luego inicialice un cliente MCP, ejecute `tools/list` (deben ser 28 herramientas) y pruebe una búsqueda SearXNG, una de proveedor directo como `search_stackoverflow` y `search_local_index` si es obligatoria. Revise `partial`, `warnings`, `errors` y `strategy` antes de declarar un incidente por resultados vacíos.

## Superficie HTTP

| Ruta | Autenticación | Uso |
|---|---:|---|
| `GET /healthz` | No | Liveness/readiness del proceso. |
| `POST /mcp` | Sí | JSON-RPC MCP. |
| `GET /mcp` | Sí | Stream SSE con keepalive cada 15 segundos. |
| `GET /metrics` | Sí | Métricas Prometheus. |

`POST /mcp` admite hasta 1 MiB. El apagado HTTP tiene un plazo de 10 segundos tras `SIGINT` o `SIGTERM`. Las rutas protegidas operan fail-closed si `SOURCERUDDER_AUTH_KEY` falta o es incorrecta.

## Métricas y alertas

Prometheus también expone collectors estándar de Go y proceso. Las métricas de SourceRudder son:

| Métrica | Labels | Significado |
|---|---|---|
| `sourcerudder_http_requests_total` | `method`, `path`, `status` | Conteo de requests HTTP. |
| `sourcerudder_search_latency_seconds` | `source` | Histograma de latencia de búsqueda. |
| `sourcerudder_search_degraded_total` | `source`, `kind` | Degradación upstream expuesta en la respuesta. |

Configure alertas por probes no disponibles, aumentos sostenidos de búsquedas degradadas, latencia/errores del proxy, reinicios repetidos y errores de carga del índice local. Defina umbrales con su tráfico; el repositorio no establece SLOs universales.

## Logs

```bash
docker compose logs --tail=200 -f sourcerudder searxng
journalctl -u sourcerudder --since '30 minutes ago' -f
kubectl logs deployment/sourcerudder -n sourcerudder --tail=200 -f
```

Los logs pueden contener consultas y URLs. Trátelos como datos operativos sensibles, con controles apropiados de acceso, retención y redacción.

## Actualización y rollback

```bash
git fetch --all --prune
git checkout '<sha-revisado>'
docker compose build --pull sourcerudder
docker compose up -d
curl --fail http://127.0.0.1:8080/healthz
```

Tras actualizar, verifique `initialize`, `tools/list` y búsquedas representativas. Para rollback, vuelva al SHA conocido, reconstruya `sourcerudder`, inicie Compose y repita los chequeos. En Kubernetes restaure la referencia inmutable anterior; en systemd restaure el binario, `BINARY_SHA256`, la unidad renderizada, `VERSION` e `IMAGE` del mismo bundle de release.

## Triage de incidentes

1. Conserve logs e identidad de despliegue (SHA, imagen, configuración y hora UTC).
2. Rote `SOURCERUDDER_AUTH_KEY` y cualquier credencial expuesta.
3. Separe fallas del proceso MCP, SearXNG y proveedores.
4. Revise `strategy`, `partial`, `warnings` y `errors`; la degradación de Reddit llega desde el índice público local de SearXNG.
5. Vuelva a una revisión verificada solo cuando reduzca riesgo.

La caché y el historial son por proceso y no durables. La entrega `MEMORY_*` es opcional y best effort; no es una auditoría. Respalde referencias de despliegue, proxy/firewall, secretos gestionados, corpus/checksum del índice local y configuración SearXNG personalizada; no la caché en memoria.

Ver [Configuración](configuration.es.md) y [Seguridad](../SECURITY.es.md).
