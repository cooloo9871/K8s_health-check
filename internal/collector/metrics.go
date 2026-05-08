package collector

import (
	"context"
	"fmt"
	"sort"

	"k8s-health-check/internal/model"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// collectMetrics 是 metrics-server 兩個子收集器的入口。如果 metrics client
// 在初始化階段就建立失敗 (例如 metrics-server 未安裝)，會直接回錯，由上層
// 把訊息掛到 Report.Errors 並讓報告區段顯示「不可用」。
func (c *Collector) collectMetrics(ctx context.Context, r *model.Report) error {
	if c.metrics == nil {
		return fmt.Errorf("metrics client unavailable; install metrics-server for resource usage")
	}

	if err := c.collectNodeMetrics(ctx, r); err != nil {
		c.addErr("node-metrics", err)
	}
	return c.collectPodMetrics(ctx, r)
}

// collectNodeMetrics 抓 metrics.k8s.io 的節點即時 CPU/Memory，搭配節點
// 容量算出百分比；同時把每個節點上目前的 Pod 數一併附上。
func (c *Collector) collectNodeMetrics(ctx context.Context, r *model.Report) error {
	nm, err := c.metrics.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	nodes, err := c.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	capByName := map[string]struct {
		cpu    resource.Quantity
		mem    resource.Quantity
		podCap int64
	}{}
	for _, n := range nodes.Items {
		entry := capByName[n.Name]
		if cpu, ok := n.Status.Capacity["cpu"]; ok {
			entry.cpu = cpu
		}
		if mem, ok := n.Status.Capacity["memory"]; ok {
			entry.mem = mem
		}
		if pods, ok := n.Status.Capacity["pods"]; ok {
			entry.podCap, _ = pods.AsInt64()
		}
		capByName[n.Name] = entry
	}

	pods, _ := c.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	podsByNode := map[string]int{}
	if pods != nil {
		for _, p := range pods.Items {
			if c.isSelf(p.Namespace, p.Name) {
				continue
			}
			podsByNode[p.Spec.NodeName]++
		}
	}

	out := make([]model.NodeMetric, 0, len(nm.Items))
	for _, m := range nm.Items {
		cap := capByName[m.Name]
		usedCPU := m.Usage["cpu"]
		usedMem := m.Usage["memory"]

		var cpuPct, memPct float64
		if cap.cpu.MilliValue() > 0 {
			cpuPct = float64(usedCPU.MilliValue()) / float64(cap.cpu.MilliValue()) * 100
		}
		if cap.mem.Value() > 0 {
			memPct = float64(usedMem.Value()) / float64(cap.mem.Value()) * 100
		}
		out = append(out, model.NodeMetric{
			Name:        m.Name,
			CPUUsed:     fmt.Sprintf("%dm", usedCPU.MilliValue()),
			CPUCapacity: fmt.Sprintf("%dm", cap.cpu.MilliValue()),
			CPUPercent:  cpuPct,
			MemUsed:     humanBytes(usedMem.Value()),
			MemCapacity: humanBytes(cap.mem.Value()),
			MemPercent:  memPct,
			PodCount:    podsByNode[m.Name],
			PodCapacity: int(cap.podCap),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	r.NodeMetrics = out
	return nil
}

// collectPodMetrics 抓 metrics.k8s.io 的 Pod 級即時用量，把全 cluster 前 10 名
// CPU 與前 10 名記憶體挑出來，並排除 collector 自己。
func (c *Collector) collectPodMetrics(ctx context.Context, r *model.Report) error {
	pm, err := c.metrics.MetricsV1beta1().PodMetricses("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	all := make([]model.PodMetric, 0, len(pm.Items))
	for _, p := range pm.Items {
		// 排除 collector 自己，避免出現在 Top CPU / Top Memory。
		if c.isSelf(p.Namespace, p.Name) {
			continue
		}
		var cpuMilli int64
		var memBytes int64
		for _, ctr := range p.Containers {
			cpuMilli += ctr.Usage.Cpu().MilliValue()
			memBytes += ctr.Usage.Memory().Value()
		}
		all = append(all, model.PodMetric{
			Namespace: p.Namespace,
			Name:      p.Name,
			CPU:       fmt.Sprintf("%dm", cpuMilli),
			CPUMillis: cpuMilli,
			Memory:    humanBytes(memBytes),
			MemoryMiB: memBytes / (1024 * 1024),
		})
	}
	byCPU := append([]model.PodMetric(nil), all...)
	sort.Slice(byCPU, func(i, j int) bool { return byCPU[i].CPUMillis > byCPU[j].CPUMillis })
	if len(byCPU) > 10 {
		byCPU = byCPU[:10]
	}
	byMem := append([]model.PodMetric(nil), all...)
	sort.Slice(byMem, func(i, j int) bool { return byMem[i].MemoryMiB > byMem[j].MemoryMiB })
	if len(byMem) > 10 {
		byMem = byMem[:10]
	}
	r.TopCPU = byCPU
	r.TopMemory = byMem
	return nil
}

// humanBytes 把 bytes 轉成 KiB/MiB/GiB 的人類可讀字串。
func humanBytes(b int64) string {
	const (
		KiB = 1024
		MiB = 1024 * KiB
		GiB = 1024 * MiB
	)
	switch {
	case b >= GiB:
		return fmt.Sprintf("%.2f GiB", float64(b)/float64(GiB))
	case b >= MiB:
		return fmt.Sprintf("%.0f MiB", float64(b)/float64(MiB))
	case b >= KiB:
		return fmt.Sprintf("%.0f KiB", float64(b)/float64(KiB))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
