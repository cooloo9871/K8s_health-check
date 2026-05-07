package report

import (
	"fmt"
	"strings"
	"time"

	"github.com/brobridge/k8s-health-check/internal/model"
	"github.com/jung-kurt/gofpdf"
)

// WritePDF renders the Report as a multi-section PDF.  The font is the
// built-in Helvetica so we never depend on external font files; that
// keeps the docker image tiny and avoids per-distro font hassles.
func WritePDF(r *model.Report, path string) error {
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.SetMargins(12, 14, 12)
	pdf.SetAutoPageBreak(true, 14)
	pdf.AliasNbPages("")

	pdf.SetHeaderFunc(func() {
		pdf.SetFont("Helvetica", "I", 8)
		pdf.SetTextColor(120, 120, 120)
		pdf.CellFormat(0, 6, "Kubernetes Health Check Report", "", 1, "R", false, 0, "")
		pdf.SetTextColor(0, 0, 0)
	})
	pdf.SetFooterFunc(func() {
		pdf.SetY(-12)
		pdf.SetFont("Helvetica", "I", 8)
		pdf.SetTextColor(120, 120, 120)
		pdf.CellFormat(0, 6, fmt.Sprintf("Page %d / {nb}", pdf.PageNo()),
			"", 0, "C", false, 0, "")
		pdf.SetTextColor(0, 0, 0)
	})

	renderCover(pdf, r)
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
	pdf.SetFont("Helvetica", "B", 12)
	pdf.CellFormat(0, 8, " "+title, "", 1, "L", true, 0, "")
	pdf.SetTextColor(0, 0, 0)
	pdf.Ln(2)
}

func note(pdf *gofpdf.Fpdf, msg string) {
	pdf.SetFont("Helvetica", "I", 9)
	pdf.SetTextColor(110, 110, 110)
	pdf.MultiCell(0, 5, msg, "", "L", false)
	pdf.SetTextColor(0, 0, 0)
}

func tableHeader(pdf *gofpdf.Fpdf, widths []float64, headers []string) {
	pdf.SetFillColor(220, 230, 245)
	pdf.SetFont("Helvetica", "B", 9)
	for i, h := range headers {
		pdf.CellFormat(widths[i], 6, h, "1", 0, "L", true, 0, "")
	}
	pdf.Ln(-1)
	pdf.SetFont("Helvetica", "", 8)
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
		pdf.CellFormat(widths[i], 5.5, ascii(c), "1", 0, "L", true, 0, "")
	}
	pdf.Ln(-1)
}

// ascii strips characters Helvetica cannot render so we never emit
// garbage glyphs for unicode messages from the cluster.
func ascii(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 32 && r <= 126 {
			b.WriteRune(r)
		} else {
			b.WriteRune('?')
		}
	}
	return b.String()
}

func kv(pdf *gofpdf.Fpdf, k, v string) {
	pdf.SetFont("Helvetica", "B", 10)
	pdf.CellFormat(50, 6, k, "", 0, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 10)
	pdf.MultiCell(0, 6, ascii(v), "", "L", false)
}

func pct(v float64) string {
	return fmt.Sprintf("%.1f%%", v)
}

// ----- sections -------------------------------------------------------

