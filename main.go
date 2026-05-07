package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/brobridge/k8s-health-check/internal/collector"
	"github.com/brobridge/k8s-health-check/internal/report"
)

func main() {
	outDir := flag.String("out", "/reports", "directory to write the PDF report")
	timeout := flag.Duration("timeout", 5*time.Minute, "overall collection timeout")
	kubeconfig := flag.String("kubeconfig", "", "optional path to kubeconfig (when running outside cluster)")
	pkiDir := flag.String("pki-dir", "/host/etc/kubernetes/pki", "directory to scan for kubeadm-style certificates (optional)")
	sleepAfter := flag.Duration("sleep-after", 0, "after writing the PDF, sleep for this long so an operator can kubectl cp the file")
	flag.Parse()

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		log.Fatalf("create output dir: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	c, err := collector.New(*kubeconfig, *pkiDir)
	if err != nil {
		log.Fatalf("init collector: %v", err)
	}

	log.Println("collecting cluster health data...")
	rep := c.Collect(ctx)

	stamp := time.Now().Format("20060102-150405")
	clusterTag := rep.Cluster.Distribution
	if clusterTag == "" {
		clusterTag = "k8s"
	}
	outPath := filepath.Join(*outDir, fmt.Sprintf("%s-health-%s.pdf", clusterTag, stamp))

	if err := report.WritePDF(rep, outPath); err != nil {
		log.Fatalf("write pdf: %v", err)
	}

	log.Printf("report written to %s", outPath)
	if len(rep.Errors) > 0 {
		log.Printf("collection completed with %d non-fatal errors", len(rep.Errors))
		for _, e := range rep.Errors {
			log.Printf("  - %s", e)
		}
	}

	if *sleepAfter > 0 {
		log.Printf("staying alive for %s — fetch the report with:", *sleepAfter)
		log.Printf("  kubectl -n <ns> cp <pod>:%s ./report.pdf", outPath)
		time.Sleep(*sleepAfter)
	}
}
