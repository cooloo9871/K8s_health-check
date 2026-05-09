package collector

import "testing"

// TestDerivePKIHostPrefix 驗證從 pkiDir 推回 host 前綴: pkiDir 結尾若是
// 標準的 /etc/kubernetes/pki 才會 strip, 否則回空字串以避免誤切。
func TestDerivePKIHostPrefix(t *testing.T) {
	cases := []struct {
		pkiDir, want string
	}{
		{"/host/etc/kubernetes/pki", "/host"},
		{"/host/etc/kubernetes/pki/", "/host"},   // 結尾斜線會被 Clean 正規化
		{"/etc/kubernetes/pki", ""},              // 真本機路徑, 沒有 host 前綴
		{"/rootfs/etc/kubernetes/pki", "/rootfs"},
		{"/some/weird/path", ""},                 // 不符合標準後綴 → 不切
		{"", ""},
	}
	for _, tc := range cases {
		got := derivePKIHostPrefix(tc.pkiDir)
		if got != tc.want {
			t.Errorf("derivePKIHostPrefix(%q) = %q, want %q", tc.pkiDir, got, tc.want)
		}
	}
}

// TestStripHostPrefix_Collector 與 agent 的同名 helper 採同一份規格,
// 此處重點驗證 collector 端 walk 出來的容器內路徑能被正確還原回節點視角。
func TestStripHostPrefix_Collector(t *testing.T) {
	cases := []struct {
		prefix, path, want string
	}{
		{"/host", "/host/etc/kubernetes/pki/apiserver.crt", "/etc/kubernetes/pki/apiserver.crt"},
		{"", "/etc/kubernetes/pki/x.crt", "/etc/kubernetes/pki/x.crt"},
		{"/host", "/host", "/"},
		{"/host", "/var/lib/foo", "/var/lib/foo"},
	}
	for _, tc := range cases {
		got := stripHostPrefix(tc.prefix, tc.path)
		if got != tc.want {
			t.Errorf("stripHostPrefix(%q, %q) = %q, want %q", tc.prefix, tc.path, got, tc.want)
		}
	}
}
