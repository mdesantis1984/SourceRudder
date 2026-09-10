# Configuración

IA_Buscar combina flags de línea de comandos, variables de entorno y archivos de despliegue. Esta referencia describe el comportamiento efectivo del binario `1.5.0`.

## Flags del binario

| Flag | Default | Uso |
|---|---|---|
| `-transport` | `stdio` | `stdio` o `http`. Cualquier otro valor aborta el arranque. |
| `-http-addr` | `:8080` | Dirección de escucha para HTTP. |
| `-searxng-url` | `SEARXNG_URL` o `http://localhost:8888` | Base URL de SearXNG. |
| `-cache-ttl` | `300` | TTL de la caché en proceso, en segundos. |
| `-fetch-timeout-ms` | `30000` | Timeout total del fetcher, en milisegundos. |
| `-auth-key` | vacío | Clave HTTP; tiene precedencia sobre `IA_BUSCAR_AUTH_KEY`. |
| `-memory-url` | vacío | URL exacta que recibe observaciones por `POST`. |
| `-memory-apikey` | vacío | Bearer token para IA_Recuerdo. |
| `-local-index-path` | vacío | Corpus JSON del índice local. |
| `-reddit-user-agent` | vacío | Obsoleto y sin efecto. |
| `-reddit-base-url` | vacío | Obsoleto y sin efecto. |

Usa `-auth-key` solo para depuración puntual. En servidores, `IA_BUSCAR_AUTH_KEY` evita exponer la clave en argv, historial de shell y listados de procesos.

## Variables de entorno

| Variable | Default | Efecto |
|---|---|---|
| `IA_BUSCAR_AUTH_KEY` | vacío | Clave de `/mcp` y `/metrics`; vacío significa rechazo total. |
| `SEARXNG_URL` | `http://localhost:8888` | Backend de búsqueda SearXNG. |
| `FETCH_USER_AGENT` | `ia-buscar/1.2 (anonymous-only)` | User-Agent del fetcher. |
| `FETCH_MAX_REDIRECTS` | `5` | Máximo de redirecciones validadas. |
| `FETCH_MAX_ATTEMPTS` | `3` | Máximo de intentos por fetch. |
| `FETCH_TIMEOUT_MS` | `30000` | Declarada por manifests; ver la advertencia siguiente. |
| `MEMORY_URL` | vacío | URL de IA_Recuerdo; vacío deshabilita el envío. |
| `MEMORY_APIKEY` | vacío | Bearer token de IA_Recuerdo. |
| `LOCAL_INDEX_PATH` | vacío | Habilita `search_local_index`. |
| `REDDIT_USER_AGENT` | vacío | Compatibilidad obsoleta, sin efecto. |
| `REDDIT_BASE_URL` | vacío | Compatibilidad obsoleta, sin efecto. |

### Advertencia sobre `FETCH_TIMEOUT_MS`

En el binario actual, el flag `-fetch-timeout-ms` tiene como valor predeterminado `30000` y siempre prevalece sobre `FETCH_TIMEOUT_MS`. Por eso la variable no puede modificar el timeout por sí sola. Para cambiarlo hoy, pasa el flag explícitamente:

```bash
./bin/ia-buscar -transport http -fetch-timeout-ms 45000
```

Los manifiestos conservan `FETCH_TIMEOUT_MS` por compatibilidad, pero no debe considerarse un override efectivo hasta que cambie la implementación.

## Precedencia

Cuando un ajuste admite flag y entorno:

1. Flag no vacío.
2. Variable de entorno.
3. Valor predeterminado compilado.

Esta regla aplica a SearXNG, autenticación, IA_Recuerdo e índice local. La excepción actual es `FETCH_TIMEOUT_MS`, detallada arriba.

La clave leída desde `IA_BUSCAR_AUTH_KEY` elimina espacios iniciales y finales. El valor de `-auth-key` se conserva exactamente como fue recibido.

## Archivo `.env` de Compose

El Compose principal consume:

