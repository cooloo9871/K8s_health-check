// Package report 負責把 model.Report 渲染成多頁 PDF. 
//
// 字型以 go:embed 嵌入 Noto Sans TC, 因此完全不依賴系統字型. 
// 所有時間欄位皆轉成台灣當地時間 (Asia/Taipei) 後再格式化. 
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

// 以 go:embed 把 Noto Sans TC 字型靜態打進 binary, 避免 runtime 依賴外部
// 字型檔. fontsource 已將字符集 subset 至繁體中文常用字. 
//
//go:embed fonts/NotoSansTC-Regular.ttf
var fontRegular []byte

//go:embed fonts/NotoSansTC-Bold.ttf
var fontBold []byte

const fontFamily = "NotoSansTC"

// WritePDF 把 Report 渲染為多區段的 PDF 檔案. 
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
	renderWorkloads(pdf, r)
	renderTopMetrics(pdf, r)
	renderAllPods(pdf, r)
	renderStorage(pdf, r)
	renderControlPlane(pdf, r)
	renderCerts(pdf, r)
	renderEvents(pdf, r)
	renderErrors(pdf, r)

	return pdf.OutputFileAndClose(path)
}

// ----- 共用 helpers -----------------------------------------------------

// sectionTitle 產出每個章節的藍色色塊標題, 並視需要換頁. 
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

// subTitle 用於章節內的子標題 (粗體 11pt 黑字). 
func subTitle(pdf *gofpdf.Fpdf, s string) {
	pdf.SetFont(fontFamily, "B", 11)
	pdf.CellFormat(0, 7, s, "", 1, "L", false, 0, "")
}

// note 印出灰色的補充說明文字, 常用於"無資料"的提示. 
func note(pdf *gofpdf.Fpdf, msg string) {
	pdf.SetFont(fontFamily, "", 9)
	pdf.SetTextColor(110, 110, 110)
	pdf.MultiCell(0, 5, msg, "", "L", false)
	pdf.SetTextColor(0, 0, 0)
}

// tableHeader 畫出表格標題列; 後續呼叫 tableRow 會接在底下. 
func tableHeader(pdf *gofpdf.Fpdf, widths []float64, headers []string) {
	pdf.SetFillColor(220, 230, 245)
	pdf.SetFont(fontFamily, "B", 9)
	for i, h := range headers {
		pdf.CellFormat(widths[i], 6, fit(pdf, h, widths[i]), "1", 0, "L", true, 0, "")
	}
	pdf.Ln(-1)
	pdf.SetFont(fontFamily, "", 8)
}

// tableRow 畫一列資料; fill 為 true 時填淡藍色背景, 做斑馬條紋效果. 
// 過長的內容會自動換行 (而非截斷), row 高度依最長那格的行數動態調整. 
func tableRow(pdf *gofpdf.Fpdf, widths []float64, cells []string, fill bool) {
	if fill {
		multiCellRow(pdf, widths, cells, 245, 247, 252)
	} else {
		multiCellRow(pdf, widths, cells, 255, 255, 255)
	}
}

// 表格內格距與行高常數. lineH=4.5 + padV=0.5 → 單行 row 高 5.5 (與舊版相容). 
const (
	tableLineH = 4.5
	tableCellPadV = 0.5
	tableCellPadH = 0.7
)

