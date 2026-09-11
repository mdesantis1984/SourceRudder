[English](brand.md) | [Español](brand.es.md)

# SourceRudder visual system

The issue #10 visual system uses a deep navy field, cyan-to-teal signal paths, and a friendly research guide to make multi-source navigation recognizable without changing product claims.

## Core assets

| Asset | Use |
|---|---|
| `assets/sourcerudder-social-preview.png` | README hero and GitHub Social Preview upload. |
| `assets/brand/sourcerudder-hero-master.png` | Maintainer-approved 2:1 source artwork. |
| `assets/brand/sourcerudder-{og,linkedin,x}-*.png` | Channel-specific social cards. |
| `assets/brand/sourcerudder-avatar-*.png` | Square mascot crops. |
| `assets/brand/sourcerudder-mark.svg` | Scalable project mark and favicon source. |
| `assets/brand/sourcerudder-lockup.svg` | Horizontal logo lockup. |
| `assets/brand/gentleman-programming-recognition.svg` | Original community recognition card. |

Run `scripts/export-brand-assets.sh` to rebuild the raster matrix from the committed masters. ImageMagick is required. Dimensions, roles, and the source issue are recorded in `assets/brand/manifest.json` and enforced by the quality tests.

## Boundaries

Keep product counts and wording tied to tested contracts. Do not add provider marks, third-party artwork, endorsements, or partnership claims without permission. GitHub Social Preview is repository metadata: after approving the tracked image, upload it separately in repository settings and verify the public Open Graph result.
