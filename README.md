# IA_Buscar

[![Go](https://img.shields.io/badge/Go-1.23+-00ADD8?logo=go)](https://go.dev/)
[![MCP](https://img.shields.io/badge/MCP-Compatible-FF6B6B?logo=robot)](https://modelcontextprotocol.io/)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

Servicio MCP de búsqueda, extracción, síntesis y citación para agentes IA locales. Usa CT-BUSCAR:5000 como motor de conectores.

---

## Resumen

- 13 conectores de búsqueda: web, GitHub, StackOverflow, npm, NuGet, PyPI, DockerHub, Academic, Reddit, YouTube, Images.
- Herramientas de extracción y síntesis de contenido.
- Caché en proceso con TTL limitado (sin persistencia, sin superficie MCP).
- Protección SSRF en fetch/extract.
- 25 tools MCP registradas.
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
| `search_reddit` | Reddit | Discusiones y experiencias reales |
| `search_youtube` | SearxNG (youtube,brave) | Tutoriales y demos |
| `search_images` | SearxNG (bing images) | Diagramas y material visual |
| `search_news` | SearxNG (qwant news) | Noticias y actualidad |

---

## Acceso

| Endpoint | URL | Descripción |
|---|---|---|
| MCP HTTP | `http://<HOST>:5000/mcp` | Protocolo MCP para agentes IA |
| Health | `http://<HOST>:5000/healthz` | Verificación de estado del servicio |

---

## Requisitos

1. CT-BUSCAR:5000 ejecutándose como servicio Go.
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
    "port": 5000,
    "transport": "http"
  },
  "searxng": {
    "url": "http://<HOST>:8080"
  },
  "cache": {
    "ttl_seconds": 300
  },
  "reddit": {
    "user_agent": "ia-buscar/<deployment> (by /r/ThisCloudServices)",
    "client_id": "<reddit-oauth-app-client-id>",
    "client_secret": "<reddit-oauth-app-client-secret>",
    "base_url": "https://oauth.reddit.com"
  }
}
```

Los flags equivalentes en la línea de comando son
`--reddit-user-agent`, `--reddit-client-id`, `--reddit-client-secret` y
`--reddit-base-url`, con variables de entorno
`REDDIT_USER_AGENT`, `REDDIT_CLIENT_ID`, `REDDIT_CLIENT_SECRET` y
`REDDIT_BASE_URL`. Sin `REDDIT_CLIENT_ID` + `REDDIT_CLIENT_SECRET`, el
conector intenta pedidos anónimos y devuelve
`strategy: "reddit_unconfigured"` cuando Reddit los rechaza con 401/403.

---

## Arquitectura

```
CT-BUSCAR (Go Service :5000)
  │
  ├─ MCP Handler
  │   └─ 25 Tools registradas
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
  │   ├─ search_reddit ──> Reddit API
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
- **Reddit con seam de configuración y degradación explícita**: el
  conector ya no usa un `User-Agent` hard-coded; admite
  `--reddit-user-agent` / `REDDIT_USER_AGENT`,
  `--reddit-client-id` / `REDDIT_CLIENT_ID` y
  `--reddit-client-secret` / `REDDIT_CLIENT_SECRET`. Cuando Reddit
  rechaza un pedido anónimo con 401/403 y no hay credenciales OAuth
  configuradas, la respuesta devuelve `strategy: "reddit_unconfigured"`
  con un warning accionable que menciona cada variable. Si OAuth sí está
  configurado y Reddit igual rechaza, la respuesta se clasifica como
  degradación del upstream (`partial: true`), no como configuración.
  Se conservan los manejos seguros de 429 y transporte.
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
- **Arquitectura stateless**: se eliminaron los tools `get_cached`,
  `invalidate_cache` y `get_search_history`. La caché de proceso con TTL
  sigue funcionando internamente como optimización, pero no es accesible ni
  invalida-ble vía MCP.
- Se eliminó toda integración con memoria externa (IA_Recuerdo / CT 110).
  El servicio ya no envía, recibe ni persiste observaciones fuera de su
  propio proceso.
- Se eliminaron los flags `--memory-url` y `--memory-apikey` y sus
  equivalentes en el template de systemd y en Kubernetes.
- 25 tools MCP registradas (antes 28).

### 1.1.0 — 2026-05-02
- **SearxNG Migration**: Images, News, YouTube, Academic ahora usan SearxNG en LXC 201 (10.0.0.201:8080).
- Conectores migrados: search_images, search_news, search_youtube, search_academic.
- Se eliminaron APIs deprecated (Invidious, Semantic Scholar).
- Proceso duplicado identificado y resuelto (usuario 100997).
- Deploy: `/opt/ia-buscar/bin/ia-buscar` en LXC 15.

### 1.0.0 — 2026-04-30
- Servicio MCP de búsqueda inicial con 13 conectores.
- 25 tools MCP registradas.
- conectores: search_web, search_github, search_github_pr, search_github_issue, search_stackoverflow, search_npm, search_nuget, search_pypi, search_docker_hub, search_academic, search_reddit, search_youtube, search_images.
- Tools adicionales: fetch_url, fetch_and_extract, extract_structured, validate_url, check_link_status, summarize_results, deep_research, compare_sources, get_current_date.
- Protección SSRF en operaciones de fetch.

---

## Seguridad

- Protección SSRF en fetch/extract de URLs.
- Validación de URLs antes de realizar solicitudes.
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
  pero la política de Reddit bloqueó el pedido anónimo con 401/403 y
  no hay OAuth configurado. El warning nombra
  `REDDIT_CLIENT_ID` / `REDDIT_CLIENT_SECRET` / `REDDIT_USER_AGENT`
  como remediación. El agente debe presentar la respuesta como "Reddit
  no disponible por configuración" y NO como un resultado vacío real.
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

Además de las 25 tools, el server anuncia un recurso MCP estático pensado
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
