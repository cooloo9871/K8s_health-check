package collector

import (
	"context"
	"sort"

	"k8s-health-check/internal/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func (c *Collector) collectPods(ctx context.Context, r *model.Report) error {
	pods, err := c.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	sum := model.PodSummary{}
	problems := []model.PodInfo{}
	for _, p := range pods.Items {
		sum.Total++
		switch p.Status.Phase {
		case corev1.PodRunning:
			sum.Running++
		case corev1.PodPending:
			sum.Pending++
		case corev1.PodSucceeded:
			sum.Succeeded++
		case corev1.PodFailed:
			sum.Failed++
		default:
			sum.Unknown++
		}

		if isProblemPod(&p) {
			var restarts int32
			for _, cs := range p.Status.ContainerStatuses {
				restarts += cs.RestartCount
			}
			reason, message := podProblemReason(&p)
			problems = append(problems, model.PodInfo{
				Namespace: p.Namespace,
				Name:      p.Name,
				Status:    string(p.Status.Phase),
				Restarts:  restarts,
				Node:      p.Spec.NodeName,
				Age:       humanAge(p.CreationTimestamp.Time),
				Reason:    reason,
				Message:   truncate(message, 80),
			})
		}
	}

	sort.Slice(problems, func(i, j int) bool {
		if problems[i].Restarts != problems[j].Restarts {
			return problems[i].Restarts > problems[j].Restarts
		}
		if problems[i].Namespace != problems[j].Namespace {
			return problems[i].Namespace < problems[j].Namespace
		}
		return problems[i].Name < problems[j].Name
	})
	if len(problems) > 60 {
		problems = problems[:60]
	}
	r.PodSummary = sum
	r.ProblemPods = problems
	return nil
}

func isProblemPod(p *corev1.Pod) bool {
	switch p.Status.Phase {
	case corev1.PodFailed, corev1.PodPending, corev1.PodUnknown:
		return true
	}
	for _, cs := range p.Status.ContainerStatuses {
		if !cs.Ready && p.Status.Phase == corev1.PodRunning {
			return true
		}
		if cs.RestartCount >= 5 {
			return true
		}
		if cs.State.Waiting != nil {
			r := cs.State.Waiting.Reason
			if r == "CrashLoopBackOff" || r == "ImagePullBackOff" || r == "ErrImagePull" || r == "CreateContainerConfigError" {
				return true
			}
		}
	}
	return false
}

func podProblemReason(p *corev1.Pod) (string, string) {
	if p.Status.Reason != "" {
		return p.Status.Reason, p.Status.Message
	}
	for _, cs := range p.Status.ContainerStatuses {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
			return cs.State.Waiting.Reason, cs.State.Waiting.Message
		}
		if cs.State.Terminated != nil && cs.State.Terminated.Reason != "" {
			return cs.State.Terminated.Reason, cs.State.Terminated.Message
		}
	}
	for _, cond := range p.Status.Conditions {
		if cond.Status != corev1.ConditionTrue && cond.Reason != "" {
			return cond.Reason, cond.Message
		}
	}
	return "", ""
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "..."
}
