# Configuración

[English](configuration.md) | [Español](configuration.es.md)

SourceRudder 2.0.0 se ejecuta como `sourcerudder`. Se configura con flags, variables de entorno y manifiestos de despliegue. Los flags no vacíos tienen prioridad sobre el entorno y luego sobre los valores predeterminados.

## Inicio rápido

```bash
export SOURCERUDDER_AUTH_KEY='reemplazar-por-un-secreto'
sourcerudder -transport http -http-addr :8080
```

En servidores, use `SOURCERUDDER_AUTH_KEY` antes que `-auth-key` para no exponer secretos en el historial de shell ni en listados de procesos.

## Ajustes de runtime

| Flag | Predeterminado | Uso |
|---|---|---|
| `-transport` | `stdio` | `stdio` o `http`; otro valor detiene el arranque. |
| `-http-addr` | `:8080` | Dirección de escucha HTTP. |
| `-searxng-url` | `SEARXNG_URL` o `http://localhost:8888` | Endpoint SearXNG local. |
| `-cache-ttl` | `300` | TTL de caché en proceso, en segundos. |
| `-fetch-timeout-ms` | `FETCH_TIMEOUT_MS` o `30000` | Timeout de fetch, en milisegundos. |
| `-auth-key` | vacío | Reemplaza `SOURCERUDDER_AUTH_KEY`. |
| `-memory-url` / `-memory-apikey` | vacío | Reemplazan `MEMORY_URL` / `MEMORY_APIKEY`. |
| `-local-index-path` | vacío | Reemplaza `LOCAL_INDEX_PATH`. |

## Entorno y Compose

| Variable | Predeterminado | Efecto |
|---|---|---|
| `SOURCERUDDER_AUTH_KEY` | vacío | Credencial para `/mcp` y `/metrics`. Vacía implica rechazo total de rutas protegidas. |
| `SOURCERUDDER_PORT` | `8080` en Compose | Puerto host loopback publicado por Compose; no reemplaza `-http-addr`. |
| `SEARXNG_URL` | `http://localhost:8888` | URL backend de SearXNG. |
| `FETCH_USER_AGENT` | `SourceRudder/2.0.0 (anonymous-only)` | User-Agent del fetcher. |
| `FETCH_TIMEOUT_MS` | `30000` | Timeout de fetch; un `-fetch-timeout-ms` positivo tiene prioridad. |
| `FETCH_MAX_REDIRECTS` | `5` | Máximo de redirecciones validadas. |
| `FETCH_MAX_ATTEMPTS` | `3` | Máximo de intentos de fetch. |
| `MEMORY_URL` | vacío | Endpoint opcional de observaciones IA_Recuerdo. |
| `MEMORY_APIKEY` | vacío | Bearer token opcional para ese endpoint. |
| `LOCAL_INDEX_PATH` | vacío | Habilita `search_local_index`. |

Use un `.env` local, ignorado por Git y con permisos restrictivos. `MEMORY_*` es best effort: un `MEMORY_URL` vacío desactiva requests salientes; un endpoint configurado recibe JSON por `POST` y puede agregar hasta 10 segundos sin convertir una búsqueda correcta en error.

## Autenticación HTTP

`/healthz` es pública. `/mcp` y `/metrics` aceptan:

```http
X-Api-Key: <key>
```

```http
Authorization: Bearer <key>
```

Si ambos están presentes, `X-Api-Key` tiene prioridad. Credenciales ausentes, vacías o inválidas responden `401`; HTTP opera fail-closed. `stdio` no usa autenticación HTTP.

## Backends e índice local

SearXNG sirve `search_web`, `search_news`, `search_academic`, `search_reddit`, `search_youtube`, `search_images` y el fallback web de `search_doc_oficial`. Reddit usa el índice local de SearXNG filtrado a contenido público de `reddit.com`; no requiere credenciales directas de Reddit.

El índice local es opcional. Deshabilitado, `search_local_index` devuelve `strategy="local_index_unavailable"` sin fallback web. Habilitado, lee un corpus JSON estricto y de solo lectura con `strategy="local_index_lexical"`:

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

Use un archivo regular, sin symlink y no escribible por grupo u otros. El loader admite hasta 4 MiB y 10.000 documentos; un corpus inválido detiene el arranque. Ver [`configs/local-index.example.json`](../configs/local-index.example.json).

## Estado

La caché tiene un TTL predeterminado de 300 segundos; el historial conserva las 100 búsquedas más recientes. Ambos viven en proceso, no se comparten entre réplicas y se pierden al reiniciar. No los use como auditoría durable.
