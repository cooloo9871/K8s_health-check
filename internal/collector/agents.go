package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"sync"
	"time"

	"k8s-health-check/internal/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// AgentDiscovery 控制 aggregator 端如何找到並抓取 DaemonSet agent 的資料。
type AgentDiscovery struct {
	// Namespace 為 agent Pod 所在的命名空間，預設 = collector 自身所在的命名空間。
	Namespace string
	// LabelSelector 用來選出 agent Pod，預設 "app=k8s-healthcheck-agent"。
	LabelSelector string
	// Port 為 agent HTTP server 監聽的 port，預設 "8080"。
	Port string
	// Timeout 為單一 agent 抓取的逾時時間，預設 10 秒。
	Timeout time.Duration
}

// withDefaults 補上 AgentDiscovery 中的預設值，呼叫端只填關心的欄位即可。
func (a AgentDiscovery) withDefaults(fallbackNamespace string) AgentDiscovery {
	if a.Namespace == "" {
		a.Namespace = fallbackNamespace
	}
	if a.LabelSelector == "" {
		a.LabelSelector = "app=k8s-healthcheck-agent"
	}
	if a.Port == "" {
		a.Port = "8080"
	}
	if a.Timeout == 0 {
		a.Timeout = 10 * time.Second
	}
	return a
}

// collectAgents 列出所有符合條件的 agent Pod，並行 fetch 每一支的 /data，
// 把結果合併到 Report.NodeAgents、並把 agent 採到的憑證合併進 Report.Certs。
//
// 任何單一 agent 失敗 (Pod 還沒 Ready、HTTP 拒連、JSON 壞掉等) 都不會中斷整體
// 流程，會把錯誤訊息掛到 Report.Errors 讓使用者知道哪個節點漏報。
func (c *Collector) collectAgents(ctx context.Context, r *model.Report, disc AgentDiscovery) error {
	disc = disc.withDefaults(c.selfNamespace)
	if disc.Namespace == "" {
		// 沒有 namespace 也找不到 agent — 忽略，視為「未配置 DaemonSet 模式」。
		return nil
	}

	pods, err := c.clientset.CoreV1().Pods(disc.Namespace).List(ctx, metav1.ListOptions{
		LabelSelector: disc.LabelSelector,
	})
	if err != nil {
		return fmt.Errorf("list agent pods: %w", err)
	}
	if len(pods.Items) == 0 {
		return nil
	}

	type result struct {
		data model.NodeAgentData
		err  error
	}
	results := make(chan result, len(pods.Items))
	wg := sync.WaitGroup{}

	httpClient := &http.Client{Timeout: disc.Timeout}

	for _, p := range pods.Items {
		// 只抓 Running 且有 PodIP 的: 其他狀態抓也沒意義。
		if p.Status.Phase != corev1.PodRunning || p.Status.PodIP == "" {
			continue
		}
		// 排除 aggregator 自己 (label selector 通常已經分開，但極端情況下
		// 例如 ad-hoc 跑同一 Pod 兩次，仍以精確 namespace+name 過濾一次)。
		// 注意: 不能用 isSelf，因為 isSelf 會把 namespace 內所有 k8s-healthcheck-*
		// Pod 都當成「自身基礎設施」，那會把 agent 本身也過濾掉。
		if c.selfName != "" && p.Namespace == c.selfNamespace && p.Name == c.selfName {
			continue
		}
		ip := p.Status.PodIP
		podName := p.Name
		nodeName := p.Spec.NodeName
		wg.Add(1)
		go func() {
			defer wg.Done()
			data, err := fetchAgent(ctx, httpClient, ip, disc.Port)
			if err != nil {
				results <- result{
					data: model.NodeAgentData{NodeName: nodeName, Errors: []string{err.Error()}},
					err:  fmt.Errorf("agent %s on %s: %w", podName, nodeName, err),
				}
				return
			}
			// 若 agent 沒填 NodeName (例如 downward API 沒注入) 用 Pod.Spec.NodeName 補上。
			if data.NodeName == "" {
				data.NodeName = nodeName
			}
			results <- result{data: data}
		}()
	}

	wg.Wait()
	close(results)

	for res := range results {
		r.NodeAgents = append(r.NodeAgents, res.data)
		if res.err != nil {
			c.addErr("agent", res.err)
		}
	}
	// 依節點名排序，PDF 渲染順序穩定。
	sort.Slice(r.NodeAgents, func(i, j int) bool {
		return r.NodeAgents[i].NodeName < r.NodeAgents[j].NodeName
	})

	mergeAgentCerts(r)
	return nil
}

// fetchAgent 對單一 agent 發 HTTP GET，解析 JSON 回傳。
func fetchAgent(ctx context.Context, httpClient *http.Client, podIP, port string) (model.NodeAgentData, error) {
	url := fmt.Sprintf("http://%s/data", net.JoinHostPort(podIP, port))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return model.NodeAgentData{}, err
	}
	resp, err := httpClient.Do(req)
	if err != nil {
		return model.NodeAgentData{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		return model.NodeAgentData{}, fmt.Errorf("status %d: %s", resp.StatusCode, string(body))
	}
	var d model.NodeAgentData
	if err := json.NewDecoder(resp.Body).Decode(&d); err != nil {
		return model.NodeAgentData{}, fmt.Errorf("decode: %w", err)
	}
	return d, nil
}

// mergeAgentCerts 把所有 agent 採到的憑證 union 進 Report.Certs，並依
// (Path, Subject, Node) 去重。aggregator 自己 walk pkiDir 採到的憑證會被
// 保留，但通常 agent 模式下 pkiDir 不會掛載，所以不會有重複。
func mergeAgentCerts(r *model.Report) {
	seen := map[string]bool{}
	merged := make([]model.CertInfo, 0, len(r.Certs))
	add := func(c model.CertInfo) {
		key := c.Path + "|" + c.Subject + "|" + c.Node
		if seen[key] {
			return
		}
		seen[key] = true
		merged = append(merged, c)
	}
	for _, c := range r.Certs {
		// 補一個來源標籤，避免之前的本機 pkiDir 採到的憑證沒有 Source。
		if c.Source == "" {
			c.Source = "k8s-pki"
		}
		add(c)
	}
	for _, na := range r.NodeAgents {
		for _, c := range na.Certs {
			if c.Node == "" {
				c.Node = na.NodeName
			}
			add(c)
		}
	}
	// 排序: 剩餘天數少的排前面，相同則來源 + 路徑次序
	sort.Slice(merged, func(i, j int) bool {
		if merged[i].DaysLeft != merged[j].DaysLeft {
			return merged[i].DaysLeft < merged[j].DaysLeft
		}
		if merged[i].Node != merged[j].Node {
			return merged[i].Node < merged[j].Node
		}
		return merged[i].Path < merged[j].Path
	})
	r.Certs = merged
}
