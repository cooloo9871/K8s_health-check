package report

import (
	_ "embed"
	"fmt"
	"strings"
	"time"

	"k8s-health-check/internal/model"
	"github.com/jung-kurt/gofpdf"
)

// 以 go:embed 把 Noto Sans TC 字型靜態打進 binary，避免 runtime 依賴外部
// 字型檔。fontsource 已將字符集 subset 至繁體中文常用字。
//
//go:embed fonts/NotoSansTC-Regular.ttf
var fontRegular []byte

//go:embed fonts/NotoSansTC-Bold.ttf
var fontBold []byte

const fontFamily = "NotoSansTC"

// WritePDF renders the Report as a multi-section PDF in 繁體中文.
// 字型用 go:embed 嵌入，因此完全不依賴系統字型。
func WritePDF(r *model.Report, path string) error {
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(12, 14, 12)
	pdf.SetAutoPageBreak(true, 14)
	pdf.AliasNbPages("")

	pdf.AddUTF8FontFromBytes(fontFamily, "", fontRegular)
	pdf.AddUTF8FontFromBytes(fontFamily, "B", fontBold)

	pdf.SetHeaderFunc(func() {
		pdf.SetFont(fontFamily, "", 8)
		pdf.SetTextColor(120, 120, 120)
		pdf.CellFormat(0, 6, "Kubernetes 叢集健檢報告", "", 1, "R", false, 0, "")
		pdf.SetTextColor(0, 0, 0)
	})
	pdf.SetFooterFunc(func() {
		pdf.SetY(-12)
		pdf.SetFont(fontFamily, "", 8)
		pdf.SetTextColor(120, 120, 120)
		pdf.CellFormat(0, 6, fmt.Sprintf("第 %d / {nb} 頁", pdf.PageNo()),
			"", 0, "C", false, 0, "")
		pdf.SetTextColor(0, 0, 0)
	})

	renderCover(pdf, r)
	renderConclusion(pdf, r)
	renderClusterOverview(pdf, r)
	renderNodes(pdf, r)
	renderNodeMetrics(pdf, r)
	renderPodSummary(pdf, r)
	renderProblemPods(pdf, r)
	renderTopMetrics(pdf, r)
	renderWorkloads(pdf, r)
	renderStorage(pdf, r)
	renderControlPlane(pdf, r)
	renderCerts(pdf, r)
	renderEvents(pdf, r)
	renderErrors(pdf, r)

	return pdf.OutputFileAndClose(path)
}

// ----- helpers --------------------------------------------------------

func sectionTitle(pdf *gofpdf.Fpdf, title string) {
	if pdf.GetY() > 240 {
		pdf.AddPage()
	} else {
		pdf.Ln(4)
	}
	pdf.SetFillColor(40, 90, 160)
	pdf.SetTextColor(255, 255, 255)
	pdf.SetFont(fontFamily, "B", 12)
	pdf.CellFormat(0, 8, " "+title, "", 1, "L", true, 0, "")
	pdf.SetTextColor(0, 0, 0)
	pdf.Ln(2)
}

func note(pdf *gofpdf.Fpdf, msg string) {
	pdf.SetFont(fontFamily, "", 9)
	pdf.SetTextColor(110, 110, 110)
	pdf.MultiCell(0, 5, msg, "", "L", false)
	pdf.SetTextColor(0, 0, 0)
}

func tableHeader(pdf *gofpdf.Fpdf, widths []float64, headers []string) {
	pdf.SetFillColor(220, 230, 245)
	pdf.SetFont(fontFamily, "B", 9)
	for i, h := range headers {
		pdf.CellFormat(widths[i], 6, h, "1", 0, "L", true, 0, "")
	}
	pdf.Ln(-1)
	pdf.SetFont(fontFamily, "", 8)
}

func tableRow(pdf *gofpdf.Fpdf, widths []float64, cells []string, fill bool) {
	if pdf.GetY() > 270 {
		pdf.AddPage()
	}
	if fill {
		pdf.SetFillColor(245, 247, 252)
	} else {
		pdf.SetFillColor(255, 255, 255)
	}
	for i, c := range cells {
		pdf.CellFormat(widths[i], 5.5, c, "1", 0, "L", true, 0, "")
	}
	pdf.Ln(-1)
}

func kv(pdf *gofpdf.Fpdf, k, v string) {
	pdf.SetFont(fontFamily, "B", 10)
	pdf.CellFormat(50, 6, k, "", 0, "L", false, 0, "")
	pdf.SetFont(fontFamily, "", 10)
	pdf.MultiCell(0, 6, v, "", "L", false)
}

