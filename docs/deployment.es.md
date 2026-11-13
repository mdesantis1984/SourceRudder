[English](deployment.md) | [Español](deployment.es.md)

# Modelo de despliegue de SourceRudder

Este documento describe los artefactos de despliegue y las expectativas operativas de SourceRudder; no autoriza ni realiza un despliegue.

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

La unidad systemd lee secretos desde `/etc/sourcerudder/sourcerudder.env`, inicia `/opt/sourcerudder/bin/sourcerudder` en `127.0.0.1:8080` y verifica `/opt/sourcerudder/VERSION`, `/opt/sourcerudder/IMAGE` y el archivo de root `/etc/sourcerudder/release/BINARY_SHA256` antes del inicio. Los archivos Linux incluyen el `BINARY_SHA256` específico de su arquitectura; cada release también incluye la unidad renderizada, `VERSION` e `IMAGE`. Instálelos con el binario correspondiente como un conjunto atómico. La unidad vuelve ambos paths de release de solo lectura para el proceso `sourcerudder`. Un bridge administrado por el operador puede reemplazar la dirección de escucha por una IP privada explícita; no use un wildcard sin host en un sistema dual-stack.

Antes de instalar un archivo descargado, verifique su checksum publicado y la
attestation de procedencia de GitHub:

```bash
sha256sum -c checksums.txt --ignore-missing
gh attestation verify sourcerudder_3.0.0_linux_amd64.tar.gz \
  --repo mdesantis1984/SourceRudder
```

El control local `BINARY_SHA256` detecta un binario mezclado o reemplazado por
error después de la instalación. La attestation de GitHub es el ancla de
procedencia independiente; ninguno de los controles protege un host después de
un compromiso de root.

## Transcripciones de YouTube

`fetch_youtube_transcript` (y la delegación de YouTube en `fetch_url` / `fetch_and_extract`) ejecuta yt-dlp en el host, porque YouTube responde con HTTP 429 al scraping directo de páginas. Sin los componentes siguientes la herramienta devuelve `youtube_unavailable`. Fuera de alcance por diseño: cookies de cuentas de Google, proxies de pago o residenciales y descarga de audio o video (solo se obtiene una pista de subtítulos).

### Requisitos del host

- Un yt-dlp reciente (`yt-dlp[default]`, instalado en un venv en `/opt/yt-dlp`), con IPv4 forzado; SourceRudder ya pasa `--force-ipv4`.
- Un runtime de JavaScript para yt-dlp (Node.js 20+ o Deno).
- El plugin de PO tokens `bgutil-ytdlp-pot-provider` 2.0.2 para yt-dlp y el servidor bgutil de la misma versión escuchando solo en `127.0.0.1:4416`. El servidor declara Node.js 22+; Debian 13 trae Node.js 20, por lo que el instalador resuelve en el momento de la instalación el LTS actual de Node.js desde `https://nodejs.org/dist/index.json`, verifica el tarball oficial contra `SHASUMS256.txt`, lo extrae en `/opt/nodejs-<versión>` y repunta de forma atómica el enlace simbólico `/opt/nodejs`. Nunca toca el `nodejs` de apt ni `/usr/bin/node`.

### Instalación

Ejecute como root en el host Debian/Ubuntu, desde una copia de este repositorio:

```bash
sudo deploy/youtube/install-bgutil.sh   # /opt/nodejs (LTS actual), /opt/bgutil-pot-provider, usuario "bgutil", bgutil-pot-provider.service
sudo deploy/youtube/install-ytdlp.sh    # venv /opt/yt-dlp, yt-dlp + plugin bgutil 2.0.2, /usr/local/bin/yt-dlp
sudo install -m 0644 deploy/youtube/yt-dlp-update.service deploy/youtube/yt-dlp-update.timer /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now yt-dlp-update.timer
```

Ambos scripts son idempotentes; la unidad y `YTDLP_JS_RUNTIMES` usan la ruta neutral a la versión `/opt/nodejs/bin/node`. El servidor bgutil corre como el usuario sin privilegios `bgutil` con `--host 127.0.0.1 --port 4416`, endurecimiento básico (`NoNewPrivileges`, `ProtectSystem=strict`, `ProtectHome`, `PrivateTmp`) y `Restart=on-failure`. `MemoryDenyWriteExecute` queda sin definir a propósito porque Node.js necesita JIT. El `sourcerudder.service` publicado ya define `YTDLP_PATH=/opt/yt-dlp/bin/yt-dlp`, `YTDLP_JS_RUNTIMES=node:/opt/nodejs/bin/node` y `YOUTUBE_POT_BASE_URL=http://127.0.0.1:4416`; yt-dlp corre con `--no-cache-dir`, por lo que la unidad no necesita rutas de escritura adicionales.

