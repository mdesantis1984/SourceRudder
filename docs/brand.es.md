[English](brand.md) | [Español](brand.es.md)

# Sistema visual de SourceRudder

SourceRudder usa un sistema índigo basado en `#6366F1`. El hero detallado del README y las piezas específicas de campaña son assets separados para que cada superficie pública conserve el recorte, el texto y el área segura adecuados.

## Assets principales

| Asset | Uso |
|---|---|
| `assets/brand/sourcerudder-hero-master.png` | Arte fuente aprobado por el mantenedor. Debe conservarse sin cambios. |
| `assets/brand/campaign/sourcerudder-hero-indigo.png` | Hero detallado de 1774x887 para el README, con cambio exclusivo de paleta. |
| `assets/sourcerudder-social-preview.png` | Imagen aprobada de 1280x640 para GitHub Social Preview. |
| `assets/brand/campaign/sourcerudder-{github,linkedin,youtube}-*.png` | Piezas de campaña en español específicas para cada canal. |
| `assets/brand/campaign/sourcerudder-linkedin-carrusel-*.png` | Carrusel de LinkedIn de seis láminas. |
| `assets/brand/campaign/sourcerudder-icono-master.png` | Ícono de campaña seguro para uso cuadrado y circular. |
| `assets/brand/campaign/sourcerudder-logo-horizontal.png` | Lockup de campaña con fondo transparente. |
| `assets/brand/sourcerudder-{mark,lockup}.svg` | Masters vectoriales editables en índigo. |
| `assets/brand/gentle-ai-banner.webp` | Visual oficial de Gentleman Programming aprobado por el mantenedor para el reconocimiento. |

## Reconstrucción

Ejecuta `scripts/export-brand-assets.sh` para reconstruir y publicar atómicamente la matriz raster. Requiere ImageMagick, Python 3 con Pillow y Noto Sans Regular/Bold. El exportador ejecuta `scripts/build-campaign-assets.py` dentro del directorio temporal antes de reemplazar el árbol de assets completo.

`assets/brand/manifest.json` registra el inventario completo, los límites, el generador, el issue de origen y la procedencia de terceros. `assets/brand/campaign/manifest.json` registra el texto exacto y el SHA-256 de cada pieza de campaña.

## Procedencia y límites

El generador de campaña solo lee `sourcerudder-hero-master.png`; los borradores independientes rechazados no son entradas del proyecto. Mantén las cantidades y los textos del producto ligados a contratos probados. No agregues marcas de proveedores, respaldos, afirmaciones de asociación ni métricas inventadas.

`gentle-ai-banner.webp` es una copia exacta del [asset oficial de Gentleman Programming](https://gentlemanprogramming.com/branding/gentle-ai-banner.webp), suministrada y aprobada por el mantenedor para este reconocimiento. Solo puede redimensionarse o recortarse; no debe recolorearse, redibujarse, retocarse, restilizarse ni recibir texto superpuesto. El reconocimiento circundante establece el límite de no afiliación.

GitHub Social Preview es metadata del repositorio. Después de aprobar la imagen versionada, cárgala por separado en la configuración y verifica el resultado Open Graph público.
