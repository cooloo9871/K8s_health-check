// k8s-healthcheck CLI 進入點。同一支 binary 支援兩種角色:
//
//	--mode=aggregator (預設): 從 cluster API 收集資料、向各 agent 拉資料、
//	                          產出 PDF 報告。設計上是一次性執行 (Pod 跑完即結束)。
//	--mode=agent             : 駐留在 DaemonSet 上，提供 HTTP /data 端點回傳
//	                          本機節點的磁碟與憑證資訊。
//
// 所有時間統一以台灣當地時間呈現。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"k8s-health-check/internal/advisor"
	"k8s-health-check/internal/agent"
	"k8s-health-check/internal/collector"
	"k8s-health-check/internal/report"
	"k8s-health-check/internal/tz"
)

func main() {
	mode := flag.String("mode", "aggregator", "執行模式: aggregator (預設) | agent")

	// --- aggregator 專用旗標 ---
	outDir := flag.String("out", "/reports", "PDF 輸出目錄 (僅 aggregator 模式)")
	timeout := flag.Duration("timeout", 5*time.Minute, "整體收集逾時時間")
	kubeconfig := flag.String("kubeconfig", "", "外部 kubeconfig 路徑 (cluster 外執行時使用)")
	pkiDir := flag.String("pki-dir", "/host/etc/kubernetes/pki", "kubeadm 風格憑證目錄 (本機 fallback，agent 模式建議用 DaemonSet 收)")
	sleepAfter := flag.Duration("sleep-after", 0, "PDF 寫出後讓 Pod 多存活的時間，方便 kubectl cp")
	env := flag.String("env", "auto", "環境標籤: dev | staging | production | auto")
	agentNamespace := flag.String("agent-namespace", "", "agent Pod 所在 namespace，預設 = aggregator 自身的 namespace")
	agentSelector := flag.String("agent-selector", "app=k8s-healthcheck-agent", "agent Pod 的 label selector")
	agentPort := flag.String("agent-port", "8080", "agent HTTP server port")
	agentTimeout := flag.Duration("agent-timeout", 10*time.Second, "向單一 agent 拉資料的逾時時間")

	// --- agent 專用旗標 ---
	listen := flag.String("listen", ":8080", "agent HTTP server 監聽位址 (僅 agent 模式)")
	hostPrefix := flag.String("host-prefix", "/host", "host 根目錄在容器中的掛點 (僅 agent 模式)")
	flag.Parse()

	switch *mode {
	case "agent":
		runAgent(*listen, *hostPrefix)
	case "aggregator", "":
		runAggregator(aggregatorOpts{
			OutDir:         *outDir,
			Timeout:        *timeout,
			Kubeconfig:     *kubeconfig,
			PkiDir:         *pkiDir,
			SleepAfter:     *sleepAfter,
			Env:            *env,
			AgentNamespace: *agentNamespace,
			AgentSelector:  *agentSelector,
			AgentPort:      *agentPort,
			AgentTimeout:   *agentTimeout,
		})
	default:
		log.Fatalf("unknown --mode: %s (有效值: aggregator | agent)", *mode)
	}
}

// runAgent 啟動 DaemonSet 端 HTTP server，會封鎖直到收到 SIGINT/SIGTERM。
func runAgent(listen, hostPrefix string) {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	if err := agent.Run(ctx, agent.Config{Listen: listen, HostPrefix: hostPrefix}); err != nil {
		log.Fatalf("agent 啟動失敗: %v", err)
	}
}

type aggregatorOpts struct {
	OutDir         string
	Timeout        time.Duration
	Kubeconfig     string
	PkiDir         string
	SleepAfter     time.Duration
	Env            string
	AgentNamespace string
	AgentSelector  string
	AgentPort      string
	AgentTimeout   time.Duration
}

// runAggregator 是原本 CLI 的主流程: 蒐集叢集資料、彙整 agent 資料、產出 PDF。
func runAggregator(o aggregatorOpts) {
	if err := os.MkdirAll(o.OutDir, 0o755); err != nil {
		log.Fatalf("create output dir: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), o.Timeout)
	defer cancel()

	c, err := collector.New(o.Kubeconfig, o.PkiDir)
	if err != nil {
		log.Fatalf("init collector: %v", err)
	}
	c.Agents = collector.AgentDiscovery{
		Namespace:     o.AgentNamespace,
		LabelSelector: o.AgentSelector,
		Port:          o.AgentPort,
		Timeout:       o.AgentTimeout,
	}

	log.Println("開始收集 cluster 健檢資料...")
	rep := c.Collect(ctx)
	log.Printf("已抓到 %d 個 agent 節點資料", len(rep.NodeAgents))

	envArg := o.Env
	if envArg == "auto" {
		envArg = ""
	}
	advisor.Analyze(rep, envArg)
	log.Printf("advisor: 環境=%s 整體=%s 發現=%d 建議=%d",
		rep.Conclusion.Environment, rep.Conclusion.OverallStatus,
		len(rep.Conclusion.Findings), len(rep.Conclusion.Recommendations))

	stamp := tz.Now().Format("20060102-150405")
	clusterTag := rep.Cluster.Distribution
	if clusterTag == "" {
		clusterTag = "k8s"
	}
	outPath := filepath.Join(o.OutDir, fmt.Sprintf("%s-health-%s.pdf", clusterTag, stamp))

	if err := report.WritePDF(rep, outPath); err != nil {
		log.Fatalf("write pdf: %v", err)
	}

	log.Printf("報告已寫出: %s", outPath)
	if len(rep.Errors) > 0 {
		log.Printf("收集完成但有 %d 項非致命錯誤", len(rep.Errors))
		for _, e := range rep.Errors {
			log.Printf("  - %s", e)
		}
	}

	if o.SleepAfter > 0 {
		log.Printf("多存活 %s，請以下列指令取出 PDF:", o.SleepAfter)
		log.Printf("  kubectl -n <ns> cp <pod>:%s ./report.pdf", outPath)
		time.Sleep(o.SleepAfter)
	}
}
