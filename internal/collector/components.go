package collector

import (
	"context"

	"github.com/brobridge/k8s-health-check/internal/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// collectComponents pulls /componentstatuses.  This API is deprecated since
// 1.19 and disabled on some managed distributions; if the call fails we
// simply skip the section without erroring out.
func (c *Collector) collectComponents(ctx context.Context, r *model.Report) error {
	cs, err := c.clientset.CoreV1().ComponentStatuses().List(ctx, metav1.ListOptions{})
	if err != nil {
		// not fatal — many clusters disable this endpoint
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
