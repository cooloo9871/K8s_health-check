# k8s-healthcheck

一個 Go 撰寫的 Kubernetes 叢集健檢工具，會在叢集**內部**以 CronJob 排程執行，
掃描叢集健康狀態並產出**繁體中文** PDF 報告 (含視覺化圖表)。

設計目標是支援**任何 K8s 發行版** (kubeadm、k3s、RKE2、TKG、OpenShift、EKS、
GKE、AKS...)。資料完全透過標準 Kubernetes API + 節點本機檔案取得；當
metrics-server、`/etc/kubernetes/pki`、`ComponentStatus` 等選擇性資料來源
缺席時會優雅降級，不會中斷整份報告。

報告所有時間欄位皆為**台灣當地時間 (Asia/Taipei)**，叢集識別以 cluster name
為準 (不再分 dev / staging / production)。

## 架構

短命 (ephemeral) 設計: 沒有常駐元件，只有一個 CronJob。每次觸發時建立 Job →
Runner Pod，由 Runner 動態拉起 agent DaemonSet 收集資料，產報後立即刪除
DaemonSet。

```
                       ┌──────────────────┐
                       │  CronJob         │  schedule (預設每日 08:00)
                       │  k8s-healthcheck │
                       └────────┬─────────┘
                                │ 派生
                                ▼
   ┌─────────────────────────────────────────────────────────┐
   │  Job → Runner Pod (k8s-healthcheck, --mode=aggregator)  │
   │                                                         │
   │   1. client-go 動態建立 DaemonSet (agent)               │
   │   2. 等所有 agent Ready                                 │
   │   3. HTTP GET /data 拉每節點資訊  ◀────┐                │
   │   4. advisor.Analyze 產出結論          │                │
   │   5. 寫 PDF 到 emptyDir                │                │
   │   6. 刪除 agent DaemonSet              │                │
   │   7. sleep --sleep-after (供 kubectl cp 取 PDF)         │
   └────────────────────────────────────────┼────────────────┘
                                            │
                          短命 DaemonSet ───┘
              ┌───────────────────────────────────────┐
              │   k8s-healthcheck-agent  (--mode=agent)│
              │   每節點 1 Pod, 唯讀 hostPath /        │
              │   :8080 /data → DiskInfo + CertInfo    │
              └───────────────────────────────────────┘
```

* **Runner Pod (`--mode=aggregator`)**: 由 Job 派生的一次性容器, 透過 API server
  動態 create/delete DaemonSet, 收集 cluster API 資料, 拉所有 agent 的 `/data`,
  彙整出 PDF, 最後 sleep 等使用者 `kubectl cp` 把 PDF 取出.
* **Agent (`--mode=agent`)**: 短命 DaemonSet, 每節點 1 Pod. 提供 `:8080/data`
  HTTP 端點回傳該節點的磁碟使用率、kubelet 憑證、Control-plane 憑證 (僅
  control-plane 節點才有). 唯讀掛入 host `/`, drop ALL caps.

`concurrencyPolicy: Replace` 確保下一輪觸發時若上一個 Runner 還在 sleep, 會被
取代; agent DaemonSet 因為 Runner 主動刪除 + defer 兜底, 不會殘留.

## 收集內容 (PDF 章節)

| 章節 | 內容 |
| --- | --- |
| **0. 結論與建議** | 整體狀態 (健康 / 警告 / 嚴重)、摘要、主要發現表、依優先級排序的建議事項表; 異常 Pod / 節點名稱會直接列在 Detail 中 |
| **1. 視覺化儀表板** | Pod 狀態圓餅 (含紅色「異常」切片)、憑證到期分佈直方、節點 CPU / 記憶體 / 磁碟水平條 |
| 2. 叢集總覽 | 版本、平台、節點 / Namespace / Pod 數、發行版本標籤、agent 回報節點數 |
| 3. 節點 | 名稱、角色、狀態、kubelet/runtime/OS、內部 IP、存活時間, 以及**所有節點 Conditions** |
| 4. 節點資源使用率 | `metrics.k8s.io`: CPU / 記憶體 使用 vs 容量 (% 與絕對值)、Pod 數 vs Pod 上限 |
| 5. **節點磁碟使用率** | 來自 agent: 每節點 / 每掛點 Total / Used / Avail / 使用率 / 狀態 (label 過長自動換行) |
| 6. Pod 摘要 | Total / Running / Pending / Succeeded / Failed / Unknown |
| 7. 問題 Pod | 未 Ready / CrashLoopBackOff / ImagePullBackOff / >=5 次重啟 / Pending. **Phase 欄會顯示實際狀態** (CrashLoop pod 不再誤顯示為 Running) |
| **8. Pod 總覽** | **全部** (非 collector 自身) Pod 的詳細清單: ns / name / phase / Pod IP / 排程節點 / **HostPath 掛載目錄**. kube-system Pod 一律排在最後 |
| 9. 資源耗用前段班 | CPU / 記憶體 用量前 10 名 |
| 10. 工作負載 | Deployments / DaemonSets / StatefulSets / ReplicaSets / Jobs / CronJobs 統計 + 不健康清單 |
| 11. 儲存 | StorageClasses (含 default 標記) + PV 詳情 + PVC 詳情 |
| 12. Control-plane 健康 | `/healthz` `/livez` `/readyz?verbose` (失敗子檢查列出來), 退回 `componentstatuses` |
| **13. 憑證到期** | 依來源分組: K8s pki / etcd / kubelet / kubeconfig 內嵌; 每張顯示節點 / **完整路徑 (自動換行)** / 到期日 / 剩餘天數 / 狀態 |
| 14. 近期警告事件 | 最近 50 筆全 cluster 非 Normal 事件 |
| 15. 蒐集備註 | 任何非致命的收集器錯誤 |