// multiCellRow 畫一列可自動換行的表格資料. 流程:
//  1. 先用 SplitText 量測每格在欄寬內會被切成幾行, 取最大值決定 row 高
//  2. 若超出底邊就先 AddPage
//  3. 為每一格畫 fill+border 的矩形; 再以 MultiCell 寫入文字 (不再加邊框)
//  4. 結束時把游標重置到下一列起始 (左邊界, y+rowH)
func multiCellRow(pdf *gofpdf.Fpdf, widths []float64, cells []string, fillR, fillG, fillB int) {
	// 量測每格行數
	maxLines := 1
	for i, c := range cells {
		innerW := widths[i] - 2*tableCellPadH
		if innerW < 1 {
			innerW = 1
		}
		n := countWrapLines(pdf, c, innerW)
		if n > maxLines {
			maxLines = n
		}
	}
	rowH := float64(maxLines)*tableLineH + 2*tableCellPadV

	// 換頁判斷: 用 PageHeight - bottom margin (與 SetAutoPageBreak 一致 = 14mm)
	_, ph := pdf.GetPageSize()
	if pdf.GetY()+rowH > ph-14 {
		pdf.AddPage()
	}

	leftX := pdf.GetX()
	y := pdf.GetY()

	pdf.SetFillColor(fillR, fillG, fillB)
	pdf.SetDrawColor(0, 0, 0)

	x := leftX
	for i, c := range cells {
		// 邊框 + 填色背景
		pdf.Rect(x, y, widths[i], rowH, "FD")
		// 寫入文字 (MultiCell 自動換行, 不再加邊框與填色)
		innerW := widths[i] - 2*tableCellPadH
		pdf.SetXY(x+tableCellPadH, y+tableCellPadV)
		pdf.MultiCell(innerW, tableLineH, c, "", "L", false)
		x += widths[i]
	}

	// 把游標放到下一列開頭, 讓下次 tableRow 直接接上去
	pdf.SetXY(leftX, y+rowH)
}

// countWrapLines 利用 gofpdf.SplitText 估算 text 在 width 寬欄位內換行後
// 會佔多少行, 至少回傳 1. 空字串視為 1 行 (保留版面對齊). 
func countWrapLines(pdf *gofpdf.Fpdf, text string, width float64) int {
	if text == "" {
		return 1
	}
	lines := pdf.SplitText(text, width)
	if len(lines) == 0 {
		return 1
	}
	return len(lines)
}

// fit 把字串縮短到實際寬度不超過 maxWidth, 超過時在尾端接上 "...". 
// 用 PDF 自身的 GetStringWidth 量測, 依目前字型字級實際寬度判斷, 比起按
// 字數截斷更精準 (中英混排寬度差很多). 
func fit(pdf *gofpdf.Fpdf, s string, maxWidth float64) string {
	const padding = 1.5 // mm, 避免貼齊邊界產生毛邊
	target := maxWidth - padding
	if target <= 0 || s == "" {
		return s
	}
	if pdf.GetStringWidth(s) <= target {
		return s
	}
	const suffix = "..."
	suffixW := pdf.GetStringWidth(suffix)
	if suffixW > target {
		// 連 "..." 都塞不下時直接回空字串
		return ""
	}
	rs := []rune(s)
	for n := len(rs) - 1; n > 0; n-- {
		candidate := string(rs[:n])
		if pdf.GetStringWidth(candidate)+suffixW <= target {
			return candidate + suffix
		}
	}
	return suffix
}

