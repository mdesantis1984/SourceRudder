[English](CONTRIBUTING.md) | [Español](CONTRIBUTING.es.md)

# Contribuir a SourceRudder

Las contribuciones deben ser pequeñas, verificables y estar vinculadas a un issue concreto. Para el desarrollo local y QA, consulte la [guía de desarrollo](docs/development.es.md).

## Antes de programar

1. Revise si ya existe un issue; abra o solicite uno para trabajo significativo.
2. Acuerde alcance, comportamiento observable y criterios de aceptación.
3. Reporte vulnerabilidades de forma privada según [SECURITY.es.md](SECURITY.es.md).

Los maintainers pueden exceptuar correcciones triviales de texto de la regla de issue previo.

## Preparar el entorno

```bash
git clone https://github.com/mdesantis1984/SourceRudder.git
cd SourceRudder
go mod download
go test ./...
```

SourceRudder 2.0 usa Go `1.26.6`. Docker Compose v2 y Python 3 son necesarios para las suites de QA y calidad.

## Verificar el cambio

Ejecute el control más acotado durante la iteración y el conjunto completo antes de pedir revisión:

```bash
# Test Go focalizado
go test ./internal/auth -run TestValidator

# Controles Go completos
go build ./...
go vet ./...
go test ./...
go test -race ./...

# Tests de calidad Python
python3 -m unittest discover -s scripts/quality -p 'test_*.py' -v

# Validar la definición Compose de QA sin iniciar contenedores
docker compose -f deploy/qa/docker-compose.yml config --quiet
```

Ejecute los tests shell independientes de manifiestos Kubernetes y QA cuando el alcance afectado lo requiera:

```bash
bash tests/k8s/k8s_deployment_test.sh
bash tests/qa/qa_scripts_test.sh
```

Cuando un cambio afecte el stack QA local, ejecute también su ciclo acotado:

```bash
make qa-up
make qa-smoke
make qa-down
```

Los scripts de QA solo administran el proyecto `sourcerudder-qa`. Son verificación local, no un despliegue a producción.

## Ramas, commits y pull requests

Use ramas descriptivas como `feat/local-index-ranking` o `fix/http-auth-header`. Mantenga cada commit como una unidad de trabajo revisable: incluya sus tests y documentación, y no mezcle refactors no relacionados ni formateo masivo.

Use Conventional Commits:

```text
feat(search): add provider filter
fix(auth): reject empty bearer tokens
docs(clients): clarify remote headers
```

No agregues trailers `Co-Authored-By`, atribución automática, secretos, salidas locales, binarios ni evidencia temporal.

Los pull requests deben vincular el issue aprobado (por ejemplo, `Closes #123`), explicar el cambio de comportamiento, listar los comandos realmente ejecutados e indicar riesgos, límites y pasos de rollback. El release gate aplica un presupuesto de 1.000 líneas autoradas contra el merge base; divida el trabajo mayor en unidades revisables salvo que exista una excepción aprobada y registrada.

Espere a CI y al release gate antes de solicitar merge. Este repositorio documenta y verifica cambios; no autoriza ni realiza despliegues a producción.

## Compatibilidad y licencia

Conserve exactamente los nombres de herramientas MCP, campos JSON, flags, variables de entorno y demás contratos de red implementados. En particular, use `sourcerudder` y `SOURCERUDDER_AUTH_KEY`; no cambie contratos externos en la documentación.

SourceRudder y sus contribuciones aceptadas se distribuyen bajo la [Licencia MIT](LICENSE) del repositorio. Cualquier cambio futuro de licencia debe ser explícito y prospectivo.
