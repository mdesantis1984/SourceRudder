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
| `-result-set-ttl` | `RESULT_SET_TTL` o `900` | Segundos durante los que un set de resultados guardado se puede paginar con `cursor`. |
| `-result-set-max-sets` | `RESULT_SET_MAX_SETS` o `64` | Máximo de sets guardados; se expulsan primero los más antiguos. |
| `-result-set-max-results` | `RESULT_SET_MAX_RESULTS` o `2000` | Máximo de resultados guardados entre todos los sets; se expulsan primero los sets más antiguos. |

## Entorno y Compose

| Variable | Predeterminado | Efecto |
|---|---|---|
| `SOURCERUDDER_AUTH_KEY` | vacío | Credencial para `/mcp` y `/metrics`. Vacía implica rechazo total de rutas protegidas. |
| `SOURCERUDDER_PORT` | `8080` en Compose | Puerto host loopback publicado por Compose; no reemplaza `-http-addr`. |
| `SEARXNG_URL` | `http://localhost:8888` | URL backend de SearXNG. |
| `FETCH_USER_AGENT` | User-Agent de Chrome de escritorio (`fetch.DefaultUserAgent`) | User-Agent del fetcher. Muchos sitios rechazan agentes que se identifican como bot; un valor explícito se usa sin cambios. |
| `FETCH_TIMEOUT_MS` | `30000` | Timeout de fetch; un `-fetch-timeout-ms` positivo tiene prioridad. |
| `FETCH_MAX_REDIRECTS` | `5` | Máximo de redirecciones validadas. |
| `FETCH_MAX_ATTEMPTS` | `3` | Máximo de intentos de fetch. |
| `LOCAL_INDEX_PATH` | vacío | Habilita `search_local_index`. |
| `RESULT_SET_TTL` | `900` | Vida del set de resultados en segundos; `-result-set-ttl` tiene prioridad. Un valor no positivo vuelve al predeterminado. |
| `RESULT_SET_MAX_SETS` | `64` | Máximo de sets de resultados guardados; `-result-set-max-sets` tiene prioridad. |
| `RESULT_SET_MAX_RESULTS` | `2000` | Máximo total de resultados guardados; `-result-set-max-results` tiene prioridad. |
| `YOUTUBE_TRANSCRIPTS` | habilitado | `off` deshabilita `fetch_youtube_transcript`, la delegación de YouTube en `fetch_url` / `fetch_and_extract` y la búsqueda directa con yt-dlp de `search_youtube` (solo SearXNG). |
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

### Noticias: Google News RSS y `search_tech_news`

`search_news` consulta primero SearXNG. Cuando SearXNG devuelve menos resultados que `maxResults`, o responde solo en parte, el servidor agrega a continuación resultados de la búsqueda RSS de Google News (`https://news.google.com/rss/search?q=<consulta>&hl=..&gl=..&ceid=..`). Un `language` que empieza con `es` usa `es-419` / `AR`; cualquier otro usa `en-US` / `US`. `timeRange` pasa a `when:1d`, `when:7d`, `when:30d` o `when:1y` (`when:1m` no devuelve nada en Google News) dentro de la consulta. Los resultados se deduplican por URL y por título normalizado, y sus puntajes siguen decreciendo después de los de SearXNG. Si falla la solicitud RSS, se conservan los resultados de SearXNG y la respuesta lleva el warning `google news rss supplement failed: <motivo>`. La `url` de los resultados de Google News es un enlace de redirección de `news.google.com` (el feed no trae la URL del medio); `source` es el medio.

`search_tech_news` cubre noticias de tecnología e ingeniería de software sin clave de API. Lee tres tipos de fuente en paralelo:

- HN Algolia (`https://hn.algolia.com/api/v1/search`, solo stories). La `url` del resultado es el enlace de la story, o la página del ítem de HN si no tiene; el `snippet` lleva puntos y comentarios y el enlace a la discusión de HN.
- Búsqueda RSS de Google News, como arriba.
- 19 feeds RSS/Atom curados (InfoQ, Martin Fowler, The Pragmatic Engineer, ByteByteGo, Netflix TechBlog, Cloudflare, GitHub Engineering, Stack Overflow Blog, AWS Architecture, Google Developers, The New Stack, Go Blog, Ars Technica, TechCrunch, The Verge, Wired, The Register, Xataka, Genbeta), en una sola tabla de `internal/connectors/technews.go`.

