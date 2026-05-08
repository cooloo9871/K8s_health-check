// Package report 負責把 model.Report 渲染成多頁 PDF。
//
// 字型以 go:embed 嵌入 Noto Sans TC，因此完全不依賴系統字型。
// 所有時間欄位皆轉成台灣當地時間 (Asia/Taipei) 後再格式化。
package report

import (
	_ "embed"
	"fmt"
	"sort"
	"strings"

	"github.com/jung-kurt/gofpdf"
	"k8s-health-check/internal/model"
	"k8s-health-check/internal/tz"
)

// 以 go:embed 把 Noto Sans TC 字型靜態打進 binary，避免 runtime 依賴外部
// 字型檔。fontsource 已將字符集 subset 至繁體中文常用字。
//
//go:embed fonts/NotoSansTC-Regular.ttf
var fontRegular []byte

//go:embed fonts/NotoSansTC-Bold.ttf
var fontBold []byte

const fontFamily = "NotoSansTC"

// WritePDF 把 Report 渲染為多區段的 PDF 檔案。
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
	renderDashboard(pdf, r)
	renderClusterOverview(pdf, r)
	renderNodes(pdf, r)
	renderNodeMetrics(pdf, r)
	renderNodeDisks(pdf, r)
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

// ----- 共用 helpers -----------------------------------------------------

// sectionTitle 產出每個章節的藍色色塊標題，並視需要換頁。
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

// subTitle 用於章節內的子標題 (粗體 11pt 黑字)。
func subTitle(pdf *gofpdf.Fpdf, s string) {
	pdf.SetFont(fontFamily, "B", 11)
	pdf.CellFormat(0, 7, s, "", 1, "L", false, 0, "")
}

// note 印出灰色的補充說明文字，常用於「無資料」的提示。
func note(pdf *gofpdf.Fpdf, msg string) {
	pdf.SetFont(fontFamily, "", 9)
	pdf.SetTextColor(110, 110, 110)
	pdf.MultiCell(0, 5, msg, "", "L", false)
	pdf.SetTextColor(0, 0, 0)
}

// tableHeader 畫出表格標題列；後續呼叫 tableRow 會接在底下。
func tableHeader(pdf *gofpdf.Fpdf, widths []float64, headers []string) {
	pdf.SetFillColor(220, 230, 245)
	pdf.SetFont(fontFamily, "B", 9)
	for i, h := range headers {
		pdf.CellFormat(widths[i], 6, h, "1", 0, "L", true, 0, "")
	}
	pdf.Ln(-1)
	pdf.SetFont(fontFamily, "", 8)
}

// tableRow 畫一列資料；fill 為 true 時填淡藍色背景，做斑馬條紋效果。
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

