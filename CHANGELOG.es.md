# Historial de Cambios

[English](CHANGELOG.md) | [Español](CHANGELOG.es.md)

Este archivo documenta los cambios relevantes de SourceRudder.

## [2.0.0] - No publicado

### Cambios de identidad incompatibles

- Se renombraron el producto, comando, servicio, proyecto Compose, recursos de
  Kubernetes, módulo Go, repositorio objetivo, URI de guía MCP, prefijo de
  entorno, User-Agent y prefijo de métricas Prometheus a identidades
  SourceRudder.
- `IA_BUSCAR_AUTH_KEY` y `IA_BUSCAR_PORT` fueron reemplazadas por
  `SOURCERUDDER_AUTH_KEY` y `SOURCERUDDER_PORT`.
- Consulte [Migración a 2.0](MIGRATION-TO-2.0.es.md) para los pasos de
  actualización y reversión.

### Contratos preservados

- Los nombres de tools MCP, campos JSON, conectores e identificadores de
  estrategia permanecen estables durante la migración de versión mayor.
- Las releases históricas de IA_Buscar conservan los derechos ya otorgados por
  MIT.

### Operación y cadena de suministro

- Se agregaron imágenes externas fijadas por digest, recursos acotados en
  Compose/systemd, puertos de desarrollo en loopback, ejecución sin privilegios
  y checks de contratos de despliegue.
- Se agregó un workflow de release con gates para archivos multiplataforma,
  checksums, attestations de procedencia, imágenes GHCR multiplataforma, SBOM y
  artefactos de despliegue inmutables.
- Se agregó un gate explícito de revisión legal. SourceRudder 2.0.0 no puede
  publicarse mientras la licencia raíz siga siendo MIT o falte una referencia
  de aprobación legal.

### Documentación

- Se agregó documentación canónica en inglés con archivos equivalentes en
  español para instalación, migración, seguridad, desarrollo, configuración,
  operaciones, despliegue, arquitectura, clientes MCP, calidad, agradecimientos
  y reversión.