| Variable | Requerida | Uso |
|---|---:|---|
| `IA_BUSCAR_AUTH_KEY` | Sí | Auth HTTP de IA_Buscar. |
| `SEARXNG_SECRET` | Sí | Secreto interno de SearXNG. |
| `IA_BUSCAR_PORT` | No | Puerto loopback; default `8080`. |
| `SEARXNG_PORT` | No | Puerto loopback; default `8888`. |
| `MEMORY_URL` | No | Integración IA_Recuerdo. |
| `MEMORY_APIKEY` | No | Auth de IA_Recuerdo. |

`.env.example` contiene las claves esperadas, no credenciales utilizables. Mantén `.env` fuera de Git y con permisos restrictivos.

## Autenticación HTTP

Las rutas protegidas aceptan uno de estos headers:

```http
X-Api-Key: <clave>
```

```http
Authorization: Bearer <clave>
```

Si ambos aparecen, `X-Api-Key` tiene precedencia. Una clave ausente, vacía o incorrecta responde `401`. `/healthz` es la única ruta pública.

El transporte `stdio` no atraviesa este middleware.

## SearXNG

Estas herramientas requieren SearXNG:

- `search_web`
- `search_news`
- `search_academic`
- `search_reddit`
- `search_youtube`
- `search_images`
- El fallback de `search_doc_oficial`

El Compose usa `http://searxng:8080` dentro de la red. Una ejecución nativa usa `http://localhost:8888` por defecto.

Un `/healthz` sano en IA_Buscar no garantiza que SearXNG ni sus engines estén disponibles. Las respuestas de herramientas comunican degradación mediante `partial`, `warnings` y `errors`.

## Índice local

Habilítalo con flag o entorno:

```bash
chmod 600 /srv/ia-buscar/corpus.json
LOCAL_INDEX_PATH=/srv/ia-buscar/corpus.json ./bin/ia-buscar -transport stdio
```

Puedes empezar desde [`configs/local-index.example.json`](../configs/local-index.example.json).

Contrato del corpus:

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

Reglas de carga:

- Archivo regular, no symlink, y no escribible por grupo u otros.
- Máximo 4 MiB y 10.000 documentos.
- JSON estricto: versión `1`, sin campos desconocidos ni contenido posterior.
- `id` único de 1 a 128 caracteres ASCII: letras, números, `.`, `_` o `-`.
- `title` requerido; `url`, `snippet`, `author` y `tags` opcionales.
- URL absoluta `http`, `https` o `local-index`, sin credenciales, query ni fragmento.
- Sin URL, el loader genera `local-index://document/<id>`.

Un corpus inválido aborta el arranque. La búsqueda exige que todos los términos de la consulta aparezcan en `title`, `tags`, `snippet` o `id`; luego ordena por un score léxico determinista. Devuelve como máximo 50 resultados.

## IA_Recuerdo

`MEMORY_URL` es opcional. Cuando está vacío, el cliente no hace requests y las búsquedas siguen funcionando.

Cuando está configurado, IA_Buscar envía por `POST`:

```json
{
  "query": "consulta completada",
  "source": "nombre-del-conector"
}
```

Si `MEMORY_APIKEY` no está vacío, lo envía como Bearer token. El timeout es 10 segundos. Los errores de memoria son best-effort y no convierten una búsqueda correcta en error MCP. El envío es síncrono: un endpoint lento o inaccesible puede agregar hasta 10 segundos a una respuesta de búsqueda.

Usa una URL de ingestión completa y confirma el contrato del servicio receptor. IA_Buscar no agrega automáticamente un path a `MEMORY_URL`.

## Caché e historial

- Caché: map en memoria con TTL de 300 segundos por default.
- Limpieza: oportunista durante `Get`, `Set` y listado de claves.
- Capacidad: no existe un límite para entradas activas.
- Historial: ring buffer de 100 búsquedas, newest-first.
- Persistencia: ninguna; todo se pierde al reiniciar.

En despliegues con varias réplicas, cada proceso conserva estado distinto. No uses estas superficies como almacenamiento distribuido ni auditoría permanente.
