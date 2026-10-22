// Package model holds the plain domain types shared by sources, aggregation and the UI.
// SDK types never leave the source packages.
package model

import "time"

// Snapshot is the full observed state. Sources replace whole sub-structures,
// never mutate them in place, so copying a Snapshot by value is safe.
type Snapshot struct {
	Docker  DockerState
	Kube    KubeState
	Engine  EngineState
	Cluster ClusterState
	Health  map[string]HealthResult // keyed by logical service name
	Drift   DriftState
	History map[string][]Sample // keyed by HistoryKey
}

type DockerState struct {
	Available  bool
	Err        string
	Containers []Container
	Images     []Image
	Stats      map[string]Usage // keyed by container ID
}

type Container struct {
	ID             string
	Name           string
	Image          string
	ImageID        string
	State          string // running, exited, created, restarting, paused, dead
	Status         string // human status from Docker, e.g. "Up 3 hours (healthy)"
	Health         string // healthy, unhealthy, starting, or ""
	ExitCode       int
	Created        time.Time
	Labels         map[string]string
	ComposeProject string
	ComposeService string
	Ports          []string
}

func (c Container) Running() bool { return c.State == "running" }

type Image struct {
	ID       string
	Tags     []string
	Size     int64
	Created  time.Time
	Dangling bool
	InUse    int // number of containers using the image
}

type KubeState struct {
	Available        bool
	Err              string
	Context          string
	MetricsErr       string
	Pods             []Pod
	Deployments      []Deployment
	Nodes            []Node
	Events           []Event
	Releases         []Release
	PodMetrics       map[string]Usage // keyed by namespace/name
	NodeMetrics      map[string]Usage // keyed by node name
	MetricsAvailable bool
}

type Pod struct {
	Namespace  string
	Name       string
	Labels     map[string]string
	Phase      string
	Status     string // kubectl-style display status, e.g. CrashLoopBackOff
	Problem    string // non-empty when the pod needs attention
	Ready      int
	Total      int
	Restarts   int
	Created    time.Time
	Node       string
	OwnerKind  string
	OwnerName  string
	Containers []PodContainer
}

func (p Pod) Key() string { return p.Namespace + "/" + p.Name }

type PodContainer struct {
	Name                  string
	Image                 string
	ImageID               string
	Ready                 bool
	State                 string
	Reason                string
	Restarts              int
	LastTerminationReason string
	Init                  bool
}

type Deployment struct {
	Namespace      string
	Name           string
	Labels         map[string]string
	TemplateLabels map[string]string
	Selector       map[string]string
	Replicas       int32
	Ready          int32
	Updated        int32
	Available      int32
	Created        time.Time
	Images         []string
}

func (d Deployment) Key() string { return d.Namespace + "/" + d.Name }

type Node struct {
	Name        string
	Ready       bool
	Roles       []string
	Version     string
	Pressure    []string // MemoryPressure, DiskPressure, PIDPressure when true
	CPUMilli    int64    // allocatable
	MemoryBytes int64    // allocatable
	Created     time.Time
}

type Event struct {
	Namespace string
	Kind      string // involved object kind
	Name      string // involved object name
	Type      string // Normal, Warning
	Reason    string
	Message   string
	Count     int32
	Last      time.Time
}

type Release struct {
	Name       string
	Namespace  string
	Chart      string
	AppVersion string
	Status     string
	Revision   int
	Updated    time.Time
	Notes      string
}

type Usage struct {
	CPUMilli    float64 // millicores
	MemoryBytes int64
	MemoryLimit int64 // 0 when unknown
}

// EngineState describes the Docker engine host, typically the Colima VM.
type EngineState struct {
	Available  bool
	Err        string
	Kind       string // colima, docker
	Name       string
	CPUs       int
	CPUPercent float64 // -1 when unknown
	MemTotal   int64
	MemUsed    int64 // 0 when unknown
	DiskTotal  int64
	DiskUsed   int64 // 0 when unknown
}

func (e EngineState) DiskPercent() float64 {
	if e.DiskTotal == 0 || e.DiskUsed == 0 {
		return -1
	}
	return float64(e.DiskUsed) / float64(e.DiskTotal) * 100
}

type ClusterState struct {
	Name           string
	Found          bool
	Servers        int
	ServersRunning int
	Agents         int
	AgentsRunning  int
	NodeContainers []string // container names of k3d server/agent nodes
}

type HealthResult struct {
	Target  string
	OK      bool
	Detail  string // e.g. "200 OK" or error text
	Latency time.Duration
	Checked time.Time
}

// DriftState holds image fingerprints inside the k3d nodes, keyed by normalized image reference.
type DriftState struct {
	Err   string
	Local map[string]ImageFingerprint // from the Docker engine
	Nodes map[string]NodeImages       // node container name -> images
}

type NodeImages map[string]ImageFingerprint

type ImageFingerprint struct {
	ID      string   // image ID as seen by the runtime (config digest on nodes)
	Layers  []string // rootfs diff IDs
	Created time.Time
}

// Sample is one point in a CPU/memory history.
type Sample struct {
	At          time.Time
	CPUMilli    float64
	MemoryBytes int64
}

func HistoryKeyPod(ns, name string) string { return "pod:" + ns + "/" + name }
func HistoryKeyContainer(id string) string { return "ctr:" + id }
func HistoryKeyNode(name string) string    { return "node:" + name }

const HistoryKeyEngine = "engine"