func pct(v float64) string {
	return fmt.Sprintf("%.1f%%", v)
}

func safe(s string) string {
	if s == "" {
		return "n/a"
	}
	return s
}

// ----- 封面 -----------------------------------------------------------

func renderCover(pdf *gofpdf.Fpdf, r *model.Report) {
	pdf.AddPage()
	pdf.SetFont(fontFamily, "B", 22)
	pdf.Ln(40)
	pdf.CellFormat(0, 12, "Kubernetes 叢集", "", 1, "C", false, 0, "")
	pdf.CellFormat(0, 12, "健檢報告", "", 1, "C", false, 0, "")
	pdf.Ln(10)

	pdf.SetFont(fontFamily, "", 12)
	pdf.CellFormat(0, 8, fmt.Sprintf("產生時間：%s", r.GeneratedAt.Format("2006-01-02 15:04:05 MST")),
		"", 1, "C", false, 0, "")
	pdf.CellFormat(0, 8, fmt.Sprintf("發行版本：%s", strings.ToUpper(safe(r.Cluster.Distribution))),
		"", 1, "C", false, 0, "")
	pdf.CellFormat(0, 8, fmt.Sprintf("Kubernetes 版本：%s", safe(r.Cluster.Version)),
		"", 1, "C", false, 0, "")
	if r.Conclusion.Environment != "" {
		envLabel := r.Conclusion.Environment
		if r.Conclusion.EnvironmentAuto {
			envLabel += "（自動判定）"
		} else {
			envLabel += "（指定）"
		}
		pdf.CellFormat(0, 8, "環境："+envLabel, "", 1, "C", false, 0, "")
	}
	pdf.Ln(20)

	// quick scoreboard
	pdf.SetFont(fontFamily, "B", 14)
	pdf.CellFormat(0, 8, "重點摘要", "", 1, "C", false, 0, "")
	pdf.SetFont(fontFamily, "", 11)
	rows := [][2]string{
		{"節點數", fmt.Sprintf("%d", r.Cluster.NodeCount)},
		{"命名空間數", fmt.Sprintf("%d", r.Cluster.NamespaceCnt)},
		{"Pod 總數", fmt.Sprintf("%d", r.Cluster.TotalPods)},
		{"Running Pod", fmt.Sprintf("%d", r.PodSummary.Running)},
		{"Pending Pod", fmt.Sprintf("%d", r.PodSummary.Pending)},
		{"Failed Pod", fmt.Sprintf("%d", r.PodSummary.Failed)},
		{"問題 Pod 數", fmt.Sprintf("%d", len(r.ProblemPods))},
		{"警告事件數", fmt.Sprintf("%d", len(r.Events))},
		{"不健康工作負載", fmt.Sprintf("%d", len(r.Workloads.Unhealthy))},
		{"追蹤中憑證", fmt.Sprintf("%d", len(r.Certs))},
	}
	for _, kv := range rows {
		pdf.CellFormat(70, 7, kv[0], "", 0, "R", false, 0, "")
		pdf.CellFormat(60, 7, kv[1], "", 1, "L", false, 0, "")
	}
}

// ----- 結論與建議 ------------------------------------------------------

// 整體狀態色塊背景顏色
func statusFill(status string) (r, g, b int) {
	switch status {
	case "嚴重":
		return 198, 40, 40 // red
	case "警告":
		return 240, 160, 30 // amber
	default:
		return 50, 140, 70 // green
	}
}

func severityFill(sev string) (r, g, b int) {
	switch sev {
	case "嚴重":
		return 250, 230, 230
	case "警告":
		return 252, 244, 224
	default:
		return 232, 240, 248
	}
}

func priorityFill(p string) (r, g, b int) {
	switch p {
	case "高":
		return 250, 230, 230
	case "中":
		return 252, 244, 224
	default:
		return 232, 240, 248
	}
}