// kv 印出「key 粗體: value」的 key-value 格式。
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
	pdf.CellFormat(0, 8, fmt.Sprintf("產生時間: %s", tz.In(r.GeneratedAt).Format("2006-01-02 15:04:05 MST")),
		"", 1, "C", false, 0, "")
	pdf.CellFormat(0, 8, fmt.Sprintf("發行版本: %s", strings.ToUpper(safe(r.Cluster.Distribution))),
		"", 1, "C", false, 0, "")
	pdf.CellFormat(0, 8, fmt.Sprintf("Kubernetes 版本: %s", safe(r.Cluster.Version)),
		"", 1, "C", false, 0, "")
	if r.Conclusion.Environment != "" {
		envLabel := r.Conclusion.Environment
		if r.Conclusion.EnvironmentAuto {
			envLabel += " (自動判定)"
		} else {
			envLabel += " (指定)"
		}
		pdf.CellFormat(0, 8, "環境: "+envLabel, "", 1, "C", false, 0, "")
	}
	pdf.Ln(20)

	// 重點摘要 (簡易計分板)
	pdf.SetFont(fontFamily, "B", 14)
	pdf.CellFormat(0, 8, "重點摘要", "", 1, "C", false, 0, "")
	pdf.SetFont(fontFamily, "", 11)
	rows := [][2]string{
		{"節點數", fmt.Sprintf("%d", r.Cluster.NodeCount)},
		{"DaemonSet agent 回報節點", fmt.Sprintf("%d", len(r.NodeAgents))},
		{"Namespace 數", fmt.Sprintf("%d", r.Cluster.NamespaceCnt)},
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

// statusFill 依整體狀態決定色塊背景: 嚴重=紅、警告=琥珀、其他=綠。
func statusFill(status string) (r, g, b int) {
	switch status {
	case "嚴重":
		return 198, 40, 40
	case "警告":
		return 240, 160, 30
	default:
		return 50, 140, 70
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

	cr, cg, cb := statusFill(r.Conclusion.OverallStatus)
	pdf.SetFillColor(cr, cg, cb)
	pdf.SetTextColor(255, 255, 255)
	pdf.SetFont(fontFamily, "B", 16)
	pdf.CellFormat(0, 12, " 整體狀態: "+safe(r.Conclusion.OverallStatus), "", 1, "L", true, 0, "")
	pdf.SetTextColor(0, 0, 0)
	pdf.Ln(2)

	if r.Conclusion.Summary != "" {
		pdf.SetFont(fontFamily, "", 10)
		pdf.MultiCell(0, 6, r.Conclusion.Summary, "", "L", false)
		pdf.Ln(2)
	}

	subTitle(pdf, "主要發現")
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
				pdf.SetFillColor(min255(fr+8), min255(fg+8), min255(fb+8))
			}
			pdf.CellFormat(widths[0], 5.5, f.Severity, "1", 0, "L", true, 0, "")
			pdf.CellFormat(widths[1], 5.5, f.Category, "1", 0, "L", true, 0, "")
			pdf.CellFormat(widths[2], 5.5, f.Title, "1", 0, "L", true, 0, "")
			pdf.CellFormat(widths[3], 5.5, f.Detail, "1", 1, "L", true, 0, "")
		}
	}
	pdf.Ln(3)

	subTitle(pdf, "建議事項")
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

// ----- 1. 視覺化儀表板 ------------------------------------------------

