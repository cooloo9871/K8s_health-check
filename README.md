# k8s-healthcheck

一個 Go 撰寫的 Kubernetes 叢集健檢工具，會在叢集**內部**執行，掃描叢集
健康狀態並產出**繁體中文** PDF 報告 (含視覺化圖表)。

設計目標是支援**任何 K8s 發行版**（kubeadm、k3s、RKE2、EKS、GKE、AKS、OpenShift…），
完全透過標準 Kubernetes API 取得資料；當 metrics-server、`/etc/kubernetes/pki`、
`ComponentStatus` 等選擇性資料來源缺席時會優雅降級，不會中斷整份報告。

報告所有時間欄位皆為**台灣當地時間 (Asia/Taipei)**。

## 架構

兩種角色，同一支 binary，以 `--mode` 切換：

```
┌──────────────────────────┐         HTTP GET /data           ┌──────────────────────────┐
│   Deployment             │──────────────────────────────────▶│   DaemonSet (1 / node)   │
│   k8s-healthcheck-       │  list pods + per-node fetch       │   k8s-healthcheck-agent  │
│   aggregator (1 副本)    │◀──────────────────────────────────│   --mode=agent           │
│   --mode=aggregator      │   { Disks, Certs, NodeName, ... } │   :8080/data             │
└──────────────────────────┘                                   └──────────────────────────┘
        │                                                                ▲
        │                                                                │ hostPath /
        ▼                                                                │ (read-only)
   /reports/*.pdf                                                  每個 K8s 節點
```

* **Agent (DaemonSet)**：每個節點上一個 Pod，常駐提供 HTTP 服務，回傳該節點的
  磁碟使用率、kubelet 憑證、控制平面憑證 (在 control-plane 節點上才有)。
  以 root + drop ALL caps + readOnlyRootFilesystem 執行，僅唯讀掛入 host `/`。
* **Aggregator (Deployment, 1 副本)**：一次性執行。透過 API server 列出所有
  agent Pod、HTTP 拉取每一支的 `/data`，把資料與自己抓的 cluster API 資料
  彙整，呼叫 advisor 產生結論與建議，最後寫成 PDF。寫完後 sleep `--sleep-after`，
  期間操作員可以 `kubectl cp` 取出 PDF；之後容器自然結束、Deployment 重建
  → 等於每隔 `--sleep-after` 自動產出新報告。

## 收集內容

| PDF 章節 | 內容 |
| --- | --- |
| **0. 結論與建議** | 整體狀態 (健康 / 警告 / 嚴重)、摘要、主要發現表、依優先級排序的建議事項表 |
| **1. 視覺化儀表板** | Pod 狀態圓餅、憑證到期分佈直方、節點 CPU/記憶體/磁碟水平條 |
| 2. 叢集總覽 | 版本、平台、節點 / Namespace / Pod 數、發行版本標籤、agent 回報節點數 |
| 3. 節點 | 角色、狀態、kubelet/runtime/OS/kernel、內部 IP、存活時間、taints、節點上 Pod 數，以及**所有節點 Conditions** |
| 4. 節點資源使用率 | 透過 `metrics.k8s.io` 取得 CPU/記憶體 使用 vs 容量 (% 與絕對值)、Pod 數 vs Pod 上限 |
| 5. **節點磁碟使用率** | 來自 DaemonSet agent: 每節點 / 每掛點的 Total / Used / Avail / 使用率 / 狀態 |
| 6. Pod 摘要 | Total / Running / Pending / Succeeded / Failed / Unknown |
| 7. 問題 Pod | 未 Ready、CrashLoopBackOff、ImagePullBackOff、≥5 次重啟、Pending |
| 8. 資源耗用前段班 | CPU / 記憶體 用量前 10 名 |
| 9. 工作負載 | Deployments / DaemonSets / StatefulSets / ReplicaSets / Jobs / CronJobs 統計 + 不健康清單 |
| 10. 儲存 | PV phase 統計、PVC phase 統計、StorageClasses (含 default)、Pending PVCs |
| 11. 控制平面健康 | `/healthz`、`/livez`、`/readyz?verbose` (失敗子檢查會列出來)，退回 `componentstatuses` |
| 12. **憑證到期** | 依來源分組: K8s pki / etcd / kubelet / kubeconfig 內嵌；每張顯示節點 / 路徑 / Subject / 到期日 / 剩餘天數 / 狀態 |
| 13. 近期警告事件 | 最近 50 筆全 cluster 非 Normal 事件 |
| 14. 蒐集備註 | 任何非致命的收集器錯誤 |

> collector 自身的 Pod (aggregator + agents) 都會自動從 Pod / Top CPU / Top Memory /
> Events / Pod 總數 等區段中濾掉，不會自我汙染。

### 視覺化圖表