func renderConclusion(pdf *gofpdf.Fpdf, r *model.Report) {
	pdf.AddPage()
	sectionTitle(pdf, "0. 結論與建議")

	// 整體狀態色塊
	cr, cg, cb := statusFill(r.Conclusion.OverallStatus)
	pdf.SetFillColor(cr, cg, cb)
	pdf.SetTextColor(255, 255, 255)
	pdf.SetFont(fontFamily, "B", 16)
	pdf.CellFormat(0, 12, " 整體狀態：" + safe(r.Conclusion.OverallStatus), "", 1, "L", true, 0, "")
	pdf.SetTextColor(0, 0, 0)
	pdf.Ln(2)

	// 摘要
	if r.Conclusion.Summary != "" {
		pdf.SetFont(fontFamily, "", 10)
		pdf.MultiCell(0, 6, r.Conclusion.Summary, "", "L", false)
		pdf.Ln(2)
	}

	// 主要發現
	pdf.SetFont(fontFamily, "B", 11)
	pdf.CellFormat(0, 7, "主要發現", "", 1, "L", false, 0, "")
	if len(r.Conclusion.Findings) == 0 {
		note(pdf, "未偵測到明顯問題。")
	} else {
		widths := []float64{20, 24, 56, 86}
		tableHeader(pdf, widths, []string{"嚴重度", "類別", "標題", "說明"})
		for i, f := range r.Conclusion.Findings {
			fr, fg, fb := severityFill(f.Severity)
			if pdf.GetY() > 265 {
				pdf.AddPage()
			}
			if i%2 == 0 {
				pdf.SetFillColor(fr, fg, fb)
			} else {
				// 交錯時稍微淡一點
				pdf.SetFillColor(min255(fr+8), min255(fg+8), min255(fb+8))
			}
			pdf.CellFormat(widths[0], 5.5, f.Severity, "1", 0, "L", true, 0, "")
			pdf.CellFormat(widths[1], 5.5, f.Category, "1", 0, "L", true, 0, "")
			pdf.CellFormat(widths[2], 5.5, f.Title, "1", 0, "L", true, 0, "")
			pdf.CellFormat(widths[3], 5.5, f.Detail, "1", 1, "L", true, 0, "")
		}
	}
	pdf.Ln(3)

	// 建議事項
	pdf.SetFont(fontFamily, "B", 11)
	pdf.CellFormat(0, 7, "建議事項", "", 1, "L", false, 0, "")
	if len(r.Conclusion.Recommendations) == 0 {
		note(pdf, "本次掃描沒有額外建議。")
		return
	}
	widths := []float64{16, 24, 80, 66}
	tableHeader(pdf, widths, []string{"優先級", "類別", "建議動作", "原因"})
	for i, rec := range r.Conclusion.Recommendations {
		pr, pg, pb := priorityFill(rec.Priority)
		if pdf.GetY() > 265 {
			pdf.AddPage()
		}
		if i%2 == 0 {
			pdf.SetFillColor(pr, pg, pb)
		} else {
			pdf.SetFillColor(min255(pr+8), min255(pg+8), min255(pb+8))
		}
		pdf.CellFormat(widths[0], 5.5, rec.Priority, "1", 0, "L", true, 0, "")
		pdf.CellFormat(widths[1], 5.5, rec.Category, "1", 0, "L", true, 0, "")
		pdf.CellFormat(widths[2], 5.5, rec.Action, "1", 0, "L", true, 0, "")
		pdf.CellFormat(widths[3], 5.5, rec.Rationale, "1", 1, "L", true, 0, "")
	}
}

func min255(v int) int {
	if v > 255 {
		return 255
	}
	return v
}

// ----- 各區段 ----------------------------------------------------------

func renderClusterOverview(pdf *gofpdf.Fpdf, r *model.Report) {
	pdf.AddPage()
	sectionTitle(pdf, "1. 叢集總覽")
	kv(pdf, "Kubernetes 版本", safe(r.Cluster.Version))
	kv(pdf, "平台", safe(r.Cluster.Platform))
	kv(pdf, "發行版本標籤", safe(r.Cluster.Distribution))
	kv(pdf, "節點數", fmt.Sprintf("%d", r.Cluster.NodeCount))
	kv(pdf, "命名空間數", fmt.Sprintf("%d", r.Cluster.NamespaceCnt))
	kv(pdf, "Pod 總數", fmt.Sprintf("%d", r.Cluster.TotalPods))
}

