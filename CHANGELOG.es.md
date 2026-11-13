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

### Cambiado

- El servidor ahora registra 29 herramientas MCP (antes 28).

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
