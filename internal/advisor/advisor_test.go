package advisor

import (
	"fmt"
	"strings"
	"testing"

	"k8s-health-check/internal/model"
)

// TestCheckControlPlane_HealthyDoesNotTriggerFinding 驗證 collector 端
// API health 端點回傳 "Healthy" 時，advisor 不會誤判為Control-plane異常。
// (歷史 bug: 舊版只接受 "ok" / 含 "200"，導致 healthy cluster 一直被報嚴重。)
func TestCheckControlPlane_HealthyDoesNotTriggerFinding(t *testing.T) {
	r := &model.Report{
		APIHealth: []model.APIHealth{
			{Endpoint: "/healthz", Status: "Healthy"},
			{Endpoint: "/livez", Status: "Healthy"},
			{Endpoint: "/readyz", Status: "Healthy"},
		},
	}
	c := &model.Conclusion{}
	checkControlPlane(r, c)
	for _, f := range c.Findings {
		if strings.Contains(f.Title, "Control-plane健康端點") {
			t.Fatalf("Healthy 端點被誤判為異常: %s — %s", f.Title, f.Detail)
		}
	}
}

// TestCheckControlPlane_DegradedTriggersFinding 確認真正異常 (Degraded /
// Failed) 仍會被偵測為嚴重發現。
func TestCheckControlPlane_DegradedTriggersFinding(t *testing.T) {
	r := &model.Report{
		APIHealth: []model.APIHealth{
			{Endpoint: "/healthz", Status: "Healthy"},
			{Endpoint: "/livez", Status: "Degraded", Detail: "etcd timeout"},
			{Endpoint: "/readyz", Status: "Failed", Detail: "connection refused"},
		},
	}
	c := &model.Conclusion{}
	checkControlPlane(r, c)
	hit := 0
	for _, f := range c.Findings {
		if strings.Contains(f.Title, "Control-plane健康端點有 2 項異常") {
			hit++
		}
	}
	if hit != 1 {
		t.Fatalf("預期 1 筆「2 項異常」finding, 實際 finding=%v", c.Findings)
	}
}

// TestPodAndNodeFindingsCarryAffectedNames 驗證異常 Pod / Node finding 的
// Detail 會包含實際的 ns/name 清單，不只是「X 個」的數量。
func TestPodAndNodeFindingsCarryAffectedNames(t *testing.T) {
	r := &model.Report{
		Nodes: []model.NodeInfo{
			{Name: "node-a", Status: "Ready", Conditions: []model.NodeCondition{{Type: "Ready", Status: "True"}}},
			{Name: "node-b", Status: "NotReady", Conditions: []model.NodeCondition{{Type: "Ready", Status: "False"}}},
			{Name: "node-c", Status: "Ready", Conditions: []model.NodeCondition{
				{Type: "Ready", Status: "True"},
				{Type: "DiskPressure", Status: "True"},
			}},
		},
		PodSummary: model.PodSummary{Total: 4, Running: 2, Failed: 1, Pending: 1},
		ProblemPods: []model.PodInfo{
			{Namespace: "team-a", Name: "api-x", Status: "Running", Restarts: 9, Reason: "CrashLoopBackOff", Message: "back-off"},
			{Namespace: "team-b", Name: "worker", Status: "Failed", Reason: "Error"},
			{Namespace: "kube-system", Name: "stuck", Status: "Pending", Reason: "Unschedulable"},
			{Namespace: "team-c", Name: "leaky", Status: "Running", Restarts: 3, Reason: "Error", Message: "OOMKilled"},
		},
	}
	Analyze(r)

	// 期望這些字串都出現在 conclusion 的某個 finding Detail 中
	mustHave := []string{
		"node-b",                  // not-ready 節點
		"node-c",                  // pressure 節點
		"team-a/api-x",            // CrashLoop Pod
		"team-b/worker",           // Failed Pod
		"kube-system/stuck",       // Pending Pod
		"team-c/leaky",            // OOMKilled Pod
	}
	all := strings.Builder{}
	for _, f := range r.Conclusion.Findings {
		all.WriteString(f.Detail)
		all.WriteString(" | ")
	}
	dump := all.String()
	for _, want := range mustHave {
		if !strings.Contains(dump, want) {
			t.Errorf("Conclusion.Findings Detail 缺少 %q\n--- 實際內容 ---\n%s", want, dump)
		}
	}
}

// TestSystemVsApplicationSeverity 驗證系統元件問題標 Critical, 應用 Pod 問題
// 標 Warning。覆蓋 user 的需求: "整體狀態 只有是 k8s 相關的元件出問題才顯示嚴重 ...
// 跑在 k8s 上面的應用 workload 出現問題就顯示警告等級就好"。
func TestSystemVsApplicationSeverity(t *testing.T) {
	r := &model.Report{
		ProblemPods: []model.PodInfo{
			// 系統元件 (kube-system) 的 CrashLoop → Critical
			{Namespace: "kube-system", Name: "etcd-master", Status: "Running", Restarts: 3, Reason: "CrashLoopBackOff"},
			// 應用 Pod (其他 ns) 的 CrashLoop → Warning
			{Namespace: "team-a", Name: "api-x", Status: "Running", Restarts: 3, Reason: "CrashLoopBackOff"},
			// 應用 Pod 的 OOMKilled → Warning
			{Namespace: "team-b", Name: "leaky", Status: "Running", Reason: "Error", Message: "OOMKilled"},
		},
	}
	Analyze(r)

	wantSeverity := map[string]string{
		"系統元件 CrashLoopBackOff":   SeverityCritical,
		"應用 Pod CrashLoopBackOff": SeverityWarning,
		"應用 Pod OOMKilled":        SeverityWarning,
	}
	for keyword, wantSev := range wantSeverity {
		found := false
		for _, f := range r.Conclusion.Findings {
			if strings.Contains(f.Title, keyword) {
				found = true
				if f.Severity != wantSev {
					t.Errorf("%q finding 嚴重度應為 %q, 實際 %q", keyword, wantSev, f.Severity)
				}
			}
		}
		if !found {
			t.Errorf("找不到含關鍵字 %q 的 finding", keyword)
		}
	}
	// 額外檢查: kube-system Pod 不能被歸到應用 Critical 也不該到 Warning;
	// 應該 strictly 在系統 Critical 中。
	for _, f := range r.Conclusion.Findings {
		if strings.Contains(f.Title, "應用 Pod CrashLoopBackOff") {
			if strings.Contains(f.Detail, "kube-system/etcd-master") {
				t.Errorf("系統 Pod 不該出現在「應用 Pod CrashLoopBackOff」finding 中: %s", f.Detail)
			}
		}
	}
}

