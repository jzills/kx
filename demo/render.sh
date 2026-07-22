#!/usr/bin/env bash
# Render every VHS tape in demo/ to its GIF (each tape's Output directive
# decides the filename). Run from anywhere; tapes execute from the repo root
# so their hidden prelude can source .venv.
#
# Prereqs: vhs (https://github.com/charmbracelet/vhs), a seeded cluster
# (demo/seed.sh), and kubectl's current namespace set to `diagnostics`.
#
# Usage:
#   demo/render.sh              # render all tapes
#   demo/render.sh diag         # render just demo/diag.tape
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

if [[ $# -gt 0 ]]; then
  tapes=()
  for name in "$@"; do
    tapes+=("demo/${name%.tape}.tape")
  done
else
  tapes=(demo/*.tape)
fi

for tape in "${tapes[@]}"; do
  echo "==> vhs $tape"
  vhs "$tape"
done
