#!/usr/bin/env bash

set -euo pipefail

repo_root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
assets_dir="$repo_root/docs/assets"
brand_dir="$assets_dir/brand"
master="$brand_dir/sourcerudder-hero-master.png"
campaign_builder="$repo_root/scripts/build-campaign-assets.py"

if command -v magick >/dev/null 2>&1; then
  image=(magick)
  identify=(magick identify)
elif command -v convert >/dev/null 2>&1 && command -v identify >/dev/null 2>&1; then
  image=(convert)
  identify=(identify)
else
  printf 'ImageMagick is required to export SourceRudder brand assets.\n' >&2
  exit 1
fi

if [ ! -f "$master" ]; then
  printf 'Missing hero master: %s\n' "$master" >&2
  exit 1
fi

if ! python3 -c 'import PIL' >/dev/null 2>&1; then
  printf 'Python 3 with Pillow is required to export SourceRudder campaign assets.\n' >&2
  exit 1
fi

dimensions=$("${identify[@]}" -format '%wx%h' "$master")
if [ "$dimensions" != "1774x887" ]; then
  printf 'Unexpected hero master dimensions: %s (want 1774x887).\n' "$dimensions" >&2
  exit 1
fi

export_root=$(mktemp -d "$repo_root/docs/.assets-export.XXXXXX")
chmod --reference="$assets_dir" "$export_root"
cp -a "$assets_dir/." "$export_root/"
export_brand="$export_root/brand"
trap 'rm -rf "$export_root"' EXIT

python3 "$campaign_builder" \
  --source "$export_brand/sourcerudder-hero-master.png" \
  --output "$export_brand/campaign" \
  --social-preview "$export_root/sourcerudder-social-preview.png"
campaign_hero="$export_brand/campaign/sourcerudder-hero-indigo.png"
cp "$export_brand/campaign/sourcerudder-linkedin-empresa.png" \
  "$export_brand/sourcerudder-linkedin-1200x627.png"
"${image[@]}" "$campaign_hero" -strip -colorspace sRGB -filter Lanczos -resize 1200x600! \
  -background '#071521' -gravity center -extent 1200x630 -define png:compression-level=9 \
  "$export_brand/sourcerudder-og-1200x630.png"
"${image[@]}" "$campaign_hero" -strip -colorspace sRGB -filter Lanczos -resize 1600x800! \
  -define png:compression-level=9 "$export_brand/sourcerudder-x-1600x800.png"

"${image[@]}" "$campaign_hero" -strip -colorspace sRGB -crop 760x760+0+64 +repage \
  -filter Lanczos -resize 1024x1024! -define png:compression-level=9 \
  "$export_brand/sourcerudder-avatar-1024.png"
for size in 512 256 128; do
  "${image[@]}" "$export_brand/sourcerudder-avatar-1024.png" -strip -filter Lanczos \
    -resize "${size}x${size}!" -define png:compression-level=9 \
    "$export_brand/sourcerudder-avatar-${size}.png"
done

"${image[@]}" -background none "$export_brand/sourcerudder-mark.svg" -strip \
  -resize 512x512! -define png:compression-level=9 "$export_brand/sourcerudder-icon-512.png"
for size in 180 64; do
  "${image[@]}" "$export_brand/sourcerudder-icon-512.png" -strip -filter Lanczos \
    -resize "${size}x${size}!" -define png:compression-level=9 \
    "$export_brand/sourcerudder-icon-${size}.png"
done
"${image[@]}" "$export_brand/sourcerudder-icon-512.png" -strip -filter Lanczos \
  -resize 32x32! -define png:compression-level=9 "$export_brand/sourcerudder-favicon-32.png"

previous_assets="$repo_root/docs/.assets-previous.$$"
mv "$assets_dir" "$previous_assets"
if ! mv "$export_root" "$assets_dir"; then
  mv "$previous_assets" "$assets_dir"
  exit 1
fi
rm -rf "$previous_assets"
trap - EXIT

printf 'Exported SourceRudder brand assets from %s.\n' "$master"
