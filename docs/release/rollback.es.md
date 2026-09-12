# Reversión de Releases de SourceRudder

[English](rollback.md) | [Español](rollback.es.md)

Este procedimiento aplica a releases de SourceRudder después de completar
los gates del repositorio y del release.

## Invariantes

- Revierta a un commit revisado y a un digest de imagen inmutable, nunca a una
  etiqueta mutable como `latest` o una etiqueta de versión sin digest.
- Conserve el binario, digest, configuración y nombres de secretos anteriores
  hasta comprobar que el reemplazo está saludable.
- No elimine volúmenes, cachés ni artefactos de reversión durante un incidente.
- Una reversión de Kubernetes es sólo un puente operativo; luego debe reconciliar
  Git.

## Precondiciones

1. Identifique el commit, tag de release y digest de imagen buenos anteriores.
2. Verifique que la referencia tenga el formato
   `ghcr.io/mdesantis1984/sourcerudder:<version>@sha256:<digest>`.
3. Ejecute build, vet, tests, race, contratos de despliegue y validación Compose
   sobre el commit de reversión.
4. Confirme que `SOURCERUDDER_AUTH_KEY` y los secretos del despliegue sigan
   disponibles. Nunca copie secretos a Git ni a las notas del incidente.

## Puente de Incidente en Kubernetes

```bash
kubectl rollout history deployment/sourcerudder -n sourcerudder
kubectl rollout undo deployment/sourcerudder -n sourcerudder
kubectl rollout status deployment/sourcerudder -n sourcerudder
kubectl get deployment/sourcerudder -n sourcerudder \
  -o jsonpath='{.spec.template.spec.containers[0].image}'
```

Compruebe `/healthz`, `/mcp` autenticado y `/metrics` autenticado antes de cerrar
el puente. Luego revierta o restaure el cambio Git correspondiente para que el
manifest declarado coincida con el workload activo.

## Reversión de systemd

```bash
sudo systemctl stop sourcerudder
sudo install -d -o root -g root -m 0755 /etc/sourcerudder/release
sudo install -m 0755 /opt/sourcerudder/releases/<anterior>/sourcerudder \
  /opt/sourcerudder/bin/sourcerudder
sudo install -m 0644 /opt/sourcerudder/releases/<anterior>/BINARY_SHA256 \
  /etc/sourcerudder/release/BINARY_SHA256
sudo install -m 0644 /opt/sourcerudder/releases/<anterior>/sourcerudder.service \
  /etc/systemd/system/sourcerudder.service
sudo install -m 0644 /opt/sourcerudder/releases/<anterior>/VERSION \
  /opt/sourcerudder/VERSION
sudo install -m 0644 /opt/sourcerudder/releases/<anterior>/IMAGE \
  /opt/sourcerudder/IMAGE
sudo systemctl daemon-reload
sudo systemctl start sourcerudder
sudo systemctl status sourcerudder
```

Verifique la attestation de GitHub del archivo antes de instalarlo. El binario,
el `BINARY_SHA256` propiedad de root, la unidad, `VERSION` e `IMAGE` deben
provenir del mismo bundle de release. Una diferencia debe fallar de forma
cerrada en lugar de ejecutar un binario o una imagen desconocidos.

## Reconciliación Git

Prefiera un `git revert` revisado antes que reescribir historial publicado:

```bash
git log --first-parent --oneline -20 main
git show --stat <sha-del-merge-defectuoso>
git revert --no-edit -m 1 <sha-del-merge-defectuoso>
bash scripts/release-gate.sh
```

Siga el proceso normal de ramas protegidas y revisión. No haga push directo a
`main` sólo porque ocurrió una reversión operativa.

## Checklist de Finalización

- El binario activo y el digest coinciden con la release anterior elegida.
- Salud, MCP autenticado, métricas y búsquedas representativas pasan.
- El estado de Kubernetes o systemd coincide con lo declarado en el repositorio.
- El commit y registro del incidente identifican versiones defectuosa y
  restaurada sin exponer credenciales.
- El monitoreo confirma tasas de error, latencia y consumo normales.
