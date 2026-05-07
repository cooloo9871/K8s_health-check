package collector

import (
	"context"
	"strings"

	"github.com/brobridge/k8s-health-check/internal/model"
)

// collectAPIHealth probes the kube-apiserver health endpoints. These are
// reliable on every K8s distribution and replace the deprecated
// componentstatuses API.
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
		// each line is "[+]check ok" or "[-]check failed"; surface failures
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
