# Historial de Cambios

[English](CHANGELOG.md) | [Español](CHANGELOG.es.md)

Este archivo documenta los cambios relevantes de SourceRudder.

## [Sin publicar]

### Añadido

- Herramienta MCP `fetch_youtube_transcript` (`url`, `language` opcional,
  `maxChars` opcional) que devuelve la transcripción de un video de YouTube con
  título, canal, fecha, duración, idioma, indicador manual/automática y estado
  de caché, mediante yt-dlp con PO tokens de bgutil. Las transcripciones se
  cachean por video e idioma, y un 429 o verificación anti-bot produce un único
  error `youtube_blocked` con un cooldown global corto, sin reintentos.
- `fetch_url` y `fetch_and_extract` delegan las URLs de video de YouTube al
  servicio de transcripciones en lugar de scrapear la página de visualización.
- Activos en `deploy/youtube/`: instaladores de yt-dlp y bgutil, un
  `bgutil-pot-provider.service` endurecido y solo en loopback, un timer semanal
  `yt-dlp-update` y las nuevas variables `YTDLP_PATH`, `YTDLP_JS_RUNTIMES`,
  `YOUTUBE_POT_BASE_URL`, `YOUTUBE_FETCH_POT`, `YOUTUBE_DEFAULT_LANGUAGE`, `YOUTUBE_CACHE_TTL`, `YOUTUBE_BLOCK_COOLDOWN`,
  `YTDLP_TIMEOUT`, `YOUTUBE_WORK_DIR` y `YOUTUBE_TRANSCRIPTS`, documentadas en las
  guías de despliegue y configuración.
- Sets de resultados: cada herramienta de búsqueda guarda en un almacén en proceso
  la lista completa de resultados que obtuvo y devuelve una página con `resultSetId`,
  `total` y, solo mientras quedan resultados, un `nextCursor` opaco. Enviar `cursor`
  (el `nextCursor` anterior) lee la página siguiente sin ninguna petición al
  proveedor; un cursor solo sirve con la herramienta que lo emitió. Nuevos flags
  `-result-set-ttl` (`RESULT_SET_TTL`, por defecto `900` segundos),
  `-result-set-max-sets` (`RESULT_SET_MAX_SETS`, por defecto `64`) y
  `-result-set-max-results` (`RESULT_SET_MAX_RESULTS`, por defecto `2000`) acotan el almacén.
- `summarize_results`, `deep_research` y `compare_sources` aceptan `resultSetId` en
  lugar de `results`, así no hay que reenviar la lista. Si se pasan ambos, gana
  `results` con una advertencia; un ID desconocido o vencido devuelve `result_set_not_found`.
- Carga perezosa de la página siguiente en `search_web`: paginar más allá del set
  guardado pide la siguiente página de SearXNG (como máximo una petición extra por
  llamada, como máximo 5 páginas de SearXNG por set, y solo se agregan URLs que no estaban guardadas).
- Las herramientas de búsqueda aceptan `format` (`json` por defecto, o `compact`: una
  línea `title | url | snippet` por resultado) y `detail` (`titles`, `snippets` por
  defecto, o `full`; solo con `format=compact`) para ahorrar tokens.
- Cada resultado de `tools/call` trae `structuredContent` (la respuesta tipada
  completa) junto a un bloque de texto. Las herramientas declaran `title` y
  `annotations`, y las de búsqueda y síntesis un `outputSchema` que cumple
  `structuredContent`.
- Corpus de calidad de búsqueda (`scripts/quality/corpus/search_quality.json`) y
  `scripts/quality/measure_corpus.py` (`validate`, `run`, `compare`) para comparar
  bytes, tokens estimados, latencia y calidad de los primeros resultados antes y
  después de un cambio; los resultados están en
  `docs/quality/search-result-quality-measurement.md`.
- `search_web` devuelve los `answers`, `infoboxes` (`{title, content, url}`),
  `suggestions` y `corrections` de SearXNG de la primera página como campos
  opcionales de la respuesta (se omiten si están vacíos, se sanean a líneas
  simples de hasta 500 caracteres, se cachean con la búsqueda, figuran en el
  `outputSchema` compartido y se imprimen como líneas con etiqueta con
  `format=compact`).
- Las búsquedas respaldadas por SearXNG llamadas con `timeRange` que no devuelven
  resultados agregan el warning `timeRange: SearXNG skips engines without date
  filtering; retry without timeRange for undated results`; no hay reintento
  automático.

### Cambiado

- `search_youtube` busca en YouTube directamente con yt-dlp
  (`--flat-playlist --dump-single-json ytsearchN:<consulta>`, como lo hace
  music.thiscloud.com.ar) y suma canal (`author`), duración, vistas y descripción
  al `snippet`, más `publishedAt` cuando yt-dlp informa una fecha. Reutiliza el
  binario, el timeout y el cooldown de bloqueo del servicio de transcripciones.
  SearXNG (`youtube,brave`) queda como respaldo cuando yt-dlp falta, falla, agota
  el tiempo o no encuentra nada, con un warning que nombra el motivo. Una llamada
  con `timeRange` va directo a SearXNG (`ytsearch` no filtra por fecha) y
  `YOUTUBE_TRANSCRIPTS=off` mantiene el comportamiento anterior solo con SearXNG.