func renderNodes(pdf *gofpdf.Fpdf, r *model.Report) {
	sectionTitle(pdf, "2. 節點")
	if len(r.Nodes) == 0 {
		note(pdf, "未蒐集到節點資料。")
		return
	}
	widths := []float64{40, 22, 22, 24, 26, 18, 14, 20}
	tableHeader(pdf, widths, []string{"名稱", "角色", "狀態", "Kubelet", "內部 IP", "存活時間", "Pods", "Runtime"})
	for i, n := range r.Nodes {
		tableRow(pdf, widths, []string{
			n.Name, n.Roles, n.Status, n.KubeletVersion,
			n.InternalIP, n.Age, fmt.Sprintf("%d", n.PodCount), shortRuntime(n.Runtime),
		}, i%2 == 0)
	}

	// 列出非健康狀態
	pdf.Ln(3)
	pdf.SetFont(fontFamily, "B", 10)
	pdf.CellFormat(0, 6, "節點異常條件", "", 1, "L", false, 0, "")
	pdf.SetFont(fontFamily, "", 9)
	hadAny := false
	for _, n := range r.Nodes {
		for _, c := range n.Conditions {
			abnormal := false
			switch c.Type {
			case "Ready":
				abnormal = c.Status != "True"
			default:
				abnormal = c.Status == "True"
			}
			if abnormal {
				hadAny = true
				pdf.MultiCell(0, 5,
					fmt.Sprintf("%s -> %s=%s reason=%s msg=%s",
						n.Name, c.Type, c.Status, c.Reason, truncate(c.Message, 100)),
					"", "L", false)
			}
		}
	}
	if !hadAny {
		note(pdf, "所有節點條件正常。")
	}
}

func shortRuntime(r string) string {
	r = strings.ReplaceAll(r, "://", " ")
	if len(r) > 18 {
		return r[:18]
	}
	return r
}

func renderNodeMetrics(pdf *gofpdf.Fpdf, r *model.Report) {
	sectionTitle(pdf, "3. 節點資源使用率")
	if len(r.NodeMetrics) == 0 {
		note(pdf, "metrics.k8s.io API 不可用；請安裝 metrics-server 以填入此區段。")
		return
	}
	widths := []float64{50, 22, 30, 22, 30, 18, 18}
	tableHeader(pdf, widths, []string{"節點", "CPU %", "CPU 使用 / 容量", "記憶體 %", "記憶體 使用 / 容量", "Pods", "Pod 上限"})
	for i, m := range r.NodeMetrics {
		tableRow(pdf, widths, []string{
			m.Name,
			pct(m.CPUPercent),
			fmt.Sprintf("%s / %s", m.CPUUsed, m.CPUCapacity),
			pct(m.MemPercent),
			fmt.Sprintf("%s / %s", m.MemUsed, m.MemCapacity),
			fmt.Sprintf("%d", m.PodCount),
			fmt.Sprintf("%d", m.PodCapacity),
		}, i%2 == 0)
	}
}

func renderPodSummary(pdf *gofpdf.Fpdf, r *model.Report) {
	sectionTitle(pdf, "4. Pod 摘要")
	s := r.PodSummary
	widths := []float64{30, 30, 30, 30, 30, 30}
	tableHeader(pdf, widths, []string{"總數", "Running", "Pending", "Succeeded", "Failed", "Unknown"})
	tableRow(pdf, widths, []string{
		fmt.Sprintf("%d", s.Total),
		fmt.Sprintf("%d", s.Running),
		fmt.Sprintf("%d", s.Pending),
		fmt.Sprintf("%d", s.Succeeded),
		fmt.Sprintf("%d", s.Failed),
		fmt.Sprintf("%d", s.Unknown),
	}, true)
}

func renderProblemPods(pdf *gofpdf.Fpdf, r *model.Report) {
	sectionTitle(pdf, "5. 問題 Pod（未就緒 / 重啟 / 失敗）")
	if len(r.ProblemPods) == 0 {
		note(pdf, "未偵測到問題 Pod。")
		return
	}
	widths := []float64{30, 50, 22, 16, 30, 38}
	tableHeader(pdf, widths, []string{"命名空間", "Pod", "Phase", "重啟次數", "節點", "原因"})
	for i, p := range r.ProblemPods {
		tableRow(pdf, widths, []string{
			p.Namespace, p.Name, p.Status,
			fmt.Sprintf("%d", p.Restarts), p.Node, p.Reason,
		}, i%2 == 0)
	}
}