> collector 自身的 Pod (Runner + agent DaemonSet, 凡 `k8s-healthcheck-*`)
> 都會自動從 Pod 摘要 / 問題 Pod / Pod 總覽 / Top CPU / Top Memory / Events
> 等區段中濾掉, 不會自我汙染.

### 視覺化圖表

* **Pod 狀態圓餅**: Running (健康) / 異常 (Crash 或重啟) / Pending / Failed /
  Succeeded / Unknown 比例. 「異常」切片用 K8s 原始 Phase=Running 但實際異常
  的 Pod 數量計算, 凸顯 CrashLoop 的影響.
* **憑證到期分佈直方**: 已過期 / <7 天 / 7-30 天 / 30-90 天 / 90-180 天 / >180 天
* **節點 CPU / 記憶體 / 磁碟水平條**: 各節點並列, 依使用率上色 (綠 / 琥珀 / 紅);
  label 過長自動換行而非截斷

所有圖表都用 gofpdf 原生繪圖呼叫畫出, 無外部圖庫依賴.

### 憑證收集涵蓋

agent DaemonSet 會掃下列來源 (採到後合併進 PDF 第 13 章):

| 來源 | 路徑 | 涵蓋內容 |
| --- | --- | --- |
| `k8s-pki` | `/etc/kubernetes/pki/*.crt`, `*.pem` | apiserver、apiserver-kubelet-client、front-proxy、CA |
| `etcd` | `/etc/kubernetes/pki/etcd/*.crt` | etcd server / peer / healthcheck-client |
| `kubeconfig` | `/etc/kubernetes/*.conf` 內嵌 | admin / controller-manager / scheduler / kubelet 的 client cert |
| `kubelet` | `/var/lib/kubelet/pki/*.pem` | kubelet-client-current、kubelet-server-current (rotating) |

managed K8s (EKS / GKE / AKS) 多數路徑不存在, agent 會自動略過該類別.

## 結論與建議

報告最前面會有由 `internal/advisor` 產生的「結論與建議」章節:

* **整體狀態**: 健康 / 警告 / 嚴重 (取所有發現的最高嚴重度)
* **主要發現**: 節點 / Pod / 工作負載 / 儲存 / Control-plane / 憑證 / 事件 /
  監控 / 資源 / 節點磁碟 等類別
* **建議事項**: 每項含優先級 (高 / 中 / 低)、類別、具體動作、原因說明

### 嚴重度規則

* **K8s 系統元件**異常 → **Critical** (嚴重)
  * 認定範圍: kube-system / openshift-* / vmware-system-* / calico-system /
    longhorn-system 等系統 namespace, 或名稱包含 etcd-, kube-apiserver,
    kube-controller-manager, kube-scheduler, kube-proxy, coredns, calico,
    cilium, flannel, weave-net, kube-router, kindnet 的 Pod
  * 涵蓋: CrashLoopBackOff / OOMKilled / ImagePullBackOff
* **應用 workload** 異常 → **Warning** (警告)
  * 一般 namespace 的 CrashLoop / OOMKilled / ImagePullBackOff
  * 高重啟、Pending 等
* **Unknown** Pod 永遠視為 Critical (kubelet 失聯本身就是系統問題)

### 閾值 (單一組, 不分環境)

