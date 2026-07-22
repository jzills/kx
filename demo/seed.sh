#!/usr/bin/env bash
# Seed (or tear down) the `diagnostics` demo namespace used by the VHS tapes
# and the verify flow. The namespace fills with intentionally broken workloads
# so `kx diag`, `kx top`, and status colors have something to show.
#
# Usage:
#   demo/seed.sh            # apply manifests, wait for failure states to settle
#   demo/seed.sh --delete   # tear the namespace down
set -euo pipefail

SEED_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/seed"

if [[ "${1:-}" == "--delete" ]]; then
  kubectl delete namespace diagnostics --ignore-not-found
  exit 0
fi

kubectl apply -f "$SEED_DIR"

echo "Waiting for failure states to settle (CrashLoopBackOff / ImagePullBackOff)..."
deadline=$((SECONDS + 300))
while ((SECONDS < deadline)); do
  statuses="$(kubectl get pods -n diagnostics --no-headers 2>/dev/null | awk '{print $3}')"
  if grep -q CrashLoopBackOff <<<"$statuses" && grep -qE 'ImagePullBackOff|ErrImagePull' <<<"$statuses"; then
    echo "Namespace 'diagnostics' is seeded and suitably broken:"
    kubectl get pods -n diagnostics
    exit 0
  fi
  sleep 5
done

echo "Timed out waiting for failure states; current pods:" >&2
kubectl get pods -n diagnostics >&2
exit 1
