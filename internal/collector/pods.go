package collector

import (
	"context"
	"fmt"
	"sort"

	"k8s-health-check/internal/model"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// collectPods 走訪所有 Pod，產出 PodSummary 統計與 ProblemPods 列表。
// collector 自身的 Pod 會被排除在所有計數與列表之外，避免報告自我汙染。
func (c *Collector) collectPods(ctx context.Context, r *model.Report) error {
	pods, err := c.clientset.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	sum := model.PodSummary{}
	problems := []model.PodInfo{}
	for _, p := range pods.Items {
		// 排除 collector 自己。
		if c.isSelf(p.Namespace, p.Name) {
			continue
		}
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
	// 不截斷: 使用者要求把所有異常 Pod 都列出，PDF 自會分頁。
	r.PodSummary = sum
	r.ProblemPods = problems
	return nil
}

// isProblemPod 判斷 Pod 是否進入「問題清單」。涵蓋常見故障模式:
// Failed / Pending / Unknown phase、容器未 Ready、任何重啟、典型 Waiting
// reason、上次終止為 OOMKilled / Error / 非 0 exit code，以及 init container
// 失敗。設計上偏寬鬆抓多抓滿，符合「有 crash 等異常都抓出來」的需求。
func isProblemPod(p *corev1.Pod) bool {
	switch p.Status.Phase {
	case corev1.PodFailed, corev1.PodPending, corev1.PodUnknown:
		return true
	}
	if hasAbnormalContainer(p.Status.ContainerStatuses, p.Status.Phase, false) {
		return true
	}
	if hasAbnormalContainer(p.Status.InitContainerStatuses, p.Status.Phase, true) {
		return true
	}
	return false
}

// hasAbnormalContainer 判斷一組 ContainerStatus 中是否有任一 container 進入
// 應被視為異常的狀態。isInit=true 時表示是 init container，那麼即便 Pod 已是
// Running 也要看 init 是否一直卡住。
func hasAbnormalContainer(statuses []corev1.ContainerStatus, phase corev1.PodPhase, isInit bool) bool {
	for _, cs := range statuses {
		// 非 Ready 的長壽容器
		if !cs.Ready && phase == corev1.PodRunning && !isInit {
			return true
		}
		// 任何重啟都列為異常: 雖然偶發 1 次重啟可能是部署時,
		// 但使用者要求把 crash 通通抓出來，所以採寬鬆策略。
		if cs.RestartCount > 0 {
			return true
		}
		if cs.State.Waiting != nil {
			switch cs.State.Waiting.Reason {
			case "CrashLoopBackOff", "ImagePullBackOff", "ErrImagePull",
				"CreateContainerConfigError", "InvalidImageName",
				"RegistryUnavailable", "RunContainerError",
				"PostStartHookError", "PreStartHookError":
				return true
			}
		}
		// 上次終止異常: OOMKilled / Error / 非 0 exit
		if t := cs.LastTerminationState.Terminated; t != nil {
			switch t.Reason {
			case "OOMKilled", "Error", "ContainerCannotRun", "DeadlineExceeded":
				return true
			}
			if t.ExitCode != 0 && t.Reason != "Completed" {
				return true
			}
		}
	}
	return false
}

// podProblemReason 萃取 Pod 失敗的最具參考價值的 reason / message。
// 順序: Pod.Status.Reason > 容器 Waiting > 容器 LastTermination (OOMKilled
// 等只在這裡才有) > 容器 Terminated > Pod.Conditions。涵蓋 init 容器。
func podProblemReason(p *corev1.Pod) (string, string) {
	if p.Status.Reason != "" {
		return p.Status.Reason, p.Status.Message
	}
	all := append([]corev1.ContainerStatus{}, p.Status.ContainerStatuses...)
	all = append(all, p.Status.InitContainerStatuses...)
	for _, cs := range all {
		if cs.State.Waiting != nil && cs.State.Waiting.Reason != "" {
			return cs.State.Waiting.Reason, cs.State.Waiting.Message
		}
	}
	for _, cs := range all {
		if t := cs.LastTerminationState.Terminated; t != nil && t.Reason != "" {
			msg := t.Message
			if msg == "" {
				msg = fmt.Sprintf("exit %d", t.ExitCode)
			}
			return t.Reason, msg
		}
	}
	for _, cs := range all {
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