// renderDashboard 把幾個關鍵指標以圖形方式呈現，方便快速掃過 cluster 狀況。
//   - 左上: Pod 狀態圓餅
//   - 右上: 憑證到期分佈長條
//   - 中段: 節點 CPU 使用率水平條
//   - 中段: 節點記憶體使用率水平條
//   - 下段: 節點 root 磁碟使用率水平條 (若有 agent 資料)
func renderDashboard(pdf *gofpdf.Fpdf, r *model.Report) {
	pdf.AddPage()
	sectionTitle(pdf, "1. 視覺化儀表板")

	// ----- Pod 圓餅 + 憑證直方 (上半頁) -----
	yTop := pdf.GetY()
	subTitle(pdf, "Pod 狀態分佈")
	pdf.SetXY(105, yTop)
	subTitle(pdf, "憑證到期分佈 (剩餘天數)")

	// 圓餅: 圓心 (45,yTop+30)，半徑 22；圖例放在右側 (75,yTop+10)
	s := r.PodSummary
	pieSlices := []Slice{
		{Label: "Running", Value: float64(s.Running), R: 50, G: 140, B: 70},
		{Label: "Pending", Value: float64(s.Pending), R: 240, G: 160, B: 30},
		{Label: "Failed", Value: float64(s.Failed), R: 198, G: 40, B: 40},
		{Label: "Succeeded", Value: float64(s.Succeeded), R: 110, G: 130, B: 200},
		{Label: "Unknown", Value: float64(s.Unknown), R: 130, G: 130, B: 130},
	}
	drawPie(pdf, 35, yTop+32, 22, pieSlices, 65, yTop+12)

	// 憑證直方 (右半邊): 區塊 (105, yTop+8) 寬 95mm 高 50mm
	bins := buildCertBins(r.Certs)
	drawHistogram(pdf, 107, yTop+10, 90, 50, bins)

	// 把游標推到下一段
	pdf.SetXY(12, yTop+62)

	// ----- 節點資源使用率水平條 -----
	subTitle(pdf, "節點 CPU 使用率")
	if len(r.NodeMetrics) == 0 {
		note(pdf, "metrics-server 未安裝，無法繪製。")
	} else {
		bars := make([]HorizBar, 0, len(r.NodeMetrics))
		for _, m := range r.NodeMetrics {
			bars = append(bars, HorizBar{
				Label:   m.Name,
				Value:   m.CPUPercent,
				Max:     100,
				Display: fmt.Sprintf("%.1f%%  (%s / %s)", m.CPUPercent, m.CPUUsed, m.CPUCapacity),
				Status:  pctStatus(m.CPUPercent),
			})
		}
		drawHorizBars(pdf, 12, pdf.GetY()+1, 186, 5.5, 36, 60, bars)
	}
	pdf.Ln(2)

	subTitle(pdf, "節點記憶體使用率")
	if len(r.NodeMetrics) == 0 {
		note(pdf, "metrics-server 未安裝，無法繪製。")
	} else {
		bars := make([]HorizBar, 0, len(r.NodeMetrics))
		for _, m := range r.NodeMetrics {
			bars = append(bars, HorizBar{
				Label:   m.Name,
				Value:   m.MemPercent,
				Max:     100,
				Display: fmt.Sprintf("%.1f%%  (%s / %s)", m.MemPercent, m.MemUsed, m.MemCapacity),
				Status:  pctStatus(m.MemPercent),
			})
		}
		drawHorizBars(pdf, 12, pdf.GetY()+1, 186, 5.5, 36, 60, bars)
	}
	pdf.Ln(2)

	subTitle(pdf, "節點 root 磁碟使用率")
	if len(r.NodeAgents) == 0 {
		note(pdf, "未部署 DaemonSet agent；以 --mode=agent 啟動 DaemonSet 後重跑可呈現此圖。")
	} else {
		bars := nodeRootDiskBars(r.NodeAgents)
		if len(bars) == 0 {
			note(pdf, "agent 回報資料中無 / 掛點 (可能未掛 hostPath)。")
		} else {
			drawHorizBars(pdf, 12, pdf.GetY()+1, 186, 5.5, 36, 70, bars)
		}
	}
}

// pctStatus 把百分比映射到 OK / WARN / CRITICAL 的色條。
func pctStatus(p float64) string {
	switch {
	case p >= 90:
		return "CRITICAL"
	case p >= 75:
		return "WARN"
	default:
		return "OK"
	}
}

// buildCertBins 把所有憑證依「剩餘天數區間」分桶，給直方圖用。
func buildCertBins(certs []model.CertInfo) []HistBin {
	bins := []HistBin{
		{Label: "已過期", R: 198, G: 40, B: 40},
		{Label: "<7 天", R: 220, G: 80, B: 60},
		{Label: "7-30 天", R: 240, G: 160, B: 30},
		{Label: "30-90 天", R: 220, G: 200, B: 70},
		{Label: "90-180 天", R: 100, G: 160, B: 100},
		{Label: ">180 天", R: 50, G: 140, B: 70},
	}
	for _, c := range certs {
		switch {
		case c.DaysLeft < 0:
			bins[0].Count++
		case c.DaysLeft < 7:
			bins[1].Count++
		case c.DaysLeft < 30:
			bins[2].Count++
		case c.DaysLeft < 90:
			bins[3].Count++
		case c.DaysLeft < 180:
			bins[4].Count++
		default:
			bins[5].Count++
		}
	}
	return bins
}

