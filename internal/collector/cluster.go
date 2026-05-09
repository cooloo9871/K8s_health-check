package collector

import (
	"context"
	"regexp"
	"strings"

	"k8s-health-check/internal/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// collectCluster 抓取 cluster 層級的基本資料: 版本、平台、節點/Namespace/Pod
// 數量、cluster name、發行版本。Pod 總數會扣除 collector 自身與所有
// k8s-healthcheck-* 系統 Pod, 與其他區段保持一致。
func (c *Collector) collectCluster(ctx context.Context, r *model.Report) error {
	v, err := c.clientset.Discovery().ServerVersion()
	if err == nil {
		r.Cluster.Version = v.GitVersion
		r.Cluster.Platform = v.Platform
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

	// 發行版本: 多訊號偵測。優先順序為 GitVersion > Namespace markers >
	// Node labels > kubeadm-config > 預設 "k8s"。
	r.Cluster.Distribution = c.detectDistribution(ctx, r.Cluster.Version, ns, nodes)

	// cluster name: 旗標 > kubeadm-config > kube-system UID。
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
// 內的 clusterName 欄位。
var kubeadmClusterNameRE = regexp.MustCompile(`(?m)^\s*clusterName:\s*(\S+)\s*$`)

func parseClusterNameYAML(s string) string {
	m := kubeadmClusterNameRE.FindStringSubmatch(s)
	if len(m) < 2 {
		return ""
	}
	return strings.Trim(strings.TrimSpace(m[1]), `"'`)
}

// detectDistribution 依多項訊號判斷 K8s 發行版本。回傳值為小寫短碼:
// "openshift" / "rke2" / "tkg" / "k3s" / "eks" / "gke" / "aks" / "kubeadm" / "k8s"。
//
// 偵測順序 (越前面越精準):
//  1. GitVersion 字串 (eks-/gke/aks/k3s/rke2/openshift 等都會直接帶在 GitVersion)
//  2. Namespace 特徵 (openshift-* / tkg-system / vmware-system-* 等)
//  3. Node label 特徵 (eks.amazonaws.com/* / cloud.google.com/gke-* 等)
//  4. kube-system/kubeadm-config ConfigMap 存在 → kubeadm
//  5. 其他 → 通用 "k8s"
//
// nsList 與 nodeList 可為 nil (對應 API 呼叫失敗時)，函式會優雅降級。
func (c *Collector) detectDistribution(ctx context.Context, gitVer string, nsList *corev1.NamespaceList, nodeList *corev1.NodeList) string {
	if d := detectDistributionFromVersion(gitVer); d != "k8s" {
		return d
	}
	if d := detectFromNamespaces(nsList); d != "" {
		return d
	}
	if d := detectFromNodeLabels(nodeList); d != "" {
		return d
	}
	if _, err := c.clientset.CoreV1().ConfigMaps("kube-system").Get(ctx, "kubeadm-config", metav1.GetOptions{}); err == nil {
		return "kubeadm"
	}
	return "k8s"
}

// detectDistributionFromVersion 純 GitVersion 字串檢查。獨立函式方便單元測試。
func detectDistributionFromVersion(gitVer string) string {
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

// detectFromNamespaces 由現有 namespace 名稱判斷發行版本特徵。
func detectFromNamespaces(nsList *corev1.NamespaceList) string {
	if nsList == nil {
		return ""
	}
	names := map[string]bool{}
	hasOpenshiftPrefix := false
	hasVmwarePrefix := false
	for _, n := range nsList.Items {
		names[n.Name] = true
		if strings.HasPrefix(n.Name, "openshift-") {
			hasOpenshiftPrefix = true
		}
		if strings.HasPrefix(n.Name, "vmware-system-") {
			hasVmwarePrefix = true
		}
	}
	switch {
	case names["openshift-apiserver"], names["openshift-kube-apiserver"], names["openshift"], hasOpenshiftPrefix:
		return "openshift"
	case names["tkg-system"], names["tkr-system"], names["vmware-system-tkg"], hasVmwarePrefix:
		return "tkg"
	case names["cattle-system"]:
		// Rancher 通常 = RKE/RKE2
		return "rke2"
	}
	return ""
}

// detectFromNodeLabels 由 Node label 判斷雲端 managed K8s 或特殊 distribution。
func detectFromNodeLabels(nodeList *corev1.NodeList) string {
	if nodeList == nil {
		return ""
	}
	for _, n := range nodeList.Items {
		for k := range n.Labels {
			lk := strings.ToLower(k)
			switch {
			case strings.HasPrefix(lk, "eks.amazonaws.com/"):
				return "eks"
			case strings.HasPrefix(lk, "cloud.google.com/gke"):
				return "gke"
			case strings.HasPrefix(lk, "kubernetes.azure.com/"):
				return "aks"
			case strings.HasPrefix(lk, "node.openshift.io/"):
				return "openshift"
			case strings.HasPrefix(lk, "rke.cattle.io/"):
				return "rke2"
			case strings.HasPrefix(lk, "tanzu.vmware.com/"), strings.HasPrefix(lk, "node.cluster.x-k8s.io/"):
				return "tkg"
			}
		}
	}
	return ""
}
