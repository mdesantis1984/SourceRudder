# IA_Buscar

[![Go](https://img.shields.io/badge/Go-1.26.6+-00ADD8?logo=go)](https://go.dev/)
[![MCP](https://img.shields.io/badge/MCP-Compatible-FF6B6B)](https://modelcontextprotocol.io/)
[![License: MIT](https://img.shields.io/badge/License-MIT-green.svg)](LICENSE)

IA_Buscar es un servidor MCP en Go para buscar, recuperar, validar, sintetizar
y citar información. Expone 28 herramientas mediante `stdio` o HTTP, integra
fuentes especializadas y mantiene contratos explícitos para resultados vacíos,
degradaciones y proveedores opcionales.

La versión actual es **1.5.0**. `search_local_index` puede buscar en un corpus
JSON local, explícito y de solo lectura. El proveedor está deshabilitado por
defecto y nunca recorre el sistema de archivos ni usa la web como fallback.

## Inicio rápido

### Docker Compose recomendado

Requisitos: Git, Docker y Docker Compose v2. El stack levanta IA_Buscar y una
instancia local de SearXNG; ambos puertos se publican sólo en `127.0.0.1`.

```bash
git clone https://github.com/mdesantis1984/IA_Buscar.git
cd IA_Buscar
cp .env.example .env
```

Complete `IA_BUSCAR_AUTH_KEY` y `SEARXNG_SECRET` en `.env` con dos valores
independientes. Puede generar cada uno con `openssl rand -hex 32`. Luego inicie
y verifique el servicio:

```bash
docker compose up --build -d
curl -fsS http://127.0.0.1:8080/healthz
```

La respuesta esperada es `{"status":"ok"}`. El endpoint MCP queda en
`http://127.0.0.1:8080/mcp`; consulte la
[guía de clientes MCP](docs/mcp-clients.md) para conectarlo a Claude Desktop,
Cursor, Visual Studio Code u OpenCode.

```bash
docker compose logs -f ia-buscar
docker compose down
```

### Instalación nativa

Requisitos: Go 1.26.6 o posterior, SearXNG y una clave local para proteger
`/mcp` y `/metrics` cuando se usa HTTP.

### Compilar y probar

```bash
go build -o bin/ia-buscar ./cmd/ia-buscar
go test ./...
go vet ./...
```

### Ejecutar por stdio

```bash
./bin/ia-buscar -transport stdio -searxng-url http://localhost:8888
```

### Ejecutar por HTTP

```bash
IA_BUSCAR_AUTH_KEY='<local-secret>' \
SEARXNG_URL='http://localhost:8888' \
./bin/ia-buscar -transport http -http-addr :8080
```

Endpoints:

| Endpoint | Acceso | Propósito |
|---|---|---|
| `GET /healthz` | Público | Liveness mínimo: `{"status":"ok"}`. |
| `POST /mcp` | Bearer token | Frontera JSON-RPC MCP. |
| `GET /metrics` | Bearer token | Métricas Prometheus. |

No exponga el puerto HTTP directamente a Internet. Use una red privada o un
reverse proxy con TLS, límites de request y controles de acceso adicionales.

## Herramientas MCP

### Búsqueda

| Herramienta | Backend | Comportamiento principal |
|---|---|---|
| `search_web` | SearXNG | Búsqueda web general. |
| `search_news` | SearXNG | Noticias; el planner puede aplicar `timeRange=week`. |
| `search_doc_oficial` | Registro + SearXNG | Valida resultados contra hosts oficiales registrados. |
| `search_local_index` | Corpus JSON local opcional | Ranking léxico determinista, sin red ni crawling. |
| `search_github` | GitHub API | Repositorios, archivos y commits. |
| `search_github_pr` | GitHub API | Pull requests; acepta `filters.state`. |
| `search_github_issue` | GitHub API | Issues; acepta `filters.state`. |
| `search_stackoverflow` | Stack Exchange API | Preguntas y respuestas técnicas. |
| `search_npm` | npm Registry | Paquetes Node.js y TypeScript. |
| `search_nuget` | NuGet Gallery | Paquetes .NET. |
| `search_pypi` | PyPI | Paquetes Python. |
| `search_docker_hub` | Docker Hub | Repositorios de imágenes. |
| `search_academic` | SearXNG/arXiv | Papers y referencias académicas. |
| `search_reddit` | SearXNG | Posts públicos ya indexados, sin Reddit API ni OAuth. |
| `search_youtube` | SearXNG | Videos y tutoriales. |
| `search_images` | SearXNG | Imágenes con selección léxica bounded. |

### Recuperación, validación y síntesis

| Familia | Herramientas |
|---|---|
| Fetch/extract | `fetch_url`, `fetch_and_extract`, `extract_structured` |
| Validación | `validate_url`, `check_link_status` |
| Síntesis | `summarize_results`, `deep_research`, `compare_sources` |
| Cache/historial | `get_cached`, `invalidate_cache`, `get_search_history` |
| Tiempo | `get_current_date` |

El recurso MCP `agent-guide://ia-buscar/wire-contract` contiene la guía de uso
para agentes y se obtiene mediante `resources/list` y `resources/read`.

## Índice local

### Modelo de seguridad

`search_local_index` NO recibe un directorio ni descubre archivos. El operador
crea un único corpus JSON y configura su ruta antes de iniciar el proceso. El
archivo se valida y se carga una sola vez; las búsquedas posteriores operan
exclusivamente sobre la copia inmutable en memoria.

El proveedor:

- no recorre directorios;
- no sigue symlinks;
- no realiza requests HTTP;
- no ejecuta contenido del corpus;
- no lee archivos indicados por una consulta;
- no necesita API keys;
- no hace fallback a `search_web`;
- falla al iniciar si el corpus configurado es inválido.

Sin `LOCAL_INDEX_PATH`, la herramienta conserva el contrato seguro:
`strategy="local_index_unavailable"`, `results=[]` y un warning accionable.

### Configuración

```bash
LOCAL_INDEX_PATH='/srv/ia-buscar/index.json' \
./bin/ia-buscar -transport stdio
```

También puede utilizarse `-local-index-path`. Cuando ambos están presentes, el
flag tiene precedencia. La ruta no es secreta, pero el corpus puede contener
información sensible: manténgalo fuera del repositorio y limite sus permisos.

El proceso rechaza archivos:

- mayores a 4 MiB;
- con más de 10.000 documentos;
- que no sean archivos regulares;
- que sean symlinks;
- escribibles por grupo u otros;
- con versión desconocida, campos JSON desconocidos o contenido JSON extra.

Después de modificar el corpus debe reiniciarse el proceso. No existe recarga
en caliente ni observación del filesystem.

### Formato del corpus

El archivo raíz contiene `version` y `documents`. Hay un ejemplo completo en
[`configs/local-index.example.json`](configs/local-index.example.json).

```json
{
  "version": 1,
  "documents": [
    {
      "id": "getting-started",
      "title": "Getting started",
      "url": "https://example.com/docs/getting-started",
      "snippet": "Install and configure the service.",
      "author": "Documentation team",
      "tags": ["setup", "configuration"]
    }
  ]
}
```

| Campo | Requerido | Límite y uso |
|---|---|---|
| `id` | Sí | 1-128 caracteres ASCII: letras, números, `.`, `_`, `-`. Debe ser único. |
| `title` | Sí | 1-512 caracteres. |
| `url` | No | `http`, `https` o `local-index`; máximo 2048 caracteres. |
| `snippet` | No | Texto buscable y retornado; máximo 4096 caracteres. |
| `author` | No | Máximo 256 caracteres. |
| `tags` | No | Hasta 32 tags de 1-128 caracteres. |

Las URLs con credenciales, query parameters o fragments se rechazan para evitar
que tokens terminen en respuestas, logs o historiales. Si `url` se omite, el
servidor genera `local-index://document/<id>`, que no revela paths locales.

El cargador NO intenta clasificar secretos dentro de `title`, `snippet`,
`author` o `tags`. No incluya credenciales, datos personales ni información que
los consumidores MCP no deban leer.

### Ranking

La consulta se normaliza a minúsculas y se divide en términos alfanuméricos
Unicode. Se conservan como máximo 32 términos distintos de hasta 64 caracteres.
Todos los términos deben aparecer en algún campo del documento.

| Coincidencia | Puntaje |
|---|---:|
| Término en título | +4 |
| Término en tags | +3 |
| Término en ID | +2 |
| Término en snippet | +1 |
| Frase completa en título | +8 |
| Frase completa en snippet | +2 |

Los resultados se ordenan por puntaje descendente y luego por título. El
default es 10 resultados y el máximo efectivo es 50. Una consulta sin match
devuelve un resultado vacío saludable con `strategy="local_index_lexical"`.

### Respuesta configurada

```json
{
  "query": "getting started",
  "results": [
    {
      "title": "Getting started",
      "url": "https://example.com/docs/getting-started",
      "source": "local_index",
      "type": "document",
      "score": 19,
      "citationId": "local-index:getting-started"
    }
  ],
  "sourcesUsed": ["local_index"],
  "strategy": "local_index_lexical"
}
```

## Configuración

| Variable | Default | Notas |
|---|---|---|
| `IA_BUSCAR_AUTH_KEY` | Vacío | HTTP falla cerrado con 401 cuando está vacía. Trátela como secreto. |
| `SEARXNG_URL` | `http://localhost:8888` | El flag `-searxng-url` tiene precedencia. |
| `LOCAL_INDEX_PATH` | Vacío | Habilita el corpus local de solo lectura. |
| `MEMORY_URL` | Vacío | Habilita envíos opcionales a IA_Recuerdo. Vacío evita ese I/O. |
| `MEMORY_APIKEY` | Vacío | Bearer token para IA_Recuerdo. Trátelo como secreto. |
| `FETCH_USER_AGENT` | `ia-buscar/1.2 (anonymous-only)` | User-Agent del fetcher. |
| `FETCH_TIMEOUT_MS` | `30000` | Timeout total; el flag tiene precedencia. |
| `FETCH_MAX_REDIRECTS` | `5` | Máximo de redirects validados. |
| `FETCH_MAX_ATTEMPTS` | `3` | Intentos totales para fallos transitorios. |

Los flags legacy `-auth-key` y `-memory-apikey` siguen disponibles por
compatibilidad, pero pueden exponer secretos en `argv`, historiales o listados
de procesos. En despliegues use exclusivamente variables de entorno o un
secret manager que las inyecte al proceso.

Los manifests esperan `IA_BUSCAR_AUTH_KEY` en el Secret de Kubernetes
`ia-buscar` (`auth-key`) o en `/etc/ia-buscar/ia-buscar.env` para systemd.

`configs/config.example.yaml` es una referencia para operadores; el binario no
lee YAML. La fuente de verdad son los flags y variables anteriores.

## Contrato de búsqueda

Todas las herramientas de búsqueda retornan `SearchResponse`.

| Campo | Contrato |
|---|---|
| `query` | Consulta recibida. |
| `results` | Siempre es un array; nunca `null` ni omitido. |
| `strategy` | Backend real o ruta de fallback utilizada. |
| `sourcesUsed` | Conectores que produjeron la respuesta. |
| `cached` | `true` cuando la respuesta proviene del cache en proceso. |
| `partial` | `true` cuando hubo una degradación upstream. |
| `warnings` | Condiciones recuperables o información de fallback. |
| `errors` | Errores que impidieron recuperar datos. |

Estrategias relevantes:

| Estrategia | Significado |
|---|---|
| `searxng` | SearXNG respondió. |
| `searxng_reddit_index` | Resultados públicos de Reddit indexados por SearXNG. |
| `official_doc_registry_search` | Resultados validados contra el registro oficial. |
| `official_doc_web_fallback` | La documentación especializada no pudo resolver y se usó web. |
| `local_index_lexical` | El corpus local configurado fue consultado. |
| `local_index_unavailable` | No hay corpus configurado; no se usó fallback. |

## Arquitectura

```text
MCP client
   |
   +-- stdio
   `-- HTTP /mcp -- auth.Validator
          |
          +-- ConnectorManager
          |     +-- APIs especializadas
          |     +-- SearXNG
          |     +-- registro de documentación oficial
          |     `-- corpus local in-memory (opcional)
          |
          +-- FetcherService -- validación SSRF por hop
          +-- SynthesisService
          +-- cache e historial bounded en memoria
          `-- IA_Recuerdo opcional
```

El cache y el historial desaparecen al reiniciar. IA_Recuerdo es una integración
externa opcional: si `MEMORY_URL` está vacío, su cliente no realiza I/O. Si se
configura, los payloads correspondientes salen hacia ese servicio y deben
evaluarse según la política de datos del operador.

## Seguridad

- `/mcp` y `/metrics` comparten autenticación Bearer y fallan cerrados.
- `/healthz` es público y solo revela un estado mínimo.
- El fetcher valida DNS A/AAAA en cada hop y fija el dial al IP aprobado para
  reducir SSRF y DNS rebinding.
- Los redirects se validan manualmente y tienen un máximo configurable.
- Solo se reintentan timeouts, 429 y 502-504 con límites bounded.
- El índice local no accede al filesystem después del arranque ni realiza red.
- Las credenciales no deben almacenarse en archivos versionados ni flags CLI.
- La integración de memoria está deshabilitada cuando `MEMORY_URL` está vacío.

Consulte [SECURITY.md](SECURITY.md) para reportar vulnerabilidades y revisar el
modelo de exposición antes de desplegar.

## Verificación local

```bash
go test ./...
go test -race ./...
go vet ./...
go build ./...
git diff --check
```

El stack QA se usa para pruebas del repositorio y no sustituye al
[`compose.yaml`](compose.yaml) del inicio rápido:

```bash
make qa-build
make qa-up
make qa-smoke
make qa-down
```

Por compatibilidad con el Docker daemon del entorno QA, ese stack publica el
puerto `8080` en las interfaces del host y no usa una red `internal`. Ejecútelo
sólo en una máquina de desarrollo protegida. `.env.qa` está ignorado por Git y
nunca debe contener credenciales de producción.

## Versionado

El servidor informa su versión en ambas rutas de `initialize`. Los nombres de
herramientas y estrategias forman parte del contrato MCP; los cambios
incompatibles requieren un bump mayor.

| Versión | Cambio principal |
|---|---|
| 1.5.0 | Proveedor seguro y configurable para `search_local_index`. |
| 1.4.0 | Registro autoritativo para `search_doc_oficial`. |
| 1.3.2 | Preservación de previews indexadas en imágenes. |
| 1.3.1 | Selección y contexto de resultados de imágenes. |
| 1.3.0 | Descubrimiento público de Reddit mediante SearXNG. |
| 1.2.0 | Fetch resiliente, autenticación y señales de degradación. |

## Licencia

[MIT](LICENSE) © ThisCloud Services