func renderCover(pdf *gofpdf.Fpdf, r *model.Report) {
	pdf.AddPage()
	pdf.SetFont("Helvetica", "B", 22)
	pdf.Ln(40)
	pdf.CellFormat(0, 12, "Kubernetes Cluster", "", 1, "C", false, 0, "")
	pdf.CellFormat(0, 12, "Health Check Report", "", 1, "C", false, 0, "")
	pdf.Ln(10)

	pdf.SetFont("Helvetica", "", 12)
	pdf.CellFormat(0, 8, fmt.Sprintf("Generated: %s", r.GeneratedAt.Format(time.RFC1123)),
		"", 1, "C", false, 0, "")
	pdf.CellFormat(0, 8, fmt.Sprintf("Distribution: %s", strings.ToUpper(safe(r.Cluster.Distribution))),
		"", 1, "C", false, 0, "")
	pdf.CellFormat(0, 8, fmt.Sprintf("Version: %s", safe(r.Cluster.Version)),
		"", 1, "C", false, 0, "")
	pdf.Ln(20)

	// quick scoreboard
	pdf.SetFont("Helvetica", "B", 14)
	pdf.CellFormat(0, 8, "At a Glance", "", 1, "C", false, 0, "")
	pdf.SetFont("Helvetica", "", 11)
	rows := [][2]string{
		{"Nodes", fmt.Sprintf("%d", r.Cluster.NodeCount)},
		{"Namespaces", fmt.Sprintf("%d", r.Cluster.NamespaceCnt)},
		{"Total Pods", fmt.Sprintf("%d", r.Cluster.TotalPods)},
		{"Running Pods", fmt.Sprintf("%d", r.PodSummary.Running)},
		{"Pending Pods", fmt.Sprintf("%d", r.PodSummary.Pending)},
		{"Failed Pods", fmt.Sprintf("%d", r.PodSummary.Failed)},
		{"Problem Pods Listed", fmt.Sprintf("%d", len(r.ProblemPods))},
		{"Warning Events", fmt.Sprintf("%d", len(r.Events))},
		{"Unhealthy Workloads", fmt.Sprintf("%d", len(r.Workloads.Unhealthy))},
		{"Certificates Tracked", fmt.Sprintf("%d", len(r.Certs))},
	}
	for _, kv := range rows {
		pdf.CellFormat(70, 7, kv[0], "", 0, "R", false, 0, "")
		pdf.CellFormat(60, 7, kv[1], "", 1, "L", false, 0, "")
	}
}

func safe(s string) string {
	if s == "" {
		return "n/a"
	}
	return s
}

func renderClusterOverview(pdf *gofpdf.Fpdf, r *model.Report) {
	pdf.AddPage()
	sectionTitle(pdf, "1. Cluster Overview")
	kv(pdf, "Kubernetes Version", safe(r.Cluster.Version))
	kv(pdf, "Platform", safe(r.Cluster.Platform))
	kv(pdf, "Distribution Tag", safe(r.Cluster.Distribution))
	kv(pdf, "Node Count", fmt.Sprintf("%d", r.Cluster.NodeCount))
	kv(pdf, "Namespace Count", fmt.Sprintf("%d", r.Cluster.NamespaceCnt))
	kv(pdf, "Total Pods", fmt.Sprintf("%d", r.Cluster.TotalPods))
}

func renderNodes(pdf *gofpdf.Fpdf, r *model.Report) {
	sectionTitle(pdf, "2. Nodes")
	if len(r.Nodes) == 0 {
		note(pdf, "No node data collected.")
		return
	}
	widths := []float64{40, 22, 22, 24, 26, 18, 14, 20}
	tableHeader(pdf, widths, []string{"Name", "Roles", "Status", "Kubelet", "Internal IP", "Age", "Pods", "Runtime"})
	for i, n := range r.Nodes {
		tableRow(pdf, widths, []string{
			n.Name, n.Roles, n.Status, n.KubeletVersion,
			n.InternalIP, n.Age, fmt.Sprintf("%d", n.PodCount), shortRuntime(n.Runtime),
		}, i%2 == 0)
	}

	// surface non-healthy conditions explicitly
	pdf.Ln(3)
	pdf.SetFont("Helvetica", "B", 10)
	pdf.CellFormat(0, 6, "Notable Node Conditions", "", 1, "L", false, 0, "")
	pdf.SetFont("Helvetica", "", 9)
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
					ascii(fmt.Sprintf("%s -> %s=%s reason=%s msg=%s",
						n.Name, c.Type, c.Status, c.Reason, truncate(c.Message, 100))),
					"", "L", false)
			}
		}
	}
	if !hadAny {
		note(pdf, "All node conditions nominal.")
	}
}

func shortRuntime(r string) string {
	// "containerd://1.7.13" -> "containerd 1.7.13"
	r = strings.ReplaceAll(r, "://", " ")
	if len(r) > 18 {
		return r[:18]
	}
	return r
}

