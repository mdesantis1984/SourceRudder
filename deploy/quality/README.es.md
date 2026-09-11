[English](README.md) | [Español](README.es.md)

# Línea base local de búsquedas en vivo de SourceRudder 2.0

SourceRudder 2.0 está **sin publicar**. Este flujo de calidad aislado recopila evidencia revisable de búsquedas en vivo; no es un despliegue de producción ni una calificación automática de relevancia.

Inspeccione el manifiesto fijo de nueve casos antes de cualquier ejecución en vivo:

```bash
python3 scripts/quality/live_baseline.py manifest
```

Ejecute la línea base aislada y escriba evidencia fuera del repositorio:

```bash
python3 scripts/quality/live_baseline.py run --output /tmp/sourcerudder-quality/baseline.json
```

Para precalentar el primer caso del manifiesto para una herramienta específica y conservar las 27 llamadas iniciales, use `--warm-tool`; el valor predeterminado sigue siendo `search_web`:

```bash
python3 scripts/quality/live_baseline.py run --warm-tool search_reddit --output /tmp/sourcerudder-quality/reddit.json
```

El ejecutor genera un token de autenticación local en memoria, rechaza recursos `sourcerudder-quality` preexistentes y luego inicia y elimina solamente ese proyecto. Nunca califica la relevancia automáticamente: preserva URL y solo acota snippets. Las respuestas parciales son evidencia, no fallos automáticos.

El informe JSON se escribe aun si fallan el inicio, la ejecución o la limpieza. El ejecutor termina con código distinto de cero ante fallos de ciclo de vida, transporte, protocolo o limpieza; el fallback del proveedor, los resultados parciales y las capacidades opcionales no disponibles permanecen como evidencia revisable. El texto de error se redacta por token, los cuerpos de respuesta se limitan a 2 MiB y la identidad de la fuente registra hashes más un recuento de archivos no rastreados sin guardar nombres ni contenido locales.

El bridge normal da intencionalmente a ambos servicios salida pública para llamadas directas a Reddit y StackOverflow; no afirma bloquear la LAN. No monta configuración de producción ni realiza probes de producción. Esta línea base usa la configuración predeterminada de SearXNG del release fijado, no una configuración de producción probada. La imagen amd64 fijada es `2026.9.3-a1144dda3@sha256:0b8a200aaa0ec63e6595e18816c19f12d0b9d38326b9fc4123fc1fa80a438776`.
La API oficial de tags de Docker Hub informó esa etiqueta y digest el 2026-09-05.
