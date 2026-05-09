// Package advisor 檢視已填好的 *model.Report，產出人類可讀的結論
// (整體狀態、主要發現、依優先級排序的建議事項)。
// 規則刻意偏保守: 寧可誤報也不漏報。輸出統一使用繁體中文。
//
// 從 2026-05 起，這個套件不再依「dev / staging / production」分層使用不同
// 閾值。所有 cluster 都套用同一套保守閾值；環境的識別改為由 cluster 名稱
// 直接呈現於報告。
package advisor

import (
	"fmt"
	"strings"

	"k8s-health-check/internal/model"
)

const (
	SeverityCritical = "嚴重"
	SeverityWarning  = "警告"
	SeverityInfo     = "資訊"

	StatusHealthy  = "健康"
	StatusWarning  = "警告"
	StatusCritical = "嚴重"

	PriorityHigh   = "高"
	PriorityMedium = "中"
	PriorityLow    = "低"
)

// defaultThresholds 是套用於所有 cluster 的單一閾值組。數值取舊 staging 與
// production 之間，較貼近一般正式環境的最佳實踐。
var defaultThresholds = thresholds{
	cpuWarn:      75,
	cpuCrit:      90,
	memWarn:      80,
	memCrit:      92,
	certWarnDays: 30,
	certCritDays: 7,
	eventsWarn:   25,
}

// Analyze 依據 r 其他欄位的內容填入 r.Conclusion。
// 此函式呼叫不再需要環境參數: cluster 名稱已存在 r.Cluster.Name 中，由 PDF
// 渲染端負責顯示。
func Analyze(r *model.Report) {
	c := model.Conclusion{}

	checkNodes(r, &c)
	checkPods(r, &c)
	checkWorkloads(r, &c)
	checkStorage(r, &c)
	checkControlPlane(r, &c)
	checkCerts(r, &c)
	checkEvents(r, &c)
	checkMetrics(r, &c)
	checkDistribution(r, &c)
	checkNodeAgents(r, &c)

	c.OverallStatus = rollupStatus(c.Findings)
	c.Summary = buildSummary(r, &c)
	r.Conclusion = c
}

// 套用於所有規則的單一閾值組。各規則直接讀 defaultThresholds。
type thresholds struct {
	cpuWarn, cpuCrit float64
	memWarn, memCrit float64
	certWarnDays     int
	certCritDays     int
	eventsWarn       int
}

func priorityFor(severity string) string {
	switch severity {
	case SeverityCritical:
		return PriorityHigh
	case SeverityWarning:
		return PriorityMedium
	default:
		return PriorityLow
	}
}

// ---------- 各類規則 -----------------------------------------------------

func checkNodes(r *model.Report, c *model.Conclusion) {
	if len(r.Nodes) == 0 {
		return
	}
	notReady := 0
	pressure := 0
	for _, n := range r.Nodes {
		if !strings.EqualFold(n.Status, "Ready") {
			notReady++
		}
		for _, cond := range n.Conditions {
			if cond.Type == "Ready" {
				continue
			}
			// 其他條件 True 都代表異常 (DiskPressure / MemoryPressure / PIDPressure / NetworkUnavailable)
			if cond.Status == "True" {
				pressure++
			}
		}
	}
	if notReady > 0 {
		addFinding(c, SeverityCritical, "節點",
			fmt.Sprintf("有 %d 個節點不在 Ready 狀態", notReady),
			"節點失聯會導致其上 Pod 被驅逐或無法調度，請優先檢查 kubelet 與網路。")
		addRec(c, SeverityCritical, "節點",
			"立即檢查不 Ready 節點上的 kubelet、容器執行階段與網路連線",
			"未恢復前可考慮 cordon 並 drain 受影響節點，避免新工作負載落上。")
	}
	if pressure > 0 {
		addFinding(c, SeverityWarning, "節點",
			fmt.Sprintf("偵測到 %d 項節點 Pressure / NetworkUnavailable 狀況", pressure),
			"節點層級資源不足或網路異常會直接影響工作負載穩定性。")
		addRec(c, SeverityWarning, "節點",
			"擴充節點或清理對應壓力來源 (磁碟、記憶體、PID、CNI)",
			"長期 Pressure 通常導致 OOMKilled 或 Pod 被驅逐。")
	}
}

