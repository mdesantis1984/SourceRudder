[English](architecture.md) | [Español](architecture.es.md)

# Arquitectura de SourceRudder 2.0

SourceRudder 2.0 está **sin publicar**. Es un servicio de investigación MCP que mantiene estables sus contratos externos de red mientras enruta solicitudes hacia capacidades de investigación acotadas y observables.

## Recorrido de una solicitud

1. Un cliente MCP se conecta mediante transporte `stdio` o HTTP/SSE autenticado.
2. El servidor valida el contrato estable JSON-RPC/MCP y envía la solicitud al planificador.
3. El planificador selecciona un conector mediante el gestor de conectores; los resultados pueden usar la caché en proceso.
4. Los resultados se normalizan en envolventes estables, se registran en el historial acotado en proceso y pueden pasar opcionalmente por síntesis.

## Componentes

| Componente | Responsabilidad |
|---|---|
| Transportes MCP | `stdio` para clientes locales; HTTP/SSE para clientes de red. HTTP expone salud y métricas aparte de las llamadas MCP autenticadas. |
| Planificador y gestor de conectores | Eligen la ruta de investigación solicitada o inferida, ejecutan conectores y preservan fuente, estrategia, advertencias e información de resultados parciales. |
| Conectores | Acceden a SearXNG y a fuentes directas de paquetes, hosting de código, comunidad y registros sin cambiar la forma de resultados expuesta al cliente. |
| Caché e historial | Mantienen entradas en memoria por proceso y un historial acotado, de más reciente a más antiguo. Ninguno es durable ni se comparte entre réplicas. |
| Fetch y extracción | Obtienen URL públicas solo después de que los controles SSRF validan destinos, redirecciones y límites de respuesta; la extracción devuelve contenido acotado. |
| Síntesis | Resume o compara conjuntos de resultados normalizados sin reemplazar la evidencia de las fuentes. |
| Índice local | Busca opcionalmente un corpus JSON inmutable y curado por el operador, con ranking léxico determinista y sin red ni rastreo del sistema de archivos. |
| IA_Recuerdo | Integración opcional best-effort para observaciones de búsquedas completadas; no es necesaria para que una búsqueda sea exitosa. |
| Métricas | Expone métricas compatibles con Prometheus para observación operativa del proceso y del servicio. |

## Límites de contrato y seguridad

- Las herramientas MCP usan envolventes estables; los clientes deben inspeccionar `strategy`, `partial`, `warnings` y `errors` en lugar de tratar un resultado vacío como un fallo de transporte.
- La caché, el historial y el reporte opcional a IA_Recuerdo son ayudas operativas, no un sistema distribuido de auditoría.
- El fetch está separado deliberadamente de la búsqueda. Las protecciones SSRF se aplican antes de acceder a la red y a través de las redirecciones.
- La entrada del índice local se valida al iniciar y sigue siendo un dato administrado por el operador.

## Implicación operativa

La salud prueba que el proceso SourceRudder es accesible. No prueba que SearXNG, los proveedores directos, un corpus local o IA_Recuerdo opcional estén disponibles. Monitoree de forma independiente la salud del transporte, las señales de resultados degradados y la evidencia específica de cada proveedor.
