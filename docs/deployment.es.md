[English](deployment.md) | [Español](deployment.es.md)

# Modelo de despliegue de SourceRudder 2.0

SourceRudder 2.0 está **sin publicar**. Este documento describe los artefactos de despliegue y las expectativas operativas; no autoriza ni realiza un despliegue.

## Rutas de artefactos admitidas

| Destino | Artefacto | Límite previsto |
|---|---|---|
| Compose local | `compose.yaml` | SourceRudder y SearXNG, publicados únicamente en puertos loopback. |
| Línea base de calidad aislada | `deploy/quality/docker-compose.yml` | Un proyecto y red `sourcerudder-quality` separados para evidencia de línea base en vivo. |
| Kubernetes | `deploy/kubernetes/deployment.yaml` | Un servicio `ClusterIP` con un placeholder de imagen por digest inmutable. |
| systemd | `deploy/systemd/sourcerudder.service` | Endpoint administrado por host que usa `/opt/sourcerudder` y `/etc/sourcerudder/sourcerudder.env`. |

## Controles de contenedores

Los artefactos Compose fijan la imagen externa de SearXNG por digest. Usan `restart: "no"`, límites estrictos de memoria con swap igual a memoria, límites de CPU y PID, y rotación de logs `json-file`. Los puertos publicados se enlazan a `127.0.0.1`; exponga un endpoint externamente solo mediante un proxy reverso o una política de red revisados.

El stack de calidad está aislado intencionalmente del proyecto Compose raíz. Tiene su propia red y credenciales temporales, y sirve para recolectar evidencia, no para realizar pruebas de producción.

## Kubernetes y systemd

Kubernetes usa el namespace `sourcerudder` y un placeholder `<IMAGE>` que el pipeline renderiza en un artefacto de release separado con `@sha256:...`; no confirme ni despliegue una etiqueta mutable. En ese namespace, el Secret `sourcerudder` aporta `auth-key`, y el ConfigMap `sourcerudder`, administrado por separado, debe aportar una `searxng-url` alcanzable. El manifest falla deliberadamente si falta alguna dependencia. Los límites de recursos, el token de service account deshabilitado, el filesystem raíz de sólo lectura, seccomp y los health probes protegen el pod.

La unidad systemd lee secretos desde `/etc/sourcerudder/sourcerudder.env`, inicia `/opt/sourcerudder/bin/sourcerudder` y verifica `/opt/sourcerudder/VERSION`, `/opt/sourcerudder/IMAGE` y el archivo de root `/etc/sourcerudder/release/BINARY_SHA256` antes del inicio. Los archivos Linux incluyen el `BINARY_SHA256` específico de su arquitectura; cada release también incluye la unidad renderizada, `VERSION` e `IMAGE`. Instálelos con el binario correspondiente como un conjunto atómico. La unidad vuelve ambos paths de release de solo lectura para el proceso `sourcerudder`.

Antes de instalar un archivo descargado, verifique su checksum publicado y la
attestation de procedencia de GitHub:

```bash
sha256sum -c checksums.txt --ignore-missing
gh attestation verify sourcerudder_2.0.0_linux_amd64.tar.gz \
  --repo mdesantis1984/SourceRudder
```

El control local `BINARY_SHA256` detecta un binario mezclado o reemplazado por
error después de la instalación. La attestation de GitHub es el ancla de
procedencia independiente; ninguno de los controles protege un host después de
un compromiso de root.

## Secretos y seguridad de releases

- Conserve los secretos de Compose en un archivo local de entorno ignorado y con permisos restrictivos; nunca coloque credenciales en Compose, manifests o control de versiones.
- Use Kubernetes Secrets o un gestor de secretos aprobado para credenciales de clúster.
- Registre la revisión de código aprobada y el digest inmutable de imagen antes de una actualización.
- Verifique salud, inicialización MCP, listado de herramientas y llamadas representativas a proveedores después de un cambio.
- Revierta restaurando la revisión o digest conocido como bueno. Para systemd, restaure juntos el binario, el `BINARY_SHA256` propiedad de root, la unidad renderizada, `VERSION` e `IMAGE`.
- Proteja el entorno GitHub `release` con revisores obligatorios. Configure sus secretos de aprobación legal sólo después de revisar profesionalmente el registro legal confirmado y la licencia activada, y de verificar sus hashes.

Para la configuración exacta de runtime y comandos de recuperación, consulte los artefactos de despliegue y la documentación de operaciones. Esta guía no implica ningún release ni despliegue.
