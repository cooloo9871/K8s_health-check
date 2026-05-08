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
		PodSummary:  model.PodSummary{Total: 87, Running: 80, Pending: 3, Failed: 4},
		ProblemPods: []model.PodInfo{{Namespace: "app", Name: "api-x", Status: "CrashLoopBackOff", Restarts: 9, Reason: "CrashLoopBackOff"}},
		Workloads: model.WorkloadSummary{
			Deployments: model.WorkloadStats{Total: 20, Ready: 18},
			Unhealthy:   []model.WorkloadIssue{{Kind: "Deployment", Namespace: "app", Name: "api", Desired: 3, Ready: 1, Reason: "PodFailed"}},
		},
		Storage:   model.StorageSummary{PVs: 5, PVsBound: 4, PVsFailed: 1, PVCs: 6, PVCsPending: 1, StorageClasses: []string{"default", "fast"}},
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
	advisor.Analyze(r, "production")

	if r.Conclusion.OverallStatus == "" {
		t.Fatalf("advisor 未產生 OverallStatus")
	}
	if len(r.Conclusion.Findings) == 0 {
		t.Fatalf("advisor 沒有任何 Finding，預期至少有節點/儲存/控制平面相關問題")
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
	t.Logf("smoke PDF %d bytes，環境=%s 整體=%s 發現=%d 建議=%d 節點 agent=%d",
		st.Size(), r.Conclusion.Environment, r.Conclusion.OverallStatus,
		len(r.Conclusion.Findings), len(r.Conclusion.Recommendations), len(r.NodeAgents))
}