func checkPods(r *model.Report, c *model.Conclusion) {
	s := r.PodSummary
	if s.Failed > 0 {
		addFinding(c, SeverityWarning, "Pod",
			fmt.Sprintf("有 %d 個 Pod 處於 Failed 狀態", s.Failed),
			"Failed Pod 通常是 Job 例外結束或容器啟動失敗。")
		addRec(c, SeverityWarning, "Pod",
			"檢查 Failed Pod 的 logs 與 events，確認是否需要重新調度或修正應用程式錯誤",
			"若為 Job 預期失敗可忽略；否則應排除原因避免擴散。")
	}
	if s.Unknown > 0 {
		addFinding(c, SeverityCritical, "Pod",
			fmt.Sprintf("有 %d 個 Pod 處於 Unknown 狀態", s.Unknown),
			"Unknown 通常代表節點失聯，kubelet 無法回報 Pod 狀態。")
		addRec(c, SeverityCritical, "Pod",
			"找出 Unknown Pod 所在節點並檢查 kubelet 與網路",
			"持續 Unknown 會導致 controller 重新建立同名 Pod，造成資源浪費。")
	}
	if len(r.ProblemPods) > 0 {
		// 細分 reason
		crash := 0
		imgPull := 0
		pending := 0
		highRestart := 0
		oomKilled := 0
		for _, p := range r.ProblemPods {
			low := strings.ToLower(p.Reason + " " + p.Status + " " + p.Message)
			switch {
			case strings.Contains(low, "crashloop"):
				crash++
			case strings.Contains(low, "imagepull"), strings.Contains(low, "errimage"):
				imgPull++
			case strings.EqualFold(p.Status, "Pending"):
				pending++
			}
			if strings.Contains(low, "oomkilled") {
				oomKilled++
			}
			if p.Restarts >= 5 {
				highRestart++
			}
		}
		if crash > 0 {
			addFinding(c, SeverityCritical, "Pod",
				fmt.Sprintf("CrashLoopBackOff Pod 共 %d 個", crash),
				"應用程式持續啟動失敗，通常是設定錯誤、相依服務未就緒或 OOMKilled。")
			addRec(c, SeverityCritical, "Pod",
				"檢視 CrashLoop 容器的最後一筆退出原因 (kubectl describe / logs --previous)",
				"可同時檢查資源限制與健康探針門檻是否合理。")
		}
		if oomKilled > 0 {
			addFinding(c, SeverityCritical, "Pod",
				fmt.Sprintf("OOMKilled Pod 共 %d 個", oomKilled),
				"容器記憶體用量超過 limit，被 cgroup OOM 殺掉。長期可能造成資料丟失或外部互動失敗。")
			addRec(c, SeverityCritical, "Pod",
				"放寬該 Pod 的 memory limit 或修正記憶體洩漏",
				"先用 Top Memory 表確認哪些容器穩定吃高，再調整。")
		}
		if imgPull > 0 {
			addFinding(c, SeverityWarning, "Pod",
				fmt.Sprintf("ImagePullBackOff / ErrImagePull Pod 共 %d 個", imgPull),
				"映像檔取得失敗: 可能是 tag 錯誤、registry 認證失效或網路無法到達。")
			addRec(c, SeverityWarning, "Pod",
				"確認 image 名稱、tag、imagePullSecrets 與節點對 registry 的連線",
				"建議在 CI/CD 加入推送後 smoke test，避免此類部署失敗。")
		}
		if pending > 0 {
			addFinding(c, SeverityWarning, "Pod",
				fmt.Sprintf("Pending Pod 共 %d 個", pending),
				"Pending 多半是資源不足、節點選擇器不匹配或 PVC 未綁定。")
			addRec(c, SeverityWarning, "Pod",
				"用 kubectl describe pod 看 Events，鎖定 unschedulable 的具體原因",
				"若為資源不足可考慮加節點或調整 requests。")
		}
		if highRestart > 0 {
			addFinding(c, SeverityWarning, "Pod",
				fmt.Sprintf("有 %d 個 Pod 重啟 ≥ 5 次", highRestart),
				"頻繁重啟代表應用程式不穩定，可能影響 SLO。")
			addRec(c, SeverityWarning, "Pod",
				"檢查健康探針設定與應用程式錯誤處理邏輯",
				"liveness 過於嚴格也常造成不必要的重啟。")
		}
	}
}

func checkWorkloads(r *model.Report, c *model.Conclusion) {
	if len(r.Workloads.Unhealthy) == 0 {
		return
	}
	addFinding(c, SeverityWarning, "工作負載",
		fmt.Sprintf("有 %d 個工作負載 (Deployment/DS/STS) 不健康", len(r.Workloads.Unhealthy)),
		"Desired 與 Ready 副本數不一致，使用者流量可能落到尚未就緒的副本上。")
	addRec(c, SeverityWarning, "工作負載",
		"逐一檢查 Unhealthy 表中的工作負載並排除 rollout 失敗",
		"DaemonSet 不齊通常代表特定節點上無法調度該 Pod。")
}

