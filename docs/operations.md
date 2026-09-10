# Operar IA_Buscar

La operación debe distinguir tres capas: proceso MCP, SearXNG y proveedores externos. Un proceso sano puede responder correctamente mientras una fuente está vacía o degradada.

## Superficies operativas

| Ruta | Auth | Propósito |
|---|---:|---|
| `GET /healthz` | No | Liveness/readiness del proceso. |
| `POST /mcp` | Sí | Solicitudes JSON-RPC MCP. |
| `GET /mcp` | Sí | Stream SSE con keepalive cada 15 segundos. |
| `GET /metrics` | Sí | Métricas Prometheus. |

El body de `POST /mcp` se limita a 1 MiB. El servidor aplica timeouts HTTP y apaga con un plazo de 10 segundos ante `SIGINT` o `SIGTERM`.

## Verificación diaria

```bash
curl --fail http://127.0.0.1:8080/healthz
curl --fail --silent \
  -H "Authorization: Bearer ${IA_BUSCAR_AUTH_KEY}" \
  http://127.0.0.1:8080/metrics
```

Además del healthcheck, ejecuta periódicamente:

1. `initialize` y confirma versión/protocolo.
2. `tools/list` y confirma 28 herramientas.
3. Una consulta SearXNG representativa.
4. Una consulta de proveedor directo, por ejemplo Stack Overflow.
5. `search_local_index` si esa capacidad es obligatoria.

No conviertas un resultado vacío en incidente sin revisar primero `partial`, `warnings`, `errors` y `strategy`.

## Métricas

La superficie Prometheus incluye collectors de Go/proceso y estas series propias:

| Métrica | Labels | Significado |
|---|---|---|
| `ia_buscar_http_requests_total` | `method`, `path`, `status` | Registrada, pero sin productor conectado en el runtime actual. |
| `ia_buscar_search_latency_seconds` | `source` | Registrada, pero sin productor conectado en el runtime actual. |
| `ia_buscar_search_degraded_total` | `source`, `kind` | Respuestas con degradación upstream; instrumentada actualmente. |

Las dos primeras series pueden no aparecer porque el runtime todavía no llama a sus métodos de registro. No construyas alertas sobre ellas hasta incorporar y verificar esa instrumentación.

Alertas útiles:

- Proceso o probe no disponible.
- Aumento de `ia_buscar_search_degraded_total` por fuente.
- Latencia o errores medidos por el proxy, mientras las series internas sigan sin instrumentación.
- Reinicios repetidos o errores de carga del índice local.

Define umbrales con datos de tu entorno; el repositorio no promete SLOs universales.

## Logs

Docker Compose:

```bash
docker compose logs --tail=200 -f ia-buscar searxng
```

systemd:

```bash
journalctl -u ia-buscar --since '30 minutes ago' -f
```

Kubernetes:

```bash
kubectl logs deployment/ia-buscar --tail=200 -f
```

Los logs pueden incluir consultas y URL. Trata esa salida como dato operativo sensible y aplica retención, acceso y redacción apropiados.

## Estado por proceso

La caché y el historial no se comparten ni persisten:

- Reiniciar vacía ambos.
- Dos réplicas pueden devolver distinto estado de caché.
- `get_search_history` no es un audit log.
- La caché elimina elementos expirados de forma oportunista y no limita las entradas activas.
- IA_Recuerdo es best-effort y síncrono; un fallo no cambia el resultado, pero puede sumar hasta 10 segundos de latencia.

Si necesitas auditoría o caché distribuida, debes agregar una capa externa. No dependas de estas estructuras en memoria para recuperación.

## Actualizar Compose

```bash
git fetch --all --prune
git checkout '<new-reviewed-sha>'
docker compose build --pull ia-buscar
docker compose up -d
curl --fail http://127.0.0.1:8080/healthz
```

Después ejecuta `initialize`, `tools/list` y consultas representativas. Conserva el SHA y la imagen anteriores hasta cerrar la ventana de observación.

## Rollback de Compose

```bash
git checkout '<previous-known-good-sha>'
docker compose build ia-buscar
docker compose up -d
curl --fail http://127.0.0.1:8080/healthz
```

Verifica también la identidad del contenedor y el contrato MCP. Un healthcheck correcto no demuestra que el binario sea la revisión esperada.

Para Kubernetes, restaura la referencia inmutable anterior. Para systemd, restaura juntos binario, unidad renderizada, `VERSION` e `IMAGE`; los prechecks deben validar esa combinación.

## Backup

Respalda fuera del repositorio:

- Configuración del proxy y firewall.
- Referencias de imagen/SHA desplegadas.
- Archivos de entorno cifrados o gestionados por secrets manager.
- Corpus de `LOCAL_INDEX_PATH` y su checksum.
- Configuración personalizada de SearXNG.

No hace falta respaldar caché ni historial en memoria.

## Respuesta a incidentes

1. Aisla el endpoint externo sin destruir logs ni artefactos.
2. Rota `IA_BUSCAR_AUTH_KEY` y cualquier credencial potencialmente expuesta.
3. Identifica SHA, imagen, configuración y hora UTC del proceso afectado.
4. Separa fallo MCP, fallo SearXNG y fallo de proveedor.
5. Revierte a una revisión conocida si reduce riesgo.
6. Conserva evidencia redactada y reporta vulnerabilidades por el canal privado.

## Diagnóstico rápido

| Síntoma | Comprobación |
|---|---|
| `401` en MCP | Clave, header y precedencia de `X-Api-Key`. |
| `413` | Body JSON-RPC mayor a 1 MiB. |
| Búsqueda vacía | `strategy`, `partial`, `warnings`, `errors`, logs de SearXNG. |
| Local index no disponible | `LOCAL_INDEX_PATH`, permisos, schema y logs de arranque. |
| Faltan series propias | Confirmar instrumentación: hoy solo degradación tiene productores conectados. |
| Reinicio en loop | Puerto ocupado, corpus inválido, SearXNG o prechecks de identidad. |

Consulta [Configuración](configuration.md) para los valores exactos y [Seguridad](../SECURITY.md) para el modelo de amenaza.
