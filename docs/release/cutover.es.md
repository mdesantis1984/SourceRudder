# Cutover de SourceRudder 2.0

[English](cutover.md) | [Español](cutover.es.md)

Este checklist prepara el cutover externo. No autoriza evadir la revisión
legal, ramas protegidas, revisores obligatorios ni gates de release.

## Preparación del repositorio privado

1. Cree `mdesantis1984/SourceRudder` como repositorio privado, publique el
   snapshot verificado de la migración en `main` y conserve IA_Buscar como
   remote legado y repositorio público histórico.
2. Configure `main` protegida, permisos mínimos de Actions, alertas de
   dependencias, labels de políticas para issues/PR y un entorno `release`
   restringido.
3. Confirme CI sobre el commit publicado. CodeQL permanece omitido mientras el
   repositorio privado no tenga licencia de GitHub Code Security; Gitleaks,
   gosec y govulncheck continúan siendo obligatorios.

## Antes del release público

1. Complete la revisión profesional de la licencia SourceRudder final. Confirme
   la `LICENSE` activada y el archivo correspondiente
   `docs/legal/source-rudder-license-review.json`.
2. Configure el entorno GitHub `release` con revisores obligatorios y los
   secretos de entorno `SOURCERUDDER_LEGAL_APPROVAL_REF` y
   `SOURCERUDDER_LEGAL_REVIEW_SHA256`.
3. Conserve el último tag, imagen, licencia MIT y artefactos de rollback de
   IA_Buscar 1.x. No reescriba ni relicencie copias históricas.
4. Verifique que los remotes locales apunten a repositorios independientes:

   ```bash
   git remote get-url origin
   git remote get-url legacy
   git remote -v
   ```

5. Ejecute `bash scripts/release-readiness.sh --identity-only` desde un clone
   nuevo de SourceRudder.
6. Haga público SourceRudder sólo cuando se hayan revisado la licencia,
   configuración de seguridad, metadata, integraciones externas y gates.

## Publicar 2.0.0

1. Cree el tag exacto `v2.0.0` sólo después de que el gate completo de release
   pase en un worktree limpio y sobre el commit revisado.
2. Envíe el tag. `.github/workflows/release.yml` verificará todos los gates,
   construirá archivos multiplataforma, publicará una imagen GHCR con SBOM y
   procedencia, renderizará artefactos de despliegue inmutables y creará la
   GitHub Release.
3. Verifique checksums y attestations, y confirme que la referencia de imagen
   publicada contenga el digest esperado.
4. Ejecute smoke tests en un despliegue controlado antes de redirigir clientes.
5. Mantenga disponible el despliegue 1.x anterior durante la ventana de
   observación.
6. Archive IA_Buscar sólo después de que SourceRudder sea público, estable y
   esté enlazado desde el repositorio legado. No elimine ni reescriba su historial.

## Condiciones de aborto

Detenga el cutover si la licencia o el registro no coinciden con su hash
protegido, CI no está verde, el digest es mutable o inesperado, no pueden
verificarse las attestations, se perdieron protecciones o faltan artefactos de
rollback. Siga el [procedimiento de reversión](rollback.es.md) en lugar de
improvisar.