func renderTopMetrics(pdf *gofpdf.Fpdf, r *model.Report) {
	sectionTitle(pdf, "6. 資源耗用前段班")
	if len(r.TopCPU) == 0 && len(r.TopMemory) == 0 {
		note(pdf, "沒有 Pod 度量資料；metrics-server 未安裝或無法存取。")
		return
	}
	pdf.SetFont(fontFamily, "B", 10)
	pdf.CellFormat(0, 6, "CPU 使用量前 10 名", "", 1, "L", false, 0, "")
	widths := []float64{35, 80, 30, 30}
	tableHeader(pdf, widths, []string{"命名空間", "Pod", "CPU", "記憶體"})
	for i, p := range r.TopCPU {
		tableRow(pdf, widths, []string{p.Namespace, p.Name, p.CPU, p.Memory}, i%2 == 0)
	}
	pdf.Ln(3)
	pdf.SetFont(fontFamily, "B", 10)
	pdf.CellFormat(0, 6, "記憶體使用量前 10 名", "", 1, "L", false, 0, "")
	tableHeader(pdf, widths, []string{"命名空間", "Pod", "CPU", "記憶體"})
	for i, p := range r.TopMemory {
		tableRow(pdf, widths, []string{p.Namespace, p.Name, p.CPU, p.Memory}, i%2 == 0)
	}
}

func renderWorkloads(pdf *gofpdf.Fpdf, r *model.Report) {
	sectionTitle(pdf, "7. 工作負載")
	w := r.Workloads
	pairs := [][3]string{
		{"Deployments", fmt.Sprintf("%d", w.Deployments.Total), fmt.Sprintf("%d", w.Deployments.Ready)},
		{"DaemonSets", fmt.Sprintf("%d", w.DaemonSets.Total), fmt.Sprintf("%d", w.DaemonSets.Ready)},
		{"StatefulSets", fmt.Sprintf("%d", w.StatefulSets.Total), fmt.Sprintf("%d", w.StatefulSets.Ready)},
		{"ReplicaSets（活躍）", fmt.Sprintf("%d", w.ReplicaSets.Total), fmt.Sprintf("%d", w.ReplicaSets.Ready)},
		{"Jobs", fmt.Sprintf("%d", w.Jobs.Total), fmt.Sprintf("%d", w.Jobs.Ready)},
		{"CronJobs", fmt.Sprintf("%d", w.CronJobs), "-"},
	}
	widths := []float64{60, 30, 30}
	tableHeader(pdf, widths, []string{"類別", "總數", "健康"})
	for i, p := range pairs {
		tableRow(pdf, widths, []string{p[0], p[1], p[2]}, i%2 == 0)
	}

	if len(w.Unhealthy) > 0 {
		pdf.Ln(3)
		pdf.SetFont(fontFamily, "B", 10)
		pdf.CellFormat(0, 6, "不健康工作負載", "", 1, "L", false, 0, "")
		w2 := []float64{22, 30, 60, 18, 18, 38}
		tableHeader(pdf, w2, []string{"類別", "命名空間", "名稱", "Desired", "Ready", "原因"})
		for i, u := range w.Unhealthy {
			tableRow(pdf, w2, []string{
				u.Kind, u.Namespace, u.Name,
				fmt.Sprintf("%d", u.Desired),
				fmt.Sprintf("%d", u.Ready),
				u.Reason,
			}, i%2 == 0)
		}
	}
}

func renderStorage(pdf *gofpdf.Fpdf, r *model.Report) {
	sectionTitle(pdf, "8. 儲存")
	s := r.Storage
	widths := []float64{60, 30, 30, 30, 30}
	tableHeader(pdf, widths, []string{"PersistentVolumes", "Bound", "Available", "Released", "Failed"})
	tableRow(pdf, widths, []string{
		fmt.Sprintf("%d", s.PVs),
		fmt.Sprintf("%d", s.PVsBound),
		fmt.Sprintf("%d", s.PVsAvailable),
		fmt.Sprintf("%d", s.PVsReleased),
		fmt.Sprintf("%d", s.PVsFailed),
	}, true)
	pdf.Ln(2)
	w2 := []float64{60, 30, 30}
	tableHeader(pdf, w2, []string{"PersistentVolumeClaims", "Bound", "Pending"})
	tableRow(pdf, w2, []string{
		fmt.Sprintf("%d", s.PVCs),
		fmt.Sprintf("%d", s.PVCsBound),
		fmt.Sprintf("%d", s.PVCsPending),
	}, true)

	if len(s.StorageClasses) > 0 {
		pdf.Ln(2)
		pdf.SetFont(fontFamily, "B", 10)
		pdf.CellFormat(0, 6, "Storage Classes", "", 1, "L", false, 0, "")
		pdf.SetFont(fontFamily, "", 9)
		pdf.MultiCell(0, 5, strings.Join(s.StorageClasses, ", "), "", "L", false)
	}

	if len(s.ProblemPVCs) > 0 {
		pdf.Ln(2)
		pdf.SetFont(fontFamily, "B", 10)
		pdf.CellFormat(0, 6, "Pending / 異常 PVC", "", 1, "L", false, 0, "")
		w3 := []float64{30, 60, 22, 30, 30}
		tableHeader(pdf, w3, []string{"命名空間", "名稱", "狀態", "容量", "Class"})
		for i, p := range s.ProblemPVCs {
			tableRow(pdf, w3, []string{p.Namespace, p.Name, p.Status, p.Capacity, p.Class}, i%2 == 0)
		}
	}
}

