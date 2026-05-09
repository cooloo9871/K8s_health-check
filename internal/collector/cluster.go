package collector

import (
	"context"
	"regexp"
	"strings"

	"k8s-health-check/internal/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// collectCluster 抓取 cluster 層級的基本資料: 版本、平台、節點/Namespace/Pod
// 數量、cluster name。Pod 總數會扣除 collector 自身與所有 k8s-healthcheck-*
// 系統 Pod，與其他區段保持一致。
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

	// cluster name: 旗標優先，其次 kubeadm-config，最後 kube-system UID。
	r.Cluster.Name, r.Cluster.NameSource = c.detectClusterName(ctx)
	return nil
}

// detectClusterName 依下列順序回傳 cluster 識別字串:
//  1. 使用者透過 --cluster-name 指定的值 (來源 = "flag")
//  2. kube-system/kubeadm-config ConfigMap 中的 clusterName 欄位 (來源 = "kubeadm")
//  3. kube-system Namespace 的 UID 前 12 碼 (來源 = "uid")
//  4. 都拿不到時回 "unknown"
func (c *Collector) detectClusterName(ctx context.Context) (string, string) {
	if v := strings.TrimSpace(c.clusterName); v != "" {
		return v, "flag"
	}

	cm, err := c.clientset.CoreV1().ConfigMaps("kube-system").Get(ctx, "kubeadm-config", metav1.GetOptions{})
	if err == nil {
		if cfg, ok := cm.Data["ClusterConfiguration"]; ok {
			if name := parseClusterNameYAML(cfg); name != "" {
				return name, "kubeadm"
			}
		}
	}

	ksns, err := c.clientset.CoreV1().Namespaces().Get(ctx, "kube-system", metav1.GetOptions{})
	if err == nil {
		uid := string(ksns.UID)
		if len(uid) >= 12 {
			return "cluster-" + uid[:12], "uid"
		}
		if uid != "" {
			return "cluster-" + uid, "uid"
		}
	}
	return "unknown", "unknown"
}

// kubeadmClusterNameRE 比對 kubeadm-config ConfigMap.Data["ClusterConfiguration"]
// 內的 clusterName 欄位。用 regex 而非 yaml parser 以避免把 yaml 套件帶進
// collector (該套件已透過 client-go 間接相依，但維持手動 parse 更穩定)。
var kubeadmClusterNameRE = regexp.MustCompile(`(?m)^\s*clusterName:\s*(\S+)\s*$`)

func parseClusterNameYAML(s string) string {
	m := kubeadmClusterNameRE.FindStringSubmatch(s)
	if len(m) < 2 {
		return ""
	}
	return strings.Trim(strings.TrimSpace(m[1]), `"'`)
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