func checkStorage(r *model.Report, c *model.Conclusion) {
	s := r.Storage
	if s.PVsFailed > 0 {
		addFinding(c, SeverityCritical, "儲存",
			fmt.Sprintf("有 %d 個 PV 處於 Failed 狀態", s.PVsFailed),
			"Failed PV 已無法重新綁定，仰賴它的 Pod 將永遠 Pending。")
		addRec(c, SeverityCritical, "儲存",
			"檢查底層儲存 (CSI driver、後端磁碟) 並決定回收或重建 PV",
			"建議同步檢查 CSI controller 的 logs。")
	}
	if s.PVCsPending > 0 {
		addFinding(c, SeverityWarning, "儲存",
			fmt.Sprintf("有 %d 個 PVC 處於 Pending", s.PVCsPending),
			"Pending PVC 多半是 StorageClass 不存在 / provisioner 沒在跑 / 容量不足。")
		addRec(c, SeverityWarning, "儲存",
			"確認預設 StorageClass 設定與 CSI provisioner 是否健康",
			"靜態 PV 場景請檢查節點親和性與 capacity matching。")
	}
	if len(s.StorageClasses) == 0 {
		addFinding(c, SeverityInfo, "儲存", "未偵測到 StorageClass",
			"沒有 StorageClass 表示動態配置 PV 不可用，需手動建立 PV。")
	}
}

func checkControlPlane(r *model.Report, c *model.Conclusion) {
	// API health 端點的 Status 可能是: "Healthy" (全綠)、"Degraded" (有 [-]
	// 子檢查失敗)、"Failed" (HTTP 呼叫本身失敗)。任何不是 healthy/ok 的都算異常。
	bad := 0
	for _, h := range r.APIHealth {
		status := strings.ToLower(strings.TrimSpace(h.Status))
		if status == "" || status == "healthy" || status == "ok" || strings.Contains(status, "200") {
			continue
		}
		bad++
	}
	if bad > 0 {
		addFinding(c, SeverityCritical, "控制平面",
			fmt.Sprintf("控制平面健康端點有 %d 項異常", bad),
			"/healthz、/livez 或 /readyz 失敗代表 etcd、scheduler 或 controller-manager 可能有狀況。")
		addRec(c, SeverityCritical, "控制平面",
			"檢視 verbose 的 /readyz 輸出鎖定異常子系統，並確認 etcd 健康",
			"控制平面異常會放大其他所有問題。")
	}
	for _, comp := range r.Components {
		if !strings.EqualFold(comp.Healthy, "True") && !strings.EqualFold(comp.Healthy, "Healthy") {
			addFinding(c, SeverityWarning, "控制平面",
				fmt.Sprintf("Component %s 狀態異常", comp.Name), comp.Message)
		}
	}
}

func checkCerts(r *model.Report, c *model.Conclusion) {
	if len(r.Certs) == 0 {
		return
	}
	t := defaultThresholds
	expired, crit, warn := 0, 0, 0
	soonest := -1
	for _, ct := range r.Certs {
		switch {
		case ct.DaysLeft < 0:
			expired++
		case ct.DaysLeft <= t.certCritDays:
			crit++
		case ct.DaysLeft <= t.certWarnDays:
			warn++
		}
		if soonest == -1 || ct.DaysLeft < soonest {
			soonest = ct.DaysLeft
		}
	}
	if expired > 0 {
		addFinding(c, SeverityCritical, "憑證",
			fmt.Sprintf("有 %d 張憑證已過期", expired),
			"過期憑證會導致 API server 互信中斷，整個控制平面可能停擺。")
		addRec(c, SeverityCritical, "憑證",
			"立刻續發過期憑證 (kubeadm certs renew all 或對應 distro 流程)",
			"過期後 kubeadm 自動續發功能可能也無法存取 API。")
	}
	if crit > 0 {
		addFinding(c, SeverityCritical, "憑證",
			fmt.Sprintf("有 %d 張憑證將在 %d 天內到期", crit, t.certCritDays),
			fmt.Sprintf("最近一張剩 %d 天。", soonest))
		addRec(c, SeverityCritical, "憑證",
			"在到期前安排 kubeadm certs renew 或 distro 對應流程，並備份 /etc/kubernetes/pki",
			"建議納入排程 (cronjob) 或加上監控告警避免再次逼近到期。")
	} else if warn > 0 {
		addFinding(c, SeverityWarning, "憑證",
			fmt.Sprintf("有 %d 張憑證將在 %d 天內到期", warn, t.certWarnDays),
			fmt.Sprintf("最近一張剩 %d 天。", soonest))
		addRec(c, SeverityWarning, "憑證",
			"安排換發時程並建立到期前的告警機制",
			"可結合 cert-manager 或外部監控如 prometheus blackbox。")
	}
}

