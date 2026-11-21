[English](architecture.md) | [Español](architecture.es.md)

# Arquitectura de SourceRudder 2.0

SourceRudder 2.0 está **sin publicar**. Es un servicio de investigación MCP que mantiene estables sus contratos externos de red mientras enruta solicitudes hacia capacidades de investigación acotadas y observables.

## Recorrido de una solicitud

1. Un cliente MCP se conecta mediante transporte `stdio` o HTTP/SSE autenticado.
2. El servidor valida el contrato estable JSON-RPC/MCP y envía la solicitud al planificador.
3. El planificador selecciona un conector mediante el gestor de conectores; los resultados pueden usar la caché en proceso.
4. Los resultados se normalizan en envolventes estables, se registran en el historial acotado en proceso y pueden pasar opcionalmente por síntesis.
5. El handler de búsqueda guarda la lista completa de resultados del conector en el almacén y devuelve la página pedida (`maxResults`, elegido por quien llama) con `resultSetId`, `total` y, solo mientras quedan resultados, un `nextCursor` opaco. Una llamada con `cursor` omite los pasos 2-4 y lee el set guardado localmente. Todo resultado de `tools/call` lo arma un único helper: `structuredContent` trae la respuesta tipada completa (un objeto JSON) y `content` trae un bloque de texto, JSON minificado por defecto o texto compacto con `format=compact`.

## Componentes

| Componente | Responsabilidad |
|---|---|
| Transportes MCP | `stdio` para clientes locales; HTTP/SSE para clientes de red. HTTP expone salud y métricas aparte de las llamadas MCP autenticadas. |
| Planificador y gestor de conectores | Eligen la ruta de investigación solicitada o inferida, ejecutan conectores y preservan fuente, estrategia, advertencias e información de resultados parciales. |
| Conectores | Acceden a SearXNG y a fuentes directas de paquetes, hosting de código, comunidad y registros sin cambiar la forma de resultados expuesta al cliente. |
| Almacén de sets de resultados | `internal/resultset` guarda en memoria la lista completa de resultados de cada búsqueda bajo un ID opaco (`rs_` + 16 caracteres base32 de `crypto/rand`) para que el cliente la pagine con `offset`/`resultSetId` sin otra petición al proveedor. Tiene su propio lock, TTL de 15 minutos, como máximo 64 sets y 2.000 resultados en total, y expulsa primero los más antiguos. Por proceso, no durable y no compartido entre réplicas. |
| Caché e historial | Mantienen entradas en memoria por proceso y un historial acotado, de más reciente a más antiguo. Ninguno es durable ni se comparte entre réplicas. |
| Fetch y extracción | Obtienen URL públicas solo después de que los controles SSRF validan destinos, redirecciones y límites de respuesta; la extracción devuelve contenido acotado. |
| Síntesis | Resume o compara conjuntos de resultados normalizados sin reemplazar la evidencia de las fuentes. |
| Índice local | Busca opcionalmente un corpus JSON inmutable y curado por el operador, con ranking léxico determinista y sin red ni rastreo del sistema de archivos. |
| Métricas | Expone métricas compatibles con Prometheus para observación operativa del proceso y del servicio. |

## Límites de contrato y seguridad

- Las herramientas MCP usan envolventes estables; los clientes deben inspeccionar `strategy`, `partial`, `warnings` y `errors` en lugar de tratar un resultado vacío como un fallo de transporte.
- Los resultados mediante SearXNG exponen un campo opcional `engine` con el nombre del motor de origen cuando está disponible. Agrega procedencia sin cambiar los valores existentes de `source`; los nombres ausentes se omiten.
- La caché y el historial son ayudas operativas en proceso, no un sistema distribuido de auditoría. La caché de respuestas SearXNG usa claves versionadas que incluyen la solicitud y limita la vida de respuestas degradadas y vacías al menor valor entre 30 segundos y el TTL configurado.
- Los conectores no recortan por cuenta del cliente: conservan todos los resultados únicos que ya obtuvieron y el handler aplica el tamaño de página. La paginación nunca cambia el orden. Una búsqueda hace las mismas peticiones al proveedor que antes; `search_web` mantiene su única petición a la página 2, evaluada contra `maxResults`.
- El fetch está separado deliberadamente de la búsqueda. Las protecciones SSRF se aplican antes de acceder a la red y a través de las redirecciones.
- La entrada del índice local se valida al iniciar y sigue siendo un dato administrado por el operador.

## Implicación operativa

La salud prueba que el proceso SourceRudder es accesible. No prueba que SearXNG, los proveedores directos o un corpus local estén disponibles. Monitoree de forma independiente la salud del transporte, las señales de resultados degradados y la evidencia específica de cada proveedor.
