package collector

import (
	"testing"

	"k8s-health-check/internal/model"
)

// TestNormalizeCertPath 驗證 aggregator 端的防呆 normalize: 即使收到舊版 agent
// 回傳的 "/host/..." 路徑, PDF 上的最終結果也會是節點實際絕對路徑。
func TestNormalizeCertPath(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"/host/etc/kubernetes/pki/apiserver.crt", "/etc/kubernetes/pki/apiserver.crt"},
		{"/host/var/lib/kubelet/pki/k.pem", "/var/lib/kubelet/pki/k.pem"},
		{"/host", "/"},
		{"/host/", "/"},
		{"/etc/kubernetes/pki/x.crt", "/etc/kubernetes/pki/x.crt"}, // 已是節點視角, 不動
		{"/hosted/foo", "/hosted/foo"},                              // 字首像 /host 但不是邊界, 不動
		{"", ""},
	}
	for _, tc := range cases {
		got := normalizeCertPath(tc.in)
		if got != tc.want {
			t.Errorf("normalizeCertPath(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// TestMergeAgentCertsNormalizesHostPrefix 驗證: 即使 NodeAgents.Certs.Path
// 帶有 /host 前綴 (舊版 agent), mergeAgentCerts 也會在合併到 r.Certs 時把它
// 切掉, 確保 PDF 看不到 /host。
func TestMergeAgentCertsNormalizesHostPrefix(t *testing.T) {
	r := &model.Report{
		NodeAgents: []model.NodeAgentData{
			{
				NodeName: "n1",
				Certs: []model.CertInfo{
					{Path: "/host/etc/kubernetes/pki/apiserver.crt", Subject: "kube-apiserver", Source: "k8s-pki"},
					{Path: "/host/var/lib/kubelet/pki/kubelet-client-current.pem", Subject: "system:node:n1", Source: "kubelet"},
				},
			},
		},
	}
	mergeAgentCerts(r)
	if len(r.Certs) != 2 {
		t.Fatalf("預期合併後有 2 張憑證, 實際 %d", len(r.Certs))
	}
	for _, c := range r.Certs {
		if len(c.Path) >= 5 && c.Path[:5] == "/host" {
			t.Errorf("Path 仍含 /host 前綴: %q", c.Path)
		}
	}
}
