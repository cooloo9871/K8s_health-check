package collector

import (
	"context"
	"sort"
	"strings"

	"k8s-health-check/internal/model"
	corev1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// collectStorage 收集 PV / PVC / StorageClass 的完整詳情，
// 用於在報告中呈現每個 PV/PVC 對應的 StorageClass。
func (c *Collector) collectStorage(ctx context.Context, r *model.Report) error {
	s := model.StorageSummary{}

	// 用以累計每個 StorageClass 對應的 PV / PVC 數量。
	pvCountByClass := map[string]int{}
	pvcCountByClass := map[string]int{}

	pvs, err := c.clientset.CoreV1().PersistentVolumes().List(ctx, metav1.ListOptions{})
	if err == nil {
		s.PVs = len(pvs.Items)
		s.PVList = make([]model.PVDetail, 0, len(pvs.Items))
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
			s.PVList = append(s.PVList, pvToDetail(pv))
			pvCountByClass[pv.Spec.StorageClassName]++
		}
	} else {
		c.addErr("pv-list", err)
	}

	pvcs, err := c.clientset.CoreV1().PersistentVolumeClaims("").List(ctx, metav1.ListOptions{})
	if err == nil {
		s.PVCs = len(pvcs.Items)
		s.PVCList = make([]model.PVCDetail, 0, len(pvcs.Items))
		for _, p := range pvcs.Items {
			class := ""
			if p.Spec.StorageClassName != nil {
				class = *p.Spec.StorageClassName
			}
			cap := ""
			if q, ok := p.Status.Capacity[corev1.ResourceStorage]; ok {
				cap = q.String()
			} else if q, ok := p.Spec.Resources.Requests[corev1.ResourceStorage]; ok {
				cap = q.String()
			}
			s.PVCList = append(s.PVCList, model.PVCDetail{
				Namespace: p.Namespace,
				Name:      p.Name,
				Status:    string(p.Status.Phase),
				Capacity:  cap,
				Class:     class,
				Volume:    p.Spec.VolumeName,
			})
			pvcCountByClass[class]++

			switch p.Status.Phase {
			case corev1.ClaimBound:
				s.PVCsBound++
			case corev1.ClaimPending:
				s.PVCsPending++
				s.ProblemPVCs = append(s.ProblemPVCs, model.PVCInfo{
					Namespace: p.Namespace,
					Name:      p.Name,
					Status:    string(p.Status.Phase),
					Capacity:  cap,
					Class:     class,
				})
			}
		}
		// 報告閱讀順序: namespace → name
		sort.Slice(s.PVCList, func(i, j int) bool {
			if s.PVCList[i].Namespace != s.PVCList[j].Namespace {
				return s.PVCList[i].Namespace < s.PVCList[j].Namespace
			}
			return s.PVCList[i].Name < s.PVCList[j].Name
		})
	} else {
		c.addErr("pvc-list", err)
	}

	sc, err := c.clientset.StorageV1().StorageClasses().List(ctx, metav1.ListOptions{})
	if err == nil {
		out := make([]model.StorageClassInfo, 0, len(sc.Items))
		for _, c := range sc.Items {
			out = append(out, scToInfo(c, pvCountByClass, pvcCountByClass))
		}
		// default 排最前面，其餘按名稱排序
		sort.Slice(out, func(i, j int) bool {
			if out[i].IsDefault != out[j].IsDefault {
				return out[i].IsDefault
			}
			return out[i].Name < out[j].Name
		})
		s.StorageClasses = out
	} else {
		c.addErr("storageclass-list", err)
	}

	// PV 排序: bound 排前 / 失敗排最後 / 同階段內名稱排序
	sort.Slice(s.PVList, func(i, j int) bool {
		ord := func(st string) int {
			switch st {
			case string(corev1.VolumeFailed):
				return 4
			case string(corev1.VolumeReleased):
				return 3
			case string(corev1.VolumeAvailable):
				return 2
			case string(corev1.VolumeBound):
				return 1
			default:
				return 5
			}
		}
		oi, oj := ord(s.PVList[i].Status), ord(s.PVList[j].Status)
		if oi != oj {
			return oi < oj
		}
		return s.PVList[i].Name < s.PVList[j].Name
	})

	r.Storage = s
	return nil
}

// pvToDetail 把 corev1.PersistentVolume 攤平成報告用的 PVDetail。
func pvToDetail(pv corev1.PersistentVolume) model.PVDetail {
	cap := ""
	if q, ok := pv.Spec.Capacity[corev1.ResourceStorage]; ok {
		cap = q.String()
	}
	modes := make([]string, 0, len(pv.Spec.AccessModes))
	for _, m := range pv.Spec.AccessModes {
		modes = append(modes, accessModeShort(m))
	}
	claim := ""
	if pv.Spec.ClaimRef != nil {
		claim = pv.Spec.ClaimRef.Namespace + "/" + pv.Spec.ClaimRef.Name
	}
	return model.PVDetail{
		Name:        pv.Name,
		Capacity:    cap,
		AccessModes: strings.Join(modes, ","),
		Status:      string(pv.Status.Phase),
		Class:       pv.Spec.StorageClassName,
		Claim:       claim,
	}
}

// scToInfo 把 storagev1.StorageClass 攤平成 StorageClassInfo，並補上對應的
// PV / PVC 數量 (從 caller 傳入的 map 查表)。
func scToInfo(c storagev1.StorageClass, pvByClass, pvcByClass map[string]int) model.StorageClassInfo {
	info := model.StorageClassInfo{
		Name:        c.Name,
		Provisioner: c.Provisioner,
		IsDefault: c.Annotations["storageclass.kubernetes.io/is-default-class"] == "true" ||
			c.Annotations["storageclass.beta.kubernetes.io/is-default-class"] == "true",
		PVCount:  pvByClass[c.Name],
		PVCCount: pvcByClass[c.Name],
	}
	if c.ReclaimPolicy != nil {
		info.ReclaimPolicy = string(*c.ReclaimPolicy)
	}
	if c.VolumeBindingMode != nil {
		info.VolumeBindingMode = string(*c.VolumeBindingMode)
	}
	return info
}

// accessModeShort 把 corev1.PersistentVolumeAccessMode 縮寫成 RWO/ROX/RWX/RWOP。
func accessModeShort(m corev1.PersistentVolumeAccessMode) string {
	switch m {
	case corev1.ReadWriteOnce:
		return "RWO"
	case corev1.ReadOnlyMany:
		return "ROX"
	case corev1.ReadWriteMany:
		return "RWX"
	case corev1.ReadWriteOncePod:
		return "RWOP"
	default:
		return string(m)
	}
}
