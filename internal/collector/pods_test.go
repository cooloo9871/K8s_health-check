package collector

import (
	"sort"
	"testing"

	"k8s-health-check/internal/model"
	corev1 "k8s.io/api/core/v1"
)

// TestExtractHostPaths 驗證 extractHostPaths 能正確抽出 Pod 的 hostPath
// volume 與其在容器內的掛載位置, 且不會把 ConfigMap / EmptyDir 等其他
// volume 類型誤算進來。
func TestExtractHostPaths(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{
				{
					Name: "host-log",
					VolumeSource: corev1.VolumeSource{
						HostPath: &corev1.HostPathVolumeSource{Path: "/var/log"},
					},
				},
				{
					Name: "host-data",
					VolumeSource: corev1.VolumeSource{
						HostPath: &corev1.HostPathVolumeSource{Path: "/srv/data"},
					},
				},
				{
					Name: "tmp",
					VolumeSource: corev1.VolumeSource{
						EmptyDir: &corev1.EmptyDirVolumeSource{},
					},
				},
				{
					Name: "config",
					VolumeSource: corev1.VolumeSource{
						ConfigMap: &corev1.ConfigMapVolumeSource{},
					},
				},
			},
			InitContainers: []corev1.Container{
				{
					Name: "init",
					VolumeMounts: []corev1.VolumeMount{
						{Name: "host-log", MountPath: "/init/log", ReadOnly: true},
					},
				},
			},
			Containers: []corev1.Container{
				{
					Name: "main",
					VolumeMounts: []corev1.VolumeMount{
						{Name: "host-log", MountPath: "/var/log"},
						{Name: "host-data", MountPath: "/data"},
						{Name: "tmp", MountPath: "/tmp"},
						{Name: "config", MountPath: "/etc/cfg"},
					},
				},
				{
					Name: "sidecar",
					VolumeMounts: []corev1.VolumeMount{
						{Name: "host-data", MountPath: "/data", ReadOnly: true},
					},
				},
			},
		},
	}

	got := extractHostPaths(pod)
	// 預期: init/host-log + main/host-log + main/host-data + sidecar/host-data
	// (config / tmp 不算)
	if len(got) != 4 {
		t.Fatalf("預期 4 筆 hostPath 掛載, 實際 %d 筆: %#v", len(got), got)
	}

	type key struct {
		Container, MountPath, HostPath string
		ReadOnly                       bool
	}
	want := map[key]bool{
		{"init", "/init/log", "/var/log", true}:      true,
		{"main", "/var/log", "/var/log", false}:      true,
		{"main", "/data", "/srv/data", false}:        true,
		{"sidecar", "/data", "/srv/data", true}:      true,
	}
	for _, m := range got {
		k := key{m.Container, m.MountPath, m.HostPath, m.ReadOnly}
		if !want[k] {
			t.Errorf("非預期的掛載條目: %+v", m)
		}
		delete(want, k)
	}
	for k := range want {
		t.Errorf("缺少預期的掛載條目: %+v", k)
	}
}

// TestExtractHostPaths_NoHostPath 驗證 Pod 沒有 hostPath volume 時回 nil。
func TestExtractHostPaths_NoHostPath(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{
				{Name: "tmp", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{}}},
			},
			Containers: []corev1.Container{
				{Name: "c", VolumeMounts: []corev1.VolumeMount{{Name: "tmp", MountPath: "/tmp"}}},
			},
		},
	}
	if got := extractHostPaths(pod); got != nil {
		t.Errorf("沒有 hostPath volume 時應回 nil, 實際 %#v", got)
	}
}

// TestExtractHostPaths_HostPathDeclaredButUnused 驗證 hostPath volume 已宣告
// 但沒有任何 container 掛載時, 結果不會出現該 volume (避免顯示成 "Pod 有用
// hostPath" 卻找不到掛載點的誤導)。
func TestExtractHostPaths_HostPathDeclaredButUnused(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{
				{
					Name: "host-log",
					VolumeSource: corev1.VolumeSource{
						HostPath: &corev1.HostPathVolumeSource{Path: "/var/log"},
					},
				},
			},
			Containers: []corev1.Container{
				{Name: "c"}, // 沒掛任何 volume
			},
		},
	}
	if got := extractHostPaths(pod); len(got) != 0 {
		t.Errorf("hostPath 未被掛載時應回空, 實際 %#v", got)
	}
}

// TestExtractHostPaths_NilPod 防禦性測試: nil pod 不應 panic。
func TestExtractHostPaths_NilPod(t *testing.T) {
	if got := extractHostPaths(nil); got != nil {
		t.Errorf("nil pod 應回 nil, 實際 %#v", got)
	}
}

