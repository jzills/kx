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
#   binaries/kx_<os>_<arch>/kx[.exe]   the executables, for the wheel builder
#   kx_v<version>_<os>_<arch>.tar.gz   the release archives (linux, darwin)
#   kx_v<version>_windows_<arch>.zip   the release archives (windows)
#
# Archive names and their internal layout (kx/kx) are unchanged from the
# PyInstaller builds, so .krew.yaml needs no edit and existing krew installs
# upgrade normally. Windows ships as .zip because that is what a Windows user
# can open without extra tooling; krew never selected it, so the tar.gz set the
# krew manifest names is unaffected.
set -euo pipefail

version="${1:?usage: build_binaries.sh <version> [outdir]}"
outdir="${2:-dist}"
root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

targets=(
  "linux amd64"
  "linux arm64"
  "darwin amd64"
  "darwin arm64"
  "windows amd64"
  "windows arm64"
)

binaries="$outdir/binaries"
rm -rf "$binaries"
mkdir -p "$binaries"

# Resolved once, now that the directory exists. The zip step below runs from
# inside the staging directory, where a relative outdir no longer points here.
outdir="$(cd "$outdir" && pwd)"
binaries="$outdir/binaries"

for target in "${targets[@]}"; do
  read -r os arch <<<"$target"
  stage="$binaries/kx_${os}_${arch}"
  mkdir -p "$stage"

  # Windows will not execute a file without the extension, and pip installs the
  # wheel's payload under whatever name it carries, so the suffix has to be on
  # the binary itself rather than added when it is packaged.
  exe=""
  if [ "$os" = "windows" ]; then
    exe=".exe"
  fi

  echo "building ${os}/${arch}"
  # -s -w strip the symbol and DWARF tables; the version is stamped in rather
  # than read from a metadata file, so there is nothing to ship alongside.
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" \
    go build -trimpath -ldflags "-s -w -X main.version=${version}" \
    -o "$stage/kx${exe}" "$root/cmd/kx"

  # The archive holds kx/kx, matching what .krew.yaml's bin: kx/kx expects.
  archive_root="$outdir/.stage_${os}_${arch}"
  rm -rf "$archive_root"
  mkdir -p "$archive_root/kx"
  cp "$stage/kx${exe}" "$archive_root/kx/kx${exe}"

  # krew-index rejects a plugin whose archive carries no license:
  #
  #   spec.platforms[0] failed to install: LICENSE (or alike) file is not
  #   extracted from the archive as part of installation
  #
  # The PyInstaller archives satisfied this by accident — the bundle swept in
  # kx_cli-<version>.dist-info/licenses/LICENSE along with every dependency's.
  # A Go archive holds one file, so the requirement has to be met deliberately.
  cp "$root/LICENSE" "$archive_root/kx/LICENSE"
  if [ "$os" = "windows" ]; then
    # python3's zipfile rather than zip(1): the wheel build already requires
    # python3, and zip is not installed everywhere this script runs.
    (cd "$archive_root" &&
      python3 -m zipfile -c "$outdir/kx_v${version}_${os}_${arch}.zip" kx)
  else
    tar -C "$archive_root" -czf "$outdir/kx_v${version}_${os}_${arch}.tar.gz" kx
  fi
  rm -rf "$archive_root"
done

echo
echo "archives:"
ls -1 "$outdir"/kx_v"${version}"_*.tar.gz "$outdir"/kx_v"${version}"_*.zip
