[English](brand.md) | [Español](brand.es.md)

# SourceRudder visual system

SourceRudder uses an indigo system seeded by `#6366F1`. The detailed README hero and channel-specific campaign cards are separate assets so each public surface keeps the right crop, copy, and safe area.

## Core assets

| Asset | Use |
|---|---|
| `assets/brand/sourcerudder-hero-master.png` | Maintainer-approved source artwork. Keep unchanged. |
| `assets/brand/campaign/sourcerudder-hero-indigo.png` | Detailed 1774x887 README hero with palette-only recoloring. |
| `assets/sourcerudder-social-preview.png` | Approved 1280x640 GitHub Social Preview upload. |
| `assets/brand/campaign/sourcerudder-{github,linkedin,youtube}-*.png` | Channel-specific Spanish campaign cards. |
| `assets/brand/campaign/sourcerudder-linkedin-carrusel-*.png` | Six-slide LinkedIn carousel. |
| `assets/brand/campaign/sourcerudder-icono-master.png` | Square and circular-safe campaign icon. |
| `assets/brand/campaign/sourcerudder-logo-horizontal.png` | Transparent campaign lockup. |
| `assets/brand/sourcerudder-{mark,lockup}.svg` | Editable indigo vector masters. |
| `assets/brand/gentle-ai-banner.webp` | Maintainer-approved official Gentleman Programming recognition visual. |

## Rebuild

Run `scripts/export-brand-assets.sh` to rebuild and atomically publish the raster matrix. It requires ImageMagick, Python 3 with Pillow, and Noto Sans Regular/Bold. The exporter runs `scripts/build-campaign-assets.py` inside its staging directory before swapping the completed asset tree into place.

`assets/brand/manifest.json` records the full inventory, limits, generator, source issue, and third-party provenance. `assets/brand/campaign/manifest.json` records the exact campaign copy and SHA-256 for every deliverable.

## Provenance and boundaries

The campaign generator reads only `sourcerudder-hero-master.png`; rejected standalone key-art drafts are not project inputs. Keep product counts and wording tied to tested contracts. Do not add provider marks, endorsements, partnership claims, or fabricated metrics.

`gentle-ai-banner.webp` is an exact copy of the [official Gentleman Programming asset](https://gentlemanprogramming.com/branding/gentle-ai-banner.webp), supplied and approved by the maintainer for this acknowledgement. Only resize or crop it; do not recolor, redraw, retouch, restyle, or overlay text. The surrounding acknowledgement states the non-affiliation boundary.

GitHub Social Preview is repository metadata. After approving the tracked image, upload it separately in repository settings and verify the public Open Graph result.
