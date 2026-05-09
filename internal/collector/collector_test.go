package collector

import "testing"

// TestIsSelfFiltersAllHealthcheckPods 驗證 isSelf 會把 healthcheckNamespace
// 內所有 k8s-healthcheck-* 系統 Pod (aggregator / agent / 舊單機 Pod) 都
// 識別為「自身」並從報告中過濾掉，但不會誤殺其他 namespace 或不同名稱的 Pod。
func TestIsSelfFiltersAllHealthcheckPods(t *testing.T) {
	c := &Collector{
		selfNamespace:        "k8s-healthcheck",
		selfName:             "k8s-healthcheck-aggregator-7d8d9-abc12",
		healthcheckNamespace: "k8s-healthcheck",
	}
	cases := []struct {
		ns, name string
		want     bool
	}{
		// 應被過濾
		{"k8s-healthcheck", "k8s-healthcheck-aggregator-7d8d9-abc12", true},
		{"k8s-healthcheck", "k8s-healthcheck-aggregator-other-ksh71", true},
		{"k8s-healthcheck", "k8s-healthcheck-agent-xyz12", true},
		{"k8s-healthcheck", "k8s-healthcheck", true},
		{"k8s-healthcheck", "k8s-healthcheck-future-component", true},
		// 不應被過濾
		{"k8s-healthcheck", "metrics-server-x", false}, // 同 ns 但非 healthcheck Pod
		{"kube-system", "k8s-healthcheck-something", false}, // 名字像但 ns 不對
		{"app", "k8s-healthcheck-aggregator-fake", false},
		{"default", "nginx", false},
	}
	for _, tc := range cases {
		got := c.isSelf(tc.ns, tc.name)
		if got != tc.want {
			t.Errorf("isSelf(%q, %q) = %v, want %v", tc.ns, tc.name, got, tc.want)
		}
	}
}

// TestIsSelfFallbackNamespace 驗證 healthcheckNamespace 為預設值時 (沒有
// 透過 SetHealthcheckNamespace 顯式設定)，仍會以 "k8s-healthcheck" 為過濾範圍。
func TestIsSelfFallbackNamespace(t *testing.T) {
	c := &Collector{
		// 模擬本機跑 (沒讀到 selfNamespace) 但叢集裡部署了 healthcheck 系統的情況
		healthcheckNamespace: "k8s-healthcheck",
	}
	if !c.isSelf("k8s-healthcheck", "k8s-healthcheck-agent-abc") {
		t.Errorf("即使 selfName 為空，仍應過濾 k8s-healthcheck namespace 內的 agent Pod")
	}
	if c.isSelf("default", "k8s-healthcheck") {
		t.Errorf("不應過濾 default namespace 內的同名 Pod")
	}
}