func checkEvents(r *model.Report, c *model.Conclusion) {
	if len(r.Events) > defaultThresholds.eventsWarn {
		addFinding(c, SeverityWarning, "事件",
			fmt.Sprintf("近期警告事件 %d 筆 (門檻 %d)", len(r.Events), defaultThresholds.eventsWarn),
			"事件量過多通常代表有重複失敗的 controller 或 webhook。")
		addRec(c, SeverityWarning, "事件",
			"從事件列表找出最頻繁的 reason，集中排除根因",
			"建議將事件導入長期儲存 (Loki / ES) 以便追蹤趨勢。")
	}
}

func checkMetrics(r *model.Report, c *model.Conclusion) {
	if len(r.NodeMetrics) == 0 {
		addFinding(c, SeverityInfo, "監控",
			"metrics-server 未安裝或無法存取",
			"沒有資源使用率資料就難以做容量規劃與 HPA。")
		addRec(c, SeverityInfo, "監控",
			"安裝 metrics-server 以啟用節點 / Pod 資源使用率收集",
			"managed K8s (EKS/GKE/AKS) 請啟用對應 add-on。")
		return
	}
	t := defaultThresholds
	cpuCrit, cpuWarn, memCrit, memWarn := 0, 0, 0, 0
	for _, m := range r.NodeMetrics {
		switch {
		case m.CPUPercent >= t.cpuCrit:
			cpuCrit++
		case m.CPUPercent >= t.cpuWarn:
			cpuWarn++
		}
		switch {
		case m.MemPercent >= t.memCrit:
			memCrit++
		case m.MemPercent >= t.memWarn:
			memWarn++
		}
	}
	if cpuCrit > 0 {
		addFinding(c, SeverityCritical, "資源",
			fmt.Sprintf("有 %d 個節點 CPU 使用率 ≥ %.0f%%", cpuCrit, t.cpuCrit),
			"持續高 CPU 會延遲調度與 readiness 探針，建議立即擴容。")
		addRec(c, SeverityCritical, "資源",
			"擴增節點或調整高耗用 Pod 的 requests/limits",
			"先用 Top Consumers 表找出主要耗用者。")
	} else if cpuWarn > 0 {
		addFinding(c, SeverityWarning, "資源",
			fmt.Sprintf("有 %d 個節點 CPU 使用率 ≥ %.0f%%", cpuWarn, t.cpuWarn), "")
		addRec(c, SeverityWarning, "資源",
			"安排容量檢視，考慮水平擴容或調整 HPA 條件", "")
	}
	if memCrit > 0 {
		addFinding(c, SeverityCritical, "資源",
			fmt.Sprintf("有 %d 個節點記憶體使用率 ≥ %.0f%%", memCrit, t.memCrit),
			"高記憶體壓力會觸發 OOMKilled 或 Pod 驅逐。")
		addRec(c, SeverityCritical, "資源",
			"擴增節點或調整高耗用 Pod 的記憶體 limits",
			"建議搭配 Vertical Pod Autoscaler 觀察建議值。")
	} else if memWarn > 0 {
		addFinding(c, SeverityWarning, "資源",
			fmt.Sprintf("有 %d 個節點記憶體使用率 ≥ %.0f%%", memWarn, t.memWarn), "")
		addRec(c, SeverityWarning, "資源",
			"檢視 Top Memory 表並評估是否需要擴容", "")
	}
}

func checkDistribution(r *model.Report, c *model.Conclusion) {
	d := r.Cluster.Distribution
	if d == "k3s" || d == "rke2" || d == "eks" || d == "gke" || d == "aks" {
		if len(r.Certs) == 0 {
			addFinding(c, SeverityInfo, "憑證",
				fmt.Sprintf("%s 通常不暴露 /etc/kubernetes/pki，已略過憑證掃描", d),
				"managed 或精簡 K8s 自動管理憑證輪替，無需報告掃描。")
		}
	}
	if d == "k8s" && len(r.Certs) == 0 {
		addFinding(c, SeverityWarning, "憑證",
			"未取得 PKI 目錄資料",
			"在 kubeadm 環境建議掛載 /etc/kubernetes/pki 以監控憑證到期。")
		addRec(c, SeverityWarning, "憑證",
			"確認 Pod 是否正確掛載 /etc/kubernetes/pki (hostPath)，或檢查節點上該目錄是否存在",
			"PKI 監控是 kubeadm 部署最常被忽略的維運盲區。")
	}
}