| 項目 | 警告 | 嚴重 |
| --- | --- | --- |
| 節點 CPU% | 75 | 90 |
| 節點記憶體% | 80 | 92 |
| 憑證剩餘天數 | <=30 | <=7 |
| 警告事件數 | >25 | - |
| 節點磁碟使用率 | >=80 | >=90 |

## 快速開始

```bash
# 1. 建置並推送 image (改成你自己的 registry)
docker build -t <your-registry>/k8s-healthcheck:latest .
docker push <your-registry>/k8s-healthcheck:latest

# 2. 替換 manifest 內 image
sed -i 's|quay.io/cooloo9871/k8s-hk:latest|<your-registry>/k8s-healthcheck:latest|' deploy/all-in-one.yaml

# 3. 部署 (建立 Namespace + SA + RBAC + CronJob, 暫不會真的跑)
kubectl apply -f deploy/all-in-one.yaml

# 4. 立即觸發一輪 (不等排程). NAME 必須放在 --from 之前
kubectl -n k8s-healthcheck create job k8s-healthcheck-manual-$(date +%s) \
  --from=cronjob/k8s-healthcheck

# 5. 看 Runner 跑
kubectl -n k8s-healthcheck logs -l app=k8s-healthcheck-runner -f

# 6. Runner 會 sleep --sleep-after, 期間從 emptyDir 取 PDF
POD=$(kubectl -n k8s-healthcheck get pod -l app=k8s-healthcheck-runner \
        -o jsonpath='{.items[?(@.status.phase=="Running")].metadata.name}')
PDF=$(kubectl -n k8s-healthcheck exec "${POD}" -- ls /reports | head -1)
kubectl -n k8s-healthcheck cp "${POD}:/reports/${PDF}" "./${PDF}"

# 7. 檢視複製出來的 pdf 檔
ls -l *.pdf
-rw-rw-r-- 1 bigred bigred 133805 Jun 16 15:35 topgun-health-20260616-153250.pdf
```

預設 schedule 為每日 08:00 (Asia/Taipei). 修改 `deploy/all-in-one.yaml` 的
`spec.schedule` 與 `spec.timeZone` 可調整週期.

## 旗標

### Aggregator / Runner (`--mode=aggregator`, 預設)

| Flag | 預設值 | 用途 |
| --- | --- | --- |
| `--out` | `/reports` | PDF 輸出目錄 |
| `--timeout` | `5m` | 整體收集逾時 |
| `--kubeconfig` | _(空)_ | 外部 kubeconfig 路徑 (cluster 外執行時用) |
| `--pki-dir` | `/host/etc/kubernetes/pki` | 本機 PKI 掃描路徑 (DaemonSet 模式下不需要) |
| `--sleep-after` | `0` | PDF 寫出後讓 Pod 多存活的時間, 方便 `kubectl cp` |
| `--cluster-name` | _(空 = 自動偵測)_ | 顯示在報告上的 cluster 名稱. 自動偵測順序: kubeadm-config ConfigMap → kube-system namespace UID 前 12 碼 |
| `--healthcheck-namespace` | _(空 = 自身 ns)_ | 系統部署所在 ns; 該 ns 下所有 `k8s-healthcheck-*` Pod 都會從報告中過濾 |
| `--orchestrate-agent` | `false` | 啟動時動態建立 agent DaemonSet, 結束時刪除. CronJob 模式必須開啟 |
| `--agent-image` | `$AGENT_IMAGE` | agent DaemonSet 使用的 image (通常與 Runner 同 image) |
| `--agent-daemonset-name` | `k8s-healthcheck-agent` | 動態建立的 DaemonSet 名稱 |
| `--agent-ready-timeout` | `2m` | 等待 DaemonSet 全部 Ready 的逾時 |
| `--agent-namespace` | _(空 = 自身 ns)_ | agent Pod 所在 namespace |
| `--agent-selector` | `app=k8s-healthcheck-agent` | agent Pod 的 label selector |
| `--agent-port` | `8080` | agent HTTP server port |
| `--agent-timeout` | `10s` | 對單一 agent 的 HTTP 逾時 |

### Agent (`--mode=agent`)

| Flag | 預設值 | 用途 |
| --- | --- | --- |
| `--listen` | `:8080` | HTTP server 監聽位址 |
| `--host-prefix` | `/host` | host 根目錄在容器中的掛點 |

## 為什麼能在任何發行版上運作

* **純 Kubernetes API + per-node hostPath**: Runner 端只用 client-go;
  agent 端僅讀取節點上的檔案, 不依賴 etcdctl 或 kubectl.
