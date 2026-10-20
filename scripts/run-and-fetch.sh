#!/usr/bin/env bash
# 套用 deploy/all-in-one.yaml、手動觸發一輪 CronJob，等 Runner 把 PDF 寫出來後
# kubectl cp 取出，最後提早清掉這次手動建立的 Job (Runner 本來會依
# --sleep-after 繼續待機方便 cp，取完就沒必要再等)。
#
# 用法: scripts/run-and-fetch.sh [輸出目錄，預設為目前目錄]
set -euo pipefail

NS="k8s-healthcheck"
OUT_DIR="${1:-.}"

here="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

echo ">> 套用 manifests (namespace + RBAC + CronJob)"
kubectl apply -f "${here}/deploy/all-in-one.yaml"

job_name="k8s-healthcheck-manual-$(date +%s)"
echo ">> 手動觸發一輪: ${job_name}"
kubectl -n "${NS}" create job "${job_name}" --from=cronjob/k8s-healthcheck

echo ">> 等待 Runner Pod 啟動"
pod=""
deadline=$(( $(date +%s) + 120 ))
while [[ -z "${pod}" ]]; do
  pod=$(kubectl -n "${NS}" get pod -l "job-name=${job_name}" \
        --field-selector=status.phase=Running \
        -o jsonpath='{.items[0].metadata.name}' 2>/dev/null || true)
  if [[ -n "${pod}" ]]; then
    break
  fi
  if (( $(date +%s) > deadline )); then
    echo "!! 等待 Runner Pod 啟動逾時" >&2
    kubectl -n "${NS}" describe job "${job_name}" || true
    exit 1
  fi
  sleep 3
done
echo "   pod: ${pod}"

echo ">> 等待 Runner 把 PDF 寫出來 (最長 6 分鐘)"
deadline=$(( $(date +%s) + 360 ))
pdf=""
while : ; do
  pdf=$(kubectl -n "${NS}" exec "${pod}" -- ls /reports 2>/dev/null | head -1 || true)
  if [[ -n "${pdf}" ]]; then
    echo "   找到 ${pdf}"
    break
  fi
  if (( $(date +%s) > deadline )); then
    echo "!! 等待 PDF 逾時" >&2
    kubectl -n "${NS}" logs "${pod}" || true
    exit 1
  fi
  sleep 5
done

out="${OUT_DIR%/}/${pdf}"
echo ">> 複製 ${pdf} -> ${out}"
kubectl -n "${NS}" cp "${NS}/${pod}:/reports/${pdf}" "${out}"

echo ">> 提早刪除本次手動 Job (RBAC / CronJob / Namespace 保留)"
kubectl -n "${NS}" delete job "${job_name}" --wait=false

echo ">> 完成，報告已存至 ${out}"