// TestIsSystemPod 直接驗證 isSystemPod 判定邏輯。
func TestIsSystemPod(t *testing.T) {
	cases := []struct {
		ns, name string
		want     bool
	}{
		{"kube-system", "etcd-foo", true},
		{"kube-system", "anything", true},     // ns 即可
		{"openshift-apiserver", "x", true},     // openshift- prefix
		{"vmware-system-csi", "x", true},        // vmware-system- prefix
		{"calico-system", "x", true},
		{"default", "kube-apiserver-foo", true}, // 名字含關鍵字
		{"default", "calico-node-abc", true},
		{"team-a", "my-app", false},
		{"production", "checkout-svc", false},
	}
	for _, tc := range cases {
		got := isSystemPod(model.PodInfo{Namespace: tc.ns, Name: tc.name})
		if got != tc.want {
			t.Errorf("isSystemPod(%q,%q) = %v, want %v", tc.ns, tc.name, got, tc.want)
		}
	}
}

// TestMissingAgentNodesFinding 驗證: cluster 有 N 個節點但只有 N-1 個有 agent
// 回報時, advisor 會補一個「監控」類別的 Warning finding, 列出漏報節點。常見
// 情境是節點 NotReady 導致 DaemonSet agent Pod 起不來, 報告仍應產出但要明確
// 標示哪些節點資料缺漏。
func TestMissingAgentNodesFinding(t *testing.T) {
	r := &model.Report{
		Cluster: model.ClusterInfo{NodeCount: 3},
		Nodes: []model.NodeInfo{
			{Name: "node-a", Status: "Ready", Conditions: []model.NodeCondition{{Type: "Ready", Status: "True"}}},
			{Name: "node-b", Status: "NotReady", Conditions: []model.NodeCondition{{Type: "Ready", Status: "False"}}},
			{Name: "node-c", Status: "Ready", Conditions: []model.NodeCondition{{Type: "Ready", Status: "True"}}},
		},
		// 只有 node-a / node-c 回報, node-b 因 NotReady 沒回報
		NodeAgents: []model.NodeAgentData{
			{NodeName: "node-a"}, {NodeName: "node-c"},
		},
	}
	Analyze(r)

	hit := false
	for _, f := range r.Conclusion.Findings {
		if f.Category == "監控" && strings.Contains(f.Title, "agent 回報") {
			hit = true
			if !strings.Contains(f.Detail, "node-b") {
				t.Errorf("漏報節點 finding 應包含 node-b 名稱, 實際: %s", f.Detail)
			}
			if f.Severity != SeverityWarning {
				t.Errorf("漏報節點 finding 嚴重度應為 Warning, 實際 %s", f.Severity)
			}
		}
	}
	if !hit {
		t.Fatalf("找不到「N 個節點沒有 agent 回報」的 finding\n--- findings ---\n%+v", r.Conclusion.Findings)
	}
}

// TestNoMissingAgentWhenAllReport 驗證每個節點都有 agent 回報時, 不會誤觸發
// 「漏報節點」finding。
func TestNoMissingAgentWhenAllReport(t *testing.T) {
	r := &model.Report{
		Cluster: model.ClusterInfo{NodeCount: 2},
		Nodes: []model.NodeInfo{
			{Name: "node-a", Status: "Ready", Conditions: []model.NodeCondition{{Type: "Ready", Status: "True"}}},
			{Name: "node-b", Status: "Ready", Conditions: []model.NodeCondition{{Type: "Ready", Status: "True"}}},
		},
		NodeAgents: []model.NodeAgentData{
			{NodeName: "node-a"}, {NodeName: "node-b"},
		},
	}
	Analyze(r)
	for _, f := range r.Conclusion.Findings {
		if f.Category == "監控" && strings.Contains(f.Title, "agent 回報") {
			t.Errorf("不該有漏報節點 finding, 但出現: %+v", f)
		}
	}
}

// TestJoinNamesTruncation 驗證 joinNames 超過 max 時會省略並標出總數。
func TestJoinNamesTruncation(t *testing.T) {
	long := make([]string, 20)
	for i := range long {
		long[i] = fmt.Sprintf("ns/pod-%d", i)
	}
	out := joinNames(long, 5)
	if !strings.HasPrefix(out, "ns/pod-0, ns/pod-1, ns/pod-2, ns/pod-3, ns/pod-4") {
		t.Errorf("前 5 筆未正確輸出: %q", out)
	}
	if !strings.Contains(out, "等共 20 個") {
		t.Errorf("總數標註不存在: %q", out)
	}
}
