package report

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"k8s-health-check/internal/advisor"
	"k8s-health-check/internal/model"
)

// TestSmokeRenderTC 不經過 cluster：手構一份 Report 驗證 PDF 能成功
// 寫出，且 advisor 能產出非空 Conclusion。失敗即代表字型嵌入或渲染流程
// 有壞掉。
func TestSmokeRenderTC(t *testing.T) {
	r := &model.Report{
		GeneratedAt: time.Now(),
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
		PodSummary:  model.PodSummary{Total: 87, Running: 80, Pending: 3, Failed: 4},
		ProblemPods: []model.PodInfo{{Namespace: "app", Name: "api-x", Status: "CrashLoopBackOff", Restarts: 9, Reason: "CrashLoopBackOff"}},
		Workloads: model.WorkloadSummary{
			Deployments: model.WorkloadStats{Total: 20, Ready: 18},
			Unhealthy:   []model.WorkloadIssue{{Kind: "Deployment", Namespace: "app", Name: "api", Desired: 3, Ready: 1, Reason: "PodFailed"}},
		},
		Storage: model.StorageSummary{PVs: 5, PVsBound: 4, PVsFailed: 1, PVCs: 6, PVCsPending: 1, StorageClasses: []string{"default", "fast"}},
		APIHealth: []model.APIHealth{{Endpoint: "/livez", Status: "ok"}, {Endpoint: "/readyz", Status: "fail", Detail: "etcd timeout"}},
		Certs: []model.CertInfo{
			{Path: "/etc/kubernetes/pki/apiserver.crt", Subject: "kube-apiserver", NotAfter: time.Now().Add(20 * 24 * time.Hour), DaysLeft: 20, Status: "WARN"},
		},
		Events: []model.EventInfo{{LastSeen: time.Now(), Reason: "FailedScheduling", Object: "pod/api-x", Namespace: "app", Message: "0/2 nodes available", Count: 5}},
	}
	advisor.Analyze(r, "production")

	if r.Conclusion.OverallStatus == "" {
		t.Fatalf("advisor 未產生 OverallStatus")
	}
	if len(r.Conclusion.Findings) == 0 {
		t.Fatalf("advisor 沒有任何 Finding，預期至少有節點/儲存/控制平面相關問題")
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
	if st.Size() < 4096 {
		t.Fatalf("PDF 大小僅 %d bytes，疑似渲染失敗", st.Size())
	}
	t.Logf("smoke PDF 寫出 %d bytes，環境=%s 整體=%s 發現=%d 建議=%d",
		st.Size(), r.Conclusion.Environment, r.Conclusion.OverallStatus,
		len(r.Conclusion.Findings), len(r.Conclusion.Recommendations))
}
