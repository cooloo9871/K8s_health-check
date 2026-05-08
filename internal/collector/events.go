package collector

import (
	"context"
	"fmt"
	"sort"
	"time"

	"k8s-health-check/internal/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// collectEvents 抓全 cluster 非 Normal 的事件 (Warning / 其他類型)，
// 依 LastSeen 由新到舊排序後最多保留 50 筆。collector 自身的 Pod 事件會被排除。
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
		// 排除針對 collector 自身的事件 (Pod 類型才有可比較的 Namespace + Name)。
		if e.InvolvedObject.Kind == "Pod" && c.isSelf(e.InvolvedObject.Namespace, e.InvolvedObject.Name) {
			continue
		}
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

// lastSeen 取得事件最新時間戳。優先順序為 LastTimestamp、EventTime、CreationTimestamp，
// 因為新版 events.k8s.io 不再填 LastTimestamp。
func lastSeen(e corev1.Event) time.Time {
	if !e.LastTimestamp.IsZero() {
		return e.LastTimestamp.Time
	}
	if !e.EventTime.IsZero() {
		return e.EventTime.Time
	}
	return e.CreationTimestamp.Time
}
