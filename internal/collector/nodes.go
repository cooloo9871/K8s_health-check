package collector

import (
	"context"
	"sort"
	"strings"
	"time"

	"k8s-health-check/internal/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// collectNodes 收集所有節點的基本資訊與條件，並先預先計算每個節點上的
// Pod 數量塞進 NodeInfo.PodCount，避免後續每張表都重抓一次 Pod 清單。
func (c *Collector) collectNodes(ctx context.Context, r *model.Report) error {
	nodes, err := c.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}

	// 預先依節點分桶 Pod 數量，且要扣除 collector 自身。
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

	out := make([]model.NodeInfo, 0, len(nodes.Items))
	for _, n := range nodes.Items {
		ni := model.NodeInfo{
			Name:           n.Name,
			Roles:          nodeRoles(&n),
			Status:         nodeStatus(&n),
			KubeletVersion: n.Status.NodeInfo.KubeletVersion,
			OSImage:        n.Status.NodeInfo.OSImage,
			Kernel:         n.Status.NodeInfo.KernelVersion,
			Runtime:        n.Status.NodeInfo.ContainerRuntimeVersion,
			Architecture:   n.Status.NodeInfo.Architecture,
			InternalIP:     internalIP(&n),
			Age:            humanAge(n.CreationTimestamp.Time),
			Taints:         len(n.Spec.Taints),
			PodCount:       podsByNode[n.Name],
		}
		for _, cond := range n.Status.Conditions {
			ni.Conditions = append(ni.Conditions, model.NodeCondition{
				Type:    string(cond.Type),
				Status:  string(cond.Status),
				Reason:  cond.Reason,
				Message: cond.Message,
			})
		}
		out = append(out, ni)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	r.Nodes = out
	return nil
}

// nodeRoles 從 node-role.kubernetes.io/<role> 標籤萃取所有角色名稱，
// 沒有任何 role 標籤時回 "<none>"。
func nodeRoles(n *corev1.Node) string {
	roles := []string{}
	for k := range n.Labels {
		const prefix = "node-role.kubernetes.io/"
		if strings.HasPrefix(k, prefix) {
			role := strings.TrimPrefix(k, prefix)
			if role == "" {
				continue
			}
			roles = append(roles, role)
		}
	}
	if len(roles) == 0 {
		return "<none>"
	}
	sort.Strings(roles)
	return strings.Join(roles, ",")
}

// nodeStatus 把 NodeReady 條件轉成 kubectl get nodes 風格的字串，
// 並會疊加上 SchedulingDisabled (對應 cordon 狀態)。
func nodeStatus(n *corev1.Node) string {
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			if c.Status == corev1.ConditionTrue {
				if n.Spec.Unschedulable {
					return "Ready,SchedulingDisabled"
				}
				return "Ready"
			}
			return "NotReady"
		}
	}
	return "Unknown"
}

// internalIP 回傳節點第一個 InternalIP；若沒有則回空字串。
func internalIP(n *corev1.Node) string {
	for _, a := range n.Status.Addresses {
		if a.Type == corev1.NodeInternalIP {
			return a.Address
		}
	}
	return ""
}

// humanAge 以人類可讀格式呈現 Pod / 節點的存活時間，仿造 kubectl 風格。
func humanAge(t time.Time) string {
	d := time.Since(t)
	switch {
	case d > 24*time.Hour:
		return formatDays(d)
	case d > time.Hour:
		return d.Truncate(time.Minute).String()
	default:
		return d.Truncate(time.Second).String()
	}
}

func formatDays(d time.Duration) string {
	days := int(d.Hours() / 24)
	if days >= 365 {
		years := days / 365
		rem := days % 365
		return formatPair(years, "y", rem, "d")
	}
	hours := int(d.Hours()) - days*24
	return formatPair(days, "d", hours, "h")
}

func formatPair(a int, au string, b int, bu string) string {
	if b == 0 {
		return itoa(a) + au
	}
	return itoa(a) + au + itoa(b) + bu
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	digits := []byte{}
	for i > 0 {
		digits = append([]byte{byte('0' + i%10)}, digits...)
		i /= 10
	}
	if neg {
		digits = append([]byte{'-'}, digits...)
	}
	return string(digits)
}
