[English](development.md) | [Español](development.es.md)

# Guía de desarrollo de SourceRudder

Esta guía define el camino de desarrollo y verificación local para SourceRudder 2.0. No autoriza un despliegue a producción.

## Preparación local

```bash
git clone https://github.com/mdesantis1984/SourceRudder.git
cd SourceRudder
go mod download
make build
./bin/sourcerudder -transport stdio
```

El módulo es `github.com/mdesantis1984/SourceRudder`; el binario es `sourcerudder`. Use `SOURCERUDDER_AUTH_KEY` para la autenticación HTTP en lugar de colocar un secreto en argumentos de comando:

```bash
export SOURCERUDDER_AUTH_KEY='replace-with-a-local-secret'
./bin/sourcerudder -transport http -http-addr :8080
```

## Mapa de paquetes

| Ruta | Responsabilidad |
|---|---|
| `cmd/sourcerudder` | Punto de entrada del binario, flags, transportes y wiring de servicios. |
| `internal/mcp` | Servidor MCP, transportes HTTP y stdio, y endpoints protegidos. |
| `internal/auth` | Validación HTTP de credenciales con fallo cerrado. |
| `internal/connectors` | Conectores de proveedores de búsqueda y documentación oficial. |
| `internal/fetch` | Recuperación remota acotada y protecciones SSRF. |
| `internal/search` | Planificación y administración de conectores. |
| `internal/cache`, `internal/memory`, `internal/observability` | Servicios de soporte del runtime. |
| `pkg/types` | Tipos Go públicos compartidos de requests y responses. |
| `deploy/qa`, `scripts/quality` | Stack QA aislado y baseline de calidad Python. |

## Comandos de verificación

Use un test focalizado durante la iteración y luego ejecute los controles completos:

```bash
# Test focalizado de paquete
go test ./internal/auth -run TestValidator

# Suite Go completa
go build ./...
go vet ./...
go test ./...
go test -race ./...

# Suite de calidad Python
python3 -m unittest discover -s scripts/quality -p 'test_*.py' -v

# Validación de esquema e interpolación Compose; no inicia contenedores
docker compose -f deploy/qa/docker-compose.yml config --quiet
```

Ejecute los tests shell independientes de manifiestos Kubernetes y QA cuando el alcance afectado lo requiera:

```bash
bash tests/k8s/k8s_deployment_test.sh
bash tests/qa/qa_scripts_test.sh
```

Ejecute el stack QA local solo cuando el cambio lo afecte. El ciclo está deliberadamente limitado a `sourcerudder-qa`:

```bash
make qa-up
make qa-smoke
make qa-down
```

Para el baseline de búsqueda en vivo, revise primero el manifiesto y escriba la evidencia fuera del repositorio:

```bash
python3 scripts/quality/live_baseline.py manifest
python3 scripts/quality/live_baseline.py run --output /tmp/sourcerudder-quality/baseline.json
```

## Gates de release y revisión

Antes del merge, ejecute los controles aplicables y registre en el pull request los comandos realmente ejecutados. `make release-gate` verifica un worktree limpio, el presupuesto de líneas autoradas y `go build`, `go vet`, tests completos y tests de race. Es un gate de calidad para merge, no un comando de despliegue.

Mantenga los cambios como unidades de trabajo revisables: un comportamiento coherente por commit o pull request, con sus tests y documentación. No combine refactors no relacionados, salida generada ni formateo masivo. El release gate aplica un presupuesto de 1.000 líneas autoradas salvo que exista una excepción aprobada y registrada.

## Paridad documental

Las guías públicas de contribución, seguridad y desarrollo son bilingües. Cada documento emparejado comienza con enlaces visibles `English | Español`. Actualice ambos archivos de idioma en el mismo cambio, conserve un significado equivalente y mantenga sin cambios los contratos externos: nombres de herramientas MCP, campos JSON, flags, `sourcerudder` y `SOURCERUDDER_AUTH_KEY` no se traducen.

## Límite de rollback

La verificación de desarrollo nunca despliega a producción. Si un operador necesita recuperar un despliegue, debe restaurar el digest inmutable de imagen y la revisión de código previamente aprobados, y luego verificar `/healthz`, inicialización MCP y requests representativos en su entorno controlado. Conserve el digest y la revisión anteriores hasta que cierre la ventana de observación; un endpoint sano por sí solo no prueba que esté ejecutándose el binario esperado.
