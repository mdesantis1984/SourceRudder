# SourceRudder

[English](README.md) | [Español](README.es.md)

SourceRudder es un servidor MCP en Go para investigación, recuperación,
validación, síntesis y citación. **2.0.0 es una pre-release y todavía no fue
publicada.** Soporta `stdio` y HTTP autenticado, conectores especializados,
SearXNG y entrega opcional de memoria a IA_Recuerdo.

Repositorio: <https://github.com/mdesantis1984/SourceRudder> · Módulo:
`github.com/mdesantis1984/SourceRudder`

## Inicio rápido local

Requisitos: Go 1.26.6+, Docker y Docker Compose v2.

```bash
git clone https://github.com/mdesantis1984/SourceRudder.git
cd SourceRudder
cp .env.example .env
# Configure valores independientes para SOURCERUDDER_AUTH_KEY y SEARXNG_SECRET en .env.
docker compose up --build -d
curl -fsS http://127.0.0.1:8080/healthz
docker compose logs -f sourcerudder
```

Compose ejecuta el servicio `sourcerudder` y publica el puerto HTTP en
`127.0.0.1:${SOURCERUDDER_PORT:-8080}`. Para detenerlo: `docker compose down`.

### Compilar y probar

```bash
go build -o bin/sourcerudder ./cmd/sourcerudder
go test ./...
go test -race ./...
go vet ./...
git diff --check
```

### Ejecutar localmente

```bash
./bin/sourcerudder -transport stdio -searxng-url http://localhost:8888

SOURCERUDDER_AUTH_KEY='<local-secret>' \
SEARXNG_URL='http://localhost:8888' \
./bin/sourcerudder -transport http -http-addr :8080
```

| Endpoint | Acceso | Propósito |
|---|---|---|
| `GET /healthz` | Público | Respuesta mínima de liveness. |
| `POST /mcp` | Bearer token | Frontera JSON-RPC de MCP. |
| `GET /metrics` | Bearer token | Métricas Prometheus. |

## Contrato MCP

El recurso MCP `agent-guide://sourcerudder/wire-contract` está disponible con
`resources/list` y `resources/read`. Las respuestas de búsqueda mantienen
arrays JSON para `results`, `sourcesUsed`, `warnings` y `errors`; `results`
nunca es `null`.

| Grupo | Nombres de herramientas |
|---|---|
| Búsqueda | `search_web`, `search_news`, `search_doc_oficial`, `search_local_index`, `search_github`, `search_github_pr`, `search_github_issue`, `search_stackoverflow`, `search_npm`, `search_nuget`, `search_pypi`, `search_docker_hub`, `search_academic`, `search_reddit`, `search_youtube`, `search_images` |
| Recuperación y validación | `fetch_url`, `fetch_and_extract`, `extract_structured`, `validate_url`, `check_link_status` |
| Síntesis | `summarize_results`, `deep_research`, `compare_sources` |
| Utilidades con estado | `get_cached`, `invalidate_cache`, `get_search_history`, `get_current_date` |

Los conectores incluyen SearXNG, GitHub, Stack Overflow, npm, NuGet, PyPI,
Docker Hub y el registro de documentación oficial. Se preservan los strategy IDs
relevantes: `searxng`, `searxng_reddit_index`,
`official_doc_registry_search`, `official_doc_web_fallback`,
`local_index_lexical` y `local_index_unavailable`.

`search_local_index` utiliza un único corpus JSON configurado, validado y de
solo lectura. No descubre archivos, no sigue symlinks, no hace crawling, no
realiza requests HTTP y no usa fallback a `search_web`. Sin `LOCAL_INDEX_PATH`,
devuelve `strategy="local_index_unavailable"` con `results=[]`.

## Configuración y seguridad

| Variable | Propósito |
|---|---|
| `SOURCERUDDER_AUTH_KEY` | Secreto requerido para autenticar MCP HTTP y métricas. |
| `SOURCERUDDER_PORT` | Puerto del host para Compose; el default es `8080`. |
| `SEARXNG_URL` | Endpoint de SearXNG; default `http://localhost:8888`. |
| `LOCAL_INDEX_PATH` | Corpus local de solo lectura opcional. |
| `MEMORY_URL`, `MEMORY_APIKEY` | Integración opcional con IA_Recuerdo; sin I/O si `MEMORY_URL` está vacío. |
| `FETCH_USER_AGENT`, `FETCH_TIMEOUT_MS`, `FETCH_MAX_REDIRECTS`, `FETCH_MAX_ATTEMPTS` | Controles del fetcher. |

Mantenga los secretos fuera de Git y de los flags de línea de comandos. No
exponga HTTP directamente a Internet: use una red privada o un reverse proxy
TLS con controles de requests y acceso. El fetcher valida redirects y destinos
DNS para reducir el riesgo de SSRF. Consulte [SECURITY.es.md](SECURITY.es.md).

## Entorno QA local

El stack QA aislado es distinto del stack Compose de inicio rápido. Sus pruebas
shell requieren `QA_AUTH_KEY` (o `AUTH_KEY`, o un `.env.qa` exclusivo para
desarrollo) y lo inyectan al contenedor como `SOURCERUDDER_AUTH_KEY`. Nunca use
credenciales de producción en `.env.qa`.

```bash
export QA_AUTH_KEY="$(openssl rand -hex 32)"
make qa-build
make qa-up
make qa-smoke
make qa-down
```

Úselo solo en una máquina de desarrollo protegida. El servicio QA publica el
puerto `8080` únicamente en la interfaz loopback.

## Índice de documentación

- [Migración a 2.0](MIGRATION-TO-2.0.es.md) · [Guía de migración en inglés](MIGRATION-TO-2.0.md)
- [Historial de cambios](CHANGELOG.es.md)
- [Arquitectura](docs/architecture.es.md) y [despliegue](docs/deployment.es.md)
- [Configuración](docs/configuration.es.md) y [operaciones](docs/operations.es.md)
- [Clientes MCP](docs/mcp-clients.es.md) y [desarrollo](docs/development.es.md)
- [Contribuir](CONTRIBUTING.es.md), [seguridad](SECURITY.es.md) y [agradecimientos](ACKNOWLEDGEMENTS.es.md)
- [Gate de revisión de licencia](docs/legal/README.es.md)
- [Checklist de cutover](docs/release/cutover.es.md) y [procedimiento de reversión](docs/release/rollback.es.md)
- [Ejemplo de índice local](configs/local-index.example.json)

## Licencia

La [LICENSE](LICENSE) raíz sigue siendo MIT mientras se espera la revisión
profesional de la licencia SourceRudder personalizada propuesta. No se reclama
aprobación legal. Las releases previas de IA_Buscar conservan sus derechos MIT.
