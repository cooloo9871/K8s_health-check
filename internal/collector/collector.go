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

	// 自身識別。用來把 collector 本身與所有 k8s-healthcheck-* 系統 Pod
	// 從報告裡濾掉，避免出現在 ProblemPods / TopCPU / TopMemory / Events
	// 等區段中。
	selfNamespace string
	selfName      string

	// HealthcheckNamespace 是整個 k8s-healthcheck 系統部署的 namespace。
	// 該 namespace 下名稱以 k8s-healthcheck 開頭的所有 Pod 都會被視為
	// 自身基礎設施而從報告中排除。
	healthcheckNamespace string

	// 使用者透過 --cluster-name 指定的 cluster 識別字串，空字串表示自動偵測。
	clusterName string

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

	hcNS := ns
	if hcNS == "" {
		hcNS = "k8s-healthcheck"
	}

	return &Collector{
		clientset:            cs,
		metrics:              mc,
		restCfg:              cfg,
		pkiDir:               pkiDir,
		selfNamespace:        ns,
		selfName:             name,
		healthcheckNamespace: hcNS,
	}, nil
}

// SetClusterName 由呼叫端 (main.go) 注入使用者指定的 cluster name。
// 空字串表示交由 detectClusterName 自動偵測。
func (c *Collector) SetClusterName(name string) {
	c.clusterName = strings.TrimSpace(name)
}

// SelfPod 回傳 collector 自身偵測到的 (namespace, name)。在 cluster 內透過
// downward API 環境變數或 service account token 取得；本機跑可能為空字串。
// 主要給 main.go 印「kubectl cp」教學用。
func (c *Collector) SelfPod() (namespace, name string) {
	return c.selfNamespace, c.selfName
}

// SetHealthcheckNamespace 由呼叫端覆寫 healthcheck 系統 Pod 所在的 namespace
// (預設取自 selfNamespace 或 "k8s-healthcheck")。在本機開發 / 測試時可以指定。
func (c *Collector) SetHealthcheckNamespace(ns string) {
	if ns = strings.TrimSpace(ns); ns != "" {
		c.healthcheckNamespace = ns
	}
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

// isSelf 判斷某個 (namespace, name) 是否屬於 k8s-healthcheck 系統 Pod
// (應該從報告中排除)。
//
// 規則: namespace 等於 healthcheckNamespace，且 name 以 "k8s-healthcheck"
// 開頭。涵蓋所有元件:
//   - k8s-healthcheck-aggregator-<rs>-<id>  (Deployment)
//   - k8s-healthcheck-agent-<id>            (DaemonSet)
//   - k8s-healthcheck                       (舊版單機 Pod)
//
// 即使 selfName 偵測不到 (例如本機 kubeconfig 跑) 也仍會過濾，避免叢集裡
// 真的有部署 healthcheck 系統時自我汙染。
func (c *Collector) isSelf(namespace, name string) bool {
	if namespace != c.healthcheckNamespace {
		return false
	}
	return strings.HasPrefix(name, "k8s-healthcheck")
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
