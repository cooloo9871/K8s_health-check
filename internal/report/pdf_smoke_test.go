package report

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"k8s-health-check/internal/advisor"
	"k8s-health-check/internal/model"
)

// TestSmokeRenderTC 不經過 cluster: 手構一份 Report 驗證 PDF 能成功
// 寫出，且 advisor 能產出非空 Conclusion。失敗即代表字型嵌入或渲染流程
// 有壞掉。
func TestSmokeRenderTC(t *testing.T) {
	now := time.Now()
	r := &model.Report{
		GeneratedAt: now,
		Cluster: model.ClusterInfo{
			Name: "lab-east-1", NameSource: "kubeadm",
			Version: "v1.29.4", Platform: "linux/amd64",
			NodeCount: 5, NamespaceCnt: 12, TotalPods: 87,
			Distribution: "k8s",
		},
		Nodes: []model.NodeInfo{
			{Name: "node-1", Roles: "control-plane", Status: "Ready",
				KubeletVersion: "v1.29.4", InternalIP: "10.0.0.1", Age: "30d", PodCount: 12,
				Conditions: []model.NodeCondition{{Type: "Ready", Status: "True"}}},
			{Name: "node-2", Roles: "worker", Status: "NotReady",
				KubeletVersion: "v1.29.4", InternalIP: "10.0.0.2", Age: "30d", PodCount: 0,
				Conditions: []model.NodeCondition{
					{Type: "Ready", Status: "False", Reason: "KubeletNotPosted"},
					{Type: "DiskPressure", Status: "True", Message: "free disk under 10%"},
				}},
		},
		NodeMetrics: []model.NodeMetric{
			{Name: "node-1", CPUPercent: 45, CPUUsed: "1800m", CPUCapacity: "4000m", MemPercent: 62, MemUsed: "5 GiB", MemCapacity: "8 GiB", PodCount: 12, PodCapacity: 110},
			{Name: "node-2", CPUPercent: 88, CPUUsed: "3500m", CPUCapacity: "4000m", MemPercent: 92, MemUsed: "7.4 GiB", MemCapacity: "8 GiB", PodCount: 8, PodCapacity: 110},
		},
		PodSummary: model.PodSummary{Total: 87, Running: 80, Pending: 3, Failed: 4},
		// CrashLoopBackOff Pod 在 K8s 端 Phase 仍為 Running，container 的 waiting
		// reason 才是 CrashLoopBackOff。以此模擬真實情境，驗證圓餅圖的「異常」
		// 切片會把這類 Pod 從 Running 中拆出來。
		// 模擬 collector 端 effectivePhase 後的結果: Status 顯示有效狀態,
		// Phase 保留 K8s 原始 phase (供 dashboard 圓餅依 raw phase 分桶)。
		ProblemPods: []model.PodInfo{
			{Namespace: "app", Name: "api-x", Status: "CrashLoopBackOff", Phase: "Running", Restarts: 9, Reason: "CrashLoopBackOff", Message: "back-off 5m0s restarting failed container=api"},
			{Namespace: "kube-system", Name: "img-pull-fail", Status: "ImagePullBackOff", Phase: "Pending", Reason: "ImagePullBackOff", Message: "Back-off pulling image \"foo:bar\""},
		},
		AllPods: []model.PodOverview{
			{Namespace: "app", Name: "api-x", Status: "CrashLoopBackOff", PodIP: "10.244.1.5", Node: "node-1"},
			{Namespace: "app", Name: "worker-7c4f", Status: "Running", PodIP: "10.244.2.18", Node: "node-2"},
			{
				Namespace: "kube-system", Name: "node-exporter-x", Status: "Running",
				PodIP: "10.244.0.7", Node: "node-1",
				HostPaths: []model.HostPathMount{
					{VolumeName: "rootfs", HostPath: "/", MountPath: "/host", Container: "node-exporter", ReadOnly: true},
					{VolumeName: "proc", HostPath: "/proc", MountPath: "/host/proc", Container: "node-exporter", ReadOnly: true},
				},
			},
			{Namespace: "kube-system", Name: "img-pull-fail", Status: "ImagePullBackOff", PodIP: "", Node: "node-2"},
		},
		Workloads: model.WorkloadSummary{
			Deployments: model.WorkloadStats{Total: 20, Ready: 18},
			Unhealthy:   []model.WorkloadIssue{{Kind: "Deployment", Namespace: "app", Name: "api", Desired: 3, Ready: 1, Reason: "PodFailed"}},
		},
		Storage: model.StorageSummary{
			PVs: 5, PVsBound: 4, PVsFailed: 1, PVCs: 6, PVCsPending: 1,
			StorageClasses: []model.StorageClassInfo{
				{Name: "default", Provisioner: "kubernetes.io/aws-ebs", ReclaimPolicy: "Delete", VolumeBindingMode: "WaitForFirstConsumer", IsDefault: true, PVCount: 3, PVCCount: 2},
				{Name: "fast", Provisioner: "ebs.csi.aws.com", ReclaimPolicy: "Retain", VolumeBindingMode: "Immediate", PVCount: 2, PVCCount: 4},
			},
			PVList: []model.PVDetail{
				{Name: "pv-data-1", Capacity: "20Gi", AccessModes: "RWO", Status: "Bound", Class: "default", Claim: "app/data"},
				{Name: "pv-data-2", Capacity: "100Gi", AccessModes: "RWO", Status: "Released", Class: "fast", Claim: "old/data"},
				{Name: "pv-broken", Capacity: "50Gi", AccessModes: "RWO", Status: "Failed", Class: "fast"},
			},
			PVCList: []model.PVCDetail{
				{Namespace: "app", Name: "data", Status: "Bound", Capacity: "20Gi", Class: "default", Volume: "pv-data-1"},
				{Namespace: "app", Name: "logs", Status: "Pending", Capacity: "10Gi", Class: "default"},
				{Namespace: "kube-system", Name: "etcd", Status: "Bound", Capacity: "40Gi", Class: "fast", Volume: "pv-etcd"},
			},
			ProblemPVCs: []model.PVCInfo{
				{Namespace: "app", Name: "logs", Status: "Pending", Capacity: "10Gi", Class: "default"},
			},
		},
		APIHealth: []model.APIHealth{{Endpoint: "/livez", Status: "ok"}, {Endpoint: "/readyz", Status: "fail", Detail: "etcd timeout"}},
		Certs: []model.CertInfo{
			{Path: "/etc/kubernetes/pki/apiserver.crt", Subject: "kube-apiserver", NotAfter: now.Add(20 * 24 * time.Hour), DaysLeft: 20, Status: "WARN", Source: "k8s-pki", Node: "node-1"},
			{Path: "/etc/kubernetes/pki/etcd/server.crt", Subject: "etcd-server", NotAfter: now.Add(5 * 24 * time.Hour), DaysLeft: 5, Status: "EXPIRING SOON", Source: "etcd", Node: "node-1"},
			{Path: "/var/lib/kubelet/pki/kubelet-client-current.pem", Subject: "system:node:node-2", NotAfter: now.Add(180 * 24 * time.Hour), DaysLeft: 180, Status: "OK", Source: "kubelet", Node: "node-2"},
			{Path: "/etc/kubernetes/admin.conf", Subject: "kubernetes-admin", NotAfter: now.Add(45 * 24 * time.Hour), DaysLeft: 45, Status: "WARN", Source: "kubeconfig", Node: "node-1"},
		},
		NodeAgents: []model.NodeAgentData{
			{
				NodeName: "node-1", CollectedAt: now,
				Disks: []model.DiskInfo{
					{MountPoint: "/", Filesystem: "ext4", Total: 100 * 1024 * 1024 * 1024, Used: 65 * 1024 * 1024 * 1024, Avail: 35 * 1024 * 1024 * 1024, Percent: 65, Status: "OK"},
					{MountPoint: "/var/lib/kubelet", Filesystem: "ext4", Total: 100 * 1024 * 1024 * 1024, Used: 78 * 1024 * 1024 * 1024, Avail: 22 * 1024 * 1024 * 1024, Percent: 78, Status: "OK"},
					{MountPoint: "/var/lib/containerd", Filesystem: "overlay", Total: 100 * 1024 * 1024 * 1024, Used: 92 * 1024 * 1024 * 1024, Avail: 8 * 1024 * 1024 * 1024, Percent: 92, Status: "CRITICAL"},
				},
			},
			{
				NodeName: "node-2", CollectedAt: now,
				Disks: []model.DiskInfo{
					{MountPoint: "/", Filesystem: "xfs", Total: 50 * 1024 * 1024 * 1024, Used: 40 * 1024 * 1024 * 1024, Avail: 10 * 1024 * 1024 * 1024, Percent: 80, Status: "WARN"},
				},
			},
		},
		Events: []model.EventInfo{{LastSeen: now, Reason: "FailedScheduling", Object: "pod/api-x", Namespace: "app", Message: "0/2 nodes available", Count: 5}},
	}
	advisor.Analyze(r)

	if r.Conclusion.OverallStatus == "" {
		t.Fatalf("advisor 未產生 OverallStatus")
	}
	if len(r.Conclusion.Findings) == 0 {
		t.Fatalf("advisor 沒有任何 Finding，預期至少有節點/儲存/Control-plane相關問題")
	}
	// 確認磁碟告警有觸發
	hasDisk := false
	for _, f := range r.Conclusion.Findings {
		if f.Category == "節點磁碟" {
			hasDisk = true
			break
		}
	}
	if !hasDisk {
		t.Fatalf("有 92%% 與 80%% 磁碟卻未觸發節點磁碟告警")
	}

	dir := t.TempDir()
	out := filepath.Join(dir, "smoke.pdf")
	if err := WritePDF(r, out); err != nil {
		t.Fatalf("WritePDF: %v", err)
	}
	st, err := os.Stat(out)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if st.Size() < 8192 {
		t.Fatalf("PDF 大小僅 %d bytes，疑似渲染失敗", st.Size())
	}
	t.Logf("smoke PDF %d bytes，叢集=%s 整體=%s 發現=%d 建議=%d 節點 agent=%d",
		st.Size(), r.Cluster.Name, r.Conclusion.OverallStatus,
		len(r.Conclusion.Findings), len(r.Conclusion.Recommendations), len(r.NodeAgents))
}