* **Pod 狀態圓餅**：Running / Pending / Failed / Succeeded / Unknown 比例
* **憑證到期分佈直方**：已過期 / <7 天 / 7-30 天 / 30-90 天 / 90-180 天 / >180 天
* **節點 CPU 水平條**、**節點記憶體水平條**、**節點 root 磁碟水平條**：
  各節點同時並列，並依使用率上色 (綠/琥珀/紅)

所有圖表都用 gofpdf 原生繪圖呼叫畫出，無外部圖庫依賴。

### 憑證收集涵蓋

agent DaemonSet 會掃下列來源 (採到後合併進 PDF 第 12 章)：

| 來源 | 路徑 | 涵蓋內容 |
| --- | --- | --- |
| `k8s-pki` | `/etc/kubernetes/pki/*.crt`、`*.pem` | apiserver、apiserver-kubelet-client、front-proxy、CA |
| `etcd` | `/etc/kubernetes/pki/etcd/*.crt` | etcd server / peer / healthcheck-client |
| `kubeconfig` | `/etc/kubernetes/*.conf` 內嵌 | admin / controller-manager / scheduler / kubelet 的 client cert |
| `kubelet` | `/var/lib/kubelet/pki/*.pem` | kubelet-client-current、kubelet-server-current (rotating) |

managed K8s (EKS/GKE/AKS) 多數路徑不存在，agent 會自動略過該類別。

## 結論與建議

報告最前面會有一個由 `internal/advisor` 產生的「結論與建議」章節，內容會依照
偵測到的 cluster 狀態 + 環境標籤動態調整：

* **整體狀態**：健康 / 警告 / 嚴重 (取所有發現的最高嚴重度)
* **主要發現**：節點 / Pod / 工作負載 / 儲存 / 控制平面 / 憑證 / 事件 / 監控 / 資源 / 節點磁碟 等類別
* **建議事項**：每項都含優先級 (高 / 中 / 低)、類別、具體動作、原因說明

### 環境如何判定

可以透過 `--env` 旗標明確指定 `dev` / `staging` / `production`，否則會依下列規則自動推論：

| 條件 | 推論結果 |
| --- | --- |
| Distribution 為 `eks` / `gke` / `aks` / `openshift` | production |
| 節點數 < 3 | dev |
| 節點數 3 ~ 9 | staging |
| 節點數 ≥ 10 | production |

不同環境的閾值（CPU%、記憶體%、憑證剩餘天數、警告事件數量）會跟著調整：

| 環境 | CPU 警告/嚴重 | 記憶體 警告/嚴重 | 憑證 警告/嚴重 | 事件警告 |
| --- | --- | --- | --- | --- |
| production | 70% / 85% | 75% / 90% | 60d / 14d | > 10 |
| staging | 80% / 90% | 85% / 95% | 30d / 7d | > 25 |
| dev | 90% / 98% | 90% / 98% | 14d / 3d | > 50 |

## 快速開始

```bash
# 1. 建置並推送 image (請改成你自己的 registry)
docker build -t <your-registry>/k8s-healthcheck:latest .
docker push <your-registry>/k8s-healthcheck:latest

# 2. 把 manifest 裡的 image 替換成你的版本
sed -i 's|quay.io/cooloo9871/k8s-hk:latest|<your-registry>/k8s-healthcheck:latest|' deploy/all-in-one.yaml

# 3. 部署 (含 DaemonSet agent + Deployment aggregator)
kubectl apply -f deploy/all-in-one.yaml

# 4. 等 agent 就緒
kubectl -n k8s-healthcheck rollout status ds/k8s-healthcheck-agent

# 5. 看 aggregator 產報告
kubectl -n k8s-healthcheck logs -l app=k8s-healthcheck-aggregator -f

# 6. 拉回 PDF
POD=$(kubectl -n k8s-healthcheck get pod -l app=k8s-healthcheck-aggregator \
        -o jsonpath='{.items[0].metadata.name}')
PDF=$(kubectl -n k8s-healthcheck exec "${POD}" -- ls /reports | head -1)
kubectl -n k8s-healthcheck cp "${POD}:/reports/${PDF}" "./${PDF}"
```

## 旗標

### Aggregator (`--mode=aggregator`)

| Flag | 預設值 | 用途 |
| --- | --- | --- |
| `--out` | `/reports` | PDF 輸出目錄 |
| `--timeout` | `5m` | 整體收集逾時時間 |
| `--kubeconfig` | _(空)_ | 外部 kubeconfig 路徑，cluster 外執行時使用 |
| `--pki-dir` | `/host/etc/kubernetes/pki` | 本機 PKI 掃描路徑 (agent 模式下不需掛載) |
| `--sleep-after` | `0` | PDF 寫出後讓 Pod 多存活的時間，方便 `kubectl cp` |
| `--env` | `auto` | 環境標籤：`dev` / `staging` / `production` / `auto` |
| `--agent-namespace` | _(空 = 自身 namespace)_ | agent Pod 所在 namespace |
| `--agent-selector` | `app=k8s-healthcheck-agent` | agent Pod 的 label selector |
| `--agent-port` | `8080` | agent HTTP server port |
| `--agent-timeout` | `10s` | 對單一 agent 的 HTTP 逾時 |

