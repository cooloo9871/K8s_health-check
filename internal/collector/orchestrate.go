// orchestrate.go: aggregator 在 CronJob 模式下動態建立 / 等待 / 刪除
// 短命的 agent DaemonSet。流程:
//   1. EnsureAgentDaemonSet: 用 client-go 建立 DaemonSet。若 ns 中已有同名
//      DS (前一輪殘留 / 部署模式重疊) 直接重用，避免衝突。
//   2. WaitAgentDaemonSetReady: 輪詢直到 DesiredNumberScheduled == NumberReady
//      或 ctx 逾時。
//   3. (collectAgents 在主流程被呼叫，會列舉並 HTTP 拉資料)
//   4. DeleteAgentDaemonSet: foreground propagation 把 DS 與其 Pod 一併清掉。
//
// 此檔案僅在 aggregator 模式且 --orchestrate-agent=true 時有用，agent 模式
// 不會被觸發。
package collector

import (
	"context"
	"fmt"
	"log"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

// AgentOrchestration 控制 aggregator 端是否要動態建立 / 刪除 agent DaemonSet。
type AgentOrchestration struct {
	// Enabled = true 時 aggregator 啟動會建 DS、跑完會刪 DS。
	Enabled bool

	// Image 是 agent DaemonSet 的容器 image。為空時 aggregator 不會動 DS。
	Image string

	// Name 是 DaemonSet 名稱，預設 "k8s-healthcheck-agent"。
	Name string

	// LabelSelector 對應 spec.selector 與 pod labels。預設 "app=k8s-healthcheck-agent"。
	// 該 label 也是 collectAgents 用來找這些 Pod 的依據。
	Labels map[string]string

	// HostPrefix 是 host 根掛入容器的位置。預設 /host。
	HostPrefix string

	// Port 是 agent 容器監聽的 port。預設 8080。
	Port int32

	// ReadyTimeout 是等待 DS 全部 Ready 的逾時時間。預設 2 分鐘。
	ReadyTimeout time.Duration

	// PullPolicy 控制容器影像拉取策略。預設 IfNotPresent (與部署環境一致)。
	PullPolicy corev1.PullPolicy

	// ServiceAccount 是 agent Pod 用來與 API server 互動的 SA (掛載 token，
	// 雖然 agent 端目前沒實際呼叫 API)。預設沿用 aggregator 的 SA。
	ServiceAccount string
}

// withDefaults 補上 AgentOrchestration 的預設值。
func (o AgentOrchestration) withDefaults() AgentOrchestration {
	if o.Name == "" {
		o.Name = "k8s-healthcheck-agent"
	}
	if len(o.Labels) == 0 {
		o.Labels = map[string]string{"app": "k8s-healthcheck-agent"}
	}
	if o.HostPrefix == "" {
		o.HostPrefix = "/host"
	}
	if o.Port == 0 {
		o.Port = 8080
	}
	if o.ReadyTimeout == 0 {
		o.ReadyTimeout = 2 * time.Minute
	}
	if o.PullPolicy == "" {
		o.PullPolicy = corev1.PullIfNotPresent
	}
	if o.ServiceAccount == "" {
		o.ServiceAccount = "k8s-healthcheck"
	}
	return o
}

// EnsureAgentDaemonSet 建立 (或重用) agent DaemonSet 並阻塞至所有節點 Pod
// 進入 Ready 狀態。回傳 nil 代表 DS 已建好且全 Ready。
func (c *Collector) EnsureAgentDaemonSet(ctx context.Context, o AgentOrchestration) error {
	o = o.withDefaults()
	if !o.Enabled {
		return nil
	}
	if o.Image == "" {
		return fmt.Errorf("agent image not set; pass --agent-image or AGENT_IMAGE env")
	}
	ns := c.healthcheckNamespace
	if ns == "" {
		return fmt.Errorf("healthcheck namespace unknown; aggregator must run inside k8s-healthcheck namespace")
	}

	ds := buildAgentDaemonSet(ns, o)
	created, err := c.clientset.AppsV1().DaemonSets(ns).Create(ctx, ds, metav1.CreateOptions{})
	switch {
	case err == nil:
		log.Printf("orchestrate: 已建立 DaemonSet %s/%s (image=%s)", ns, created.Name, o.Image)
	case k8serrors.IsAlreadyExists(err):
		// 通常代表前一輪掃描異常結束未刪乾淨。直接沿用; 等 Ready 即可。
		log.Printf("orchestrate: DaemonSet %s/%s 已存在，沿用既有資源", ns, o.Name)
	default:
		return fmt.Errorf("create DaemonSet: %w", err)
	}

	return c.waitAgentDaemonSetReady(ctx, ns, o)
}

// waitAgentDaemonSetReady 輪詢 DS 直到 NumberReady == DesiredNumberScheduled
// (且 DesiredNumberScheduled > 0) 或 ReadyTimeout 屆滿。
func (c *Collector) waitAgentDaemonSetReady(ctx context.Context, ns string, o AgentOrchestration) error {
	deadline := time.Now().Add(o.ReadyTimeout)
	pollInterval := 2 * time.Second
	lastDesired := int32(-1)
	lastReady := int32(-1)
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("等待 DaemonSet Ready 被取消: %w", ctx.Err())
		default:
		}
		cur, err := c.clientset.AppsV1().DaemonSets(ns).Get(ctx, o.Name, metav1.GetOptions{})
		if err == nil {
			d := cur.Status.DesiredNumberScheduled
			r := cur.Status.NumberReady
			if d != lastDesired || r != lastReady {
				log.Printf("orchestrate: DS %s/%s 進度 %d/%d Ready", ns, o.Name, r, d)
				lastDesired, lastReady = d, r
			}
			if d > 0 && r >= d {
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("等待 DaemonSet %s/%s 全部 Ready 逾時 (%s, 最後 %d/%d)",
				ns, o.Name, o.ReadyTimeout, lastReady, lastDesired)
		}
		time.Sleep(pollInterval)
	}
}

