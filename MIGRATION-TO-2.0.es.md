# Migración a SourceRudder 2.0

[English](MIGRATION-TO-2.0.md) | [Español](MIGRATION-TO-2.0.es.md)

SourceRudder 2.0.0 es una pre-release y todavía no fue publicada. Esta guía
prepara un despliegue de IA_Buscar 1.x; no debe interpretarse como un anuncio de
una release publicada.

## Identidades incompatibles

| IA_Buscar 1.x | SourceRudder 2.0 |
|---|---|
| Repositorio `https://github.com/mdesantis1984/IA_Buscar` | `https://github.com/mdesantis1984/SourceRudder` |
| Módulo `github.com/mdesantis1984/IA_Buscar` | `github.com/mdesantis1984/SourceRudder` |
| Binario/servicio `ia-buscar` | `sourcerudder` / `sourcerudder.service` |
| Servicio Compose `ia-buscar` | `sourcerudder` |
| Workload/Secret Kubernetes `ia-buscar` | `sourcerudder` |
| `IA_BUSCAR_AUTH_KEY` | `SOURCERUDDER_AUTH_KEY` |
| `IA_BUSCAR_PORT` | `SOURCERUDDER_PORT` |
| `agent-guide://ia-buscar/wire-contract` | `agent-guide://sourcerudder/wire-contract` |
| Métricas Prometheus `ia_buscar_*` | `sourcerudder_*` |
| Nombre de imagen | `ghcr.io/mdesantis1984/sourcerudder:2.0.0@sha256:<digest>` |

Actualice comandos de inicio de clientes, overrides de Compose, referencias de
unidades systemd, nombres de Kubernetes, claves de secretos, etiquetas de
monitoring y la URI del recurso MCP. Use una imagen con digest de una release;
`<digest>` es un placeholder intencional hasta que 2.0.0 se publique.

## Contratos wire sin cambios

Los nombres de herramientas MCP, el JSON de herramientas, el comportamiento de
los conectores y los strategy IDs se mantienen estables. Esto incluye SearXNG,
MCP, Go, GitHub y la integración opcional IA_Recuerdo. `results` sigue siendo
un array JSON y nunca `null`.

La superficie estable de herramientas es: `search_web`, `search_news`,
`search_doc_oficial`, `search_local_index`, `search_github`, `search_github_pr`,
`search_github_issue`, `search_stackoverflow`, `search_npm`, `search_nuget`,
`search_pypi`, `search_docker_hub`, `search_academic`, `search_reddit`,
`search_youtube`, `search_images`, `fetch_url`, `fetch_and_extract`,
`extract_structured`, `validate_url`, `check_link_status`, `summarize_results`,
`deep_research`, `compare_sources`, `get_cached`, `invalidate_cache`,
`get_search_history` y `get_current_date`.

Los strategy IDs estables incluyen `searxng`, `searxng_reddit_index`,
`official_doc_registry_search`, `official_doc_web_fallback`,
`local_index_lexical` y `local_index_unavailable`.

## Secuencia de actualización

1. Inventarie cada identidad de la tabla, incluida la configuración MCP de los clientes.
2. Cree una nueva `SOURCERUDDER_AUTH_KEY` y guárdela en el secret manager destino.
3. Actualice manifests de `sourcerudder`, `sourcerudder.service` y el workload
   Kubernetes `sourcerudder`; configure `SOURCERUDDER_PORT` si Compose necesita
   un puerto de host distinto del default.
4. Despliegue la imagen 2.0.0 con digest pinneado cuando sea publicada.
5. Verifique `/healthz`; luego autentique un `initialize` MCP y `tools/list`.
6. Lea `agent-guide://sourcerudder/wire-contract` y ejecute smoke tests para
   cada clase de conector que use su despliegue.
7. Retire 1.x solo cuando monitoring y el tráfico de clientes confirmen el nuevo servicio.

## Límite de rollback

El rollback es seguro solo antes de que clientes, automatizaciones y secretos
desplegados cambien de forma irreversible a las identidades SourceRudder.
Conserve la imagen 1.x, su `IA_BUSCAR_AUTH_KEY` y sus manifests hasta comprobar
el nuevo servicio. Si clientes hardcodean la URI del recurso nueva o dependen de
los nombres de servicio SourceRudder, el rollback exige revertir también esas
configuraciones. Los payloads MCP y los strategy IDs no requieren rollback de
formato wire.

## Puerta de transición de licencia

La [LICENSE](LICENSE) raíz sigue siendo MIT mientras se espera la revisión
profesional de la licencia SourceRudder personalizada propuesta. No presente la
licencia propuesta como aprobada, publicada ni efectiva. Las releases previas de
IA_Buscar conservan sus derechos MIT. Complete una revisión legal y una decisión
explícita de release antes de cambiar texto de licencia, avisos de distribución
y metadatos de release.

Consulte el [README](README.es.md) para la operación local y la guía de QA.