Alternativa con Docker (si prefiere un contenedor al servidor nativo):

```bash
docker run -d --init --restart unless-stopped --name bgutil-pot \
  -p 127.0.0.1:4416:4416 brainicism/bgutil-ytdlp-pot-provider:2.0.2
```

El servidor escucha en todas las interfaces dentro del contenedor; el prefijo `127.0.0.1:` del mapeo de puertos es lo que lo mantiene fuera de la red. Nunca publique el puerto sin él: el servidor no tiene autenticación y cualquiera que lo alcance puede generar tokens. Con Compose use `ports: ["127.0.0.1:4416:4416"]` y `restart: unless-stopped`. No ejecute a la vez el servidor Docker y el nativo.

### Verificación

```bash
curl -fsS http://127.0.0.1:4416/ping          # JSON con server_uptime y version
/opt/yt-dlp/bin/yt-dlp --version
/opt/yt-dlp/bin/yt-dlp -v --ignore-config --skip-download --force-ipv4 \
  --js-runtimes node:/opt/nodejs/bin/node \
  "https://www.youtube.com/watch?v=<video-id>" 2>&1 | grep -iE 'pot|bgutil'
```

La salida detallada debe listar el proveedor de PO tokens `bgutil` y una solicitud de PO token. Use el último comando con moderación: cada ejecución es una solicitud real a YouTube desde la IP del servidor.

### Actualizar yt-dlp

YouTube cambia con frecuencia y las versiones antiguas de yt-dlp dejan de funcionar. `yt-dlp-update.timer` se ejecuta semanalmente (con retraso aleatorio) y corre `pip install --upgrade "yt-dlp[default]" "bgutil-ytdlp-pot-provider==2.0.2"`. No use `yt-dlp -U`: no puede actualizar un yt-dlp instalado con pip. El plugin permanece fijado a la versión del servidor bgutil; al mover el servidor a otro tag, cambie `BGUTIL_VERSION` y la versión fijada en `yt-dlp-update.service` a la vez. El timer cubre solo yt-dlp. Para actualizar el runtime Node.js LTS o bgutil, vuelva a ejecutar `install-bgutil.sh` (instala un LTS más nuevo si existe y recompila el servidor cuando cambia la versión de Node.js). Para subir la versión de bgutil, cambie `BGUTIL_VERSION` (el tag del servidor y el plugin de pip deben coincidir) en `install-bgutil.sh` e `install-ytdlp.sh`, cambie la versión fijada en `yt-dlp-update.service`, reinstale esa unidad y vuelva a ejecutar ambos instaladores. Actualización manual:

```bash
sudo systemctl start yt-dlp-update.service
journalctl -u yt-dlp-update.service -n 20
```

### Solución de problemas de `youtube_blocked`

YouTube aplicó límite de tasa o verificación anti-bot a la IP del servidor. SourceRudder lo informa una sola vez, no reintenta, no cachea el fallo y pausa todas las solicitudes a YouTube durante `YOUTUBE_BLOCK_COOLDOWN` (15 minutos por defecto). No reintente en bucle ni pruebe otras herramientas de fetch para el mismo video. Espere, confirme que bgutil responde en `/ping` y que yt-dlp está actualizado, y pruebe de nuevo con un solo video. Si el bloqueo persiste, la IP está marcada; este despliegue no usa cookies ni proxies.

## Secretos y seguridad de releases

- Conserve los secretos de Compose en un archivo local de entorno ignorado y con permisos restrictivos; nunca coloque credenciales en Compose, manifests o control de versiones.
- Use Kubernetes Secrets o un gestor de secretos aprobado para credenciales de clúster.
- Registre la revisión de código aprobada y el digest inmutable de imagen antes de una actualización.
- Verifique salud, inicialización MCP, listado de herramientas y llamadas representativas a proveedores después de un cambio.
- Revierta restaurando la revisión o digest conocido como bueno. Para systemd, restaure juntos el binario, el `BINARY_SHA256` propiedad de root, la unidad renderizada, `VERSION` e `IMAGE`.
- Proteja el entorno GitHub `release` con una política de despliegue limitada a tags de release aprobados. Mantenga los permisos de paquetes y releases acotados al job del workflow.

Para la configuración exacta de runtime y comandos de recuperación, consulte los artefactos de despliegue y la documentación de operaciones. Esta guía no implica ningún release ni despliegue.
