# Política de seguridad

## Versiones soportadas

El proyecto corrige vulnerabilidades en la línea de desarrollo vigente. Los snapshots antiguos, forks y despliegues modificados no reciben soporte automático. Antes de reportar, confirma si el problema sigue presente en el commit más reciente de `main`.

## Reportar una vulnerabilidad

No publiques una vulnerabilidad explotable en un issue, una discusión, un pull request ni un canal público.

Usa un [GitHub Security Advisory privado](https://github.com/mdesantis1984/IA_Buscar/security/advisories/new) e incluye:

- Versión, commit o imagen afectada.
- Configuración y transporte utilizados, sin claves ni secretos.
- Pasos mínimos para reproducir el problema.
- Impacto observado y alcance estimado.
- Prueba de concepto segura, logs redactados y mitigaciones conocidas.

El equipo confirmará la recepción y coordinará la investigación por el mismo canal privado. No se promete un plazo fijo: la prioridad depende del impacto, la explotabilidad y la disponibilidad de una mitigación segura.

## Alcance de seguridad

Son especialmente relevantes los reportes sobre:

- Bypass de autenticación en `/mcp` o `/metrics`.
- Exposición de claves, tokens, consultas o contenido recuperado.
- SSRF, DNS rebinding o bypass de la validación de redirección.
- Ejecución arbitraria, escritura fuera del alcance esperado o escalada de privilegios.
- Denegación de servicio que eluda los límites de solicitudes, respuestas o concurrencia.
- Compromiso de dependencias, imágenes, workflows o artefactos de release.

Los errores funcionales sin impacto de seguridad deben reportarse como issues normales cuando el proyecto habilite ese flujo público.

## Modelo de protección actual

- HTTP falla cerrado: una clave vacía no habilita acceso anónimo.
- La comparación de credenciales usa MAC y comparación en tiempo constante.
- `X-Api-Key` tiene precedencia sobre `Authorization: Bearer`.
- `/healthz` queda abierto para probes; no incluye estado interno.
- Fetch acepta solo `http` y `https`, valida cada salto y bloquea destinos no públicos.
- El fetcher fija la conexión a la IP validada, limita respuestas a 4 MiB y permite hasta ocho fetches concurrentes.
- La imagen final corre desde `scratch` con UID/GID `65532` y sin shell.
- El Compose principal elimina capabilities y monta el contenedor de IA_Buscar como read-only.

## Responsabilidades del operador

- Genera claves aleatorias distintas por entorno y rótalas cuando exista sospecha de filtración.
- Entrega secretos por variables protegidas o un gestor de secretos, nunca por Git ni por argumentos visibles del proceso.
- Mantén IA_Buscar en loopback o una red privada y termina TLS en un proxy confiable.
- Restringe `/metrics` igual que `/mcp`.
- Revisa el corpus local: puede influir en respuestas aunque el loader valide su forma y permisos.
- Mantén actualizados Go, Docker, SearXNG, el host y las dependencias del proyecto.
- Revisa `warnings`, `errors`, `partial` y `strategy`; una respuesta degradada no debe presentarse como evidencia completa.

## Divulgación coordinada

Publica detalles técnicos solo después de que exista una corrección o mitigación y el equipo haya acordado una fecha de divulgación. Los créditos se coordinan con la persona reportante y pueden omitirse si así lo solicita.
