#!/usr/bin/env bash

set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

VERSION="${VERSION:-$(awk -F= '$1 == "VERSION" { print $2 }' Makefile)}"
DIST_DIR="${DIST_DIR:-$ROOT_DIR/dist}"
SOURCE_DATE_EPOCH="${SOURCE_DATE_EPOCH:-$(git show -s --format=%ct HEAD)}"
TARGETS="${TARGETS:-linux/amd64 linux/arm64 darwin/amd64 darwin/arm64 windows/amd64}"

[[ "$VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || {
  printf 'build-release-archives: invalid VERSION: %s\n' "$VERSION" >&2
  exit 1
}
[[ "$SOURCE_DATE_EPOCH" =~ ^[0-9]+$ ]] || {
  printf 'build-release-archives: SOURCE_DATE_EPOCH must be an integer\n' >&2
  exit 1
}

mkdir -p "$DIST_DIR"
DIST_DIR="$(cd "$DIST_DIR" && pwd)"
staging="$(mktemp -d)"
trap 'rm -rf "$staging"' EXIT

for target in $TARGETS; do
  goos="${target%/*}"
  goarch="${target#*/}"
  name="sourcerudder_${VERSION}_${goos}_${goarch}"
  archive_root="$staging/$name"
  output="$archive_root/sourcerudder"

  mkdir -p "$archive_root"
  if [[ "$goos" == "windows" ]]; then
    output="${output}.exe"
  fi

  CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" \
    go build -trimpath -o "$output" ./cmd/sourcerudder
  install -m 0644 LICENSE README.md "$archive_root/"
  chmod 0755 "$archive_root" "$output"

  if [[ "$goos" == "linux" ]]; then
    binary_hash="$(sha256sum "$output" | awk '{print $1}')"
    printf '%s  /opt/sourcerudder/bin/sourcerudder\n' "$binary_hash" \
      > "$archive_root/BINARY_SHA256"
    chmod 0644 "$archive_root/BINARY_SHA256"
  fi

  TZ=UTC touch -d "@$SOURCE_DATE_EPOCH" "$archive_root" "$archive_root"/*
  if [[ "$goos" == "windows" ]]; then
    (cd "$staging" && TZ=UTC zip -Xqr "$DIST_DIR/${name}.zip" "$name")
  else
    tar --sort=name --mtime="@$SOURCE_DATE_EPOCH" --owner=0 --group=0 \
      --numeric-owner --format=ustar -C "$staging" \
      -czf "$DIST_DIR/${name}.tar.gz" "$name"
  fi

  rm -rf "$archive_root"
done
