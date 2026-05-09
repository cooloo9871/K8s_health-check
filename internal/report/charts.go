package report

import (
	"fmt"
	"math"

	"github.com/jung-kurt/gofpdf"
)

// Slice 是圓餅圖的單一切片. Color 為 RGB triplet (0-255). 
type Slice struct {
	Label string
	Value float64
	R, G, B int
}

// drawPie 在 (cx, cy) 畫一個半徑 radius 的圓餅圖. slices 數值會自動正規化. 
// 圖例 (legend) 會被畫在 legendX/legendY 起點往下排列. 
func drawPie(pdf *gofpdf.Fpdf, cx, cy, radius float64, slices []Slice, legendX, legendY float64) {
	total := 0.0
	for _, s := range slices {
		if s.Value < 0 {
			continue
		}
		total += s.Value
	}
	if total <= 0 {
		// 整體為 0 — 畫一個灰色的空心圈說明"無資料"
		pdf.SetDrawColor(180, 180, 180)
		pdf.SetLineWidth(0.5)
		pdf.Circle(cx, cy, radius, "D")
		pdf.SetFont(fontFamily, "", 9)
		pdf.SetTextColor(120, 120, 120)
		pdf.Text(cx-12, cy+1.5, "無資料")
		pdf.SetTextColor(0, 0, 0)
		return
	}

	// 從 12 點鐘方向 (上方) 開始順時針切片, 與一般使用者預期相符. 
	startDeg := 90.0 // gofpdf Arc 採數學定義 (右為 0, 上為 90), 從 90 起對應 12 點
	for _, s := range slices {
		if s.Value <= 0 {
			continue
		}
		span := s.Value / total * 360.0
		endDeg := startDeg - span // 順時針 = 角度遞減
		fillWedge(pdf, cx, cy, radius, startDeg, endDeg, s.R, s.G, s.B)
		startDeg = endDeg
	}
	// 邊框
	pdf.SetDrawColor(255, 255, 255)
	pdf.SetLineWidth(0.4)
	pdf.Circle(cx, cy, radius, "D")
	pdf.SetDrawColor(0, 0, 0)

	// 圖例
	drawLegend(pdf, slices, total, legendX, legendY)
}

// fillWedge 在 (cx,cy) 為圓心畫一個從 startDeg 到 endDeg (數學角度, 逆時針為正)
// 的填色扇形. 使用多邊形採樣近似圓弧. 
func fillWedge(pdf *gofpdf.Fpdf, cx, cy, r, startDeg, endDeg float64, red, green, blue int) {
	pdf.SetFillColor(red, green, blue)
	// 取樣點數: 每 3 度一個點, 最少 6 個
	span := startDeg - endDeg
	if span < 0 {
		span = -span
	}
	steps := int(span / 3)
	if steps < 6 {
		steps = 6
	}
	pts := make([]gofpdf.PointType, 0, steps+2)
	pts = append(pts, gofpdf.PointType{X: cx, Y: cy})
	for i := 0; i <= steps; i++ {
		t := float64(i) / float64(steps)
		// PDF 座標 y 軸朝下, 數學角度需要翻轉 sin
		ang := (startDeg + (endDeg-startDeg)*t) * math.Pi / 180
		x := cx + r*math.Cos(ang)
		y := cy - r*math.Sin(ang)
		pts = append(pts, gofpdf.PointType{X: x, Y: y})
	}
	pdf.Polygon(pts, "F")
}

// drawLegend 把每個 slice 印成"色塊  Label  數值 (百分比)"格式. 
func drawLegend(pdf *gofpdf.Fpdf, slices []Slice, total, x, y float64) {
	pdf.SetFont(fontFamily, "", 9)
	for _, s := range slices {
		if s.Value <= 0 {
			continue
		}
		pdf.SetFillColor(s.R, s.G, s.B)
		pdf.Rect(x, y-3.4, 4, 4, "F")
		pdf.SetTextColor(0, 0, 0)
		pct := s.Value / total * 100
		text := fmt.Sprintf("%s  %.0f (%.1f%%)", s.Label, s.Value, pct)
		pdf.Text(x+6, y, text)
		y += 5.5
	}
}

// HorizBar 是水平柱狀圖的單一條目. 
type HorizBar struct {
	Label string
	// Value/Max 用於計算柱長 (Value/Max 在 0~1). 
	Value float64
	Max   float64
	// Display 是右側顯示的數值字串 (例如 "82% (12.4 / 15 GiB)"). 
	Display string
	// Status 控制著色: "OK" 綠 / "WARN" 琥珀 / "CRITICAL" 紅. 空值用中性藍. 
	Status string
}

