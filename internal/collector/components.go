package collector

import (
	"context"

	"k8s-health-check/internal/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// collectComponents 嘗試讀取 /componentstatuses。此 API 自 1.19 起已 deprecated，
// 部分 managed K8s 也會關閉，因此呼叫失敗時靜默略過、不視為錯誤。
func (c *Collector) collectComponents(ctx context.Context, r *model.Report) error {
	cs, err := c.clientset.CoreV1().ComponentStatuses().List(ctx, metav1.ListOptions{})
	if err != nil {
		// 不少 cluster 已停用此端點，視為正常情況。
		return nil
	}
	out := make([]model.ComponentStatus, 0, len(cs.Items))
	for _, c := range cs.Items {
		health := "Unknown"
		msg := ""
		for _, cond := range c.Conditions {
			if cond.Type == corev1.ComponentHealthy {
				if cond.Status == corev1.ConditionTrue {
					health = "Healthy"
				} else {
					health = "Unhealthy"
				}
				msg = cond.Message
			}
		}
		out = append(out, model.ComponentStatus{
			Name:    c.Name,
			Healthy: health,
			Message: msg,
		})
	}
	r.Components = out
	return nil
}
