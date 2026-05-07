package collector

import (
	"context"
	"fmt"
	"sort"

	"github.com/brobridge/k8s-health-check/internal/model"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (c *Collector) collectWorkloads(ctx context.Context, r *model.Report) error {
	w := model.WorkloadSummary{}
	issues := []model.WorkloadIssue{}

	deps, err := c.clientset.AppsV1().Deployments("").List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, d := range deps.Items {
			w.Deployments.Total++
			desired := int32(0)
			if d.Spec.Replicas != nil {
				desired = *d.Spec.Replicas
			}
			if d.Status.ReadyReplicas == desired {
				w.Deployments.Ready++
			} else {
				issues = append(issues, model.WorkloadIssue{
					Kind: "Deployment", Namespace: d.Namespace, Name: d.Name,
					Desired: desired, Ready: d.Status.ReadyReplicas,
					Reason: deploymentReason(d.Status.Conditions),
				})
			}
		}
	} else {
		c.addErr("deployments", err)
	}

	dss, err := c.clientset.AppsV1().DaemonSets("").List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, d := range dss.Items {
			w.DaemonSets.Total++
			if d.Status.NumberReady == d.Status.DesiredNumberScheduled && d.Status.NumberUnavailable == 0 {
				w.DaemonSets.Ready++
			} else {
				issues = append(issues, model.WorkloadIssue{
					Kind: "DaemonSet", Namespace: d.Namespace, Name: d.Name,
					Desired: d.Status.DesiredNumberScheduled, Ready: d.Status.NumberReady,
					Reason: fmt.Sprintf("unavailable=%d misscheduled=%d", d.Status.NumberUnavailable, d.Status.NumberMisscheduled),
				})
			}
		}
	} else {
		c.addErr("daemonsets", err)
	}

	sts, err := c.clientset.AppsV1().StatefulSets("").List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, s := range sts.Items {
			w.StatefulSets.Total++
			desired := int32(0)
			if s.Spec.Replicas != nil {
				desired = *s.Spec.Replicas
			}
			if s.Status.ReadyReplicas == desired {
				w.StatefulSets.Ready++
			} else {
				issues = append(issues, model.WorkloadIssue{
					Kind: "StatefulSet", Namespace: s.Namespace, Name: s.Name,
					Desired: desired, Ready: s.Status.ReadyReplicas,
				})
			}
		}
	} else {
		c.addErr("statefulsets", err)
	}

	rss, err := c.clientset.AppsV1().ReplicaSets("").List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, rs := range rss.Items {
			desired := int32(0)
			if rs.Spec.Replicas != nil {
				desired = *rs.Spec.Replicas
			}
			if desired == 0 {
				continue
			}
			w.ReplicaSets.Total++
			if rs.Status.ReadyReplicas == desired {
				w.ReplicaSets.Ready++
			}
		}
	}

	jobs, err := c.clientset.BatchV1().Jobs("").List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, j := range jobs.Items {
			w.Jobs.Total++
			if j.Status.Failed > 0 {
				issues = append(issues, model.WorkloadIssue{
					Kind: "Job", Namespace: j.Namespace, Name: j.Name,
					Desired: 1, Ready: j.Status.Succeeded,
					Reason: fmt.Sprintf("failed=%d", j.Status.Failed),
				})
				continue
			}
			if j.Status.Succeeded > 0 || j.Status.Active == 0 {
				w.Jobs.Ready++
			}
		}
	}

	cj, err := c.clientset.BatchV1().CronJobs("").List(ctx, metav1.ListOptions{})
	if err == nil {
		w.CronJobs = len(cj.Items)
	}

	sort.Slice(issues, func(i, j int) bool {
		if issues[i].Kind != issues[j].Kind {
			return issues[i].Kind < issues[j].Kind
		}
		if issues[i].Namespace != issues[j].Namespace {
			return issues[i].Namespace < issues[j].Namespace
		}
		return issues[i].Name < issues[j].Name
	})
	if len(issues) > 50 {
		issues = issues[:50]
	}
	w.Unhealthy = issues
	r.Workloads = w
	return nil
}

func deploymentReason(conds []appsv1.DeploymentCondition) string {
	for _, c := range conds {
		if c.Status == corev1.ConditionFalse && c.Reason != "" {
			return c.Reason
		}
	}
	for _, c := range conds {
		if c.Type == appsv1.DeploymentProgressing && c.Reason != "" {
			return c.Reason
		}
	}
	return ""
}