// checkNodeAgents 處理 DaemonSet agent 回傳的 per-node 資料: 主要是節點磁碟
// 使用率。控制平面憑證已在 checkCerts 統一處理，這裡不重複。
func checkNodeAgents(r *model.Report, c *model.Conclusion) {
	if len(r.NodeAgents) == 0 {
		// 多節點 cluster 卻沒有 agent 資料，提醒部署 DaemonSet。
		if r.Cluster.NodeCount > 1 {
			addFinding(c, SeverityInfo, "監控",
				"未部署 DaemonSet agent，無法取得每節點磁碟與 kubelet 憑證資料",
				"建議部署 deploy/all-in-one.yaml 中的 agent DaemonSet 以涵蓋每個節點。")
		}
		return
	}
	critNodes := map[string]bool{}
	warnNodes := map[string]bool{}
	for _, na := range r.NodeAgents {
		for _, d := range na.Disks {
			switch d.Status {
			case "CRITICAL":
				critNodes[na.NodeName] = true
			case "WARN":
				warnNodes[na.NodeName] = true
			}
		}
	}
	if len(critNodes) > 0 {
		addFinding(c, SeverityCritical, "節點磁碟",
			fmt.Sprintf("%d 個節點有掛點使用率 ≥ 90%%", len(critNodes)),
			"磁碟接近爆滿會觸發 kubelet 驅逐 (DiskPressure)，影響其上 Pod。")
		addRec(c, SeverityCritical, "節點磁碟",
			"在受影響節點清理 image / 容器層、log，必要時擴容磁碟",
			"可從報告中『節點磁碟使用率』表找出具體掛點。")
	} else if len(warnNodes) > 0 {
		addFinding(c, SeverityWarning, "節點磁碟",
			fmt.Sprintf("%d 個節點有掛點使用率 ≥ 80%%", len(warnNodes)),
			"建議排程清理或擴容，避免進入 DiskPressure。")
		addRec(c, SeverityWarning, "節點磁碟",
			"執行 crictl rmi --prune 或 docker system prune 清理閒置 image",
			"並檢查 /var/log 是否被某個 Pod 寫爆。")
	}
}

// ---------- 結論彙整 -----------------------------------------------------

func rollupStatus(fs []model.Finding) string {
	hasWarn := false
	for _, f := range fs {
		if f.Severity == SeverityCritical {
			return StatusCritical
		}
		if f.Severity == SeverityWarning {
			hasWarn = true
		}
	}
	if hasWarn {
		return StatusWarning
	}
	return StatusHealthy
}

func buildSummary(r *model.Report, c *model.Conclusion) string {
	cluster := r.Cluster.Name
	if cluster == "" {
		cluster = "unknown"
	}
	crit, warn, info := 0, 0, 0
	for _, f := range c.Findings {
		switch f.Severity {
		case SeverityCritical:
			crit++
		case SeverityWarning:
			warn++
		case SeverityInfo:
			info++
		}
	}
	switch c.OverallStatus {
	case StatusCritical:
		return fmt.Sprintf("叢集 %s 整體狀態: %s。本次掃描共發現 %d 項嚴重、%d 項警告、%d 項資訊。請優先處理嚴重項目。",
			cluster, c.OverallStatus, crit, warn, info)
	case StatusWarning:
		return fmt.Sprintf("叢集 %s 整體狀態: %s。本次掃描共發現 %d 項警告、%d 項資訊。建議於下次維護視窗排除。",
			cluster, c.OverallStatus, warn, info)
	default:
		if info > 0 {
			return fmt.Sprintf("叢集 %s 整體狀態: %s。本次掃描未偵測到嚴重或警告問題，僅 %d 項資訊性提醒。",
				cluster, c.OverallStatus, info)
		}
		return fmt.Sprintf("叢集 %s 整體狀態: %s。本次掃描未偵測到任何問題。",
			cluster, c.OverallStatus)
	}
}

func addFinding(c *model.Conclusion, sev, cat, title, detail string) {
	c.Findings = append(c.Findings, model.Finding{
		Severity: sev, Category: cat, Title: title, Detail: detail,
	})
}

func addRec(c *model.Conclusion, sev, cat, action, rationale string) {
	c.Recommendations = append(c.Recommendations, model.Recommendation{
		Priority:  priorityFor(sev),
		Category:  cat,
		Action:    action,
		Rationale: rationale,
	})
}