### Agent (`--mode=agent`)

| Flag | 預設值 | 用途 |
| --- | --- | --- |
| `--listen` | `:8080` | HTTP server 監聽位址 |
| `--host-prefix` | `/host` | host 根目錄在容器中的掛點 |

## 為什麼能在任何發行版上運作

* **純 Kubernetes API + per-node hostPath**：aggregator 端只用 client-go；
  agent 端僅讀取節點上的檔案，不依賴 etcdctl 或 kubectl。
* **選擇性輸入優雅降級**：
  * metrics-server 缺席 → 資源使用率區段註明、儀表板顯示「無資料」。
  * `/etc/kubernetes/pki` 缺席 (managed K8s 常見) → 該節點對應憑證類別略過。
  * `componentstatuses` 已 deprecated → 退回 `/healthz?verbose`。
* **容忍所有 taints**，DaemonSet agent 能落到 control-plane / GPU 等任何節點。
* **distroless 基礎映像** + 唯讀 rootfs + drop ALL capabilities，
  使用 `:debug-nonroot` 變體保留 busybox (`tar`、`sh`) 讓 `kubectl cp` 能用。
* **字型與時區內嵌**：Noto Sans TC + IANA tzdata 以 `go:embed` 打進 binary。

## 本機開發

```bash
go mod tidy
go build ./...

# aggregator 模式 (cluster 外執行，跳過 agent 拉取)
go run . --kubeconfig=$HOME/.kube/config --out=./out

# agent 模式 (本機驗證 HTTP 端點，host-prefix 用根目錄即可)
go run . --mode=agent --listen=:8080 --host-prefix=/

# 跑測試 (含 PDF 渲染 smoke test，會驗證圖表 / 中文 / 磁碟告警)
go test ./...
```

## 專案結構

```
.
├── main.go                          # CLI: --mode=aggregator|agent 分派
├── internal/
│   ├── advisor/advisor.go           # 規則式分析，產出結論與建議 (TC)
│   ├── agent/                       # DaemonSet 端
│   │   ├── server.go                # HTTP server (/healthz, /data)
│   │   ├── disk.go                  # statfs 採各掛點使用率
│   │   └── certs.go                 # K8s pki + kubelet + kubeconfig 內嵌憑證
│   ├── collector/                   # aggregator 端 (cluster API 收集)
│   │   ├── collector.go             # orchestrator + 自身識別
│   │   ├── agents.go                # 列舉 agent Pod 並 HTTP 拉資料
│   │   ├── cluster.go               # 版本、計數、發行版本判定
│   │   ├── nodes.go                 # 節點表 + Conditions
│   │   ├── metrics.go               # metrics-server: 節點與 Pod
│   │   ├── pods.go                  # Pod 摘要 + 問題偵測
│   │   ├── workloads.go             # Deploys / DS / STS / RS / Jobs / CronJobs
│   │   ├── storage.go               # PV / PVC / StorageClasses
│   │   ├── events.go                # 警告事件 (近 50 筆)
│   │   ├── components.go            # 舊版 componentstatuses
│   │   ├── api_health.go            # /healthz /livez /readyz?verbose
│   │   └── certs.go                 # 本機 PKI 後備掃描 (agent 模式下不需)
│   ├── model/model.go               # 共用資料型別 (含 NodeAgentData / DiskInfo)
│   ├── report/
│   │   ├── pdf.go                   # gofpdf 多區段渲染
│   │   ├── charts.go                # 圓餅 / 水平條 / 直方圖原生繪製
│   │   ├── pdf_smoke_test.go        # 整合測試 (含磁碟、圖表、TC 字型)
│   │   └── fonts/                   # Noto Sans TC 內嵌字型 (Regular + Bold)
│   └── tz/tz.go                     # Asia/Taipei 時區 + 內嵌 IANA tzdata
├── Dockerfile                       # 兩階段建置，最終以 distroless debug-nonroot 為底
├── deploy/all-in-one.yaml           # Namespace + SA + RBAC + DaemonSet + Service + Deployment
└── README.md
```

## 授權

* 本專案以 MIT 授權釋出，詳見 `LICENSE`。
* 內嵌的 Noto Sans TC 字型 (`internal/report/fonts/`) 使用 SIL Open Font License 1.1，
  詳見 `internal/report/fonts/OFL.txt`。