func renderNodeMetrics(pdf *gofpdf.Fpdf, r *model.Report) {
	sectionTitle(pdf, "3. Node Resource Usage")
	if len(r.NodeMetrics) == 0 {
		note(pdf, "metrics.k8s.io API not available; install metrics-server to populate this section.")
		return
	}
	widths := []float64{50, 22, 30, 22, 30, 18, 18}
	tableHeader(pdf, widths, []string{"Node", "CPU %", "CPU Used/Cap", "Mem %", "Mem Used/Cap", "Pods", "PodCap"})
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
	sectionTitle(pdf, "4. Pod Summary")
	s := r.PodSummary
	widths := []float64{30, 30, 30, 30, 30, 30}
	tableHeader(pdf, widths, []string{"Total", "Running", "Pending", "Succeeded", "Failed", "Unknown"})
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
	sectionTitle(pdf, "5. Problem Pods (not Ready / restarting / failing)")
	if len(r.ProblemPods) == 0 {
		note(pdf, "No problematic pods detected.")
		return
	}
	widths := []float64{30, 50, 22, 16, 30, 38}
	tableHeader(pdf, widths, []string{"Namespace", "Pod", "Phase", "Restarts", "Node", "Reason"})
	for i, p := range r.ProblemPods {
		tableRow(pdf, widths, []string{
			p.Namespace, p.Name, p.Status,
			fmt.Sprintf("%d", p.Restarts), p.Node, p.Reason,
		}, i%2 == 0)
	}
}

func renderTopMetrics(pdf *gofpdf.Fpdf, r *model.Report) {
	sectionTitle(pdf, "6. Top Resource Consumers")
	if len(r.TopCPU) == 0 && len(r.TopMemory) == 0 {
		note(pdf, "No pod metrics; metrics-server is not installed or unreachable.")
		return
	}
	pdf.SetFont("Helvetica", "B", 10)
	pdf.CellFormat(0, 6, "Top 10 Pods by CPU", "", 1, "L", false, 0, "")
	widths := []float64{35, 80, 30, 30}
	tableHeader(pdf, widths, []string{"Namespace", "Pod", "CPU", "Memory"})
	for i, p := range r.TopCPU {
		tableRow(pdf, widths, []string{p.Namespace, p.Name, p.CPU, p.Memory}, i%2 == 0)
	}
	pdf.Ln(3)
	pdf.SetFont("Helvetica", "B", 10)
	pdf.CellFormat(0, 6, "Top 10 Pods by Memory", "", 1, "L", false, 0, "")
	tableHeader(pdf, widths, []string{"Namespace", "Pod", "CPU", "Memory"})
	for i, p := range r.TopMemory {
		tableRow(pdf, widths, []string{p.Namespace, p.Name, p.CPU, p.Memory}, i%2 == 0)
	}
}

