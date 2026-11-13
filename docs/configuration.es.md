# Configuración

[English](configuration.md) | [Español](configuration.es.md)

SourceRudder 3.0.0 se ejecuta como `sourcerudder`. Se configura con flags, variables de entorno y manifiestos de despliegue. Los flags no vacíos tienen prioridad sobre el entorno y luego sobre los valores predeterminados.

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
| `-local-index-path` | vacío | Reemplaza `LOCAL_INDEX_PATH`. |

## Entorno y Compose

| Variable | Predeterminado | Efecto |
|---|---|---|
| `SOURCERUDDER_AUTH_KEY` | vacío | Credencial para `/mcp` y `/metrics`. Vacía implica rechazo total de rutas protegidas. |
| `SOURCERUDDER_PORT` | `8080` en Compose | Puerto host loopback publicado por Compose; no reemplaza `-http-addr`. |
| `SEARXNG_URL` | `http://localhost:8888` | URL backend de SearXNG. |
| `FETCH_USER_AGENT` | `SourceRudder/3.0.0 (anonymous-only)` | User-Agent del fetcher. |
| `FETCH_TIMEOUT_MS` | `30000` | Timeout de fetch; un `-fetch-timeout-ms` positivo tiene prioridad. |
| `FETCH_MAX_REDIRECTS` | `5` | Máximo de redirecciones validadas. |
| `FETCH_MAX_ATTEMPTS` | `3` | Máximo de intentos de fetch. |
| `LOCAL_INDEX_PATH` | vacío | Habilita `search_local_index`. |
| `YOUTUBE_TRANSCRIPTS` | habilitado | `off` deshabilita `fetch_youtube_transcript` y la delegación de YouTube en `fetch_url` / `fetch_and_extract`. |
| `YTDLP_PATH` | `yt-dlp` | Ejecutable de yt-dlp. La unidad systemd usa `/opt/yt-dlp/bin/yt-dlp`. |
| `YTDLP_JS_RUNTIMES` | vacío | Runtime de JavaScript que se pasa a yt-dlp como `RUNTIME[:RUTA]`. La unidad systemd usa `node:/opt/nodejs/bin/node`. |
| `YOUTUBE_POT_BASE_URL` | vacío (por defecto del plugin `http://127.0.0.1:4416`) | URL del servidor de PO tokens bgutil. |
| `YOUTUBE_FETCH_POT` | `always` | Política `youtube:fetch_pot` de yt-dlp (`always`, `auto`, `never`). `always` también envía un PO token con la solicitud al reproductor; con `auto`, una IP marcada recibe "Sign in to confirm you're not a bot" antes de que yt-dlp pida un token a bgutil. |
| `YOUTUBE_DEFAULT_LANGUAGE` | _(vacío)_ | Código de idioma (p. ej. `es`) que se usa como idioma solicitado de la transcripción cuando una llamada lo omite, en particular `fetch_url` y `fetch_and_extract`, que no tienen argumento de idioma. Un `language` explícito siempre tiene prioridad; un valor inválido se ignora. Conviene definirla porque el idioma hablado que declara YouTube puede diferir del idioma del título en videos con doblaje automático (p. ej. un título en español reportado como `ar`). |
| `YOUTUBE_CACHE_TTL` | `168h` | TTL de la caché de transcripciones por video e idioma. |
| `YOUTUBE_BLOCK_COOLDOWN` | `15m` | Pausa global de solicitudes a YouTube tras un 429 o verificación anti-bot. |
| `YTDLP_TIMEOUT` | `2m` | Timeout de cada invocación de yt-dlp. |
| `YOUTUBE_WORK_DIR` | vacío (`TMPDIR`) | Directorio temporal para la salida de yt-dlp. |

Use un `.env` local, ignorado por Git y con permisos restrictivos.

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

La caché tiene un TTL predeterminado de 300 segundos. Las búsquedas mediante SearXNG limitan el TTL de respuestas degradadas (`partial: true`, incluidos motores sin respuesta) a 30 segundos, sin extender un TTL configurado menor; las respuestas sanas conservan el TTL configurado. Sus claves versionadas incluyen los parámetros de solicitud, como idioma, búsqueda segura, rango temporal y límite efectivo de resultados, y no reutilizan claves anteriores. El historial conserva las 100 búsquedas más recientes. Ambos viven en proceso, no se comparten entre réplicas y se pierden al reiniciar. No los use como auditoría durable.