// DeleteAgentDaemonSet 以 foreground propagation 刪除 DS，連帶把所有 agent
// Pod 也清掉。對找不到的 DS (已被刪過) 視為成功。
func (c *Collector) DeleteAgentDaemonSet(ctx context.Context, o AgentOrchestration) error {
	o = o.withDefaults()
	if !o.Enabled {
		return nil
	}
	ns := c.healthcheckNamespace
	if ns == "" {
		return nil
	}
	// Background: 不阻塞 runner，K8s GC 非同步清掉 Pod。runner 之後會睡眠
	// 等待 kubectl cp，期間 GC 自然完成。
	bg := metav1.DeletePropagationBackground
	err := c.clientset.AppsV1().DaemonSets(ns).Delete(ctx, o.Name, metav1.DeleteOptions{
		PropagationPolicy: &bg,
	})
	if err == nil {
		log.Printf("orchestrate: 已刪除 DaemonSet %s/%s", ns, o.Name)
		return nil
	}
	if k8serrors.IsNotFound(err) {
		return nil
	}
	return fmt.Errorf("delete DaemonSet: %w", err)
}

// labelSelectorString 把 Labels map 轉成 "k=v,k=v" 形式給 collectAgents 使用。
func (o AgentOrchestration) labelSelectorString() string {
	parts := make([]string, 0, len(o.Labels))
	for k, v := range o.Labels {
		parts = append(parts, fmt.Sprintf("%s=%s", k, v))
	}
	return strings.Join(parts, ",")
}

