package collector

import (
	"context"
	"strings"

	"github.com/brobridge/k8s-health-check/internal/model"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (c *Collector) collectCluster(ctx context.Context, r *model.Report) error {
	v, err := c.clientset.Discovery().ServerVersion()
	if err == nil {
		r.Cluster.Version = v.GitVersion
		r.Cluster.Platform = v.Platform
		r.Cluster.Distribution = detectDistribution(v.GitVersion)
	}

	nodes, err := c.clientset.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err == nil {
		r.Cluster.NodeCount = len(nodes.Items)
	}

	ns, err := c.clientset.CoreV1().Namespaces().List(ctx, metav1.ListOptions{})
	if err == nil {
		r.Cluster.NamespaceCnt = len(ns.Items)
	}

	pods, err := c.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err == nil {
		r.Cluster.TotalPods = len(pods.Items)
	}
	return nil
}

// detectDistribution tries to infer the K8s flavour from the GitVersion
// string. Used purely for tagging the PDF filename — never branches behaviour.
func detectDistribution(gitVer string) string {
	v := strings.ToLower(gitVer)
	switch {
	case strings.Contains(v, "k3s"):
		return "k3s"
	case strings.Contains(v, "rke2"):
		return "rke2"
	case strings.Contains(v, "eks"):
		return "eks"
	case strings.Contains(v, "gke"):
		return "gke"
	case strings.Contains(v, "aks"):
		return "aks"
	case strings.Contains(v, "openshift"), strings.Contains(v, "ocp"):
		return "openshift"
	default:
		return "k8s"
	}
}
