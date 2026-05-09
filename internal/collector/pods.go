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
	allPods := []model.PodOverview{}
	for i := range pods.Items {
		p := &pods.Items[i]
		// 排除 collector 自己 (含 agent DaemonSet Pod)。
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

		effPhase := effectivePhase(p)

		// 全部 Pod 的總覽: ns / name / phase / IP / node / hostPath。
		allPods = append(allPods, model.PodOverview{
			Namespace: p.Namespace,
			Name:      p.Name,
			Status:    effPhase,
			PodIP:     p.Status.PodIP,
			Node:      p.Spec.NodeName,
			HostPaths: extractHostPaths(p),
		})

		if isProblemPod(p) {
			var restarts int32
			for _, cs := range p.Status.ContainerStatuses {
				restarts += cs.RestartCount
			}
			reason, message := podProblemReason(p)
			problems = append(problems, model.PodInfo{
				Namespace: p.Namespace,
				Name:      p.Name,
				Status:    effPhase,
				Phase:     string(p.Status.Phase),
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
	// 排序: kube-system 一律排最後 (使用者通常先看自家 workload),
	// 其餘 ns 字典序; 同 ns 內按 Pod 名字字典序.
	sort.Slice(allPods, func(i, j int) bool {
		ai, aj := allPods[i].Namespace == "kube-system", allPods[j].Namespace == "kube-system"
		if ai != aj {
			return !ai // i 不是 kube-system → 排前面
		}
		if allPods[i].Namespace != allPods[j].Namespace {
			return allPods[i].Namespace < allPods[j].Namespace
		}
		return allPods[i].Name < allPods[j].Name
	})
	// 不截斷: 使用者要求把所有異常 Pod 都列出，PDF 自會分頁。
	r.PodSummary = sum
	r.ProblemPods = problems
	r.AllPods = allPods
	return nil
}

// extractHostPaths 從 Pod spec 抽出 hostPath volume 與其在容器內的掛載路徑。
//
// 流程: 先建立 volumeName→hostPath 對應表 (僅 hostPath 類型納入), 再走訪所有
// container (含 init container) 的 volumeMounts, 找出有引用到 hostPath volume
// 的條目, 紀錄 容器/掛載路徑/唯讀旗標。同一個 volume 被多個 container 掛時會
// 出多筆 (使用者通常想知道哪幾個 container 接觸到該本機目錄)。
//
// 為何走訪 init container: 有些工具型 Pod 只在 init 階段碰本機路徑 (例如安裝
// 套件), 漏掉會誤導讀者以為沒接觸到 host。
func extractHostPaths(p *corev1.Pod) []model.HostPathMount {
	if p == nil {
		return nil
	}
	hostVolumes := map[string]string{}
	for _, v := range p.Spec.Volumes {
		if v.HostPath != nil {
			hostVolumes[v.Name] = v.HostPath.Path
		}
	}
	if len(hostVolumes) == 0 {
		return nil
	}
	var out []model.HostPathMount
	collect := func(containers []corev1.Container) {
		for _, ct := range containers {
			for _, vm := range ct.VolumeMounts {
				host, ok := hostVolumes[vm.Name]
				if !ok {
					continue
				}
				out = append(out, model.HostPathMount{
					VolumeName: vm.Name,
					HostPath:   host,
					MountPath:  vm.MountPath,
					Container:  ct.Name,
					ReadOnly:   vm.ReadOnly,
				})
			}
		}
	}
	collect(p.Spec.InitContainers)
	collect(p.Spec.Containers)
	return out
}

// effectivePhase 回傳一個更貼近使用者直覺的 Pod 狀態字串。
//
// 原因: K8s 把 CrashLoopBackOff / ImagePullBackOff 等容器層級的故障歸在
// ContainerStatus.State.Waiting.Reason, 而 Pod.Status.Phase 仍可能是
// Running (至少一個 container 啟動過) 或 Pending (尚未啟動). 直接顯示 Phase
// 會讓使用者誤以為一個一直 crash 的 Pod 是健康的 Running.
//
// 規則 (越前面優先):
//  1. 若任一 (init 或 main) container 處於問題型 Waiting (CrashLoop /
//     ImagePullBackOff / ErrImagePull / Create*Error 等) → 直接回該 Reason
//  2. 若任一 container 上次終止為 OOMKilled → 回 "OOMKilled"
//  3. 否則回原本的 Phase 字串 (Running / Pending / Failed / Succeeded /
//     Unknown)
//
// 注意: 一般啟動中的 ContainerCreating / PodInitializing 等暫時 Waiting
// 不被視為問題, 仍照原 Phase 顯示, 避免新 Pod 的初始化階段被誤標。
func effectivePhase(p *corev1.Pod) string {
	if p == nil {
		return ""
	}
	phase := string(p.Status.Phase)
	all := make([]corev1.ContainerStatus, 0,
		len(p.Status.InitContainerStatuses)+len(p.Status.ContainerStatuses))
	all = append(all, p.Status.InitContainerStatuses...)
	all = append(all, p.Status.ContainerStatuses...)

	for _, cs := range all {
		if cs.State.Waiting != nil && isProblemWaitingReason(cs.State.Waiting.Reason) {
			return cs.State.Waiting.Reason
		}
	}
	for _, cs := range all {
		if t := cs.LastTerminationState.Terminated; t != nil && t.Reason == "OOMKilled" {
			return "OOMKilled"
		}
	}
	return phase
}

// isProblemWaitingReason 判定一個 ContainerStatus.State.Waiting.Reason 是否
// 屬於「需要使用者介入」的故障狀態 (而非啟動過程中的暫時等待)。與 isProblemPod
// 內的清單同步, 兩處都改時請一起更新。
func isProblemWaitingReason(reason string) bool {
	switch reason {
	case "CrashLoopBackOff",
		"ImagePullBackOff", "ErrImagePull",
		"CreateContainerConfigError", "CreateContainerError",
		"InvalidImageName", "RegistryUnavailable",
		"RunContainerError",
		"PostStartHookError", "PreStartHookError":
		return true
	}
	return false
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
