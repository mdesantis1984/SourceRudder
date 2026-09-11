# Cutover de SourceRudder 2.0

[English](cutover.md) | [Español](cutover.es.md)

Este checklist gobierna el cutover público de 2.0.0. No autoriza evadir ramas
protegidas, checks obligatorios, verificación de artefactos ni gates de release.

## Preparación del repositorio

1. Mantenga `mdesantis1984/SourceRudder` público y conserve IA_Buscar como remote
   legado y repositorio histórico.
2. Mantenga permisos mínimos de Actions, alertas de dependencias, labels de
   políticas para issues/PR, ramas `main` y `develop` protegidas, y el entorno
   `release`.
3. Exija CI, CodeQL, Gitleaks, gosec, govulncheck y release gate en verde sobre
   el commit aprobado para el release.

## Antes del release público

1. Verifique que la `LICENSE` raíz sea la licencia MIT aprobada para 2.0.0 y que
   las notas y metadatos de distribución identifiquen MIT de forma consistente.
2. Restrinja el entorno GitHub `release` al tag exacto aprobado para el release.
3. Conserve el último tag, imagen, licencia MIT y artefactos de rollback de
   IA_Buscar 1.x. No reescriba ni relicencie copias históricas.
4. Verifique que los remotes locales apunten a repositorios independientes:

   ```bash
   git remote get-url origin
   git remote get-url legacy
   git remote -v
   ```

5. Ejecute los checks de identidad y licencia MIT desde un clone nuevo de
   SourceRudder.
6. Confirme visibilidad, configuración de seguridad, metadata, integraciones y
   gates antes de crear el tag.
7. Mantenga `main` protegido con los checks obligatorios `build`, `gate` y
   `Analyze Go`.

## Publicar 2.0.0

1. Cree el tag exacto `v2.0.0` sólo después de que el gate completo de release
   pase en un worktree limpio y sobre el commit aprobado.
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

Detenga el cutover si la licencia no coincide con su hash aprobado, CI no está
verde, el digest es mutable o inesperado, no pueden
verificarse las attestations, se perdieron protecciones o faltan artefactos de
rollback. Siga el [procedimiento de reversión](rollback.es.md) en lugar de
improvisar.
