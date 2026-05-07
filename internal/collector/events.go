package collector

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/brobridge/k8s-health-check/internal/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (c *Collector) collectEvents(ctx context.Context, r *model.Report) error {
	evs, err := c.clientset.CoreV1().Events("").List(ctx, metav1.ListOptions{
		FieldSelector: "type!=Normal",
		Limit:         500,
	})
	if err != nil {
		return err
	}
	out := make([]model.EventInfo, 0, len(evs.Items))
	for _, e := range evs.Items {
		out = append(out, model.EventInfo{
			LastSeen:  lastSeen(e),
			Type:      e.Type,
			Reason:    e.Reason,
			Object:    fmt.Sprintf("%s/%s", e.InvolvedObject.Kind, e.InvolvedObject.Name),
			Namespace: e.Namespace,
			Message:   truncate(e.Message, 120),
			Count:     e.Count,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen.After(out[j].LastSeen) })
	if len(out) > 50 {
		out = out[:50]
	}
	r.Events = out
	return nil
}

func lastSeen(e corev1.Event) time.Time {
	if !e.LastTimestamp.IsZero() {
		return e.LastTimestamp.Time
	}
	if !e.EventTime.IsZero() {
		return e.EventTime.Time
	}
	return e.CreationTimestamp.Time
}
