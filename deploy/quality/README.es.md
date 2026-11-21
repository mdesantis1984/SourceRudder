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

## Corpus de costo y calidad de resultados de búsqueda

`scripts/quality/measure_corpus.py` mide cuánto cuesta cada respuesta de búsqueda
y si sus primeros resultados son buenos, para comparar un cambio (por ejemplo el
formato compacto o la paginación de result sets) con el código anterior. El
corpus fijo es `scripts/quality/corpus/search_quality.json`: 20 casos sobre
`search_web`, `search_github`, `search_stackoverflow`, `search_npm`,
`search_pypi`, `search_doc_oficial` y `search_news`. Cada caso tiene `arguments`,
un `k` y subcadenas de URL en `expect_any`; cuenta como acierto si alguna de las
primeras `k` URLs la contiene (sin distinguir mayúsculas). Las expectativas son
auditables por una persona; los casos de noticias son volátiles, así que revise
un fallo antes de tratarlo como regresión.

```bash
python3 scripts/quality/measure_corpus.py validate
```

El runner reutiliza el transporte solo-loopback de `live_baseline.py` y necesita
un stack ya levantado con una clave que usted elige. Nunca guarde la clave en un
archivo ni en un reporte; los archivos de evidencia no la contienen.

```bash
export SOURCERUDDER_QUALITY_AUTH_KEY="$(openssl rand -hex 32)"
docker compose -f deploy/quality/docker-compose.yml --project-name sourcerudder-quality up --build --wait
python3 scripts/quality/measure_corpus.py run --output /tmp/sourcerudder-quality/before.json
python3 scripts/quality/measure_corpus.py run --format compact --detail snippets --output /tmp/sourcerudder-quality/after.json
docker compose -f deploy/quality/docker-compose.yml --project-name sourcerudder-quality down --remove-orphans
python3 scripts/quality/measure_corpus.py compare /tmp/sourcerudder-quality/before.json /tmp/sourcerudder-quality/after.json
```

No lo ejecute mientras corre `live_baseline.py run`: ambos usan el proyecto
`sourcerudder-quality` y el puerto 18080. Por caso, la evidencia registra los
bytes del texto visible para el modelo, una **estimación** de tokens
(`ceil(caracteres / 4)`, no un tokenizador real), la latencia, la cantidad de
resultados, partial/warnings/errors, hit@K y la posición del primer acierto.
`--repeat N` repite cada llamada; la latencia y la calidad salen de la primera
llamada (en frío) porque las siguientes pueden venir de la caché. Con
`--format compact` los resultados se leen de las líneas `<n>. <título> | <url> ...`
y de las líneas `warning:`/`error:`/`partial: true`. `compare` informa bytes y
tokens totales y medianos, latencia p50/p95 (rango más cercano), tasa de hit@K y
posición media del primer acierto, y sale con 1 si la tasa de hit@K baja (la
calidad primero), 0 en otro caso y 2 si la entrada no sirve.