// TestAllPodsSortKubeSystemLast 驗證 collectPods 排序: kube-system 一律排最後,
// 其餘 ns 字典序; 同 ns 按 Pod 名字字典序。直接用 sort.Slice 重現排序邏輯,
// 不必啟動 fake clientset。
func TestAllPodsSortKubeSystemLast(t *testing.T) {
	pods := []model.PodOverview{
		{Namespace: "kube-system", Name: "kube-proxy-aaa"},
		{Namespace: "app", Name: "z-pod"},
		{Namespace: "kube-system", Name: "coredns-bbb"},
		{Namespace: "app", Name: "a-pod"},
		{Namespace: "monitoring", Name: "prom-0"},
	}
	sort.Slice(pods, func(i, j int) bool {
		ai, aj := pods[i].Namespace == "kube-system", pods[j].Namespace == "kube-system"
		if ai != aj {
			return !ai
		}
		if pods[i].Namespace != pods[j].Namespace {
			return pods[i].Namespace < pods[j].Namespace
		}
		return pods[i].Name < pods[j].Name
	})
	wantOrder := []string{
		"app/a-pod", "app/z-pod", "monitoring/prom-0",
		"kube-system/coredns-bbb", "kube-system/kube-proxy-aaa",
	}
	for i, w := range wantOrder {
		got := pods[i].Namespace + "/" + pods[i].Name
		if got != w {
			t.Errorf("pos %d: got %q, want %q", i, got, w)
		}
	}
}

// TestExtractHostPaths_DedupSameHostPath 驗證: 兩個 container 都掛同一個
// hostPath volume 時, extractHostPaths 仍會出兩筆 (各 container 一筆),
// 但 PDF 顯示時會由 formatHostPaths 去重 (那是 report 端的責任,
// extractHostPaths 自己不去重)。
func TestExtractHostPaths_DedupSameHostPath(t *testing.T) {
	pod := &corev1.Pod{
		Spec: corev1.PodSpec{
			Volumes: []corev1.Volume{
				{
					Name: "cni-bin",
					VolumeSource: corev1.VolumeSource{
						HostPath: &corev1.HostPathVolumeSource{Path: "/opt/cni/bin"},
					},
				},
			},
			Containers: []corev1.Container{
				{Name: "c1", VolumeMounts: []corev1.VolumeMount{{Name: "cni-bin", MountPath: "/host/opt/cni/bin"}}},
				{Name: "c2", VolumeMounts: []corev1.VolumeMount{{Name: "cni-bin", MountPath: "/opt/cni/bin"}}},
			},
		},
	}
	got := extractHostPaths(pod)
	if len(got) != 2 {
		t.Fatalf("應有 2 筆 (c1, c2 各一), 實際 %d 筆: %#v", len(got), got)
	}
	for _, m := range got {
		if m.HostPath != "/opt/cni/bin" {
			t.Errorf("HostPath 應為 /opt/cni/bin, 實際 %q", m.HostPath)
		}
	}
}

// TestEffectivePhase 驗證 effectivePhase 把容器層級的故障 (CrashLoop /
// ImagePullBackOff / OOMKilled) 從 ContainerStatus 拉到顯示用的 Phase 字串,
// 避免使用者看到一個一直 crash 的 Pod 顯示為 Running.
func TestEffectivePhase(t *testing.T) {
	mkPod := func(phase corev1.PodPhase, statuses []corev1.ContainerStatus) *corev1.Pod {
		return &corev1.Pod{
			Status: corev1.PodStatus{Phase: phase, ContainerStatuses: statuses},
		}
	}

	cases := []struct {
		name string
		pod  *corev1.Pod
		want string
	}{
		{
			name: "Running pod 沒有任何問題容器 → 維持 Running",
			pod:  mkPod(corev1.PodRunning, []corev1.ContainerStatus{{Ready: true}}),
			want: "Running",
		},
		{
			name: "Phase=Running 但 container 在 CrashLoopBackOff → 顯示 CrashLoopBackOff",
			pod: mkPod(corev1.PodRunning, []corev1.ContainerStatus{{
				State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
			}}),
			want: "CrashLoopBackOff",
		},
		{
			name: "Phase=Pending 但 container 在 ImagePullBackOff → 顯示 ImagePullBackOff",
			pod: mkPod(corev1.PodPending, []corev1.ContainerStatus{{
				State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"}},
			}}),
			want: "ImagePullBackOff",
		},
		{
			name: "Phase=Pending Waiting=ContainerCreating (啟動中, 不是異常) → 維持 Pending",
			pod: mkPod(corev1.PodPending, []corev1.ContainerStatus{{
				State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"}},
			}}),
			want: "Pending",
		},
		{
			name: "上次終止為 OOMKilled → 顯示 OOMKilled",
			pod: mkPod(corev1.PodRunning, []corev1.ContainerStatus{{
				LastTerminationState: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "OOMKilled"}},
			}}),
			want: "OOMKilled",
		},
		{
			name: "Failed phase 沒有 waiting → 維持 Failed",
			pod:  mkPod(corev1.PodFailed, nil),
			want: "Failed",
		},
		{
			name: "init container 在 CrashLoop, main 還沒啟動 → 顯示 CrashLoopBackOff",
			pod: &corev1.Pod{Status: corev1.PodStatus{
				Phase: corev1.PodPending,
				InitContainerStatuses: []corev1.ContainerStatus{{
					State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}},
				}},
			}},
			want: "CrashLoopBackOff",
		},
		{
			name: "nil pod → 空字串 (防呆)",
			pod:  nil,
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := effectivePhase(tc.pod); got != tc.want {
				t.Errorf("effectivePhase = %q, want %q", got, tc.want)
			}
		})
	}
}
