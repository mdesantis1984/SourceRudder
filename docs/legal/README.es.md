# Gate de Revisión de Licencia de SourceRudder

[English](README.md) | [Español](README.es.md)

SourceRudder 2.0.0 no fue publicado. La `LICENSE` raíz sigue siendo MIT y
ninguna licencia personalizada está activa ni se presenta como legalmente
aprobada.

El workflow de release exige evidencia independiente después de una revisión
legal profesional. Este gate técnico no determina suficiencia legal: impide
publicar sin un registro de revisión que coincida con los bytes exactos de la
licencia distribuida.

## Procedimiento de activación

1. Solicite a un profesional legal calificado que revise la licencia propuesta,
   atribución, compatibilidad, jurisdicción y modelo de distribución.
2. Aplique a `LICENSE` el texto aprobado, incluido
   `SPDX-License-Identifier: LicenseRef-SourceRudder-Commercial-Attribution-1.0`.
3. Copie `source-rudder-license-review.example.json` como
   `source-rudder-license-review.json` y complete todos los campos, incluida la
   identificación profesional y la referencia del encargo u opinión. Use en
   `license_sha256` el SHA-256 en minúsculas de la `LICENSE` final.
4. Confirme licencia y registro mediante la revisión protegida normal.
5. Configure un entorno GitHub `release` protegido con revisores obligatorios.
   Guarde la referencia inmutable como `SOURCERUDDER_LEGAL_APPROVAL_REF` y el
   SHA-256 del registro confirmado como `SOURCERUDDER_LEGAL_REVIEW_SHA256` en
   secretos de ese entorno.
6. Ejecute el gate completo de release. No evada una validación legal fallida.

Las copias históricas de IA_Buscar conservan los derechos ya otorgados por MIT.