Se leen como máximo 6 feeds a la vez, cada uno con 8 s de límite y 12 s para toda la búsqueda; cada feed procesado se cachea 30 minutos. Los ítems curados se filtran por la consulta: palabras sin distinguir tildes ni mayúsculas en título y resumen, con todas las palabras significativas; si nada coincide con todas, se devuelven los que coinciden con alguna, con un warning. El orden es por fuerza de coincidencia; dentro de la misma fuerza las fuentes se turnan (cada una conserva su orden: HN por relevancia, las demás de la más nueva a la más vieja), así ninguna llena sola la página. Las consultas a HN siempre llevan un piso de fecha: la ventana de `timeRange`, o los últimos 365 días sin `timeRange`, porque HN ordena por relevancia y puntos y sus primeros resultados serían de hace años. `timeRange` filtra por fecha de publicación y descarta los ítems sin fecha. Las fuentes que fallan producen un único warning resumido, por ejemplo `tech news: 2 of 21 sources failed: hn algolia, cloudflare blog`; la respuesta es `partial` solo cuando fallan HN y Google News. Cada resultado trae `engine` y `source` con el nombre de la fuente, `type` `news`, `publishedAt` y, si el feed lo informa, `author`. El contenido de los feeds no es confiable: se quita el HTML, se colapsan los espacios, cada campo tiene tope y el cuerpo de un feed se limita a 2 MiB.

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

SearXNG sirve `search_web`, `search_news`, `search_academic`, `search_reddit`, `search_images`, `search_youtube` (como respaldo de su búsqueda directa con yt-dlp) y el fallback web de `search_doc_oficial`. Reddit usa el índice local de SearXNG filtrado a contenido público de `reddit.com`; no requiere credenciales directas de Reddit.

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

La caché tiene un TTL predeterminado de 300 segundos. Las búsquedas mediante SearXNG, GitHub y StackOverflow limitan a 30 segundos el TTL de respuestas degradadas (`partial: true`, incluidos motores sin respuesta) y vacías (sin resultados), sin extender un TTL configurado menor; las respuestas sanas con resultados conservan el TTL configurado. `search_web` devuelve hasta 20 resultados cuando se omite `maxResults` (los valores explícitos se respetan, con tope de 100), quita URLs duplicadas y pide una vez la página 2 de SearXNG cuando la página 1 trae menos resultados de los pedidos. Sus claves versionadas incluyen los parámetros de solicitud, como idioma, búsqueda segura, rango temporal y límite efectivo de resultados, y no reutilizan claves anteriores. El historial conserva las 100 búsquedas más recientes. Ambos viven en proceso, no se comparten entre réplicas y se pierden al reiniciar. No los use como auditoría durable.

## Sets de resultados y paginación

Cada herramienta de búsqueda guarda en un almacén en proceso la lista completa de resultados que obtuvo y devuelve una página con `resultSetId`, `total` y, solo mientras quedan resultados, `nextCursor`. `maxResults` es el tamaño de página que elige quien llama, no un tope de lo que se conserva. Envía `cursor` (el `nextCursor` de una respuesta anterior, con el `query` y el `maxResults` que quieras) para leer la página siguiente sin ninguna petición al proveedor; un cursor solo sirve con la herramienta que lo emitió (otra herramienta devuelve `invalid_cursor: cursor was issued by <tool>`), uno mal formado devuelve `invalid_cursor: <motivo>`, `offset` y `resultSetId` están retirados en las herramientas de búsqueda (la llamada se ejecuta igual y la respuesta avisa `offset and resultSetId are no longer supported; page with cursor=<nextCursor>`) y un set desconocido o vencido devuelve `result_set_not_found`. El almacén es por proceso y no es durable: un reinicio o una expulsión invalida los IDs. El uso de memoria está acotado por `-result-set-max-results` (2.000 resultados por defecto); dimensiónalo para la réplica.

