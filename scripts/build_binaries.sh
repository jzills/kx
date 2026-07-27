#!/usr/bin/env bash
# Cross-compile kx for every published target and package the release archives.
#
# One machine builds all four: a CGO_ENABLED=0 Go binary needs no cross
# toolchain, no per-OS runner, and no libc on the target. That is what closes
# issue #130 — the artifact links against nothing, so musl and glibc are the
# same to it.
#
#   scripts/build_binaries.sh <version> [outdir]
#
# Produces, under outdir (default dist/):
#   binaries/kx_<os>_<arch>/kx   the executables, for the wheel builder
#   kx_v<version>_<os>_<arch>.tar.gz   the release archives
#
# Archive names and their internal layout (kx/kx) are unchanged from the
# PyInstaller builds, so .krew.yaml needs no edit and existing krew installs
# upgrade normally.
set -euo pipefail

version="${1:?usage: build_binaries.sh <version> [outdir]}"
outdir="${2:-dist}"
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

targets=(
  "linux amd64"
  "linux arm64"
  "darwin amd64"
  "darwin arm64"
)

binaries="$outdir/binaries"
rm -rf "$binaries"
mkdir -p "$binaries"

for target in "${targets[@]}"; do
  read -r os arch <<<"$target"
  stage="$binaries/kx_${os}_${arch}"
  mkdir -p "$stage"

  echo "building ${os}/${arch}"
  # -s -w strip the symbol and DWARF tables; the version is stamped in rather
  # than read from a metadata file, so there is nothing to ship alongside.
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -trimpath -ldflags "-s -w -X main.version=${version}" \
    -o "$stage/kx" "$root/cmd/kx"

  # The archive holds kx/kx, matching what .krew.yaml's bin: kx/kx expects.
  archive_root="$outdir/.stage_${os}_${arch}"
  rm -rf "$archive_root"
  mkdir -p "$archive_root/kx"
  cp "$stage/kx" "$archive_root/kx/kx"
  tar -C "$archive_root" -czf "$outdir/kx_v${version}_${os}_${arch}.tar.gz" kx
  rm -rf "$archive_root"
done

echo
echo "archives:"
ls -1 "$outdir"/kx_v"${version}"_*.tar.gz
