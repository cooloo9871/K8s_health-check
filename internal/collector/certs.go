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

// collectCerts walks the kubeadm-style PKI directory (when mounted via
// hostPath) and decodes every *.crt / *.pem it finds.  On distributions
// that don't expose /etc/kubernetes/pki (k3s, managed clouds, etc.) the
// directory simply won't exist and we exit cleanly.
func (c *Collector) collectCerts(ctx context.Context, r *model.Report) error {
	if c.pkiDir == "" {
		return nil
	}
	if _, err := os.Stat(c.pkiDir); errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	out := []model.CertInfo{}
	walkErr := filepath.WalkDir(c.pkiDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil // skip unreadable paths instead of failing the whole collector
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
				Path:     path,
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