func renderControlPlane(pdf *gofpdf.Fpdf, r *model.Report) {
	sectionTitle(pdf, "9. 控制平面健康")
	if len(r.APIHealth) == 0 && len(r.Components) == 0 {
		note(pdf, "控制平面健康端點不可用。")
		return
	}
	if len(r.APIHealth) > 0 {
		widths := []float64{40, 30, 110}
		tableHeader(pdf, widths, []string{"端點", "狀態", "詳情"})
		for i, h := range r.APIHealth {
			tableRow(pdf, widths, []string{h.Endpoint, h.Status, h.Detail}, i%2 == 0)
		}
	}
	if len(r.Components) > 0 {
		pdf.Ln(2)
		pdf.SetFont(fontFamily, "B", 10)
		pdf.CellFormat(0, 6, "Component Statuses（舊版）", "", 1, "L", false, 0, "")
		widths := []float64{50, 30, 100}
		tableHeader(pdf, widths, []string{"元件", "Healthy", "訊息"})
		for i, c := range r.Components {
			tableRow(pdf, widths, []string{c.Name, c.Healthy, c.Message}, i%2 == 0)
		}
	}
}

func renderCerts(pdf *gofpdf.Fpdf, r *model.Report) {
	sectionTitle(pdf, "10. 憑證到期")
	if len(r.Certs) == 0 {
		note(pdf, "未掛載 PKI 目錄（請以 hostPath 掛載 /etc/kubernetes/pki 以填入此區段）。非 kubeadm 發行版會自動略過。")
		return
	}
	widths := []float64{70, 40, 30, 20, 26}
	tableHeader(pdf, widths, []string{"路徑", "Subject", "到期日", "剩餘天數", "狀態"})
	for i, c := range r.Certs {
		tableRow(pdf, widths, []string{
			shortPath(c.Path), c.Subject,
			c.NotAfter.Format("2006-01-02"),
			fmt.Sprintf("%d", c.DaysLeft),
			c.Status,
		}, i%2 == 0)
	}
}

func shortPath(p string) string {
	if i := strings.LastIndex(p, "/pki/"); i >= 0 {
		return "..." + p[i:]
	}
	if len(p) > 50 {
		return "..." + p[len(p)-47:]
	}
	return p
}

func renderEvents(pdf *gofpdf.Fpdf, r *model.Report) {
	sectionTitle(pdf, "11. 近期警告事件")
	if len(r.Events) == 0 {
		note(pdf, "區間內無警告 / 錯誤事件。")
		return
	}
	widths := []float64{32, 22, 25, 50, 57}
	tableHeader(pdf, widths, []string{"最後出現", "原因", "對象", "命名空間 / 對象", "訊息"})
	for i, e := range r.Events {
		tableRow(pdf, widths, []string{
			e.LastSeen.Format("01-02 15:04:05"),
			e.Reason,
			truncate(e.Object, 22),
			fmt.Sprintf("%s (x%d)", e.Namespace, e.Count),
			e.Message,
		}, i%2 == 0)
	}
}

func renderErrors(pdf *gofpdf.Fpdf, r *model.Report) {
	if len(r.Errors) == 0 {
		return
	}
	sectionTitle(pdf, "12. 蒐集備註")
	note(pdf, "以下蒐集器回報非致命錯誤，相關區段可能不完整：")
	pdf.SetFont(fontFamily, "", 9)
	for _, e := range r.Errors {
		pdf.MultiCell(0, 5, "- "+e, "", "L", false)
	}
}

// truncate 以「rune」為單位截斷，避免把中文字切到一半變成亂碼。
func truncate(s string, n int) string {
	rs := []rune(s)
	if len(rs) <= n {
		return s
	}
	if n <= 1 {
		return string(rs[:n])
	}
	return string(rs[:n-1]) + "…"
}

// 附帶提供時間格式化，未來可能用到。
var _ = time.Time{}
