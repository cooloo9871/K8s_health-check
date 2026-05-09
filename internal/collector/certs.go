package collector

import (
	"context"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"k8s-health-check/internal/model"
)

// collectCerts 走訪 kubeadm 風格的 PKI 目錄 (透過 hostPath 掛入)，
// 解析所有 *.crt / *.pem。在不暴露 /etc/kubernetes/pki 的發行版
// (k3s、各家 managed cloud) 上，目錄不存在會直接清爽地略過此區段。
//
// 寫入 CertInfo.Path 前會把容器視角的 host 前綴 strip 掉, 顯示節點上的
// 真實絕對路徑 (例如 "/host/etc/kubernetes/pki/x.crt" → "/etc/kubernetes/pki/x.crt").
// 推導方式: c.pkiDir 若以標準後綴 "/etc/kubernetes/pki" 結尾, 前面那段
// 就視為 hostPrefix; 否則不做替換 (使用者用了非標準路徑時保留原樣).
func (c *Collector) collectCerts(ctx context.Context, r *model.Report) error {
	if c.pkiDir == "" {
		return nil
	}
	if _, err := os.Stat(c.pkiDir); errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	hostPrefix := derivePKIHostPrefix(c.pkiDir)

	out := []model.CertInfo{}
	walkErr := filepath.WalkDir(c.pkiDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // 無法讀取的路徑直接略過，不要中斷整個 collector
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
		for {
			block, rest := pem.Decode(raw)
			if block == nil {
				break
			}
			raw = rest
			if block.Type != "CERTIFICATE" {
				continue
			}
			cert, err := x509.ParseCertificate(block.Bytes)
			if err != nil {
				continue
			}
			days := int(time.Until(cert.NotAfter).Hours() / 24)
			status := "OK"
			switch {
			case days < 0:
				status = "EXPIRED"
			case days < 30:
				status = "EXPIRING SOON"
			case days < 90:
				status = "WARN"
			}
			out = append(out, model.CertInfo{
				Path:     stripHostPrefix(hostPrefix, path),
				Subject:  cert.Subject.CommonName,
				NotAfter: cert.NotAfter,
				DaysLeft: days,
				Status:   status,
			})
		}
		return nil
	})
	if walkErr != nil {
		return walkErr
	}

	sort.Slice(out, func(i, j int) bool { return out[i].DaysLeft < out[j].DaysLeft })
	r.Certs = out
	return nil
}

// derivePKIHostPrefix 由 pkiDir 推回容器內掛 host 用的前綴。
// 預設 pkiDir = "/host/etc/kubernetes/pki" → 回 "/host"。若 pkiDir 不是
// 標準的 ".../etc/kubernetes/pki" 結尾 (使用者自訂奇怪路徑), 回空字串
// 表示「不要做替換」, 避免誤切。
func derivePKIHostPrefix(pkiDir string) string {
	const suffix = "/etc/kubernetes/pki"
	clean := filepath.Clean(pkiDir)
	if strings.HasSuffix(clean, suffix) {
		return strings.TrimSuffix(clean, suffix)
	}
	return ""
}

// stripHostPrefix 把 path 開頭的 hostPrefix 拿掉, 還原為節點上的真實絕對
// 路徑。空 prefix 或路徑不在 prefix 之下時直接回原值。同 agent 套件的同名
// helper, 邏輯故意一致以便兩端對 PDF 顯示出相同節點視角的路徑。
func stripHostPrefix(hostPrefix, path string) string {
	if hostPrefix == "" {
		return path
	}
	clean := filepath.Clean(hostPrefix)
	if clean == "/" || clean == "." {
		return path
	}
	if path == clean {
		return "/"
	}
	if strings.HasPrefix(path, clean+"/") {
		return path[len(clean):]
	}
	return path
}