- `search_academic` pide a SearXNG los motores `arxiv`, `crossref`, `openalex`,
  `pubmed`, `semantic scholar` y `openairepublications` sin categoría (antes
  `categories=science` más `arxiv,duckduckgo`), y acepta URLs de resultado de
  openalex.org, europepmc.org, ncbi.nlm.nih.gov, openaire.eu y crossref.org.
- El servidor ahora registra 29 herramientas MCP (antes 28).
- El User-Agent predeterminado del fetcher ahora es el de Chrome de escritorio
  (`fetch.DefaultUserAgent`) en lugar de `SourceRudder/3.0.0`, porque muchos sitios
  rechazan agentes que se identifican como bot. Un `FETCH_USER_AGENT` explícito se usa sin cambios.
- Las plantillas de despliegue de systemd y Kubernetes ya no fijan
  `FETCH_USER_AGENT`, por lo que se aplica el valor por defecto de navegador;
  `scripts/release-readiness.sh` ahora falla si una plantilla lo define. Los
  operadores pueden seguir definiendo `FETCH_USER_AGENT` explícitamente.
- Las descripciones de las herramientas ya no confunden: `search_news` no promete un
  filtro `timeRange=week`, `deep_research`, `summarize_results` y `compare_sources`
  aclaran que solo sintetizan los `results` recibidos, y `maxResults` indica su
  valor predeterminado y su tope reales.
- Las búsquedas de GitHub (repositorios, pull requests, issues) y StackOverflow
  que responden bien pero con cero resultados se reintentan una vez con una consulta
  más corta: se conservan los calificadores como `repo:`, se quitan las palabras
  vacías en inglés y español, y quedan los primeros cuatro términos. La respuesta
  conserva la `query` original y agrega la advertencia
  `query relaxed: <consulta acortada>`. Los errores (429, 4xx, 5xx, transporte)
  nunca se reintentan. Las respuestas vacías o parciales de GitHub y StackOverflow
  se cachean como máximo 30 segundos.
- `search_web` devuelve hasta 20 resultados cuando se omite `maxResults` (antes
  10; los valores explícitos se respetan, con tope de 100), quita URLs duplicadas y
  pide una vez la página 2 de SearXNG cuando la página 1 trae menos resultados de
  los pedidos. Si la página 2 falla, se conservan los resultados de la página 1 con
  una advertencia.
- Las búsquedas mediante SearXNG cachean las respuestas sin resultados como máximo
  30 segundos, igual que las degradadas, para no servir un vacío transitorio
  durante todo el TTL.
- Las herramientas de búsqueda ya no recortan por quien llama: `maxResults` es el
  tamaño de página y los conectores conservan cada resultado único que obtuvieron.
  `academic` y `youtube` ahora completan hasta `maxResults` después de filtrar. El
  orden de los resultados no cambia.
- El texto predeterminado de un resultado es JSON minificado (el mismo valor que
  antes, sin indentación), cerca de un 13 % menor en una respuesta de `search_web`
  con 20 resultados.
- **Incompatible:** los errores de ejecución, de argumentos y de negocio de las
  herramientas (por ejemplo `invalid_cursor: ...`, `result_set_not_found: ...`,
  `format` desconocido, fallos del proveedor) ahora devuelven un resultado normal
  con `isError: true` y un mensaje de texto accionable en lugar del error JSON-RPC
  `-32603`. Los errores JSON-RPC quedan solo para herramientas desconocidas y
  solicitudes mal formadas.
- **Incompatible:** `offset` y `resultSetId` en las herramientas de búsqueda quedan
  retirados a favor de `cursor` y `nextCursor`; `offset` y `hasMore` ya no se
  devuelven. La llamada igual se ejecuta como una búsqueda nueva y la respuesta avisa
  `offset and resultSetId are no longer supported; page with cursor=<nextCursor>`.
  `resultSetId` sigue siendo válido en las herramientas de síntesis.
  `SearchRequest.Offset` se eliminó de `pkg/types`.
- Paginar un set de `search_web` hasta el final puede devolver una página vacía
  extra, porque solo al pedir la última página de SearXNG se sabe que está vacía.
- `tools/list` es más grande (de unos 22,9 KB a 43,1 KB) por `title`, `annotations`
  y `outputSchema`; los clientes que cargan todos los esquemas de entrada lo pagan una vez.

### Seguridad

- Se actualizó el toolchain de Go a 1.27.4 y `github.com/klauspost/compress` a
  v1.20.4, lo que corrige GO-2026-6809 (crypto/x509), GO-2026-6859 (net/http) y
  GO-2026-6865 (klauspost/compress) informadas por govulncheck.
- CI, el release gate y la política de PR corren en el runner privado de
  Proxmox VM102 (`private-sourcerudder`); los pull requests desde forks siguen en
  runners de GitHub para que el código no confiable nunca llegue a él. CodeQL (memoria) y
  las releases (acceso al daemon de Docker) siguen en runners de GitHub.

