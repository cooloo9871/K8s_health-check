// k8s-healthcheck CLI 進入點。同一支 binary 支援兩種角色:
//
//	--mode=aggregator (預設): 一次性執行的角色 — 由 CronJob 觸發。會動態建立
//	                          agent DaemonSet、收集資料、產出 PDF、再刪除 DaemonSet。
//	                          完成後依 --sleep-after 待機，方便 kubectl cp 取出 PDF。
//	--mode=agent             : DaemonSet 端，提供 HTTP /data 端點回傳本機節點資訊。
//
// 所有時間統一以台灣當地時間呈現。
// 報告以 cluster 名稱識別來源，不再分 dev / staging / production 環境。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
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
	clusterName := flag.String("cluster-name", "", "顯示在報告上的 cluster 名稱。空字串 = 自動偵測 (kubeadm-config / namespace UID)")
	hcNamespace := flag.String("healthcheck-namespace", "", "k8s-healthcheck 系統部署所在 namespace；該 ns 下所有 k8s-healthcheck-* Pod 都會從報告中過濾。預設 = 自身 namespace")
	agentNamespace := flag.String("agent-namespace", "", "agent Pod 所在 namespace，預設 = aggregator 自身的 namespace")
	agentSelector := flag.String("agent-selector", "app=k8s-healthcheck-agent", "agent Pod 的 label selector")
	agentPort := flag.String("agent-port", "8080", "agent HTTP server port")
	agentTimeout := flag.Duration("agent-timeout", 10*time.Second, "向單一 agent 拉資料的逾時時間")

	// --- aggregator 動態 orchestrate 旗標 ---
	orchestrate := flag.Bool("orchestrate-agent", false, "啟動時動態建立 agent DaemonSet，收集完畢後刪除。CronJob 模式必須開啟")
	agentImage := flag.String("agent-image", os.Getenv("AGENT_IMAGE"), "agent DaemonSet 使用的容器 image。預設讀環境變數 AGENT_IMAGE")
	agentDSName := flag.String("agent-daemonset-name", "k8s-healthcheck-agent", "動態建立的 DaemonSet 名稱")
	agentReadyTimeout := flag.Duration("agent-ready-timeout", 2*time.Minute, "等待 DaemonSet 全部 Ready 的逾時時間")

	// --- agent 專用旗標 ---
	listen := flag.String("listen", ":8080", "agent HTTP server 監聽位址 (僅 agent 模式)")
	hostPrefix := flag.String("host-prefix", "/host", "host 根目錄在容器中的掛點 (僅 agent 模式)")
	flag.Parse()

	switch *mode {
	case "agent":
		runAgent(*listen, *hostPrefix)
	case "aggregator", "":
		runAggregator(aggregatorOpts{
			OutDir:               *outDir,
			Timeout:              *timeout,
			Kubeconfig:           *kubeconfig,
			PkiDir:               *pkiDir,
			SleepAfter:           *sleepAfter,
			ClusterName:          *clusterName,
			HealthcheckNamespace: *hcNamespace,
			AgentNamespace:       *agentNamespace,
			AgentSelector:        *agentSelector,
			AgentPort:            *agentPort,
			AgentTimeout:         *agentTimeout,
			Orchestrate:          *orchestrate,
			AgentImage:           *agentImage,
			AgentDaemonSetName:   *agentDSName,
			AgentReadyTimeout:    *agentReadyTimeout,
			AgentHostPrefix:      *hostPrefix,
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
	OutDir               string
	Timeout              time.Duration
	Kubeconfig           string
	PkiDir               string
	SleepAfter           time.Duration
	ClusterName          string
	HealthcheckNamespace string
	AgentNamespace       string
	AgentSelector        string
	AgentPort            string
	AgentTimeout         time.Duration

	Orchestrate        bool
	AgentImage         string
	AgentDaemonSetName string
	AgentReadyTimeout  time.Duration
	AgentHostPrefix    string
}

// runAggregator 是 CLI 的主流程。包成 wrapper 是為了讓 runAggregatorE 中的
// defer 在錯誤路徑也能跑到 (log.Fatalf 不會跑 defer，會殘留 DaemonSet)。
func runAggregator(o aggregatorOpts) {
	if err := runAggregatorE(o); err != nil {
		log.Fatalf("%v", err)
	}
}

// runAggregatorE 是 CLI 的主流程。在 CronJob 架構下流程為:
//  1. 動態建立 agent DaemonSet (若 --orchestrate-agent=true)
//  2. 等 DaemonSet 全部 Ready
//  3. 收集 cluster 與 agent 資料
//  4. advisor.Analyze 產出結論
//  5. 寫 PDF
//  6. 刪除 agent DaemonSet (defer，無論成功失敗都跑)
//  7. 依 --sleep-after 待機，讓使用者 kubectl cp 取出 PDF
func runAggregatorE(o aggregatorOpts) error {
	if err := os.MkdirAll(o.OutDir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), o.Timeout)
	defer cancel()

	c, err := collector.New(o.Kubeconfig, o.PkiDir)
	if err != nil {
		return fmt.Errorf("init collector: %w", err)
	}
	c.SetClusterName(o.ClusterName)
	c.SetHealthcheckNamespace(o.HealthcheckNamespace)
	c.Agents = collector.AgentDiscovery{
		Namespace:     o.AgentNamespace,
		LabelSelector: o.AgentSelector,
		Port:          o.AgentPort,
		Timeout:       o.AgentTimeout,
	}

	orch := collector.AgentOrchestration{
		Enabled:      o.Orchestrate,
		Image:        o.AgentImage,
		Name:         o.AgentDaemonSetName,
		ReadyTimeout: o.AgentReadyTimeout,
		HostPrefix:   o.AgentHostPrefix,
	}
	dsCleaned := false // 確保只執行一次刪除動作 (主流程提前刪 + defer 兜底)
	cleanupDS := func(reason string) {
		if !orch.Enabled || dsCleaned {
			return
		}
		dsCleaned = true
		cleanCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := c.DeleteAgentDaemonSet(cleanCtx, orch); err != nil {
			log.Printf("orchestrate cleanup (%s): %v", reason, err)
		}
	}

	if orch.Enabled {
		log.Printf("orchestrate: 開始建立 agent DaemonSet (image=%s)", orch.Image)
		if err := c.EnsureAgentDaemonSet(ctx, orch); err != nil {
			cleanupDS("ensure-failed")
			return fmt.Errorf("orchestrate agent: %w", err)
		}
		// 任何錯誤路徑都要刪掉 DS，避免 log.Fatalf 跳過 defer 殘留資源。
		defer cleanupDS("defer")
	}

	log.Println("開始收集 cluster 健檢資料...")
	rep := c.Collect(ctx)
	log.Printf("已抓到 %d 個 agent 節點資料", len(rep.NodeAgents))

	advisor.Analyze(rep)
	log.Printf("advisor: cluster=%s 整體=%s 發現=%d 建議=%d",
		rep.Cluster.Name, rep.Conclusion.OverallStatus,
		len(rep.Conclusion.Findings), len(rep.Conclusion.Recommendations))

	stamp := tz.Now().Format("20060102-150405")
	tag := safeTag(rep.Cluster.Name)
	if tag == "" {
		tag = rep.Cluster.Distribution
	}
	if tag == "" {
		tag = "k8s"
	}
	outPath := filepath.Join(o.OutDir, fmt.Sprintf("%s-health-%s.pdf", tag, stamp))

	if err := report.WritePDF(rep, outPath); err != nil {
		return fmt.Errorf("write pdf: %w", err)
	}

	log.Printf("報告已寫出: %s", outPath)
	if len(rep.Errors) > 0 {
		log.Printf("收集完成但有 %d 項非致命錯誤", len(rep.Errors))
		for _, e := range rep.Errors {
			log.Printf("  - %s", e)
		}
	}

	// 在進入 sleep 之前主動刪除 DaemonSet，讓 kubectl cp 期間沒有閒置 agent。
	cleanupDS("normal")

	if o.SleepAfter > 0 {
		ns, podName := c.SelfPod()
		if ns == "" {
			ns = "<ns>"
		}
		if podName == "" {
			podName = "<pod>"
		}
		log.Printf("多存活 %s，請以下列指令取出 PDF:", o.SleepAfter)
		log.Printf("  kubectl -n %s cp %s:%s ./%s",
			ns, podName, outPath, filepath.Base(outPath))
		time.Sleep(o.SleepAfter)
	}
	return nil
}

// safeTag 把 cluster 名稱轉成可安全用於檔名的字串 (替換非 ASCII 與
// 路徑字元為 '-')。長度上限 32 字元。
func safeTag(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return ""
	}
	var b strings.Builder
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	out := b.String()
	if len(out) > 32 {
		out = out[:32]
	}
	return out
}
