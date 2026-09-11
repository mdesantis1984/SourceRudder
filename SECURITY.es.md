[English](SECURITY.md) | [Español](SECURITY.es.md)

# Política de seguridad de SourceRudder

## Versiones soportadas

| Línea de versión | Estado |
|---|---|
| SourceRudder 2.0 | Línea de release actual; las correcciones de seguridad se evalúan contra el último release 2.0 y `main`. |
| Releases 1.x históricos | Solo históricos; no reciben soporte automático de seguridad. |

Los forks, despliegues modificados y snapshots sin digest quedan fuera del soporte automático.

## Reportar una vulnerabilidad

No divulgues vulnerabilidades explotables en issues, discusiones, pull requests ni otros canales públicos.

Use el [formulario privado para reportar vulnerabilidades](https://github.com/mdesantis1984/SourceRudder/security/advisories/new)
de GitHub. Si el formulario no está disponible, contacte a un maintainer por un
canal privado confiable para establecer un intercambio seguro. No incluya
detalles de explotación en un issue, discussion, pull request ni otro canal
compartido.

Incluya versión, commit o imagen afectados; pasos seguros de reproducción; impacto observado; logs redactados; y mitigaciones conocidas. No incluya secretos. La prioridad de respuesta depende del impacto, la explotabilidad y la disponibilidad de una mitigación segura.

## Modelo de seguridad

- La autenticación falla cerrada: la ausencia o el valor vacío de `SOURCERUDDER_AUTH_KEY` no permite acceso anónimo a endpoints HTTP protegidos.
- Las credenciales se comparan mediante MAC y comparación en tiempo constante. `X-Api-Key` tiene precedencia sobre `Authorization: Bearer`; `/healthz` permanece como endpoint público de probes.
- El fetch acepta solo `http` y `https`, valida cada redirección, bloquea loopback y otros destinos no públicos, y fija la conexión a la IP validada para reducir SSRF y exposición a DNS rebinding.
- El tamaño de respuesta, las redirecciones, los intentos y el trabajo concurrente están acotados para contener el uso de recursos.
- Los secretos deben estar en variables de entorno protegidas o un gestor de secretos, nunca en Git, historial de shell, argumentos visibles, logs ni cuerpos de issues.
- Las imágenes de despliegue deben estar fijadas por digest inmutable. Las definiciones Compose de QA establecen límites de memoria, memory-swap, CPU, PID, reinicio y rotación de logs; conserve controles equivalentes en despliegues administrados por operadores.

## Responsabilidades del operador

- Use claves aleatorias y distintas por entorno; rote inmediatamente una clave que pudiera haberse expuesto.
- Mantenga el servicio en loopback o una red privada y termine TLS en un proxy confiable.
- Proteja `/metrics` con el mismo rigor que `/mcp`.
- Trate consultas, URLs, logs y contenido recuperado como datos operativos sensibles.
- Revise `warnings`, `errors`, `partial` y `strategy`; un resultado degradado no es evidencia completa.
- Mantenga actualizados Go, imágenes de contenedor, SearXNG, host y dependencias del proyecto.

## Divulgación coordinada

Publique detalles técnicos solo después de que exista una corrección o mitigación y se acuerde una fecha de divulgación con la persona reportante. Los créditos se coordinan con esa persona y pueden omitirse si lo solicita.