### Corregido

- Las búsquedas web, noticias, YouTube, imágenes, académicas y Reddit mediante
  SearXNG limitan la caché de respuestas degradadas a 30 segundos (o al TTL
  configurado si es menor); las respuestas sanas conservan el TTL configurado.
  Las claves versionadas incluyen parámetros como idioma, búsqueda segura,
  rango temporal y límite efectivo de resultados.
- Los resultados SearXNG conservan la procedencia en un campo opcional `engine`;
  `source`, las advertencias de resultados parciales y las métricas no cambian.
- `initialize` ahora negocia la versión de protocolo MCP en lugar de devolver
  siempre `2024-11-05`: devuelve la versión solicitada si está admitida
  (`2025-11-25`, `2025-06-18`, `2024-11-05`) y, en caso contrario, `2025-11-25`.
  Las solicitudes HTTP GET/POST con una versión no admitida en el encabezado
  `MCP-Protocol-Version` reciben `400`.
- El apagado HTTP ahora cierra de inmediato los flujos Server-Sent Events
  abiertos de `GET /mcp` en lugar de esperar el tiempo límite de apagado; las
  solicitudes POST en curso siguen completándose.
- Los sets de resultados guardados ya no comparten slices mutables con quien llama
  (una carrera de datos cuando la paginación concurrente con otra consulta agregaba
  advertencias al mismo array), y la advertencia `intent:` ya no comparte memoria
  con las advertencias de la respuesta del conector.

### Seguridad

- Se compila con Go 1.26.9 para incorporar las correcciones de la biblioteca
  estándar en `crypto/tls`, `net/http`, `net/textproto` y `os` informadas por
  `govulncheck` (GO-2026-6603 a GO-2026-6617).

### Distribución y documentación

- Se agregó la configuración de Pi por HTTP nativo y stdio local a la guía de
  clientes MCP, incluida la exposición de herramientas y la verificación de
  conexión.

## [3.0.0] - 2026-09-12

### Cambios incompatibles

### Eliminado

- Se retiró el envío opcional de observaciones a IA_Recuerdo, incluidos sus
  flags de CLI, variables de entorno `MEMORY_*`, configuración de despliegue y
  requests salientes síncronos. La integración obsoleta se eliminó para que una
  futura capacidad de persistencia pueda diseñarse de forma deliberada; este
  cambio no incluye un reemplazo. Las 28 tools MCP y el historial de búsquedas
  en proceso permanecen sin cambios.

### Corregido

- Se conectaron los contadores de requests HTTP y los histogramas de latencia
  por fuente a la ruta MCP de producción para que `/metrics` refleje tráfico real.
- La unidad systemd distribuible ahora se enlaza a loopback por defecto para que
  hosts dual-stack no expongan el endpoint de salud sin autenticación en todas
  las interfaces.

### Distribución y documentación

- Se incorporaron la identidad visual completa de SourceRudder y el conjunto de
  recursos de campaña índigo.
- La automatización de release ahora deriva las versiones de archivos, imágenes
  y despliegues desde la versión revisada en `Makefile`; release readiness rechaza
  tags inconsistentes y notas de release ausentes.

## [2.0.0] - 2026-09-11

### Cambios de identidad incompatibles

- Se renombraron el producto, comando, servicio, proyecto Compose, recursos de
  Kubernetes, módulo Go, repositorio objetivo, URI de guía MCP, prefijo de
  entorno, User-Agent y prefijo de métricas Prometheus a identidades
  SourceRudder.
- `IA_BUSCAR_AUTH_KEY` y `IA_BUSCAR_PORT` fueron reemplazadas por
  `SOURCERUDDER_AUTH_KEY` y `SOURCERUDDER_PORT`.
- Consulte [Migración a 2.0](MIGRATION-TO-2.0.es.md) para los pasos de
  actualización y reversión.

### Contratos preservados

- Los nombres de tools MCP, campos JSON, conectores e identificadores de
  estrategia permanecen estables durante la migración de versión mayor.
- Las releases históricas de IA_Buscar conservan los derechos ya otorgados por
  MIT.

### Operación y cadena de suministro

- Se agregaron imágenes externas fijadas por digest, recursos acotados en
  Compose/systemd, puertos de desarrollo en loopback, ejecución sin privilegios
  y checks de contratos de despliegue.
- Se agregó un workflow de release con gates para archivos multiplataforma,
  checksums, attestations de procedencia, imágenes GHCR multiplataforma, SBOM y
  artefactos de despliegue inmutables.
- Se confirmó MIT como licencia activa de SourceRudder 2.0.0 y se hizo que
  release readiness verifique la licencia aprobada exacta antes de publicar.

### Documentación

- Se agregó documentación canónica en inglés con archivos equivalentes en
  español para instalación, migración, seguridad, desarrollo, configuración,
  operaciones, despliegue, arquitectura, clientes MCP, calidad, agradecimientos
  y reversión.