En `search_web`, paginar más allá del final del set guardado puede traer más: cuando `cursor` apunta al final de un set cuya fuente SearXNG puede tener más resultados, el servidor pide la página siguiente de SearXNG (`pageno`), agrega solo las URL que no estaban guardadas y las devuelve. Cada llamada así hace como máximo una petición extra al proveedor, nunca al buscar, y un set lee como máximo 5 páginas de SearXNG en total (contando las del momento de la búsqueda). `nextCursor` se omite al leer la 5.ª página, cuando una página no aporta URL nuevas o cuando se llenó el presupuesto de resultados (un warning lo indica). Se reutilizan la consulta, `language`, `timeRange` y `safeSearch` de la búsqueda original. Si la página extra falla, la respuesta viene vacía con un warning y se puede reintentar el mismo cursor. Las demás herramientas de búsqueda nunca piden más: su `nextCursor` se omite al final del set guardado.

Los resultados se filtran antes de guardarse o devolverse, tanto en la primera página como en las pedidas de forma diferida: siempre se eliminan los resultados cuya URL no es una URL `http(s)` con host (enlaces ocultos de motores como `http:///go/v1/...`), salvo en `search_local_index`, que conserva sus URL `local-index://`. Con `safeSearch` activo también se eliminan los resultados para adultos: dominios para adultos conocidos y sus subdominios, marcas para adultos con cualquier TLD o unidas con `-`, TLD para adultos (`.xxx`, `.adult`, `.porn`, `.sex`, `.sexy`), etiquetas del host que contienen `porn` (salvo Pornic y la investigación sobre pornografía) o los tokens `xxx`, `nsfw`, `hentai`, y títulos con `porn`, `porno`, `xxx`, `nsfw` o `hentai` como palabra completa (los fragmentos nunca se evalúan). `safeSearch` vale `true` por defecto (moderado, `safesearch=1` de SearXNG) cuando se omite; `safeSearch: false` lo apaga y desactiva el filtro de dominios para adultos. Cada eliminación agrega a la respuesta un warning con la cantidad (`removed N results without a valid URL host (cloaked engine links, searxng#6967)`, `removed N results from adult domains (safe search)`).

Una llamada necesita `query` o `cursor`: con solo `cursor` lee la página siguiente y sin ninguno de los dos devuelve `invalid_arguments: provide query or cursor`. Una página por cursor conserva el tamaño de la primera página salvo que la llamada envíe `maxResults` (tope 100).

`summarize_results`, `deep_research` y `compare_sources` aceptan `resultSetId` (el valor que devolvió una herramienta de búsqueda) en lugar de `results`, así no hay que reenviar la lista. El set guardado se lee localmente, sin petición de red. Si se pasan ambos, gana `results` y la respuesta trae el warning `resultSetId ignored: results were provided in the same call; results win`. Un ID desconocido o vencido devuelve un resultado de error: `result_set_not_found: <id> (expired or unknown; run the search again)`.

### Formato del resultado

Todo resultado de `tools/call` trae `structuredContent` (la respuesta tipada completa, siempre un objeto JSON) y exactamente un bloque de texto. Las herramientas de búsqueda aceptan dos argumentos opcionales para ese texto: `format` es `json` (por defecto; el mismo valor que `structuredContent`, minificado) o `compact` (una línea por resultado, `título | url | snippet`, con `resultSetId`, `total` y `nextCursor` en una línea de cabecera), y `detail` (solo con `format=compact`) es `titles`, `snippets` (por defecto) o `full`. `structuredContent` es la respuesta completa en ambos formatos. Algunos clientes muestran al modelo solo el texto y cortan el texto de más de unos 20 KB; para páginas grandes usa `format=compact` con `detail=titles`. Un valor desconocido de `format` o `detail` se rechaza con un error de argumentos que lista los valores permitidos.

Los metadatos y errores de las herramientas siguen MCP 2025-11-25. `tools/list` da a cada herramienta un `title` corto y `annotations` (`readOnlyHint`, `destructiveHint`, `idempotentHint`, `openWorldHint`; los clientes las tratan como pistas, no como garantías). Las herramientas de búsqueda y de fetch son de solo lectura y de mundo abierto; las de síntesis (`summarize_results`, `deep_research`, `compare_sources`), `search_local_index` y las de estado local (`get_cached`, `invalidate_cache`, `get_search_history`, `get_current_date`) son de mundo cerrado. Las 17 herramientas de búsqueda comparten un único `outputSchema` (la respuesta de búsqueda: `query`, `results[]` con `title`, `url` y `source` obligatorios más `snippet`, `total` y, opcionales, `resultSetId`, `nextCursor`, `partial`, `warnings`, `errors`, `answers`, `infoboxes`, `suggestions`, `corrections`; es mínimo a propósito para ahorrar tokens en `tools/list`, y `structuredContent` conserva sus demás campos) y las tres de síntesis comparten otro; todo `structuredContent` que devuelven lo cumple, así que los clientes pueden tiparlo (los scripts de codemode de Pi pueden filtrarlo antes de que algo llegue al modelo). Las demás herramientas no declaran `outputSchema`.

