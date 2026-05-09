package collector

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// TestBuildAgentDaemonSet 驗證動態產生的 DaemonSet 物件結構, 不能漏掉
// agent 必要的旗標 / 掛點 / 容器 / labels。
func TestBuildAgentDaemonSet(t *testing.T) {
	o := AgentOrchestration{
		Image: "registry.example.com/healthcheck:v1",
	}.withDefaults()

	ds := buildAgentDaemonSet("k8s-healthcheck", o)

	if ds.Name != "k8s-healthcheck-agent" {
		t.Errorf("DS Name = %q, want k8s-healthcheck-agent", ds.Name)
	}
	if got := ds.Labels["app"]; got != "k8s-healthcheck-agent" {
		t.Errorf("DS label app = %q, want k8s-healthcheck-agent", got)
	}
	if ds.Spec.Selector == nil || ds.Spec.Selector.MatchLabels["app"] != "k8s-healthcheck-agent" {
		t.Errorf("DS selector wrong: %+v", ds.Spec.Selector)
	}
	pod := ds.Spec.Template.Spec
	if len(pod.Containers) != 1 {
		t.Fatalf("expected 1 container, got %d", len(pod.Containers))
	}
	con := pod.Containers[0]
	if con.Image != o.Image {
		t.Errorf("container image = %q, want %q", con.Image, o.Image)
	}
	args := strings.Join(con.Args, " ")
	if !strings.Contains(args, "--mode=agent") {
		t.Errorf("args missing --mode=agent: %q", args)
	}
	if !strings.Contains(args, "--listen=:8080") {
		t.Errorf("args missing --listen: %q", args)
	}
	if !strings.Contains(args, "--host-prefix=/host") {
		t.Errorf("args missing --host-prefix: %q", args)
	}

	// host root 唯讀掛入
	foundMount := false
	for _, vm := range con.VolumeMounts {
		if vm.MountPath == "/host" {
			foundMount = true
			if !vm.ReadOnly {
				t.Errorf("host root mount must be read-only")
			}
		}
	}
	if !foundMount {
		t.Errorf("host root volume mount not found")
	}
	foundVol := false
	for _, v := range pod.Volumes {
		if v.Name == "host-root" && v.HostPath != nil && v.HostPath.Path == "/" {
			foundVol = true
		}
	}
	if !foundVol {
		t.Errorf("host-root hostPath volume not found")
	}

	// 容器以 root 啟動 + drop ALL caps + readOnlyRootFilesystem
	if pod.SecurityContext == nil || pod.SecurityContext.RunAsUser == nil || *pod.SecurityContext.RunAsUser != 0 {
		t.Errorf("agent container must run as UID 0 to read kubelet 0600 keys")
	}
	if con.SecurityContext == nil ||
		con.SecurityContext.ReadOnlyRootFilesystem == nil ||
		!*con.SecurityContext.ReadOnlyRootFilesystem {
		t.Errorf("agent container should have readOnlyRootFilesystem=true")
	}
	if con.SecurityContext.Capabilities == nil ||
		len(con.SecurityContext.Capabilities.Drop) != 1 ||
		con.SecurityContext.Capabilities.Drop[0] != corev1.Capability("ALL") {
		t.Errorf("agent container should drop ALL capabilities, got %+v", con.SecurityContext.Capabilities)
	}

	// 必要的 downward API env
	wantEnv := map[string]string{
		"NODE_NAME":     "spec.nodeName",
		"POD_NAME":      "metadata.name",
		"POD_NAMESPACE": "metadata.namespace",
	}
	for name, fp := range wantEnv {
		found := false
		for _, e := range con.Env {
			if e.Name == name {
				if e.ValueFrom == nil || e.ValueFrom.FieldRef == nil || e.ValueFrom.FieldRef.FieldPath != fp {
					t.Errorf("env %s wrong fieldRef: %+v", name, e.ValueFrom)
				}
				found = true
			}
		}
		if !found {
			t.Errorf("env var %s missing", name)
		}
	}
}

// TestIsNodeReady 驗證 Ready 條件判定: 只有 Ready=True 才算 Ready,
// Ready=False / Unknown / 完全沒有 Ready 條件都視為 NotReady.
func TestIsNodeReady(t *testing.T) {
	cases := []struct {
		name string
		conds []corev1.NodeCondition
		want  bool
	}{
		{"Ready=True", []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionTrue}}, true},
		{"Ready=False", []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionFalse}}, false},
		{"Ready=Unknown", []corev1.NodeCondition{{Type: corev1.NodeReady, Status: corev1.ConditionUnknown}}, false},
		{"沒有 Ready 條件", []corev1.NodeCondition{{Type: corev1.NodeMemoryPressure, Status: corev1.ConditionFalse}}, false},
		{"空條件清單", nil, false},
		{"Ready=True 與其他壓力共存", []corev1.NodeCondition{
			{Type: corev1.NodeMemoryPressure, Status: corev1.ConditionTrue},
			{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
		}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := corev1.Node{
				ObjectMeta: metav1.ObjectMeta{Name: "n"},
				Status:     corev1.NodeStatus{Conditions: tc.conds},
			}
			if got := isNodeReady(n); got != tc.want {
				t.Errorf("isNodeReady = %v, want %v", got, tc.want)
			}
		})
	}
}