func renderWorkloads(pdf *gofpdf.Fpdf, r *model.Report) {
	sectionTitle(pdf, "7. Workloads")
	w := r.Workloads
	pairs := [][3]string{
		{"Deployments", fmt.Sprintf("%d", w.Deployments.Total), fmt.Sprintf("%d", w.Deployments.Ready)},
		{"DaemonSets", fmt.Sprintf("%d", w.DaemonSets.Total), fmt.Sprintf("%d", w.DaemonSets.Ready)},
		{"StatefulSets", fmt.Sprintf("%d", w.StatefulSets.Total), fmt.Sprintf("%d", w.StatefulSets.Ready)},
		{"ReplicaSets (active)", fmt.Sprintf("%d", w.ReplicaSets.Total), fmt.Sprintf("%d", w.ReplicaSets.Ready)},
		{"Jobs", fmt.Sprintf("%d", w.Jobs.Total), fmt.Sprintf("%d", w.Jobs.Ready)},
		{"CronJobs", fmt.Sprintf("%d", w.CronJobs), "-"},
	}
	widths := []float64{60, 30, 30}
	tableHeader(pdf, widths, []string{"Kind", "Total", "Healthy"})
	for i, p := range pairs {
		tableRow(pdf, widths, []string{p[0], p[1], p[2]}, i%2 == 0)
	}

	if len(w.Unhealthy) > 0 {
		pdf.Ln(3)
		pdf.SetFont("Helvetica", "B", 10)
		pdf.CellFormat(0, 6, "Unhealthy Workloads", "", 1, "L", false, 0, "")
		w2 := []float64{22, 30, 60, 18, 18, 38}
		tableHeader(pdf, w2, []string{"Kind", "Namespace", "Name", "Desired", "Ready", "Reason"})
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
	sectionTitle(pdf, "8. Storage")
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
		pdf.SetFont("Helvetica", "B", 10)
		pdf.CellFormat(0, 6, "Storage Classes", "", 1, "L", false, 0, "")
		pdf.SetFont("Helvetica", "", 9)
		pdf.MultiCell(0, 5, ascii(strings.Join(s.StorageClasses, ", ")), "", "L", false)
	}

	if len(s.ProblemPVCs) > 0 {
		pdf.Ln(2)
		pdf.SetFont("Helvetica", "B", 10)
		pdf.CellFormat(0, 6, "Pending / Problem PVCs", "", 1, "L", false, 0, "")
		w3 := []float64{30, 60, 22, 30, 30}
		tableHeader(pdf, w3, []string{"Namespace", "Name", "Status", "Capacity", "Class"})
		for i, p := range s.ProblemPVCs {
			tableRow(pdf, w3, []string{p.Namespace, p.Name, p.Status, p.Capacity, p.Class}, i%2 == 0)
		}
	}
}

func renderControlPlane(pdf *gofpdf.Fpdf, r *model.Report) {
	sectionTitle(pdf, "9. Control Plane Health")
	if len(r.APIHealth) == 0 && len(r.Components) == 0 {
		note(pdf, "Control plane health endpoints unavailable.")
		return
	}
	if len(r.APIHealth) > 0 {
		widths := []float64{40, 30, 110}
		tableHeader(pdf, widths, []string{"Endpoint", "Status", "Detail"})
		for i, h := range r.APIHealth {
			tableRow(pdf, widths, []string{h.Endpoint, h.Status, h.Detail}, i%2 == 0)
		}
	}
	if len(r.Components) > 0 {
		pdf.Ln(2)
		pdf.SetFont("Helvetica", "B", 10)
		pdf.CellFormat(0, 6, "Component Statuses (legacy)", "", 1, "L", false, 0, "")
		widths := []float64{50, 30, 100}
		tableHeader(pdf, widths, []string{"Component", "Healthy", "Message"})
		for i, c := range r.Components {
			tableRow(pdf, widths, []string{c.Name, c.Healthy, c.Message}, i%2 == 0)
		}
	}
}

func renderCerts(pdf *gofpdf.Fpdf, r *model.Report) {
	sectionTitle(pdf, "10. Certificate Expiration")
	if len(r.Certs) == 0 {
		note(pdf, "PKI directory not mounted (mount /etc/kubernetes/pki via hostPath to populate this section). Skipped on non-kubeadm distros.")
		return
	}
	widths := []float64{70, 40, 30, 20, 26}
	tableHeader(pdf, widths, []string{"Path", "Subject", "Not After", "Days", "Status"})
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
	sectionTitle(pdf, "11. Recent Warning Events")
	if len(r.Events) == 0 {
		note(pdf, "No warning/error events in window.")
		return
	}
	widths := []float64{32, 22, 25, 50, 57}
	tableHeader(pdf, widths, []string{"Last Seen", "Reason", "Object", "Namespace/Object", "Message"})
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
	sectionTitle(pdf, "12. Collection Notes")
	note(pdf, "The following collectors reported non-fatal errors. Sections may be partial.")
	pdf.SetFont("Helvetica", "", 9)
	for _, e := range r.Errors {
		pdf.MultiCell(0, 5, ascii("- "+e), "", "L", false)
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "..."
}
