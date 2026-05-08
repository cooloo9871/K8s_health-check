// Package model 定義 collector 與 report 之間共享的純資料型別。
// 這個套件刻意不引入 k8s.io/* 之類的 client 相依，確保 report 端可以
// 獨立被測試。
package model

import "time"

// Report 是 PDF 產生器所需的完整資料集。所有區段資料都掛在這個結構上。
type Report struct {
	GeneratedAt time.Time
	Cluster     ClusterInfo
	Nodes       []NodeInfo
	NodeMetrics []NodeMetric
	PodSummary  PodSummary
	ProblemPods []PodInfo
	TopCPU      []PodMetric
	TopMemory   []PodMetric
	Workloads   WorkloadSummary
	Storage     StorageSummary
	Events      []EventInfo
	Components  []ComponentStatus
	APIHealth   []APIHealth
	// Certs 為彙整後的憑證清單 (含 K8s pki、kubelet、etcd)。Source 欄位區分來源。
	Certs []CertInfo
	// NodeAgents 為各節點 DaemonSet agent 回報的本機資料 (磁碟、kubelet 憑證等)。
	NodeAgents []NodeAgentData
	Errors     []string
	Conclusion Conclusion
}

// Conclusion 存放由 advisor 產生的摘要結論，會渲染在 PDF 最前面的章節。
// 由 internal/advisor 套件填值。
type Conclusion struct {
	Environment     string           // 自動判斷或 --env 指定 (dev/staging/production)
	EnvironmentAuto bool             // true = 由 cluster 狀態推論，false = 使用者指定
	OverallStatus   string           // 健康 / 警告 / 嚴重
	Summary         string           // 一段話總結
	Findings        []Finding        // 主要發現
	Recommendations []Recommendation // 建議事項
}

type Finding struct {
	Severity string // 嚴重 / 警告 / 資訊
	Category string // 節點 / Pod / 工作負載 / 儲存 / 控制平面 / 憑證 / 事件
	Title    string
	Detail   string
}

type Recommendation struct {
	Priority  string // 高 / 中 / 低
	Category  string
	Action    string
	Rationale string
}

type ClusterInfo struct {
	Version       string
	Platform      string
	NodeCount     int
	NamespaceCnt  int
	TotalPods     int
	Distribution  string
}

type NodeInfo struct {
	Name           string
	Roles          string
	Status         string
	KubeletVersion string
	OSImage        string
	Kernel         string
	Runtime        string
	InternalIP     string
	Architecture   string
	Age            string
	Taints         int
	PodCount       int
	Conditions     []NodeCondition
}

type NodeCondition struct {
	Type    string
	Status  string
	Reason  string
	Message string
}

type NodeMetric struct {
	Name        string
	CPUUsed     string
	CPUCapacity string
	CPUPercent  float64
	MemUsed     string
	MemCapacity string
	MemPercent  float64
	PodCount    int
	PodCapacity int
}

type PodSummary struct {
	Total     int
	Running   int
	Pending   int
	Succeeded int
	Failed    int
	Unknown   int
}

type PodInfo struct {
	Namespace string
	Name      string
	Status    string
	Restarts  int32
	Node      string
	Age       string
	Reason    string
	Message   string
}

type PodMetric struct {
	Namespace  string
	Name       string
	Container  string
	CPU        string
	CPUMillis  int64
	Memory     string
	MemoryMiB  int64
}

type WorkloadSummary struct {
	Deployments  WorkloadStats
	DaemonSets   WorkloadStats
	StatefulSets WorkloadStats
	ReplicaSets  WorkloadStats
	Jobs         WorkloadStats
	CronJobs     int
	Unhealthy    []WorkloadIssue
}

type WorkloadStats struct {
	Total int
	Ready int
}

type WorkloadIssue struct {
	Kind      string
	Namespace string
	Name      string
	Desired   int32
	Ready     int32
	Reason    string
}

type StorageSummary struct {
	PVs            int
	PVsBound       int
	PVsAvailable   int
	PVsReleased    int
	PVsFailed      int
	PVCs           int
	PVCsBound      int
	PVCsPending    int
	StorageClasses []string
	ProblemPVCs    []PVCInfo
}

type PVCInfo struct {
	Namespace string
	Name      string
	Status    string
	Capacity  string
	Class     string
}

type EventInfo struct {
	LastSeen  time.Time
	Type      string
	Reason    string
	Object    string
	Namespace string
	Message   string
	Count     int32
}

type ComponentStatus struct {
	Name    string
	Healthy string
	Message string
}

type APIHealth struct {
	Endpoint string
	Status   string
	Detail   string
}

// CertInfo 為單張憑證的彙總資料。
//   - Source 欄位區分來源類別: "k8s-pki" / "kubelet" / "etcd" / "kubeconfig" 等
//   - Node 欄位代表這張憑證從哪個節點上採到 (DaemonSet 模式才會有意義)
type CertInfo struct {
	Path     string
	Subject  string
	NotAfter time.Time
	DaysLeft int
	Status   string
	Source   string
	Node     string
}

// NodeAgentData 是 DaemonSet 端 agent 回傳給 aggregator 的單一節點資料。
type NodeAgentData struct {
	NodeName    string
	CollectedAt time.Time
	Disks       []DiskInfo
	Certs       []CertInfo // kubelet + (若該節點是 control-plane) k8s pki 全部憑證
	Errors      []string
}

// DiskInfo 描述節點上單一掛點的容量資訊。
type DiskInfo struct {
	MountPoint string  // 例如 "/", "/var/lib/kubelet"
	Filesystem string  // 例如 "ext4", "xfs", "overlay"
	Total      uint64  // bytes
	Used       uint64  // bytes
	Avail      uint64  // bytes
	Percent    float64 // 0~100
	Status     string  // OK / WARN / CRITICAL
}
