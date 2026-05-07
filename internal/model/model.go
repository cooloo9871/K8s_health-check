package model

import "time"

// Report is the full data set rendered by the PDF generator.
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
	Certs       []CertInfo
	Errors      []string
	Conclusion  Conclusion
}

// Conclusion holds the advisor-generated summary that is rendered at the
// top of the PDF.  Populated by internal/advisor.
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

type CertInfo struct {
	Path     string
	Subject  string
	NotAfter time.Time
	DaysLeft int
	Status   string
}
