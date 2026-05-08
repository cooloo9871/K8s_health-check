package collector

import (
	"context"
	"sort"

	"k8s-health-check/internal/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// collectStorage 收集 PV / PVC / StorageClass 三類儲存資源的統計，
// 並把 Pending 的 PVC 額外列在 ProblemPVCs 給報告呈現。
func (c *Collector) collectStorage(ctx context.Context, r *model.Report) error {
	s := model.StorageSummary{}

	pvs, err := c.clientset.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err == nil {
		s.PVs = len(pvs.Items)
		for _, pv := range pvs.Items {
			switch pv.Status.Phase {
			case corev1.VolumeBound:
				s.PVsBound++
			case corev1.VolumeAvailable:
				s.PVsAvailable++
			case corev1.VolumeReleased:
				s.PVsReleased++
			case corev1.VolumeFailed:
				s.PVsFailed++
			}
		}
	} else {
		c.addErr("pv-list", err)
	}

	pvcs, err := c.clientset.CoreV1().PersistentVolumeClaims("").List(ctx, metav1.ListOptions{})
	if err == nil {
		s.PVCs = len(pvcs.Items)
		for _, p := range pvcs.Items {
			switch p.Status.Phase {
			case corev1.ClaimBound:
				s.PVCsBound++
			case corev1.ClaimPending:
				s.PVCsPending++
				class := ""
				if p.Spec.StorageClassName != nil {
					class = *p.Spec.StorageClassName
				}
				cap := ""
				if q, ok := p.Status.Capacity[corev1.ResourceStorage]; ok {
					cap = q.String()
				}
				s.ProblemPVCs = append(s.ProblemPVCs, model.PVCInfo{
					Namespace: p.Namespace,
					Name:      p.Name,
					Status:    string(p.Status.Phase),
					Capacity:  cap,
					Class:     class,
				})
			}
		}
	} else {
		c.addErr("pvc-list", err)
	}

	sc, err := c.clientset.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
	if err == nil {
		names := make([]string, 0, len(sc.Items))
		for _, c := range sc.Items {
			n := c.Name
			if c.Annotations["storageclass.kubernetes.io/is-default-class"] == "true" {
				n += " (default)"
			}
			names = append(names, n)
		}
		sort.Strings(names)
		s.StorageClasses = names
	}
	r.Storage = s
	return nil
}
