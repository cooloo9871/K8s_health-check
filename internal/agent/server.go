// Package agent 是 DaemonSet 模式下執行的 HTTP 伺服。
//
// 每個 Kubernetes 節點上會跑一個 Pod，這支 server 在 :8080 提供 /data
// 端點返回該節點的 NodeAgentData (磁碟、kubelet 憑證、Control-plane 憑證...)。
// aggregator 端會在產生報告時主動拉取所有 agent 的 /data 並彙整。
//
// 此套件刻意不引入 client-go，因為 agent 不需要對 API server 發 request。
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"k8s-health-check/internal/model"
	"k8s-health-check/internal/tz"
)

// Config 控制 agent server 的執行參數。所有欄位都可以由 main.go 注入。
type Config struct {
	// Listen 是 HTTP server 綁的位址，例如 ":8080"。
	Listen string
	// HostPrefix 是 host 根目錄在容器中的掛點，預設 "/host"。
	HostPrefix string
	// NodeName 為這個節點的名字 (來自 downward API spec.nodeName)。
	NodeName string
}

// Run 啟動 agent HTTP server。會封鎖至 ctx 取消。
func Run(ctx context.Context, cfg Config) error {
	if cfg.Listen == "" {
		cfg.Listen = ":8080"
	}
	if cfg.HostPrefix == "" {
		cfg.HostPrefix = "/host"
	}
	if cfg.NodeName == "" {
		cfg.NodeName = strings.TrimSpace(os.Getenv("NODE_NAME"))
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	mux.HandleFunc("/data", func(w http.ResponseWriter, _ *http.Request) {
		data := collect(cfg)
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		_ = json.NewEncoder(w).Encode(data)
	})

	srv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	log.Printf("agent: listening on %s, node=%s, hostPrefix=%s",
		cfg.Listen, cfg.NodeName, cfg.HostPrefix)

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.ListenAndServe()
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
		return nil
	case err := <-errCh:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

// collect 集中執行 agent 端所有資料收集邏輯，把結果包成 NodeAgentData。
// 收集子項目失敗會把訊息塞到 Errors，不中斷整體流程。
func collect(cfg Config) model.NodeAgentData {
	d := model.NodeAgentData{
		NodeName:    cfg.NodeName,
		CollectedAt: tz.Now(),
	}
	d.Disks = CollectDisks(cfg.HostPrefix)
	d.Certs = CollectCerts(DefaultCertScanPaths(cfg.HostPrefix), cfg.HostPrefix, cfg.NodeName)
	return d
}