// drawHorizBars 在指定區域內畫水平柱狀圖, label 過長會自動換行,
// row 高度依 label 行數動態調整 (不會被截掉).
//   - x, y: 區塊左上角
//   - w, baseRowH: 整體寬, 預設單列高 (label 1 行時的高度)
//   - labelW: 左側 label 欄位寬
//   - displayW: 右側 display 欄位寬
//   - barW (= w - labelW - displayW) 為柱子實際寬
func drawHorizBars(pdf *gofpdf.Fpdf, x, y, w, baseRowH, labelW, displayW float64, bars []HorizBar) {
	barW := w - labelW - displayW
	if barW < 10 {
		barW = w / 2
	}
	pdf.SetFont(fontFamily, "", 8)
	const labelLineH = 3.5

	_, ph := pdf.GetPageSize()
	for _, b := range bars {
		// 估算 label 在 labelW 內會被切成幾行
		lines := pdf.SplitText(b.Label, labelW-1.5)
		lineCount := len(lines)
		if lineCount < 1 {
			lineCount = 1
		}
		rowH := baseRowH
		if h := float64(lineCount)*labelLineH + 1.0; h > rowH {
			rowH = h
		}
		// 換頁: 留 14mm 底部 margin
		if pdf.GetY()+rowH > ph-14 {
			pdf.AddPage()
			y = pdf.GetY()
		}

		// Label 用 MultiCell 自動換行寫入
		pdf.SetTextColor(40, 40, 40)
		pdf.SetXY(x, y+0.4)
		pdf.MultiCell(labelW, labelLineH, b.Label, "", "L", false)

		// Bar 背景與填色 (置於 row 中央, 高度 rowH-2)
		barH := rowH - 2
		barY := y + 1
		pdf.SetFillColor(232, 235, 240)
		pdf.Rect(x+labelW, barY, barW, barH, "F")

		ratio := 0.0
		if b.Max > 0 {
			ratio = b.Value / b.Max
		}
		if ratio > 1 {
			ratio = 1
		}
		if ratio < 0 {
			ratio = 0
		}
		r, g, bl := statusBarColor(b.Status, ratio)
		pdf.SetFillColor(r, g, bl)
		pdf.Rect(x+labelW, barY, barW*ratio, barH, "F")

		// Display 文字置於 bar 旁中央
		pdf.SetTextColor(40, 40, 40)
		pdf.Text(x+labelW+barW+3, y+rowH/2+1, b.Display)

		y += rowH
		pdf.SetY(y)
	}
	pdf.SetTextColor(0, 0, 0)
}

// statusBarColor 依 Status 字串挑色, 沒有指定時以填色比例自動上色. 
func statusBarColor(status string, ratio float64) (int, int, int) {
	switch status {
	case "OK":
		return 50, 140, 70
	case "WARN":
		return 240, 160, 30
	case "CRITICAL":
		return 198, 40, 40
	}
	switch {
	case ratio >= 0.9:
		return 198, 40, 40
	case ratio >= 0.75:
		return 240, 160, 30
	default:
		return 60, 110, 180
	}
}

// HistBin 是憑證到期分佈的單一直方欄. 
type HistBin struct {
	Label string
	Count int
	R, G, B int
}

// drawHistogram 畫垂直直方圖 (用於憑證到期分佈, 節點狀態等離散分類). 
//   - x, y: 圖表左上角
//   - w, h: 整體寬, 整體高 (含標籤區)
func drawHistogram(pdf *gofpdf.Fpdf, x, y, w, h float64, bins []HistBin) {
	if len(bins) == 0 {
		return
	}
	maxCount := 0
	for _, b := range bins {
		if b.Count > maxCount {
			maxCount = b.Count
		}
	}
	if maxCount == 0 {
		// 沒有任何憑證資料: 用 Regular 字型 (我們只 embed Regular + Bold, 沒有 Italic)
		pdf.SetFont(fontFamily, "", 9)
		pdf.SetTextColor(120, 120, 120)
		pdf.Text(x, y+h/2, "無憑證資料")
		pdf.SetTextColor(0, 0, 0)
		return
	}

	plotH := h - 12 // 底部 12mm 留給標籤與數值
	gap := 3.0
	colW := (w - gap*float64(len(bins)-1)) / float64(len(bins))
	if colW < 8 {
		colW = 8
	}

	pdf.SetFont(fontFamily, "", 8)
	for i, b := range bins {
		bx := x + float64(i)*(colW+gap)
		ratio := float64(b.Count) / float64(maxCount)
		barH := plotH * ratio
		if barH < 0.5 && b.Count > 0 {
			barH = 0.5
		}
		by := y + plotH - barH
		pdf.SetFillColor(b.R, b.G, b.B)
		pdf.Rect(bx, by, colW, barH, "F")
		// 數值置中於柱頂上方 (用 SetXY 鎖定位置再 CellFormat). 
		pdf.SetTextColor(40, 40, 40)
		pdf.SetXY(bx, by-4.5)
		pdf.CellFormat(colW, 4, fmt.Sprintf("%d", b.Count), "", 0, "C", false, 0, "")
		// 底部標籤
		pdf.SetXY(bx, y+plotH+1)
		pdf.SetFont(fontFamily, "", 7)
		pdf.CellFormat(colW, 4, b.Label, "", 0, "C", false, 0, "")
		pdf.SetFont(fontFamily, "", 8)
	}
	pdf.SetTextColor(0, 0, 0)
	// 結束後把 y 推到底, 避免下一個元素重疊
	pdf.SetXY(x, y+h+2)
}
