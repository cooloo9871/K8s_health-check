package collector

import (
	"context"
	"strings"

	"k8s-health-check/internal/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// collectCluster 抓取 cluster 層級的基本資料: 版本、平台、節點/Namespace/Pod
// 數量。Pod 總數會扣除 collector 自身，與其他區段保持一致。
func (c *Collector) collectCluster(ctx context.Context, r *model.Report) error {
	v, err := c.clientset.Discovery().ServerVersion()
	if err == nil {
		r.Cluster.Version = v.GitVersion
		r.Cluster.Platform = v.Platform
		r.Cluster.Distribution = detectDistribution(v.GitVersion)
	}

	nodes, err := c.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err == nil {
		r.Cluster.NodeCount = len(nodes.Items)
	}

	ns, err := c.clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err == nil {
		r.Cluster.NamespaceCnt = len(ns.Items)
	}

	pods, err := c.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err == nil {
		total := 0
		for _, p := range pods.Items {
			if c.isSelf(p.Namespace, p.Name) {
				continue
			}
			total++
		}
		r.Cluster.TotalPods = total
	}
	return nil
}

// detectDistribution 依 GitVersion 字串推論 K8s 發行版本，只用於 PDF 檔名
// 標籤與報告顯示，不會影響任何收集邏輯。
func detectDistribution(gitVer string) string {
	v := strings.ToLower(gitVer)
	switch {
	case strings.Contains(v, "k3s"):
		return "k3s"
	case strings.Contains(v, "rke2"):
		return "rke2"
	case strings.Contains(v, "eks"):
		return "eks"
	case strings.Contains(v, "gke"):
		return "gke"
	case strings.Contains(v, "aks"):
		return "aks"
	case strings.Contains(v, "openshift"), strings.Contains(v, "ocp"):
		return "openshift"
	default:
		return "k8s"
	}
}
