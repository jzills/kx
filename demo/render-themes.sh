#!/usr/bin/env bash
# Record demo.tape once per kx palette, for the site's theme picker.
#
# The per-theme tapes are generated first, from demo/demo.tape, so the
# recordings can never demonstrate a different script than the canonical one:
#
#   go run ./tools/gen-demo-tapes
#
# Unlike demo/render.sh this does not touch ~/.kx/config.toml — each tape sets
# KX_THEME for its own recording — and it records the working tree's kx rather
# than whatever is installed, so a GIF cannot show a command the code no longer
# accepts. (demo.tape did exactly that: it ran `kx tree 7 --index` for a while
# after the flag became --no-index.)
#
# Prereqs: vhs, and a seeded cluster with the current namespace set to
# `diagnostics` — see demo/seed.sh.
#
# Usage:
#   demo/render-themes.sh              # every palette
#   demo/render-themes.sh dracula nord # just these
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

# The working tree's kx, ahead of any installed copy.
BIN_DIR="$(mktemp -d)"
trap 'rm -rf "$BIN_DIR"' EXIT
go build -o "$BIN_DIR/kx" ./cmd/kx
export PATH="$BIN_DIR:$PATH"

go run ./tools/gen-demo-tapes

if [[ $# -gt 0 ]]; then
  tapes=()
  for name in "$@"; do
    tapes+=("site/tapes/demo-${name}.tape")
  done
else
  tapes=(site/tapes/demo-*.tape)
fi

for tape in "${tapes[@]}"; do
  echo "==> vhs $tape"
  vhs "$tape"
done
