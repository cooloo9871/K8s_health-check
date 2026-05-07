#!/usr/bin/env bash
# Apply the manifests, wait for the pod to write the PDF, then copy it
# out and clean up.
set -euo pipefail

NS="k8s-healthcheck"
POD="k8s-healthcheck"
OUT="${1:-./k8s-health-report.pdf}"

here="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

echo ">> applying RBAC + pod"
kubectl apply -f "${here}/deploy/rbac.yaml"
kubectl apply -f "${here}/deploy/pod.yaml"

echo ">> waiting for pod to be Ready"
kubectl -n "${NS}" wait --for=condition=Ready "pod/${POD}" --timeout=2m

echo ">> waiting for collector to write the PDF (up to 6 min)"
deadline=$(( $(date +%s) + 360 ))
while : ; do
  pdf=$(kubectl -n "${NS}" exec "${POD}" -- /bin/sh -c 'ls /reports/*.pdf 2>/dev/null | head -1' 2>/dev/null || true)
  if [[ -n "${pdf}" ]]; then
    echo "   found ${pdf}"
    break
  fi
  if (( $(date +%s) > deadline )); then
    echo "!! timed out waiting for PDF" >&2
    kubectl -n "${NS}" logs "${POD}" || true
    exit 1
  fi
  sleep 5
done

echo ">> copying ${pdf} -> ${OUT}"
kubectl -n "${NS}" cp "${POD}:${pdf}" "${OUT}"

echo ">> deleting pod (RBAC and namespace are kept; remove with 'kubectl delete -f deploy/rbac.yaml' if you want)"
kubectl -n "${NS}" delete pod "${POD}" --wait=false

echo ">> done. Report saved to ${OUT}"
