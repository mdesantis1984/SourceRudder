[English](brand.md) | [Español](brand.es.md)

# Sistema visual de SourceRudder

El sistema visual del issue #10 usa un fondo azul noche, señales de cian a verde azulado y un guía de investigación amigable para hacer reconocible la navegación entre fuentes sin cambiar afirmaciones del producto.

## Assets principales

| Asset | Uso |
|---|---|
| `assets/sourcerudder-social-preview.png` | Hero del README y carga de GitHub Social Preview. |
| `assets/brand/sourcerudder-hero-master.png` | Arte fuente 2:1 aprobado por el mantenedor. |
| `assets/brand/sourcerudder-{og,linkedin,x}-*.png` | Tarjetas específicas para cada canal. |
| `assets/brand/sourcerudder-avatar-*.png` | Recortes cuadrados de la mascota. |
| `assets/brand/sourcerudder-mark.svg` | Marca escalable y fuente del favicon. |
| `assets/brand/sourcerudder-lockup.svg` | Lockup horizontal del logo. |
| `assets/brand/gentleman-programming-recognition.svg` | Tarjeta original de reconocimiento a la comunidad. |

Ejecuta `scripts/export-brand-assets.sh` para reconstruir la matriz raster desde los masters versionados. Requiere ImageMagick. `assets/brand/manifest.json` registra dimensiones, usos e issue de origen; los tests de calidad validan ese contrato.

## Límites

Mantén cantidades y textos del producto ligados a contratos probados. No agregues marcas de proveedores, arte de terceros, respaldos ni afirmaciones de asociación sin permiso. GitHub Social Preview es metadata del repositorio: después de aprobar la imagen versionada, cárgala por separado en la configuración y verifica el resultado Open Graph público.
