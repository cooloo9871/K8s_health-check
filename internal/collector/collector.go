package collector

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/brobridge/k8s-health-check/internal/model"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"
)

// Collector wires the kubernetes client(s) together and runs each
// topical sub-collector.  It is intentionally lenient: any sub-collector
// that fails will just attach its error to Report.Errors so the rest of
// the report still gets produced.
type Collector struct {
	clientset    kubernetes.Interface
	metrics      metricsv.Interface
	restCfg      *rest.Config
	pkiDir       string

	mu     sync.Mutex
	errors []string
}

// New builds a Collector. If kubeconfig is empty, in-cluster config is used.
func New(kubeconfig, pkiDir string) (*Collector, error) {
	cfg, err := loadConfig(kubeconfig)
	if err != nil {
		return nil, fmt.Errorf("load kube config: %w", err)
	}
	cfg.QPS = 50
	cfg.Burst = 100

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("kubernetes clientset: %w", err)
	}

	mc, err := metricsv.NewForConfig(cfg)
	if err != nil {
		// metrics-server may not be installed; we degrade gracefully later
		log.Printf("warning: metrics client init failed: %v", err)
	}

	return &Collector{
		clientset: cs,
		metrics:   mc,
		restCfg:   cfg,
		pkiDir:    pkiDir,
	}, nil
}

func loadConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	cfg, err := rest.InClusterConfig()
	if err == nil {
		return cfg, nil
	}
	// fall back to default kubeconfig
	loader := clientcmd.NewDefaultClientConfigLoadingRules()
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(
		loader, &clientcmd.ConfigOverrides{}).ClientConfig()
}

func (c *Collector) addErr(section string, err error) {
	if err == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.errors = append(c.errors, fmt.Sprintf("%s: %v", section, err))
}

// Collect runs all sub-collectors and returns a populated Report.
// Sub-collectors that don't depend on each other run concurrently.
func (c *Collector) Collect(ctx context.Context) *model.Report {
	r := &model.Report{GeneratedAt: time.Now()}

	// Cluster info first (fast, sets distribution tag used for filename).
	if err := c.collectCluster(ctx, r); err != nil {
		c.addErr("cluster", err)
	}

	var wg sync.WaitGroup
	run := func(name string, fn func() error) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := fn(); err != nil {
				c.addErr(name, err)
			}
		}()
	}

	run("nodes", func() error { return c.collectNodes(ctx, r) })
	run("metrics", func() error { return c.collectMetrics(ctx, r) })
	run("pods", func() error { return c.collectPods(ctx, r) })
	run("workloads", func() error { return c.collectWorkloads(ctx, r) })
	run("storage", func() error { return c.collectStorage(ctx, r) })
	run("events", func() error { return c.collectEvents(ctx, r) })
	run("components", func() error { return c.collectComponents(ctx, r) })
	run("api-health", func() error { return c.collectAPIHealth(ctx, r) })
	run("certs", func() error { return c.collectCerts(ctx, r) })

	wg.Wait()

	c.mu.Lock()
	r.Errors = append(r.Errors, c.errors...)
	c.mu.Unlock()
	return r
}
