package advisor

import (
	"strings"
	"testing"

	"k8s-health-check/internal/model"
)

// TestCheckControlPlane_HealthyDoesNotTriggerFinding 驗證 collector 端
// API health 端點回傳 "Healthy" 時，advisor 不會誤判為控制平面異常。
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
		if strings.Contains(f.Title, "控制平面健康端點") {
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
		if strings.Contains(f.Title, "控制平面健康端點有 2 項異常") {
			hit++
		}
	}
	if hit != 1 {
		t.Fatalf("預期 1 筆「2 項異常」finding, 實際 finding=%v", c.Findings)
	}
}
