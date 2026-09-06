# IA_Buscar

[![Go](https://img.shields.io/badge/Go-1.23+-00ADD8?logo=go)](https://go.dev/)
[![MCP](https://img.shields.io/badge/MCP-Compatible-FF6B6B?logo=robot)](https://modelcontextprotocol.io/)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

Servicio MCP de búsqueda, extracción, síntesis y citación para agentes IA locales. Escucha en `:8080` por defecto (configurable vía `-http-addr`).

---

## Resumen

- 13 conectores de búsqueda: web, GitHub, StackOverflow, npm, NuGet, PyPI, DockerHub, Academic, Reddit, YouTube, Images.
- Herramientas de extracción y síntesis de contenido.
- Caché en proceso con TTL limitado (sin persistencia en disco).
- Protección SSRF en fetch/extract.
- 28 tools MCP registradas (16 search, 3 fetch, 2 validate, 3 synthesis, 3 cache/history, 1 date).
- Recurso estático `agent-guide://ia-buscar/wire-contract` para discoverability de agentes IA.

---

## Características principales

| Característica | Descripción |
|---|---|
| Conectores múltiples | 13 conectores para diferentes fuentes de información |
| Búsqueda web | SearxNG para búsqueda web descentralizada |
| APIs especializadas | GitHub, StackOverflow, npm, NuGet, PyPI, DockerHub, Semantic Scholar |
| Fetch/Extract | Extracción de contenido con protección SSRF |
| Síntesis | summarize_results, deep_research, compare_sources |
| Caché en proceso | TTL en memoria, desaparece al reiniciar el proceso |

---

## Conectores (13 disponibles)

| Conector | Fuente | Descripción |
|---|---|---|
| `search_web` | SearxNG (brave) | Búsqueda web general descentralizada |
| `search_github` | GitHub API | Repositorios, archivos y commits |
| `search_github_pr` | GitHub API | Pull requests |
| `search_github_issue` | GitHub API | Issues |
| `search_stackoverflow` | StackOverflow API | Q&A técnica |
| `search_npm` | npm Registry | Paquetes Node/TypeScript |
| `search_nuget` | NuGet Gallery | Paquetes .NET |
| `search_pypi` | PyPI | Paquetes Python |
| `search_docker_hub` | Docker Hub | Imágenes Docker |
| `search_academic` | SearxNG (arxiv) | Papers y referencias académicas |
| `search_reddit` | SearXNG indexed web | Public Reddit posts discovered through SearXNG |
| `search_youtube` | SearxNG (youtube,brave) | Tutoriales y demos |
| `search_images` | SearxNG (bing images) | Diagramas y material visual |
| `search_news` | SearxNG (qwant news) | Noticias y actualidad |

---

## Reddit indexed-web capability (1.3.0 release candidate)

`search_reddit` searches public Reddit posts already indexed by the configured
SearXNG service. It adds a `site:reddit.com` restriction, accepts public post
URLs, deduplicates canonical posts, and returns the stable
`strategy: "searxng_reddit_index"` value.

The MCP `SearchResponse` schema and tool input schema are unchanged. SearXNG
provider failures are surfaced as `partial: true` with warnings; an empty 200
response is a valid empty result. Indexed coverage, provider availability, and
freshness are not guaranteed.

`--reddit-user-agent`, `--reddit-base-url`, `REDDIT_USER_AGENT`, and
`REDDIT_BASE_URL` remain accepted as deprecated compatibility no-ops;
`search_reddit` does not call Reddit's API directly. This 1.3.0 candidate has
local Docker QA evidence only: it is not deployed or published.

---

## Acceso

| Endpoint | URL | Descripción |
|---|---|---|
| MCP HTTP | `http://<HOST>:8080/mcp` | Protocolo MCP para agentes IA |
| Health | `http://<HOST>:8080/healthz` | Verificación de estado del servicio |

---

## Requisitos

1. Servicio `ia-buscar` ejecutándose y escuchando en `:8080` (o el puerto configurado vía `-http-addr`).
2. SearxNG disponible en `<HOST>:8080` para búsqueda web.
3. Acceso a APIs externas: GitHub, StackOverflow, npm, NuGet, PyPI, DockerHub, Semantic Scholar, Reddit, YouTube.

---

## Uso rápido (MCP)

```json
{
  "method": "tools/call",
  "params": {
    "name": "search_web",
    "arguments": {
      "query": "búsqueda de ejemplo",
      "maxResults": 10
    }
  }
}
```

---

## Configuración (ejemplo)

```jsonc
{
  "mcp_server": {
    "host": "<HOST>",
    "port": 8080,
    "transport": "http"
  },
  "searxng": {
    "url": "http://<HOST>:8080"
  },
  "cache": {
    "ttl_seconds": 300
  },
  "reddit": {
    "discovery": "public Reddit posts indexed by the configured SearXNG service"
  }
}
```

The deprecated compatibility options `--reddit-user-agent` and
`--reddit-base-url`, and their `REDDIT_USER_AGENT` and `REDDIT_BASE_URL`
environment variables, are accepted but have no effect. No Reddit OAuth,
credentials, or direct Reddit API request is used.

---

## Arquitectura

```
CT-BUSCAR (Go Service :8080)
  │
  ├─ MCP Handler
  │   └─ 28 Tools registradas
  │
  ├─ Conectores
  │   ├─ search_web ──> SearxNG :8080
  │   ├─ search_github ──> GitHub API
  │   ├─ search_stackoverflow ──> StackOverflow API
  │   ├─ search_npm ──> npm Registry
  │   ├─ search_nuget ──> NuGet Gallery
  │   ├─ search_pypi ──> PyPI
  │   ├─ search_docker_hub ──> Docker Hub
  │   ├─ search_academic ──> SearxNG (arxiv)
  │   ├─ search_reddit ──> SearXNG indexed public Reddit posts
  │   ├─ search_youtube ──> SearxNG (youtube,brave)
  │   ├─ search_images ──> SearxNG (bing images)
  │   └─ search_news ──> SearxNG (qwant news)
  │
  ├─ Fetch/Extract
  │   ├─ fetch_url
  │   ├─ fetch_and_extract
  │   └─ extract_structured
  │
  ├─ Síntesis
  │   ├─ summarize_results
  │   ├─ deep_research
  │   └─ compare_sources
  │
  └─ Cache Layer (in-process only)
      └─ TTL bounded, ephemeral, no MCP surface
```

El servicio es **stateless**: la única caché es en proceso y desaparece al
apagar el binario. No hay memoria externa, no hay historial de búsquedas
expuesto. El estado perceptible para el cliente es exactamente la respuesta
de la request actual.

---

## Changelog

### 1.3.1 — unreleased bugfix candidate
- **Image result selection**: `search_images` validates image URLs, preserves
  bounded upstream context, forwards SearXNG search options, and ranks
  candidates using conservative whole-word lexical hints before applying `maxResults`;
  this does not infer semantic relevance. Partial upstream
  warnings remain visible; the MCP schema is unchanged. No tag, deployment,
  publication, or release was created.

### 1.3.0 — 2026-09-05 (release candidate; not deployed)
- **Reddit indexed web**: `search_reddit` now discovers public Reddit posts
  through the configured SearXNG service, filters to canonical public post
  URLs, and returns `strategy="searxng_reddit_index"`. The MCP search schema
  is unchanged. SearXNG failures return `partial=true` with warnings; a 200
  empty result remains healthy. This does not guarantee complete Reddit
  coverage, provider availability, or index freshness.
- **Compatibility**: `--reddit-user-agent`, `--reddit-base-url`,
  `REDDIT_USER_AGENT`, and `REDDIT_BASE_URL` are deprecated no-ops retained
  for CLI compatibility. Reddit's API is not called directly.
- **Release state**: local Docker QA evidence was obtained for this candidate;
  no deployment, tag, publication, or release was created.

### 1.5.0 — 2026-08-21 (`restore-runtime-contract`)
- **Contrato restaurado**: 28 tools MCP registradas (25 → 28). Se
  re-introdujeron los 3 tools stateful `get_cached`,
  `invalidate_cache` y `get_search_history` respaldados por un
  `HistoryService` bounded en proceso y por la superficie
  `DeleteIfPresent` (race-safe) del cache.
- **IA_Recuerdo re-introducido**: paquete `internal/memory` con
  `Client` que short-circuitea `Save` a `nil` cuando `baseURL==""`
  (preserva la propiedad "no I/O cuando la integración está
  deshabilitada"). Flags `-memory-url` / `-memory-apikey` con
  defaults de `MEMORY_URL` / `MEMORY_APIKEY`, precedence
  flag-sobrescribe-env. `mcp.NewServer` extendido a 13 args con el
  mismo `*memory.Client` y `*cache.HistoryService` propagados a los
  handlers.
- **Deploy re-sincronizado**: `deploy/systemd/ia-buscar.service`
  expone `Environment=MEMORY_URL` y `Environment=MEMORY_APIKEY`;
  `deploy/kubernetes/deployment.yaml` expone `MEMORY_URL` en el
  container `ia-buscar`. Sin cambios al proceso de deploy.
- **Override documentado**: autorizado por decisión `#4280` con
  `size:exception` (precedente `#4125`). Override del baseline
  no-regression de 12 objetivos de
  `close-fetch-resilience-and-release-gates`. La fase 16 de ese
  change sigue `pending` hasta que este PR merge y `local-docker-qa`
  pase.
- **Sin deploy de producción**: este cambio es puramente de código
  + manifests. No se ejecutó `kubectl apply`, `systemctl
  daemon-reload`, ni push de contenedor.

### 1.2.0 — 2026-08-20 (exception delivery)
- **Reddit anonymous-only**: se removieron todos los campos OAuth
  (`ClientID`, `ClientSecret`, `HasOAuthCredentials`, bearer-token
  storage) y los flags/env (`--reddit-client-id`, `--reddit-client-secret`,
  `REDDIT_CLIENT_ID`, `REDDIT_CLIENT_SECRET`). El conector hit el
  endpoint público anónimo de Reddit; cuando Reddit lo rechaza con
  401/403 la respuesta es `strategy="reddit_unconfigured"` con un
  warning que nombra `REDDIT_USER_AGENT` como única remediación.
  Métrica: `ia_buscar_search_degraded_total{source="reddit",kind="anonymous_blocked"}`.
- **Fetch engine explícito**: `internal/fetch/fetcher.go` ahora expone
  `Config{UserAgent, TimeoutMs, MaxRedirects, MaxAttempts, BaseBackoff}`
  y `NewFetcherServiceWithConfig(Config)`. `NewFetcherService(int)` se
  conserva para legacy callers. La respuesta de fetch lleva cuatro
  campos nuevos (`Outcome`, `Status`, `RedirectChain`, `Attempts`) con
  la taxonomía: `success`, `blocked-target`, `blocked-redirect`,
  `too-many-redirects`, `timeout`, `transport-error`, `http-error`,
  `non-transient-failure`, `transient-failure-retried-exhausted`. SSRF:
  pre-resuelve A/AAAA en cada hop (target + cada redirect), rechaza
  si alguna dirección cae en rango no público, diala la IP aprobada
  preservando Host/TLS. Retry policy: SOLO 429/502-504/timeout con
  backoff exponencial y jitter determinístico.
- **Cache key con `TimeRange`**: `cache.GenerateCacheKey` ahora recibe
  `(query, sources, timeRange)`. Conectores que pasan `req.TimeRange`:
  academic, images, news, youtube, web. Reddit, GitHub, DockerHub,
  npm, NuGet, PyPI, StackOverflow pasan timeRange vacío.
- **`recordDegraded(source, kind, resp, err)`**: helper central que
  incrementa la métrica, setea `Partial=true` y agrega el warning.
  Cableado en dockerhub, github, npm, nuget, stackoverflow. Los
  conectores searxng-based (academic, images, news, youtube, web)
  conservan su patrón inline sobre `UnresponsiveEngines`.
- **Slashes eliminados**: 10 conectores ya no duermen artificialmente
  antes del HTTP request. Latencia del path del cuerpo baja de
  ~500ms–1s a <50ms cuando la respuesta upstream es inmediata.
- **Release gate ejecutable**: `scripts/release-gate.sh` corre en cada
  PR vía `.github/workflows/release-gate.yml`. Verifica worktree
  limpio (allow-list: docs/release/reviews/, .atl/, .codegraph/),
  review placeholder, diff vs merge-base, y `go build/vet/test/test -race`.
  Carve-out `RELEASE_GATE_SIZE_EXCEPTION=<exact-branch>` matchea SOLO
  la rama nombrada; default OFF para cualquier otra.
- **MCP server version 1.2.0**: bumpeada en `HandleInitialize` y
  `handleMCPInitialize`. Changelog row agregada en
  `internal/mcp/resources.go` sección 12.
- **`--fetch-timeout-ms`** ahora llega al `FetcherService` en lugar
  del literal `30000` (Phase 6.4).

### 1.4.0 — 2026-08-19
- **Recurso MCP `agent-guide://ia-buscar/wire-contract`**: nuevo
  recurso estático accesible vía `resources/list` y `resources/read`
  a través de la frontera JSON-RPC `/mcp`. Entrega una guía en español
  que describe cuándo usar cada familia de tools, los campos estables
  de `SearchResponse` (`results`, `strategy`, `partial`, `warnings`,
  `errors`, `cached`), cómo distinguir healthy empty de degradado y
  unconfigured, las limitaciones de los tools especializados, y los
  shapes de fetch/validación/síntesis/tiempo. La capacidad `resources`
  se anuncia explícitamente en `initialize`.
- **Schemas de tools honestos**: los `inputSchema` ahora exponen
  enums estables para `timeRange` (`""`, `day`, `week`, `month`,
  `year`) y `mode` (`auto`, `article`, `documentation`, `raw`); el
  schema de fetch/extract ya no anuncia `timeoutMs` (el server lo
  decide en boot) y el schema de síntesis ya no anuncia `style` /
  `goal` (los handlers los descartan). Se agregó un sub-schema
  `SearchResultItem` para que el array `results` de las tools de
  síntesis sea componible.
- **Descripciones de tools más precisas**: cada tool menciona el
  backend que usa o la estrategia que devuelve cuando no hay
  proveedor real (`reddit_unconfigured`, `local_index_unavailable`,
  `official_doc_web_fallback`).

### 1.3.0 — 2026-08-19
- **Contrato estable del wire MCP**: todo `SearchResponse` se serializa
  con `results: []` siempre (nunca `null`, nunca omitido), incluso cuando
  la respuesta viene de caché con campos previos. Hay un helper
  `normalizeSearchResponse` en `internal/mcp` que garantiza el contrato en
  el camino de respuesta, y se removió `omitempty` del campo `Results`
  en `pkg/types/types.go`. Se agregó el campo `strategy` para que los
  agentes distingan qué backend realmente respondió.
- **Reddit anonymous-only**: el conector ya no usa un `User-Agent`
  hard-coded; admite `--reddit-user-agent` / `REDDIT_USER_AGENT`. Esta
  entrega es **anonymous-only**: no hay OAuth, ni client_id, ni
  client_secret, ni bearer-token. Cuando Reddit rechaza un pedido
  anónimo con 401/403, la respuesta devuelve
  `strategy: "reddit_unconfigured"` con un warning accionable que
  menciona `REDDIT_USER_AGENT`. Se conservan los manejos seguros de 429
  y transporte. La métrica
  `ia_buscar_search_degraded_total{source="reddit",kind="anonymous_blocked"}`
  tickea para dashboards.
- **Tools especializados honestos**:
  - `search_doc_oficial` no pretende tener un proveedor curado de
    documentación. Cuando lo invocan, devuelve `strategy:
    "official_doc_web_fallback"` y un warning que explica que la
    respuesta vino del web connector (SearxNG). El AI puede distinguir
    ese caso de una búsqueda especializada real.
  - `search_local_index` ya no redirige silenciosamente al web
    connector. Mientras no haya un proveedor real de índice local
    configurado, devuelve `strategy: "local_index_unavailable"`,
    `results: []`, `warnings` y `errors` con la etiqueta
    `local_index_unavailable`. La descripción de la tool en
    `tools/list` también lo anuncia.
- **Observabilidad de SearxNG degradado**: nuevo contador Prometheus
  `ia_buscar_search_degraded_total{source, kind}`. Las conectores de
  SearxNG (`web`, `news`, `youtube`, `images`, `academic`) lo
  incrementan cuando reciben `unresponsive_engines` no vacío. Reddit
  también lo incrementa con `kind` en
  `{unconfigured, rate_limited, transport, upstream_http_4xx, upstream_http_5xx}`.
  Es solo observabilidad: el servicio no intenta reparar motores
  externos.
- **Tests de contrato**: nuevas pruebas atraviesan la frontera MCP real
  (con `httptest` real, no call-count) para `results: []` estable en
  camino fresco y de caché (incluyendo payloads de caché que no tienen
  el campo `results`), `search_doc_oficial` con `strategy`, `search_local_index`
  con `local_index_unavailable` y sin hit al upstream, la degradación
  configurada/no-configurada de Reddit, y la emisión del contador de
  degradación desde cada conector.

### 1.2.0 — 2026-08-19
- **SUPERSEDED by `restore-runtime-contract`**: la entrega original que
  removía los tools `get_cached` / `invalidate_cache` /
  `get_search_history` y la integración con IA_Recuerdo (CT 110) ha sido
  revertida. La línea base stateless fue aprobada por `#4007` y
  preservada por la fase 12 de `close-fetch-resilience-and-release-gates`,
  pero el contrato original (28 tools + memoria externa opcional) era el
  requerido por `local-docker-qa` y la decisión `#4280` autorizó la
  restauración con `size:exception`. El binario actualmente expone las
  28 tools y los flags `-memory-url` / `-memory-apikey` están
  re-introducidos en systemd y Kubernetes.
- 28 tools MCP registradas (16 search + 3 fetch + 2 validate + 3
  synthesis + 3 cache/history + 1 date).

### 1.1.0 — 2026-05-02
- **SearxNG Migration**: Images, News, YouTube, Academic ahora usan SearxNG en LXC 201 (10.0.0.201:8080).
- Conectores migrados: search_images, search_news, search_youtube, search_academic.
- Se eliminaron APIs deprecated (Invidious, Semantic Scholar).
- Proceso duplicado identificado y resuelto (usuario 100997).
- Deploy: `/opt/ia-buscar/bin/ia-buscar` en LXC 15.

### 1.0.0 — 2026-04-30
- Servicio MCP de búsqueda inicial con 13 conectores.
- 25 tools MCP registradas (la entrega inicial pre-stateless contaba con
  menos tools; el contrato creció a 28 con la restauración del runtime
  contract — ver `restore-runtime-contract`).
- conectores: search_web, search_github, search_github_pr, search_github_issue, search_stackoverflow, search_npm, search_nuget, search_pypi, search_docker_hub, search_academic, search_reddit, search_youtube, search_images.
- Tools adicionales: fetch_url, fetch_and_extract, extract_structured, validate_url, check_link_status, summarize_results, deep_research, compare_sources, get_current_date.
- Protección SSRF en operaciones de fetch.

---

## Configuración por variables de entorno

Variables de entorno que el binario honra en tiempo de ejecución. El
deploy canónico (k8s en `deploy/kubernetes/deployment.yaml` y systemd
en `deploy/systemd/ia-buscar.service`) las setea por valores seguros
por defecto.

| Variable | Default | Notas |
|----------|---------|-------|
| `FETCH_USER_AGENT` | `ia-buscar/1.2 (anonymous-only)` | UA del fetch engine. Necesario para SearxNG y Reddit. |
| `FETCH_TIMEOUT_MS` | `30000` | Timeout del ciclo completo. El flag `--fetch-timeout-ms` gana cuando está presente. |
| `FETCH_MAX_REDIRECTS` | `5` | Cap de hops del redirect loop manual. |
| `FETCH_MAX_ATTEMPTS` | `3` | Intentos totales (incluye reintentos). |
| `REDDIT_USER_AGENT` | `ia-buscar/1.2 (anonymous-only)` | UA dedicado a Reddit; requerido por su contrato anonymous. |
| `REDDIT_BASE_URL` | `https://www.reddit.com` | Endpoint JSON público. |
| `AUTH_KEY` | (vacío) | Si no se setea, el middleware rechaza toda request (no bypass). |

---

## Seguridad

- Protección SSRF en fetch/extract de URLs (validación DNS A/AAAA + dial pinneado al IP aprobado).
- Validación de URLs antes de realizar solicitudes.
- Middleware de autenticación con SHA256. Sin clave configurada, el servicio rechaza toda request con 401.
- Sin telemetría ni envío de datos a terceros fuera de las APIs especificadas.
- Sin persistencia local ni sincronización con servicios de memoria externa.

---

## Degradación del upstream

Los conectores de búsqueda (`search_web`, `search_news`, `search_youtube`,
`search_images`, `search_academic`, `search_reddit`) pueden distinguir, en sus
respuestas MCP, entre un resultado vacío real y una degradación del upstream
(SearxNG caído, motores no respondedores, o Reddit con `429`/timeout).

Contrato del cliente (`SearchResponse`):

- `partial`: es `true` cuando un conector de búsqueda falló contra su upstream
  o recibió `unresponsive_engines` no vacíos con `results` vacíos. En
  respuestas `200 OK` limpias sigue siendo `false`.
- `warnings`: lista legible con detalle del fallo. Cada entrada nombra el
  motor (SearxNG) o el código HTTP/condición (Reddit), por ejemplo
  `"searxng: 2 unresponsive engines [youtube, brave]: timeout"` o
  `"reddit API error: 429"`.
- Un arreglo `warnings` no vacío indica degradación del upstream **aunque**
  `errors` esté vacío, ya que el fallo se reporta como metadato de la
  respuesta y no como excepción MCP.

Implicancia práctica: un cliente puede mostrar un banner "búsqueda parcial —
revisa warnings" cuando `partial == true` o `len(warnings) > 0`, sin
interpretar un resultado vacío como error del agente.

---

## Contrato del wire para agentes IA

Todo tool de búsqueda MCP expone el mismo tipo `SearchResponse` con un
contrato estable pensado para que un agente IA pueda razonar sobre la
respuesta sin volver a inspeccionar el HTTP crudo:

| Campo | Tipo | Notas para el agente |
|---|---|---|
| `query` | string | El query que envió el agente. |
| `results` | array | **Siempre presente**. `[]` cuando no hay resultados. Nunca `null`, nunca omitido — incluso si el conector upstream devolvió un resultado vacío real o si el payload vino de una caché previa al contrato. |
| `summary` | string | opcional |
| `keyFindings` | array | opcional |
| `sourcesUsed` | array | opcional pero siempre `[]` cuando no hay. |
| `strategy` | string | opcional. Cuando está presente, nombra el backend concreto que respondió. Valores actuales: `reddit`, `reddit_unconfigured`, `official_doc_web_fallback`, `local_index_unavailable`. |
| `confidence` | number | opcional |
| `cached` | bool | `true` cuando la respuesta vino del cache en proceso (TTL limitado). |
| `partial` | bool | `true` cuando el upstream real devolvió error o motores no respondedores con resultados vacíos. |
| `warnings` | array | opcional pero siempre `[]` cuando no hay. Errores / degradaciones del upstream se reportan acá, no como excepción MCP. |
| `errors` | array | opcional pero siempre `[]` cuando no hay. Mensajes de error de mayor jerarquía que un warning. |

### Cómo leer el campo `strategy`

- Si `strategy == "official_doc_web_fallback"`: el tool
  `search_doc_oficial` no tiene un proveedor especializado de
  documentación configurado y cayó al web connector (SearxNG). El
  warning incluido en la respuesta nombra explícitamente el fallback.
  El agente no debe presentar los resultados como "documentación
  oficial curada".
- Si `strategy == "local_index_unavailable"`: el tool
  `search_local_index` no encontró un proveedor de índice local
  configurado. La respuesta tiene `results: []` y warnings/errors con la
  etiqueta `local_index_unavailable`. **El agente NO debe redirigir a
  `search_web` por su cuenta sin pedirlo**; este tool no se conecta al
  web connector.
- Si `strategy == "reddit_unconfigured"`: `search_reddit` fue llamado
  pero la política de Reddit bloqueó el pedido anónimo con 401/403.
  Esta entrega es anonymous-only, así que el warning solo nombra
  `REDDIT_USER_AGENT` como remediación (no hay OAuth que configurar).
  El agente debe presentar la respuesta como "Reddit no disponible"
  y NO como un resultado vacío real.
- Si `strategy == "reddit"` con `partial == true`: Reddit se intentó
  pero rechazó (429, 5xx, decode error). Es degradación del upstream,
  no un problema de configuración.
- Si `strategy` está vacío o es el nombre del conector: el backend
  respondió normalmente.

### Degradación del upstream (métricas)

Los conectores incrementan el contador Prometheus
`ia_buscar_search_degraded_total{source, kind}`. La etiqueta `source`
es el nombre del conector (`web`, `news`, `youtube`, `images`,
`academic`, `reddit`). La etiqueta `kind` nombra la condición:

| `kind` | Cuándo se incrementa |
|---|---|
| `unresponsive_engines` | SearxNG devolvió `unresponsive_engines` no vacío con `results` vacío. |
| `unconfigured` | Reddit anónimo fue rechazado con 401/403 y no hay OAuth. |
| `rate_limited` | Reddit devolvió 429. |
| `transport` | Fallo de transporte en Reddit (DNS, TCP, timeout). |
| `upstream_http_4xx` | Reddit devolvió 4xx con OAuth configurado. |
| `upstream_http_5xx` | Reddit devolvió 5xx. |

El servicio no intenta reparar motores externos. Este contador existe
solo para que operadores detecten cuándo el upstream está mal.

---

## Recursos MCP para discoverability

Además de las 28 tools, el server anuncia un recurso MCP estático pensado
para que agentes IA descubran el contrato por sí mismos sin tener que
memorizarlo ni hacer scraping del README.

| Recurso | URI | MIME | Contenido |
|---|---|---|---|
| `ia-buscar-wire-contract` | `agent-guide://ia-buscar/wire-contract` | `text/markdown` | Guía en español: familias de tools, cuándo invocar cada una, campos estables de `SearchResponse`, cómo distinguir healthy empty de degradado y unconfigured, shapes de fetch/validación/síntesis/tiempo. |

El recurso se sirve a través de la misma frontera JSON-RPC `/mcp`
(producción y tests). Un cliente MCP puede listarlo con `resources/list`
y leerlo con `resources/read`. La capacidad `resources` se anuncia
explícitamente en la respuesta `initialize`.

Ejemplo:

```json
{
  "method": "resources/read",
  "params": { "uri": "agent-guide://ia-buscar/wire-contract" }
}
```

El URI es estable — no lo renombres sin bump mayor del server y un
changelog explícito.

---

## Licencia

MIT © ThisCloud Services

---

## Local QA environment

Isolated Docker Compose stack for proving boot, MCP wiring, and SearxNG plumbing without ever reaching the production endpoints (`10.0.0.201:8080` SearxNG, `127.0.0.1:7438` IA_Recuerdo). Additive — no production deploy artefact touched.

### Prerequisites

Docker Engine + Compose v2; outbound only for the initial `searxng/searxng:latest` pull.

### Lifecycle

```bash
make qa-build    # build the ia-buscar image
make qa-up       # boot, wait healthy (≤60s), scoped cleanup on failure
make qa-smoke    # probe /healthz, /mcp tools/list, search_web, canary
make qa-down     # stop containers, keep volumes
make qa-clean    # drop volumes and the qa-net bridge
```

### What the smoke verifies

- `GET /healthz` returns `200 {"status":"ok"}` in <1s.
- `POST /mcp` `tools/list` returns the full registry (28 tools), every
  entry non-empty name + description.
- `POST /mcp` `search_web` returns `200`, `results: []`, `cached: false`
  — in-stack SearxNG has empty engines, no egress.
- Every `/mcp` carries `Authorization: Bearer $QA_AUTH_KEY` matching
  the server-side `IA_BUSCAR_AUTH_KEY` env var (NOT `-auth-key`
  argv — see CT201). Wrong/missing header → validator 401 → smoke
  fails.
- Zero packets leave `qa-net` to `10.0.0.201:8080` or
  `127.0.0.1:7438` (`internal: true`; only `127.0.0.1:8080` published).

### Layout

```
deploy/qa/docker-compose.yml       # project ia-buscar-qa, qa-net internal
deploy/qa/searxng/settings.yml     # JSON output, empty engine list
deploy/qa/searxng/limiter.toml     # rate limiter off
scripts/qa-up.sh                   # boot + scoped cleanup
scripts/qa-down.sh                 # stop, preserve volumes
scripts/qa-smoke.sh                # behavior probes (28 tools, auth, canary)
tests/qa/qa_scripts_test.sh        # red→green contract tests
.dockerignore                      # exclude .git, bin, *.db, coverage.*, …
```

### Local auth (`.env.qa`, dev-only)

QA stack enables the live `auth.Validator` (same middleware production
uses). The key is provisioned through the `IA_BUSCAR_AUTH_KEY` env
var inside the container — **never** via the `-auth-key` CLI flag —
so the secret never reaches `argv` (and therefore never reaches
`ps aux`, process listings, shell history, or compose's recorded
command). `.env.qa` at the repo root is **DEV-ONLY** — gitignored,
MUST NEVER carry a production credential:

```
QA_AUTH_KEY=<any-non-empty-dev-string>
```

`qa-up.sh` mirrors the precedence `$AUTH_KEY` → `.env.qa` → `""`
(fail-closed) and exports `QA_AUTH_KEY` into the compose project;
`deploy/qa/docker-compose.yml` then forwards it as
`IA_BUSCAR_AUTH_KEY` on the `ia-buscar` service (env path — see
CT201). `qa-smoke.sh` sends `Authorization: Bearer $QA_AUTH_KEY` on
every `/mcp` POST so the Bearer header matches the server-side
`IA_BUSCAR_AUTH_KEY` env var (NOT `-auth-key` argv).

The `-auth-key` flag is preserved as an explicit override for ad-hoc
local debugging and for backward compatibility with existing scripts,
but the documented path for any operator-managed deployment is the
env var.

### Rollback

The whole scope is additive. To remove it:

```bash
git rm -r deploy/qa scripts/qa-*.sh tests/qa
git checkout -- Makefile README.md .dockerignore
```

No production deploy artefact, binary, or flag references the QA scope.
