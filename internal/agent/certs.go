// agent/certs 在 DaemonSet 端解析節點上各種類型的 x509 憑證:
//   - K8s pki 目錄下的所有 CA / 服務憑證 (control-plane 節點才會有)
//   - kubelet 的 client / server 憑證
//   - 各種 kubeconfig 內 base64 嵌入的 client cert
//
// 所有採到的憑證會被填好 NotAfter / DaysLeft / Status / Source / Node。
package agent

import (
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"k8s-health-check/internal/model"
)

// CertScanPaths 描述要走訪的根目錄與其對應的 Source 標籤。
type CertScanPaths struct {
	K8sPKI       string // 通常 = /host/etc/kubernetes/pki
	EtcdPKI      string // 通常 = /host/etc/kubernetes/pki/etcd
	KubeletPKI   string // 通常 = /host/var/lib/kubelet/pki
	KubeconfDir  string // 通常 = /host/etc/kubernetes (用來掃 *.conf 內嵌憑證)
}

// DefaultCertScanPaths 回傳 hostPrefix 為基底的標準路徑組合。
func DefaultCertScanPaths(hostPrefix string) CertScanPaths {
	return CertScanPaths{
		K8sPKI:      filepath.Join(hostPrefix, "etc/kubernetes/pki"),
		EtcdPKI:     filepath.Join(hostPrefix, "etc/kubernetes/pki/etcd"),
		KubeletPKI:  filepath.Join(hostPrefix, "var/lib/kubelet/pki"),
		KubeconfDir: filepath.Join(hostPrefix, "etc/kubernetes"),
	}
}

// CollectCerts 解析 paths 中各類來源的憑證，回傳合併後的清單。
// nodeName 會填入每張憑證的 Node 欄位以利 aggregator 區分。
func CollectCerts(paths CertScanPaths, nodeName string) []model.CertInfo {
	out := []model.CertInfo{}

	// K8s control-plane PKI
	out = append(out, scanDirCerts(paths.K8sPKI, "k8s-pki", nodeName)...)
	// etcd 通常已經是 K8sPKI/etcd，但別名掃描以防 distro 把它分離出來
	if paths.EtcdPKI != "" && paths.EtcdPKI != paths.K8sPKI {
		out = append(out, scanDirCerts(paths.EtcdPKI, "etcd", nodeName)...)
	}
	// kubelet
	out = append(out, scanDirCerts(paths.KubeletPKI, "kubelet", nodeName)...)
	// kubeconfig 內嵌
	out = append(out, scanKubeconfigCerts(paths.KubeconfDir, nodeName)...)

	// 依剩餘天數由少到多排序，最緊迫的排前面
	sortCerts(out)
	return out
}

// scanDirCerts 走訪 root 目錄下所有 *.crt / *.pem，把每個 PEM block
// 解出來填成 CertInfo。Source 會被覆寫為傳入值；遇到 etcd 子目錄會
// 自動把 source 改為 "etcd" 以便 PDF 端依類別著色。
func scanDirCerts(root, source, nodeName string) []model.CertInfo {
	if root == "" {
		return nil
	}
	if _, err := os.Stat(root); err != nil {
		return nil
	}
	out := []model.CertInfo{}
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(path))
		if ext != ".crt" && ext != ".pem" {
			return nil
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			return nil
		}
		src := source
		if strings.Contains(path, string(os.PathSeparator)+"etcd"+string(os.PathSeparator)) {
			src = "etcd"
		}
		for _, c := range parsePEMCerts(raw) {
			out = append(out, certToInfo(c, path, src, nodeName))
		}
		return nil
	})
	return out
}

// kubeconfigCertRE 比對 kubeconfig 中的 base64 內嵌 client-certificate-data。
var kubeconfigCertRE = regexp.MustCompile(`(?m)^\s*client-certificate-data:\s*(\S+)\s*$`)

// scanKubeconfigCerts 走訪 dir 下的所有 *.conf，從中萃取 base64 編碼的
// client cert。kubeadm 預設會在 /etc/kubernetes 下放 admin.conf /
// controller-manager.conf / scheduler.conf / kubelet.conf 等，這些 client
// cert 並不會出現在 PKI 目錄裡，必須從 kubeconfig 解出來才能監控到期。
func scanKubeconfigCerts(dir, nodeName string) []model.CertInfo {
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	out := []model.CertInfo{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".conf") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		raw, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		for _, m := range kubeconfigCertRE.FindAllStringSubmatch(string(raw), -1) {
			b64 := strings.TrimSpace(m[1])
			pemBytes, err := base64.StdEncoding.DecodeString(b64)
			if err != nil {
				continue
			}
			for _, c := range parsePEMCerts(pemBytes) {
				out = append(out, certToInfo(c, path, "kubeconfig", nodeName))
			}
		}
	}
	return out
}

// parsePEMCerts 把可能含有多筆 PEM block 的 bytes 拆出 *x509.Certificate 列表。
func parsePEMCerts(raw []byte) []*x509.Certificate {
	out := []*x509.Certificate{}
	for {
		block, rest := pem.Decode(raw)
		if block == nil {
			break
		}
		raw = rest
		if block.Type != "CERTIFICATE" {
			continue
		}
		c, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			continue
		}
		out = append(out, c)
	}
	return out
}

// certToInfo 把 x509.Certificate 轉成報告用 CertInfo，順手算好 DaysLeft / Status。
func certToInfo(c *x509.Certificate, path, source, nodeName string) model.CertInfo {
	days := int(time.Until(c.NotAfter).Hours() / 24)
	status := "OK"
	switch {
	case days < 0:
		status = "EXPIRED"
	case days < 30:
		status = "EXPIRING SOON"
	case days < 90:
		status = "WARN"
	}
	subject := c.Subject.CommonName
	if subject == "" && len(c.Subject.Organization) > 0 {
		subject = c.Subject.Organization[0]
	}
	return model.CertInfo{
		Path:     path,
		Subject:  subject,
		NotAfter: c.NotAfter,
		DaysLeft: days,
		Status:   status,
		Source:   source,
		Node:     nodeName,
	}
}

// sortCerts 以 DaysLeft 由少到多排序，相同則以路徑次序排列。
func sortCerts(s []model.CertInfo) {
	// 簡易插入排序: 元素數量永遠很小 (數十張)，避免 import sort。
	for i := 1; i < len(s); i++ {
		for j := i; j > 0; j-- {
			a, b := s[j-1], s[j]
			if a.DaysLeft < b.DaysLeft {
				break
			}
			if a.DaysLeft == b.DaysLeft && a.Path <= b.Path {
				break
			}
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
