# Changelog

[English](CHANGELOG.md) | [Español](CHANGELOG.es.md)

All notable SourceRudder changes are documented here.

## [Unreleased]

## [3.0.0] - 2026-09-12

### Breaking changes

### Removed

- Retired the optional IA_Recuerdo observation forwarder, including its CLI
  flags, `MEMORY_*` environment variables, deployment settings, and synchronous
  outbound requests. The obsolete integration was removed so any future
  persistence capability can be designed deliberately; no replacement is part
  of this change. The 28 MCP tools and in-process search history are unchanged.

### Fixed

- Wired HTTP request counters and per-source search latency histograms into the
  production MCP request path so `/metrics` reflects real traffic.
- Bound the distributable systemd unit to loopback by default so dual-stack
  hosts do not expose the unauthenticated health endpoint on every interface.

### Distribution and documentation

- Added the complete SourceRudder visual identity and indigo campaign asset set.
- Made release automation derive artifact, image, and deployment versions from
  the reviewed `Makefile` version while release readiness rejects mismatched
  tags and missing release notes.

## [2.0.0] - 2026-09-11

### Breaking identity changes

- Renamed the product, command, service, Compose project, Kubernetes resources,
  Go module, repository target, MCP guide URI, environment prefix, User-Agent,
  and Prometheus metric prefix to SourceRudder identities.
- Replaced `IA_BUSCAR_AUTH_KEY` and `IA_BUSCAR_PORT` with
  `SOURCERUDDER_AUTH_KEY` and `SOURCERUDDER_PORT`.
- See [Migration to 2.0](MIGRATION-TO-2.0.md) for upgrade and rollback steps.

### Preserved contracts

- Kept MCP tool names, JSON fields, connector names, and strategy identifiers
  stable across the major-version migration.
- Kept historical IA_Buscar releases under the rights already granted by MIT.

### Operations and supply chain

- Added digest-pinned external container images, bounded Compose/systemd
  resources, loopback development ports, non-root runtime execution, and
  deployment contract checks.
- Added a gated release workflow for cross-platform archives, checksums,
  provenance attestations, multi-platform GHCR images, SBOM, and immutable
  deployment assets.
- Confirmed MIT as the active SourceRudder 2.0.0 license and made release
  readiness verify the exact approved license before publication.

### Documentation

- Added canonical English documentation with Spanish companion files for
  setup, migration, security, development, configuration, operations,
  deployment, architecture, MCP clients, quality, acknowledgements, and
  rollback.