// nodeRootDiskBars 從每個 agent 的回報中找 "/" 掛點，做成水平條。
func nodeRootDiskBars(nas []model.NodeAgentData) []HorizBar {
	out := make([]HorizBar, 0, len(nas))
	for _, na := range nas {
		var root *model.DiskInfo
		for i := range na.Disks {
			if na.Disks[i].MountPoint == "/" {
				root = &na.Disks[i]
				break
			}
		}
		if root == nil {
			continue
		}
		out = append(out, HorizBar{
			Label:   na.NodeName,
			Value:   root.Percent,
			Max:     100,
			Display: fmt.Sprintf("%.1f%%  (%s 已用 / %s)", root.Percent, humanBytes(root.Used), humanBytes(root.Total)),
			Status:  root.Status,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Value > out[j].Value })
	return out
}

// humanBytes 格式化 bytes 數量為 GiB / MiB。
func humanBytes(b uint64) string {
	const (
		KiB = 1024
		MiB = 1024 * KiB
		GiB = 1024 * MiB
	)
	switch {
	case b >= GiB:
		return fmt.Sprintf("%.1f GiB", float64(b)/float64(GiB))
	case b >= MiB:
		return fmt.Sprintf("%.0f MiB", float64(b)/float64(MiB))
	case b >= KiB:
		return fmt.Sprintf("%.0f KiB", float64(b)/float64(KiB))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// ----- 各區段 ----------------------------------------------------------

func renderClusterOverview(pdf *gofpdf.Fpdf, r *model.Report) {
	pdf.AddPage()
	sectionTitle(pdf, "2. 叢集總覽")
	kv(pdf, "Kubernetes 版本", safe(r.Cluster.Version))
	kv(pdf, "平台", safe(r.Cluster.Platform))
	kv(pdf, "發行版本標籤", safe(r.Cluster.Distribution))
	kv(pdf, "節點數", fmt.Sprintf("%d", r.Cluster.NodeCount))
	kv(pdf, "Namespace 數", fmt.Sprintf("%d", r.Cluster.NamespaceCnt))
	kv(pdf, "Pod 總數", fmt.Sprintf("%d", r.Cluster.TotalPods))
	kv(pdf, "DaemonSet agent 回報節點", fmt.Sprintf("%d", len(r.NodeAgents)))
}

func renderNodes(pdf *gofpdf.Fpdf, r *model.Report) {
	sectionTitle(pdf, "3. 節點")
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

	pdf.Ln(3)
	subTitle(pdf, "節點異常條件")
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
	sectionTitle(pdf, "4. 節點資源使用率")
	if len(r.NodeMetrics) == 0 {
		note(pdf, "metrics.k8s.io API 不可用; 請安裝 metrics-server 以填入此區段。")
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

// renderNodeDisks 利用 NodeAgents 的資料畫出每個節點上各掛點的磁碟使用率，
// 同一節點的所有掛點集中放在同一張表，便於對比。
func renderNodeDisks(pdf *gofpdf.Fpdf, r *model.Report) {
	sectionTitle(pdf, "5. 節點磁碟使用率")
	if len(r.NodeAgents) == 0 {
		note(pdf, "未部署 DaemonSet agent，無磁碟資料。請部署 deploy/all-in-one.yaml 中的 agent DaemonSet。")
		return
	}
	for _, na := range r.NodeAgents {
		subTitle(pdf, fmt.Sprintf("節點: %s  (採樣時間 %s)",
			safe(na.NodeName), tz.In(na.CollectedAt).Format("2006-01-02 15:04:05")))
		if len(na.Disks) == 0 {
			note(pdf, "agent 回報無可讀取的掛點，請確認 hostPath 已掛入 /host。")
			pdf.Ln(2)
			continue
		}
		widths := []float64{40, 25, 26, 26, 26, 24, 19}
		tableHeader(pdf, widths, []string{"掛點", "檔案系統", "總容量", "已用", "可用", "使用率", "狀態"})
		for i, d := range na.Disks {
			tableRow(pdf, widths, []string{
				d.MountPoint,
				safe(d.Filesystem),
				humanBytes(d.Total),
				humanBytes(d.Used),
				humanBytes(d.Avail),
				fmt.Sprintf("%.1f%%", d.Percent),
				d.Status,
			}, i%2 == 0)
		}
		pdf.Ln(3)
	}
}

func renderPodSummary(pdf *gofpdf.Fpdf, r *model.Report) {
	sectionTitle(pdf, "6. Pod 摘要")
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
	sectionTitle(pdf, "7. 問題 Pod (未就緒 / 重啟 / 失敗)")
	if len(r.ProblemPods) == 0 {
		note(pdf, "未偵測到問題 Pod。")
		return
	}
	widths := []float64{30, 50, 22, 16, 30, 38}
	tableHeader(pdf, widths, []string{"Namespace", "Pod", "Phase", "重啟次數", "節點", "原因"})
	for i, p := range r.ProblemPods {
		tableRow(pdf, widths, []string{
			p.Namespace, p.Name, p.Status,
			fmt.Sprintf("%d", p.Restarts), p.Node, p.Reason,
		}, i%2 == 0)
	}
}

func renderTopMetrics(pdf *gofpdf.Fpdf, r *model.Report) {
	sectionTitle(pdf, "8. 資源耗用前段班")
	if len(r.TopCPU) == 0 && len(r.TopMemory) == 0 {
		note(pdf, "沒有 Pod 度量資料; metrics-server 未安裝或無法存取。")
		return
	}
	subTitle(pdf, "CPU 使用量前 10 名")
	widths := []float64{35, 80, 30, 30}
	tableHeader(pdf, widths, []string{"Namespace", "Pod", "CPU", "記憶體"})
	for i, p := range r.TopCPU {
		tableRow(pdf, widths, []string{p.Namespace, p.Name, p.CPU, p.Memory}, i%2 == 0)
	}
	pdf.Ln(3)
	subTitle(pdf, "記憶體使用量前 10 名")
	tableHeader(pdf, widths, []string{"Namespace", "Pod", "CPU", "記憶體"})
	for i, p := range r.TopMemory {
		tableRow(pdf, widths, []string{p.Namespace, p.Name, p.CPU, p.Memory}, i%2 == 0)
	}
}

func renderWorkloads(pdf *gofpdf.Fpdf, r *model.Report) {
	sectionTitle(pdf, "9. 工作負載")
	w := r.Workloads
	pairs := [][3]string{
		{"Deployments", fmt.Sprintf("%d", w.Deployments.Total), fmt.Sprintf("%d", w.Deployments.Ready)},
		{"DaemonSets", fmt.Sprintf("%d", w.DaemonSets.Total), fmt.Sprintf("%d", w.DaemonSets.Ready)},
		{"StatefulSets", fmt.Sprintf("%d", w.StatefulSets.Total), fmt.Sprintf("%d", w.StatefulSets.Ready)},
		{"ReplicaSets (活躍)", fmt.Sprintf("%d", w.ReplicaSets.Total), fmt.Sprintf("%d", w.ReplicaSets.Ready)},
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
		subTitle(pdf, "不健康工作負載")
		w2 := []float64{22, 30, 60, 18, 18, 38}
		tableHeader(pdf, w2, []string{"類別", "Namespace", "名稱", "Desired", "Ready", "原因"})
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
	sectionTitle(pdf, "10. 儲存")
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
		subTitle(pdf, "Storage Classes")
		pdf.SetFont(fontFamily, "", 9)
		pdf.MultiCell(0, 5, strings.Join(s.StorageClasses, ", "), "", "L", false)
	}

	if len(s.ProblemPVCs) > 0 {
		pdf.Ln(2)
		subTitle(pdf, "Pending / 異常 PVC")
		w3 := []float64{30, 60, 22, 30, 30}
		tableHeader(pdf, w3, []string{"Namespace", "名稱", "狀態", "容量", "Class"})
		for i, p := range s.ProblemPVCs {
			tableRow(pdf, w3, []string{p.Namespace, p.Name, p.Status, p.Capacity, p.Class}, i%2 == 0)
		}
	}
}

func renderControlPlane(pdf *gofpdf.Fpdf, r *model.Report) {
	sectionTitle(pdf, "11. 控制平面健康")
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
		subTitle(pdf, "Component Statuses (舊版)")
		widths := []float64{50, 30, 100}
		tableHeader(pdf, widths, []string{"元件", "Healthy", "訊息"})
		for i, c := range r.Components {
			tableRow(pdf, widths, []string{c.Name, c.Healthy, c.Message}, i%2 == 0)
		}
	}
}

// renderCerts 把所有憑證依「來源」分組呈現，方便分辨 K8s pki / kubelet / etcd /
// kubeconfig 不同類別的到期狀況。
func renderCerts(pdf *gofpdf.Fpdf, r *model.Report) {
	sectionTitle(pdf, "12. 憑證到期")
	if len(r.Certs) == 0 {
		note(pdf, "未取得憑證資料。aggregator 模式下請部署 DaemonSet agent，agent 會掃 /etc/kubernetes/pki、/var/lib/kubelet/pki 與 *.conf 內嵌憑證。")
		return
	}
	// 依 source 分類
	groups := map[string][]model.CertInfo{}
	order := []string{"k8s-pki", "etcd", "kubeconfig", "kubelet", ""}
	titles := map[string]string{
		"k8s-pki":    "K8s 控制平面憑證 (/etc/kubernetes/pki)",
		"etcd":       "etcd 憑證",
		"kubeconfig": "Kubeconfig 內嵌 client 憑證",
		"kubelet":    "kubelet 憑證 (/var/lib/kubelet/pki)",
		"":           "其他",
	}
	for _, c := range r.Certs {
		groups[c.Source] = append(groups[c.Source], c)
	}
	first := true
	for _, key := range order {
		certs := groups[key]
		if len(certs) == 0 {
			continue
		}
		if !first {
			pdf.Ln(2)
		}
		first = false
		subTitle(pdf, titles[key])
		widths := []float64{30, 60, 36, 22, 18, 22}
		tableHeader(pdf, widths, []string{"節點", "路徑", "Subject", "到期日", "剩餘天數", "狀態"})
		for i, c := range certs {
			tableRow(pdf, widths, []string{
				safe(c.Node),
				shortPath(c.Path),
				truncate(c.Subject, 28),
				tz.In(c.NotAfter).Format("2006-01-02"),
				fmt.Sprintf("%d", c.DaysLeft),
				c.Status,
			}, i%2 == 0)
		}
	}
}

func shortPath(p string) string {
	if i := strings.LastIndex(p, "/pki/"); i >= 0 {
		return "..." + p[i:]
	}
	if i := strings.LastIndex(p, "/kubelet/"); i >= 0 {
		return "..." + p[i:]
	}
	if i := strings.LastIndex(p, "/kubernetes/"); i >= 0 {
		return "..." + p[i:]
	}
	if len(p) > 50 {
		return "..." + p[len(p)-47:]
	}
	return p
}

func renderEvents(pdf *gofpdf.Fpdf, r *model.Report) {
	sectionTitle(pdf, "13. 近期警告事件")
	if len(r.Events) == 0 {
		note(pdf, "區間內無警告 / 錯誤事件。")
		return
	}
	widths := []float64{32, 22, 25, 50, 57}
	tableHeader(pdf, widths, []string{"最後出現", "原因", "對象", "Namespace / 對象", "訊息"})
	for i, e := range r.Events {
		tableRow(pdf, widths, []string{
			tz.In(e.LastSeen).Format("01-02 15:04:05"),
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
	sectionTitle(pdf, "14. 蒐集備註")
	note(pdf, "以下蒐集器回報非致命錯誤，相關區段可能不完整:")
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
	return string(rs[:n-1]) + "..."
}