// buildAgentDaemonSet 構造 agent DaemonSet 的完整 spec。
//
// 設計重點:
//   - 容忍所有 taint，確保能落到 control-plane / GPU 節點。
//   - hostPath 唯讀掛入整個 host /，agent 自己挑 /etc/kubernetes/pki、
//     /var/lib/kubelet/pki 等子路徑。
//   - 為了能讀 0600 權限的私鑰檔 (kubelet-client-current.pem 等)，agent
//     以 runAsUser: 0 + drop ALL caps + readOnlyRootFilesystem 執行。
//   - 提供 readiness/liveness probe 讓 aggregator 等 Ready 訊號可信。
func buildAgentDaemonSet(ns string, o AgentOrchestration) *appsv1.DaemonSet {
	rootUser := int64(0)
	allowPriv := false
	readOnlyFs := true
	hostPathDir := corev1.HostPathDirectory

	cpuReq := resource.MustParse("25m")
	memReq := resource.MustParse("48Mi")
	cpuLim := resource.MustParse("200m")
	memLim := resource.MustParse("128Mi")

	return &appsv1.DaemonSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      o.Name,
			Namespace: ns,
			Labels:    o.Labels,
			Annotations: map[string]string{
				"k8s-healthcheck.bigred/managed-by": "aggregator",
				"k8s-healthcheck.bigred/created-at": time.Now().UTC().Format(time.RFC3339),
			},
		},
		Spec: appsv1.DaemonSetSpec{
			Selector: &metav1.LabelSelector{MatchLabels: o.Labels},
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{Labels: o.Labels},
				Spec: corev1.PodSpec{
					ServiceAccountName: o.ServiceAccount,
					Tolerations:        []corev1.Toleration{{Operator: corev1.TolerationOpExists}},
					SecurityContext: &corev1.PodSecurityContext{
						RunAsUser:  &rootUser,
						RunAsGroup: &rootUser,
						SeccompProfile: &corev1.SeccompProfile{
							Type: corev1.SeccompProfileTypeRuntimeDefault,
						},
					},
					Containers: []corev1.Container{
						{
							Name:            "agent",
							Image:           o.Image,
							ImagePullPolicy: o.PullPolicy,
							Args: []string{
								"--mode=agent",
								fmt.Sprintf("--listen=:%d", o.Port),
								"--host-prefix=" + o.HostPrefix,
							},
							Env: []corev1.EnvVar{
								{Name: "NODE_NAME", ValueFrom: &corev1.EnvVarSource{
									FieldRef: &corev1.ObjectFieldSelector{FieldPath: "spec.nodeName"},
								}},
								{Name: "POD_NAME", ValueFrom: &corev1.EnvVarSource{
									FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.name"},
								}},
								{Name: "POD_NAMESPACE", ValueFrom: &corev1.EnvVarSource{
									FieldRef: &corev1.ObjectFieldSelector{FieldPath: "metadata.namespace"},
								}},
							},
							Ports: []corev1.ContainerPort{{
								Name: "http", ContainerPort: o.Port, Protocol: corev1.ProtocolTCP,
							}},
							ReadinessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/healthz",
										Port: intstr.FromString("http"),
									},
								},
								PeriodSeconds:  5,
								TimeoutSeconds: 2,
							},
							LivenessProbe: &corev1.Probe{
								ProbeHandler: corev1.ProbeHandler{
									HTTPGet: &corev1.HTTPGetAction{
										Path: "/healthz",
										Port: intstr.FromString("http"),
									},
								},
								PeriodSeconds:  20,
								TimeoutSeconds: 3,
							},
							Resources: corev1.ResourceRequirements{
								Requests: corev1.ResourceList{
									corev1.ResourceCPU:    cpuReq,
									corev1.ResourceMemory: memReq,
								},
								Limits: corev1.ResourceList{
									corev1.ResourceCPU:    cpuLim,
									corev1.ResourceMemory: memLim,
								},
							},
							VolumeMounts: []corev1.VolumeMount{{
								Name:             "host-root",
								MountPath:        o.HostPrefix,
								ReadOnly:         true,
								MountPropagation: hostToContainerPtr(),
							}},
							SecurityContext: &corev1.SecurityContext{
								AllowPrivilegeEscalation: &allowPriv,
								ReadOnlyRootFilesystem:   &readOnlyFs,
								Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
							},
						},
					},
					Volumes: []corev1.Volume{{
						Name: "host-root",
						VolumeSource: corev1.VolumeSource{
							HostPath: &corev1.HostPathVolumeSource{
								Path: "/",
								Type: &hostPathDir,
							},
						},
					}},
				},
			},
		},
	}
}

func hostToContainerPtr() *corev1.MountPropagationMode {
	m := corev1.MountPropagationHostToContainer
	return &m
}