* **多訊號發行版偵測** (`internal/collector/cluster.go`):
  GitVersion → Namespace marker → Node label → kubeadm-config ConfigMap →
  退回 generic `k8s`. 涵蓋 OCP / RKE2 / TKG / EKS / GKE / AKS / k3s /
  kubeadm.
* **選擇性輸入優雅降級**:
  * metrics-server 缺席 → 資源使用率區段註明, 儀表板顯示「無資料」
  * `/etc/kubernetes/pki` 缺席 (managed K8s 常見) → 該節點對應憑證類別略過
  * `componentstatuses` 已 deprecated → 退回 `/healthz?verbose`
* **容忍所有 taints**, agent DaemonSet 能落到 control-plane / GPU 等任何節點.
* **Distroless `:debug-nonroot` 基礎映像** + 唯讀 rootfs + drop ALL caps,
  保留 busybox (`tar`, `sh`) 讓 `kubectl cp` 能用.
* **字型與時區內嵌**: Noto Sans TC + IANA tzdata 以 `go:embed` 打進 binary,
  PDF 不依賴系統字型, 也能在 distroless 中正確顯示 Asia/Taipei.

## 本機開發

```bash
go mod tidy
go build ./...

# Aggregator 模式 (cluster 外執行, 無 DaemonSet 動態管理)
go run . --kubeconfig=$HOME/.kube/config --out=./out

# Agent 模式 (本機驗證 HTTP 端點; host-prefix 用根目錄即可)
go run . --mode=agent --listen=:8080 --host-prefix=/

# 跑測試 (含 PDF 渲染 smoke test, 驗證圖表 / TC 字型 / 磁碟告警)
go test ./...

# 完整三輪驗證 (vet + build + test)
go vet ./... && go build ./... && go test ./... -count=1
```

## 專案結構

```
.
├── main.go                            # CLI: --mode=aggregator|agent 分派
├── internal/
│   ├── advisor/
│   │   ├── advisor.go                 # 規則式分析, 產出結論與建議 (TC)
│   │   └── advisor_test.go            # 嚴重度分類 / 系統 vs 應用 / 受影響清單
│   ├── agent/                         # DaemonSet 端
│   │   ├── server.go                  # HTTP server (/healthz, /data)
│   │   ├── disk.go                    # statfs 採各掛點使用率
│   │   └── certs.go                   # K8s pki + kubelet + kubeconfig 內嵌憑證
│   ├── collector/                     # aggregator 端 (cluster API 收集)
│   │   ├── collector.go               # orchestrator + 自身識別
│   │   ├── orchestrate.go             # 動態建立 / 刪除 agent DaemonSet
│   │   ├── agents.go                  # 列舉 agent Pod 並 HTTP 拉資料
│   │   ├── cluster.go                 # 版本、計數、發行版本判定
│   │   ├── nodes.go                   # 節點表 + Conditions
│   │   ├── metrics.go                 # metrics-server: 節點與 Pod
│   │   ├── pods.go                    # Pod 摘要 + 問題偵測 + Pod 總覽 + hostPath
│   │   ├── workloads.go               # Deploys / DS / STS / RS / Jobs / CronJobs
│   │   ├── storage.go                 # PV / PVC / StorageClasses
│   │   ├── events.go                  # 警告事件 (近 50 筆)
│   │   ├── components.go              # 舊版 componentstatuses
│   │   ├── api_health.go              # /healthz /livez /readyz?verbose
│   │   ├── certs.go                   # 本機 PKI 後備掃描
│   │   └── *_test.go                  # isSelf / hostPath 抽取 / Pod 排序 / DaemonSet 建構
│   ├── model/model.go                 # 共用資料型別 (含 PodOverview / HostPathMount / NodeAgentData)
│   ├── report/
│   │   ├── pdf.go                     # gofpdf 多區段渲染
│   │   ├── charts.go                  # 圓餅 / 水平條 / 直方圖原生繪製
│   │   ├── pdf_smoke_test.go          # 整合測試 (含磁碟、圖表、TC 字型)
│   │   └── fonts/                     # Noto Sans TC 內嵌字型 (Regular + Bold)
│   └── tz/tz.go                       # Asia/Taipei 時區 + 內嵌 IANA tzdata
├── Dockerfile                         # 兩階段建置, 最終以 distroless debug-nonroot 為底
├── deploy/all-in-one.yaml             # Namespace + SA + RBAC + CronJob
└── README.md
```

## 授權

* 本專案以 MIT 授權釋出, 詳見 `LICENSE`.
* 內嵌的 Noto Sans TC 字型 (`internal/report/fonts/`) 使用 SIL Open Font
  License 1.1, 詳見 `internal/report/fonts/OFL.txt`.