// TestIsSelfFiltersHealthcheckPods 確認 isSelf 會把 namespace 內所有
// k8s-healthcheck-* Pod (agent + aggregator) 都濾掉。
// 這是 collector 套件的功能，但放在 report 套件的測試裡會跨套件 import 麻煩，
// 因此實際測試在 collector 套件內 (見 collector/agents_test.go 若有)，這裡
// 僅以註解保留設計意圖。

// TestStripHostFromCertPath 驗證 PDF 渲染端的最後一道防線: 即使 r.Certs
// 中 c.Path 仍帶有 /host 前綴, 顯示前一定會被切掉。
func TestStripHostFromCertPath(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"/host/etc/kubernetes/pki/apiserver.crt", "/etc/kubernetes/pki/apiserver.crt"},
		{"/host/var/lib/kubelet/pki/kubelet.crt", "/var/lib/kubelet/pki/kubelet.crt"},
		{"/host", "/"},
		{"/host/", "/"},
		{"/etc/kubernetes/pki/x.crt", "/etc/kubernetes/pki/x.crt"}, // 已乾淨, 不動
		{"/hosted/foo", "/hosted/foo"},                              // 字首像但非邊界, 不動
		{"", ""},
	}
	for _, tc := range cases {
		if got := stripHostFromCertPath(tc.in); got != tc.want {
			t.Errorf("stripHostFromCertPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestRenderEmptyCerts 模擬 cluster 完全沒有憑證資料的情境，
// 用來防止 charts.go 的「無憑證」fallback 用到未註冊的字型 style (例如 Italic)。
func TestRenderEmptyCerts(t *testing.T) {
	r := &model.Report{
		GeneratedAt: time.Now(),
		Cluster: model.ClusterInfo{
			Name: "test-no-certs", Version: "v1.29",
			NodeCount: 1, Distribution: "k8s",
		},
		Nodes: []model.NodeInfo{
			{Name: "n", Status: "Ready", Conditions: []model.NodeCondition{{Type: "Ready", Status: "True"}}},
		},
		PodSummary: model.PodSummary{Total: 1, Running: 1},
	}
	advisor.Analyze(r)
	dir := t.TempDir()
	if err := WritePDF(r, filepath.Join(dir, "no-certs.pdf")); err != nil {
		t.Fatalf("WritePDF empty-certs: %v", err)
	}
}