// kv 印出"key 粗體: value"的 key-value 格式. key 寬固定 50mm 會被 fit
// 截斷; value 用 MultiCell 自動換行不必截斷. 
func kv(pdf *gofpdf.Fpdf, k, v string) {
	pdf.SetFont(fontFamily, "B", 10)
	pdf.CellFormat(50, 6, fit(pdf, k, 50), "", 0, "L", false, 0, "")
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

// clusterLabel 回傳 cluster name 純字串 (不附來源括號). 
// 名稱來源仍會以 r.Cluster.NameSource 保留供其他需要時使用. 
func clusterLabel(r *model.Report) string {
	if r.Cluster.Name == "" {
		return "unknown"
	}
	return r.Cluster.Name
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
	pdf.CellFormat(0, 8, fmt.Sprintf("叢集名稱: %s", clusterLabel(r)),
		"", 1, "C", false, 0, "")
	pdf.CellFormat(0, 8, fmt.Sprintf("產生時間: %s", tz.In(r.GeneratedAt).Format("2006-01-02 15:04:05 MST")),
		"", 1, "C", false, 0, "")
	pdf.CellFormat(0, 8, fmt.Sprintf("發行版本: %s", strings.ToUpper(safe(r.Cluster.Distribution))),
		"", 1, "C", false, 0, "")
	pdf.CellFormat(0, 8, fmt.Sprintf("Kubernetes 版本: %s", safe(r.Cluster.Version)),
		"", 1, "C", false, 0, "")
	pdf.Ln(20)

	// 重點摘要 (簡易計分板)
	pdf.SetFont(fontFamily, "B", 14)
	pdf.CellFormat(0, 8, "重點摘要", "", 1, "C", false, 0, "")
	pdf.SetFont(fontFamily, "", 11)
	rows := [][2]string{
		{"節點數", fmt.Sprintf("%d", r.Cluster.NodeCount)},
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

// statusFill 依整體狀態決定色塊背景: 嚴重=紅, 警告=琥珀, 其他=綠. 
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
		note(pdf, "未偵測到明顯問題. ")
	} else {
		widths := []float64{20, 24, 56, 86}
		tableHeader(pdf, widths, []string{"嚴重度", "類別", "標題", "說明"})
		for i, f := range r.Conclusion.Findings {
			fr, fg, fb := severityFill(f.Severity)
			if i%2 == 1 {
				fr, fg, fb = min255(fr+8), min255(fg+8), min255(fb+8)
			}
			multiCellRow(pdf, widths,
				[]string{f.Severity, f.Category, f.Title, f.Detail},
				fr, fg, fb)
		}
	}
	pdf.Ln(3)

	subTitle(pdf, "建議事項")
	if len(r.Conclusion.Recommendations) == 0 {
		note(pdf, "本次掃描沒有額外建議. ")
		return
	}
	widths := []float64{16, 24, 80, 66}
	tableHeader(pdf, widths, []string{"優先級", "類別", "建議動作", "原因"})
	for i, rec := range r.Conclusion.Recommendations {
		pr, pg, pb := priorityFill(rec.Priority)
		if i%2 == 1 {
			pr, pg, pb = min255(pr+8), min255(pg+8), min255(pb+8)
		}
		multiCellRow(pdf, widths,
			[]string{rec.Priority, rec.Category, rec.Action, rec.Rationale},
			pr, pg, pb)
	}
}

func min255(v int) int {
	if v > 255 {
		return 255
	}
	return v
}

// ----- 1. 視覺化儀表板 ------------------------------------------------

// renderDashboard 把幾個關鍵指標以圖形方式呈現, 方便快速掃過 cluster 狀況. 
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

	// CrashLoopBackOff / 重啟過多 / container未 Ready 的 Pod 在 K8s 階段上仍是
	// Running, 但其實處於異常狀態. 把這類從 Running 切片中拆出來, 獨立顯示
	// 為紅色"異常"切片, 使用者才能在圓餅上看到 crash 影響.
	//
	// 注意: 用 p.Phase (K8s 原始 phase) 判斷, 不能用 p.Status — 後者已被
	// effectivePhase 改為 CrashLoopBackOff 等顯示字串, 不再是 "Running".
	problemRunning := 0
	for _, p := range r.ProblemPods {
		if strings.EqualFold(p.Phase, "Running") {
			problemRunning++
		}
	}
	s := r.PodSummary
	healthyRunning := s.Running - problemRunning
	if healthyRunning < 0 {
		healthyRunning = 0
	}
	pieSlices := []Slice{
		{Label: "Running (健康)", Value: float64(healthyRunning), R: 50, G: 140, B: 70},
		{Label: "異常 (Crash/重啟)", Value: float64(problemRunning), R: 220, G: 80, B: 60},
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
		note(pdf, "metrics-server 未安裝, 無法繪製. ")
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
		note(pdf, "metrics-server 未安裝, 無法繪製. ")
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

	subTitle(pdf, "節點各掛點磁碟使用率")
	if len(r.NodeAgents) == 0 {
		note(pdf, "未部署 DaemonSet agent; 以 --mode=agent 啟動 DaemonSet 後重跑可呈現此圖. ")
	} else {
		bars := allNodeDiskBars(r.NodeAgents)
		if len(bars) == 0 {
			note(pdf, "agent 回報資料中無任何掛點 (可能未掛 hostPath). ")
		} else {
			drawHorizBars(pdf, 12, pdf.GetY()+1, 186, 5.5, 60, 60, bars)
		}
	}
}

// pctStatus 把百分比映射到 OK / WARN / CRITICAL 的色條. 
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

// buildCertBins 把所有憑證依"剩餘天數區間"分桶, 給直方圖用. 
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

// allNodeDiskBars 把每個 agent 回報的"所有掛點"攤平成水平條. 
// 標籤格式為 "<node>  <掛點>", 依使用率由高到低排序, 方便一眼看到最緊迫的. 
func allNodeDiskBars(nas []model.NodeAgentData) []HorizBar {
	out := make([]HorizBar, 0)
	for _, na := range nas {
		for _, d := range na.Disks {
			out = append(out, HorizBar{
				Label:   fmt.Sprintf("%s  %s", na.NodeName, d.MountPoint),
				Value:   d.Percent,
				Max:     100,
				Display: fmt.Sprintf("%.1f%%  (%s / %s)", d.Percent, humanBytes(d.Used), humanBytes(d.Total)),
				Status:  d.Status,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Value > out[j].Value })
	return out
}

// humanBytes 格式化 bytes 數量為 GiB / MiB. 
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
	kv(pdf, "叢集名稱", clusterLabel(r))
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
		note(pdf, "未蒐集到節點資料. ")
		return
	}
	// 移除 Pods 欄: 節點 Pod 數已在第 6/7 章 (Pod 摘要 / 問題 Pod / 總覽) 呈現,
	// 此表的"Pods"欄重複且資訊量低; 14mm 全數補回名稱欄, 讓長節點名不易被截斷.
	widths := []float64{54, 22, 22, 24, 26, 18, 20}
	tableHeader(pdf, widths, []string{"名稱", "角色", "狀態", "Kubelet", "內部 IP", "存活時間", "Runtime"})
	for i, n := range r.Nodes {
		tableRow(pdf, widths, []string{
			n.Name, n.Roles, n.Status, n.KubeletVersion,
			n.InternalIP, n.Age, shortRuntime(n.Runtime),
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
		note(pdf, "所有節點條件正常. ")
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
		note(pdf, "metrics.k8s.io API 不可用; 請安裝 metrics-server 以填入此區段. ")
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

// renderNodeDisks 利用 NodeAgents 的資料畫出每個節點上各掛點的磁碟使用率, 
// 同一節點的所有掛點集中放在同一張表, 便於對比. 
func renderNodeDisks(pdf *gofpdf.Fpdf, r *model.Report) {
	sectionTitle(pdf, "5. 節點磁碟使用率")
	if len(r.NodeAgents) == 0 {
		note(pdf, "未部署 DaemonSet agent, 無磁碟資料. 請部署 deploy/all-in-one.yaml 中的 agent DaemonSet. ")
		return
	}
	for _, na := range r.NodeAgents {
		subTitle(pdf, fmt.Sprintf("節點: %s  (採樣時間 %s)",
			safe(na.NodeName), tz.In(na.CollectedAt).Format("2006-01-02 15:04:05")))
		if len(na.Disks) == 0 {
			note(pdf, "agent 回報無可讀取的掛點, 請確認 hostPath 已掛入 /host. ")
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
		note(pdf, "未偵測到問題 Pod. ")
		return
	}
	widths := []float64{30, 50, 22, 16, 30, 38}
	tableHeader(pdf, widths, []string{"Namespace", "Pod", "Status", "重啟次數", "節點", "原因"})
	for i, p := range r.ProblemPods {
		tableRow(pdf, widths, []string{
			p.Namespace, p.Name, p.Status,
			fmt.Sprintf("%d", p.Restarts), p.Node, p.Reason,
		}, i%2 == 0)
	}
}

// renderAllPods 列出 cluster 中所有 (非 collector 自身) Pod 的詳細資訊,
// 包含 namespace, Pod 名, 狀態, IP, 排程節點, 以及 hostPath 掛載 (有的話)。
//
// hostPath 欄位採多行格式 "/host/path → /in/container [container, ro]",
// 一個 Pod 有多個 hostPath 掛載時會以換行分隔; MultiCell 自動擴展 row 高度。
func renderAllPods(pdf *gofpdf.Fpdf, r *model.Report) {
	sectionTitle(pdf, "10. Pod 總覽")
	if len(r.AllPods) == 0 {
		note(pdf, "沒有可顯示的 Pod (collector 自身 Pod 一律排除). ")
		return
	}
	note(pdf, fmt.Sprintf("共 %d 個 Pod (已排除 k8s-healthcheck 蒐集器自身 Pod). HostPath 欄位列出 Pod 掛載到節點本機的目錄.", len(r.AllPods)))
	pdf.Ln(1)

	widths := []float64{30, 50, 18, 24, 26, 38}
	tableHeader(pdf, widths, []string{"Namespace", "Pod", "Status", "Pod IP", "節點", "HostPath 掛載"})
	for i, p := range r.AllPods {
		tableRow(pdf, widths, []string{
			p.Namespace,
			p.Name,
			safe(p.Status),
			safe(p.PodIP),
			safe(p.Node),
			formatHostPaths(p.HostPaths),
		}, i%2 == 0)
	}
}

// formatHostPaths 把 HostPathMount 清單轉為人讀字串, 僅顯示宿主機路徑 (例如
// "/opt/cni/bin"). 同一 Pod 多個 hostPath 以換行分隔, 重複路徑會去重 (例如
// 兩個 container 都掛同一個 host 目錄, 顯示一次即可)。空清單回 "-".
func formatHostPaths(mounts []model.HostPathMount) string {
	if len(mounts) == 0 {
		return "-"
	}
	seen := map[string]bool{}
	lines := make([]string, 0, len(mounts))
	for _, m := range mounts {
		if seen[m.HostPath] {
			continue
		}
		seen[m.HostPath] = true
		lines = append(lines, m.HostPath)
	}
	return strings.Join(lines, "\n")
}

func renderTopMetrics(pdf *gofpdf.Fpdf, r *model.Report) {
	sectionTitle(pdf, "9. 資源耗用前段班")
	if len(r.TopCPU) == 0 && len(r.TopMemory) == 0 {
		note(pdf, "沒有 Pod 度量資料; metrics-server 未安裝或無法存取. ")
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
	sectionTitle(pdf, "8. 工作負載")
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

// renderStorage 把儲存章節整合成"StorageClasses + PV 詳情 + PVC 詳情"三段, 
// 各自帶上各階段計數的 subtitle, 避免與舊版重複出現"概覽計數表 + 詳細表". 
func renderStorage(pdf *gofpdf.Fpdf, r *model.Report) {
	sectionTitle(pdf, "11. 儲存")
	s := r.Storage

	// === StorageClass 詳細表 ===
	subTitle(pdf, fmt.Sprintf("Storage Classes (共 %d 個)", len(s.StorageClasses)))
	if len(s.StorageClasses) == 0 {
		note(pdf, "未偵測到 StorageClass. ")
	} else {
		w := []float64{50, 60, 24, 30, 12, 12}
		tableHeader(pdf, w, []string{"名稱", "Provisioner", "Reclaim", "Binding 模式", "PV", "PVC"})
		for i, sc := range s.StorageClasses {
			name := sc.Name
			if sc.IsDefault {
				name += " *"
			}
			tableRow(pdf, w, []string{
				name,
				sc.Provisioner,
				sc.ReclaimPolicy,
				sc.VolumeBindingMode,
				fmt.Sprintf("%d", sc.PVCount),
				fmt.Sprintf("%d", sc.PVCCount),
			}, i%2 == 0)
		}
		note(pdf, "* 表示 default StorageClass. ")
	}

	// === PersistentVolumes 詳情 (含階段彙總) ===
	pdf.Ln(3)
	subTitle(pdf, fmt.Sprintf(
		"PersistentVolumes (共 %d / Bound %d / Available %d / Released %d / Failed %d)",
		s.PVs, s.PVsBound, s.PVsAvailable, s.PVsReleased, s.PVsFailed))
	if len(s.PVList) == 0 {
		note(pdf, "未偵測到 PV. ")
	} else {
		w := []float64{56, 22, 16, 24, 50, 20}
		tableHeader(pdf, w, []string{"名稱", "容量", "存取", "StorageClass", "Claim", "狀態"})
		for i, pv := range s.PVList {
			tableRow(pdf, w, []string{
				pv.Name,
				pv.Capacity,
				pv.AccessModes,
				safe(pv.Class),
				safe(pv.Claim),
				pv.Status,
			}, i%2 == 0)
		}
	}

	// === PersistentVolumeClaims 詳情 (含階段彙總) ===
	pdf.Ln(3)
	subTitle(pdf, fmt.Sprintf(
		"PersistentVolumeClaims (共 %d / Bound %d / Pending %d)",
		s.PVCs, s.PVCsBound, s.PVCsPending))
	if len(s.PVCList) == 0 {
		note(pdf, "未偵測到 PVC. ")
	} else {
		w := []float64{30, 50, 18, 22, 24, 44}
		tableHeader(pdf, w, []string{"Namespace", "名稱", "容量", "狀態", "StorageClass", "綁定 PV"})
		for i, p := range s.PVCList {
			tableRow(pdf, w, []string{
				p.Namespace,
				p.Name,
				p.Capacity,
				p.Status,
				safe(p.Class),
				safe(p.Volume),
			}, i%2 == 0)
		}
	}
}

func renderControlPlane(pdf *gofpdf.Fpdf, r *model.Report) {
	sectionTitle(pdf, "12. Control-plane健康")
	if len(r.APIHealth) == 0 && len(r.Components) == 0 {
		note(pdf, "Control-plane健康端點不可用. ")
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

// renderCerts 把所有憑證依"來源"分組呈現, 方便分辨 K8s pki / kubelet / etcd /
// kubeconfig 不同類別的到期狀況. 
func renderCerts(pdf *gofpdf.Fpdf, r *model.Report) {
	sectionTitle(pdf, "13. 憑證到期")
	if len(r.Certs) == 0 {
		note(pdf, "未取得憑證資料. aggregator 模式下請部署 DaemonSet agent, agent 會掃 /etc/kubernetes/pki, /var/lib/kubelet/pki 與 *.conf 內嵌憑證. ")
		return
	}
	// 依 source 分類
	groups := map[string][]model.CertInfo{}
	order := []string{"k8s-pki", "etcd", "kubeconfig", "kubelet", ""}
	titles := map[string]string{
		"k8s-pki":    "K8s Control-plane憑證 (/etc/kubernetes/pki)",
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
		// 移除 Subject 欄, 路徑改顯示完整字串 (multiCellRow 會自動依寬度換行).
		// 寬度合計 28+96+22+18+22 = 186mm 對齊 A4 可用寬.
		widths := []float64{28, 96, 22, 18, 22}
		tableHeader(pdf, widths, []string{"節點", "路徑", "到期日", "剩餘天數", "狀態"})
		for i, c := range certs {
			tableRow(pdf, widths, []string{
				safe(c.Node),
				c.Path,
				tz.In(c.NotAfter).Format("2006-01-02"),
				fmt.Sprintf("%d", c.DaysLeft),
				c.Status,
			}, i%2 == 0)
		}
	}
}

func renderEvents(pdf *gofpdf.Fpdf, r *model.Report) {
	sectionTitle(pdf, "14. 近期警告事件")
	if len(r.Events) == 0 {
		note(pdf, "區間內無警告 / 錯誤事件. ")
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
	sectionTitle(pdf, "15. 蒐集備註")
	note(pdf, "以下蒐集器回報非致命錯誤, 相關區段可能不完整:")
	pdf.SetFont(fontFamily, "", 9)
	for _, e := range r.Errors {
		pdf.MultiCell(0, 5, "- "+e, "", "L", false)
	}
}

// truncate 以"rune"為單位截斷, 避免把中文字切到一半變成亂碼. 
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
