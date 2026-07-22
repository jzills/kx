#!/usr/bin/env bash
# Render every VHS tape in demo/ to its GIF (each tape's Output directive
# decides the filename). Run from anywhere; tapes execute from the repo root.
#
# Handles recording setup so the tapes don't have to: activates .venv so `kx`
# is on PATH inside vhs, pins the theme to github-dark for consistent output,
# and restores your configured theme afterward.
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

export VIRTUAL_ENV_DISABLE_PROMPT=1
# shellcheck disable=SC1091
source .venv/bin/activate

# Pin the theme for recording; put the user's theme back afterward
# (theme.tape switches themes, which persists to ~/.kx/config.toml).
orig_theme="$(kx theme 2>/dev/null | awk '/→/ {print $3}')"
restore_theme() {
  [[ -n "$orig_theme" ]] && kx theme "$orig_theme" >/dev/null
}
trap restore_theme EXIT
kx theme github-dark >/dev/null

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