### Extras de SearXNG y `timeRange`

`search_web` también devuelve lo que SearXNG envía junto a los resultados en su primera página: `answers` (textos, por ejemplo un hash o una definición), `infoboxes` (`[{title, content, url}]`, un panel de conocimiento como el resumen de Wikipedia), `suggestions` (consultas relacionadas) y `corrections` (correcciones ortográficas). Cada campo se omite cuando SearXNG no envió nada. Cada texto es una sola línea recortada (CR, LF, U+2028 y U+2029 pasan a espacios, se colapsan los espacios y se corta en 500 caracteres) y se descartan las entradas vacías o repetidas. Forman parte de la búsqueda, así que se cachean con ella y se repiten en cada página que se lee con `cursor`. Con `format=compact` son una línea con etiqueta cada una (`answer:`, `infobox: título | contenido | url`, `suggestions:`, `corrections:`) después de los resultados y antes de los warnings, en cualquier `detail`.

Cuando una búsqueda respaldada por SearXNG (`search_web`, `search_news`, `search_images`, `search_academic`, `search_youtube`, `search_reddit`) se llama con `timeRange` y no devuelve resultados, la respuesta trae el warning `timeRange: SearXNG skips engines without date filtering; retry without timeRange for undated results`. SearXNG descarta en silencio todo motor que no puede filtrar por fecha, así que una respuesta vacía puede significar "no hay resultados con fecha", no "no hay resultados". El servidor no reintenta por su cuenta. El warning no se agrega cuando la llamada falló por completo y no marca la respuesta como `partial`.

`search_youtube` busca en YouTube directamente con el mismo yt-dlp de las transcripciones (`yt-dlp --flat-playlist --dump-single-json ytsearchN:<consulta>`, un solo elemento de argv después de `--`, sin shell; N es `maxResults`, máximo 50; como mucho 30 s por búsqueda, o `YTDLP_TIMEOUT` si es menor; stdout limitado a 1 MiB). Los resultados traen `engine`/`source` `yt-dlp`, `author` (canal) y un `snippet` con canal, duración, vistas y descripción, más `publishedAt` cuando yt-dlp informa una fecha (los resultados planos de búsqueda casi nunca la traen). Se omiten canales, listas de reproducción, directos y estrenos próximos, y entradas sin id o título. SearXNG (`engines=youtube,brave`) es el respaldo cuando yt-dlp falta, falla, agota el tiempo, YouTube lo bloquea (429 o verificación anti-bot; rige el cooldown de las transcripciones) o no devuelve nada: la respuesta trae entonces el warning `yt-dlp search failed, used SearXNG fallback: <motivo>` o `yt-dlp search returned no results, used SearXNG fallback`, no es `partial` y se cachea solo 30 s. `ytsearch` no filtra por fecha, así que una llamada con `timeRange` omite yt-dlp y va directo a SearXNG, con el warning de `timeRange` de arriba si no devuelve nada. Con `YOUTUBE_TRANSCRIPTS=off` la herramienta usa solo SearXNG, como antes.

`search_academic` pide a SearXNG los motores `arxiv`, `crossref`, `openalex`, `pubmed`, `semantic scholar` y `openairepublications` (sin categoría), los seis que respondieron en la instancia de producción; los motores bloqueados o que agotan el tiempo ahí no se piden.

Una llamada fallida es un resultado normal, `{"content":[{"type":"text","text":"<mensaje>"}],"isError":true}` sin `structuredContent`, para que el modelo lea el mensaje y reintente. Cubre la validación de argumentos (`format` o `detail` desconocido, `invalid_cursor: <motivo>`, tipos de argumento incorrectos), los errores de negocio (`result_set_not_found: <id> ...`) y los fallos del proveedor o del handler. Los errores JSON-RPC quedan solo para fallos de protocolo: una herramienta desconocida (`-32602`), JSON mal formado o `params` mal formados en `tools/call`, y una versión de protocolo no admitida.
