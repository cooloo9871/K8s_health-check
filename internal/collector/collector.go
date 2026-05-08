package collector

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"

	"k8s-health-check/internal/model"
	"k8s-health-check/internal/tz"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	metricsv "k8s.io/metrics/pkg/client/clientset/versioned"
)

// Collector 串接 Kubernetes client 並執行各主題子收集器。
// 設計上刻意寬容: 任一子收集器失敗只會把錯誤掛到 Report.Errors，
// 其他區段仍會被填入，確保至少能產出一份部分報告。
type Collector struct {
	clientset kubernetes.Interface
	metrics   metricsv.Interface
	restCfg   *rest.Config
	pkiDir    string

	// 自身識別。用來把 collector 本身的 Pod 從報告裡濾掉，避免
	// 在 ProblemPods / TopCPU / TopMemory / Events 等區段中露出。
	selfNamespace string
	selfName      string

	// AgentDiscovery 為 DaemonSet 模式下找尋 agent Pod 的設定。
	// 若 LabelSelector 為空 (零值) 表示不啟用 agent 收集，行為退回單機模式。
	Agents AgentDiscovery

	mu     sync.Mutex
	errors []string
}

// New 建立 Collector。kubeconfig 為空字串時會使用 in-cluster config。
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
		// metrics-server 未安裝時不視為錯誤，後續會在報告中註記。
		log.Printf("warning: metrics client init failed: %v", err)
	}

	ns, name := detectSelf()
	if name != "" {
		log.Printf("self-pod identified as %s/%s (將從報告中排除)", ns, name)
	}

	return &Collector{
		clientset:     cs,
		metrics:       mc,
		restCfg:       cfg,
		pkiDir:        pkiDir,
		selfNamespace: ns,
		selfName:      name,
	}, nil
}

// detectSelf 嘗試找出 collector 自己的 Pod 名稱與 namespace。
// 依序嘗試: 
//  1. 環境變數 POD_NAME / POD_NAMESPACE（建議用 downward API 注入，最可靠）
//  2. os.Hostname()（in-cluster 預設等於 Pod 名稱）+
//     /var/run/secrets/kubernetes.io/serviceaccount/namespace
// 任一項拿不到就回空字串，呼叫端需相容空值。
func detectSelf() (namespace, name string) {
	if v := strings.TrimSpace(os.Getenv("POD_NAME")); v != "" {
		name = v
	} else if h, err := os.Hostname(); err == nil {
		name = h
	}
	if v := strings.TrimSpace(os.Getenv("POD_NAMESPACE")); v != "" {
		namespace = v
	} else if b, err := os.ReadFile("/var/run/secrets/kubernetes.io/serviceaccount/namespace"); err == nil {
		namespace = strings.TrimSpace(string(b))
	}
	return namespace, name
}

// isSelf 判斷一個 (namespace, name) 是否就是 collector 本身。
// 若 selfName 偵測不到（例如本機跑），永遠回 false 不過濾。
func (c *Collector) isSelf(namespace, name string) bool {
	if c.selfName == "" {
		return false
	}
	if namespace != c.selfNamespace {
		return false
	}
	return name == c.selfName
}

func loadConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	cfg, err := rest.InClusterConfig()
	if err == nil {
		return cfg, nil
	}
	// 退回讀取使用者預設的 kubeconfig（適用本機開發）。
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

// Collect 執行所有子收集器並回傳填好的 Report。彼此沒有相依的子收集器
// 會以 goroutine 並行執行。
func (c *Collector) Collect(ctx context.Context) *model.Report {
	r := &model.Report{GeneratedAt: tz.Now()}

	// 先抓 cluster 資訊（速度快，且 Distribution 標籤會用於 PDF 檔名）。
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
	// agents 必須在所有 cluster 級收集完成之後再合併，因此放最後序列執行。
	wg.Wait()
	if err := c.collectAgents(ctx, r, c.Agents); err != nil {
		c.addErr("agents", err)
	}

	c.mu.Lock()
	r.Errors = append(r.Errors, c.errors...)
	c.mu.Unlock()
	return r
}
