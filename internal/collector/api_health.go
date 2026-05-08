package collector

import (
	"context"
	"strings"

	"k8s-health-check/internal/model"
)

// collectAPIHealth 探測 kube-apiserver 的 /healthz, /livez, /readyz 端點。
// 這些端點在所有 K8s 發行版上都可靠，可以取代已 deprecated 的 componentstatuses API。
func (c *Collector) collectAPIHealth(ctx context.Context, r *model.Report) error {
	endpoints := []string{"/healthz", "/livez", "/readyz"}
	rt := c.clientset.Discovery().RESTClient()
	out := make([]model.APIHealth, 0, len(endpoints))
	for _, ep := range endpoints {
		raw, err := rt.Get().AbsPath(ep).Param("verbose", "true").DoRaw(ctx)
		entry := model.APIHealth{Endpoint: ep}
		if err != nil {
			entry.Status = "Failed"
			entry.Detail = truncate(err.Error(), 120)
			out = append(out, entry)
			continue
		}
		body := string(raw)
		entry.Status = "Healthy"
		// 每一行格式為 "[+]check ok" 或 "[-]check failed"，把失敗的檢查項目蒐集起來
		var failed []string
		for _, line := range strings.Split(body, "\n") {
			if strings.HasPrefix(line, "[-]") {
				failed = append(failed, strings.TrimPrefix(line, "[-]"))
			}
		}
		if len(failed) > 0 {
			entry.Status = "Degraded"
			entry.Detail = truncate(strings.Join(failed, "; "), 200)
		}
		out = append(out, entry)
	}
	r.APIHealth = out
	return nil
}
