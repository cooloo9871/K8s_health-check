package agent

import "testing"

// TestStripHostPrefix 驗證 agent 在寫 CertInfo.Path 之前能把容器視角的
// /host 前綴 strip 掉, 還原為節點上的真實絕對路徑。
func TestStripHostPrefix(t *testing.T) {
	cases := []struct {
		name              string
		prefix, path, want string
	}{
		{"標準 /host 前綴", "/host", "/host/etc/kubernetes/pki/apiserver.crt", "/etc/kubernetes/pki/apiserver.crt"},
		{"路徑等於 prefix → 回 /", "/host", "/host", "/"},
		{"空 prefix → path 原樣回", "", "/etc/kubernetes/pki/x.crt", "/etc/kubernetes/pki/x.crt"},
		{"prefix 為 / → 不切 (避免回空)", "/", "/etc/kubernetes/pki/x.crt", "/etc/kubernetes/pki/x.crt"},
		{"path 不在 prefix 下 → 不動", "/host", "/var/lib/kubelet/pki/k.pem", "/var/lib/kubelet/pki/k.pem"},
		{"prefix 末尾多斜線 → 正規化後仍 strip", "/host/", "/host/etc/x.crt", "/etc/x.crt"},
		{"自訂 prefix /rootfs", "/rootfs", "/rootfs/var/lib/k.pem", "/var/lib/k.pem"},
		{"prefix 是 path 的字首但不到 / 邊界 → 不切", "/host", "/hosted/etc/x.crt", "/hosted/etc/x.crt"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := stripHostPrefix(tc.prefix, tc.path)
			if got != tc.want {
				t.Errorf("stripHostPrefix(%q, %q) = %q, want %q", tc.prefix, tc.path, got, tc.want)
			}
		})
	}
}
