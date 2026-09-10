# Contribuir a IA_Buscar

Gracias por mejorar el proyecto. Los cambios deben ser pequeños, verificables y trazables a un problema concreto.

## Antes de programar

1. Busca si ya existe un issue para el problema.
2. Abre o solicita un issue antes de una implementación relevante.
3. Acuerda el alcance, el comportamiento observable y los criterios de aceptación.
4. Para una vulnerabilidad, usa el flujo privado de [SECURITY.md](SECURITY.md).

Los arreglos triviales de texto pueden exceptuarse del issue previo cuando un maintainer lo indique.

## Preparar el entorno

```bash
git clone https://github.com/mdesantis1984/IA_Buscar.git
cd IA_Buscar
go mod download
go test ./...
```

El proyecto y CI usan Go `1.26.6`. Docker Compose v2 y Python 3 son necesarios para los entornos de QA y calidad.

## Trabajar en una rama

Usa un nombre que exprese tipo y alcance:

```text
feat/local-index-ranking
fix/http-auth-header
docs/client-setup
test/fetch-redirects
```

No mezcles refactors, formato masivo ni cambios no relacionados. El release gate aplica un presupuesto de 1000 líneas agregadas y eliminadas contra el merge-base. Si el cambio necesita más, divídelo en unidades revisables antes de abrir el PR.

## Verificar el cambio

Ejecuta como mínimo:

```bash
go build ./...
go vet ./...
go test ./...
go test -race ./...
python3 -m unittest discover -s scripts/quality -p 'test_*.py' -v
```

Usa las suites Docker solo cuando el alcance las necesite. [La guía de desarrollo](docs/development.md) explica QA local, baseline vivo y release gate.

## Commits

Usa Conventional Commits:

```text
feat(search): add provider filter
fix(auth): reject empty bearer tokens
docs(clients): clarify remote headers
test(fetch): cover blocked redirects
```

- Un commit debe representar una unidad de trabajo coherente.
- Incluye tests y documentación junto al comportamiento que justifican.
- No agregues trailers `Co-Authored-By` ni atribución automática.
- No incluyas secretos, salidas locales, binarios ni evidencia temporal.

## Pull requests

- Vincula el issue aprobado con `Closes #N`.
- Selecciona un solo tipo de PR.
- Explica el comportamiento anterior y el nuevo, no solo los archivos tocados.
- Enumera los comandos de verificación realmente ejecutados.
- Declara riesgos, límites y pasos de rollback.
- Espera que CI y el release gate terminen correctamente antes de solicitar merge.

No fuerces un cambio grande dentro de un único PR. Separar unidades reduce el riesgo y hace posible una revisión técnica real.

## Estilo

- Código, identificadores y comentarios técnicos: inglés.
- Documentación pública: español claro, salvo que el documento establezca otra audiencia.
- Mantén nombres MCP, campos JSON, flags y variables exactamente como aparecen en el código.
- Prefiere cambios mínimos y elimina compatibilidad solo cuando no existan consumidores reales.

## Licencia

Al contribuir, aceptas que tu aporte se distribuya bajo la [licencia MIT](LICENSE) del repositorio.
