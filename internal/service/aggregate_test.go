package service

import (
	"testing"
	"time"

	"github.com/swehacker/lazydev/internal/config"
	"github.com/swehacker/lazydev/internal/model"
)

const orderRef = "docker.io/library/order-service:local"

func orderPod(name string, ready bool, problem, imageID string) model.Pod {
	p := model.Pod{
		Namespace: "platform", Name: name, Labels: map[string]string{"app": "order-service"},
		Status: "Running", Total: 1, Problem: problem, Restarts: 2, Created: time.Unix(100, 0),
		Containers: []model.PodContainer{{Name: "app", Image: orderRef, ImageID: imageID}},
	}
	if ready {
		p.Ready = 1
	}
	return p
}

func baseSnapshot() model.Snapshot {
	return model.Snapshot{
		Kube: model.KubeState{
			Available: true,
			Deployments: []model.Deployment{{
				Namespace: "platform", Name: "order-service", Replicas: 2, Ready: 2,
				TemplateLabels: map[string]string{"app": "order-service"}, Selector: map[string]string{"app": "order-service"},
				Images: []string{"order-service:local"},
			}},
			Pods: []model.Pod{
				orderPod("order-service-1", true, "", "sha256:node"),
				orderPod("order-service-2", true, "", "sha256:node"),
				{Namespace: "other", Name: "other", Labels: map[string]string{"app": "order-service"}},
			},
			PodMetrics: map[string]model.Usage{"platform/order-service-1": {CPUMilli: 10, MemoryBytes: 100}},
		},
		Docker: model.DockerState{
			Available: true,
			Containers: []model.Container{
				{ID: "p1", Name: "infra-postgresql-1", State: "running", Health: "healthy", ComposeProject: "infra", ComposeService: "postgresql"},
				{ID: "s1", Name: "infra-setup-1", State: "exited", ExitCode: 0, ComposeProject: "infra", ComposeService: "setup"},
				{ID: "x1", Name: "other-postgresql-1", State: "exited", ExitCode: 1, ComposeProject: "other", ComposeService: "postgresql"},
			},
			Stats: map[string]model.Usage{"p1": {CPUMilli: 5, MemoryBytes: 50}},
		},
		Cluster: model.ClusterState{Found: true, Name: "dev"},
		Drift: model.DriftState{
			Local: map[string]model.ImageFingerprint{orderRef: {ID: "sha256:index", Layers: []string{"l1", "l2"}}},
			Nodes: map[string]model.NodeImages{"k3d-dev-server-0": {orderRef: {ID: "sha256:node", Layers: []string{"l1", "l2"}}}},
		},
	}
}

func cfgWith(services ...config.Service) *config.Config {
	return &config.Config{Compose: config.Compose{Project: "infra"}, Services: services}
}

var orderSvc = config.Service{
	Name: "order-service", Health: "https://api/order/health",
	K8s: &config.K8sRef{Namespace: "platform", Selector: "app=order-service"},
}

func TestAggregateHealthyK8sService(t *testing.T) {
	svcs := Aggregate(cfgWith(orderSvc), baseSnapshot())
	if len(svcs) != 1 {
		t.Fatalf("services = %d", len(svcs))
	}
	s := svcs[0]
	if s.Status != Healthy || s.Desired != 2 || s.Ready != 2 || s.Restarts != 4 || len(s.Pods) != 2 {
		t.Errorf("service = %+v", s)
	}
	if !s.HasUsage || s.Usage.CPUMilli != 10 {
		t.Errorf("usage = %+v", s.Usage)
	}
	if s.Image != orderRef || s.Drift != InSync {
		t.Errorf("image = %q drift = %v (%s)", s.Image, s.Drift, s.DriftDetail)
	}
}

func TestAggregateK8sStatuses(t *testing.T) {
	cases := map[string]struct {
		mutate func(*model.Snapshot)
		status Status
		reason string
	}{
		"partially ready": {func(s *model.Snapshot) { s.Kube.Deployments[0].Ready = 1 }, Degraded, "1/2 ready"},
		"none ready":      {func(s *model.Snapshot) { s.Kube.Deployments[0].Ready = 0 }, Down, "0/2 ready"},
		"crashloop with one ready": {func(s *model.Snapshot) {
			s.Kube.Deployments[0].Ready = 1
			s.Kube.Pods[1] = orderPod("order-service-2", false, "CrashLoopBackOff", "")
		}, Degraded, "CrashLoopBackOff"},
		"crashloop all down": {func(s *model.Snapshot) {
			s.Kube.Deployments[0].Ready = 0
			s.Kube.Pods[0] = orderPod("order-service-1", false, "CrashLoopBackOff", "")
		}, Down, "CrashLoopBackOff"},
		"scaled to zero": {func(s *model.Snapshot) {
			s.Kube.Deployments[0].Replicas, s.Kube.Deployments[0].Ready = 0, 0
		}, Down, "scaled to 0"},
		"not deployed": {func(s *model.Snapshot) {
			s.Kube.Deployments, s.Kube.Pods = nil, nil
		}, Down, "not deployed"},
		"cluster down": {func(s *model.Snapshot) { s.Kube = model.KubeState{} }, Unknown, "cluster unavailable"},
		"health failing": {func(s *model.Snapshot) {
			s.Health = map[string]model.HealthResult{"order-service": {OK: false, Detail: "503 Service Unavailable"}}
		}, Degraded, "health: 503 Service Unavailable"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			snap := baseSnapshot()
			tc.mutate(&snap)
			s := Aggregate(cfgWith(orderSvc), snap)[0]
			if s.Status != tc.status || s.Reason != tc.reason {
				t.Errorf("status = %v %q, want %v %q", s.Status, s.Reason, tc.status, tc.reason)
			}
		})
	}
}

func TestAggregateComposeServices(t *testing.T) {
	svcs := Aggregate(cfgWith(
		config.Service{Name: "postgres", Compose: "postgresql"},
		config.Service{Name: "setup", Compose: "setup"},
		config.Service{Name: "mailhog", Compose: "mailhog"},
	), baseSnapshot())
	if s := svcs[0]; s.Status != Healthy || len(s.Containers) != 1 || s.Usage.CPUMilli != 5 {
		t.Errorf("postgres = %+v (project filter must exclude other/postgresql)", s)
	}
	if s := svcs[1]; s.Status != Completed {
		t.Errorf("setup = %v", s.Status)
	}
	if s := svcs[2]; s.Status != Down || s.Reason != "container not found" {
		t.Errorf("mailhog = %v %q", s.Status, s.Reason)
	}
}

func TestAggregateAutoDiscovery(t *testing.T) {
	svcs := Aggregate(&config.Config{Compose: config.Compose{Project: "infra"}}, baseSnapshot())
	var names []string
	for _, s := range svcs {
		names = append(names, s.Name)
		if !s.Auto {
			t.Errorf("%s should be marked auto", s.Name)
		}
	}
	want := []string{"order-service", "postgresql", "setup"}
	if len(names) != len(want) {
		t.Fatalf("names = %v, want %v", names, want)
	}
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("names = %v, want %v", names, want)
		}
	}
	if svcs[0].Status != Healthy || svcs[0].Drift != InSync {
		t.Errorf("auto order-service = %v drift %v", svcs[0].Status, svcs[0].Drift)
	}
}

func TestClassifyDrift(t *testing.T) {
	pods := []model.Pod{orderPod("p", true, "", "sha256:node")}
	cases := map[string]struct {
		mutate   func(*model.DriftState, *[]model.Pod)
		explicit bool
		want     Drift
	}{
		"in sync":                {func(*model.DriftState, *[]model.Pod) {}, false, InSync},
		"missing local":          {func(d *model.DriftState, _ *[]model.Pod) { d.Local = nil }, true, Missing},
		"registry image ignored": {func(d *model.DriftState, _ *[]model.Pod) { d.Local = nil }, false, DriftNA},
		"not imported": {func(d *model.DriftState, _ *[]model.Pod) {
			d.Nodes["k3d-dev-agent-0"] = model.NodeImages{}
		}, false, NotImported},
		"stale import": {func(d *model.DriftState, _ *[]model.Pod) {
			d.Local = map[string]model.ImageFingerprint{orderRef: {Layers: []string{"l1", "l3"}}}
		}, false, StaleImport},
		"not rolled out": {func(_ *model.DriftState, p *[]model.Pod) {
			(*p)[0].Containers[0].ImageID = "sha256:old"
		}, false, NotRolledOut},
		"repo digest is not compared": {func(_ *model.DriftState, p *[]model.Pod) {
			(*p)[0].Containers[0].ImageID = "docker.io/library/order-service@sha256:other"
		}, false, InSync},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			d := baseSnapshot().Drift
			p := append([]model.Pod(nil), pods...)
			p[0].Containers = append([]model.PodContainer(nil), pods[0].Containers...)
			tc.mutate(&d, &p)
			if got, detail := ClassifyDrift(orderRef, tc.explicit, d, p, true); got != tc.want {
				t.Errorf("drift = %v (%s), want %v", got, detail, tc.want)
			}
		})
	}
	if got, _ := ClassifyDrift(orderRef, false, baseSnapshot().Drift, pods, false); got != DriftNA {
		t.Errorf("without cluster drift should be n/a, got %v", got)
	}
}

func TestProblemsOrdersBySeverity(t *testing.T) {
	snap := baseSnapshot()
	snap.Kube.Deployments[0].Ready = 0
	snap.Kube.Nodes = []model.Node{{Name: "n1", Ready: true, Pressure: []string{"DiskPressure"}}}
	snap.Engine = model.EngineState{Name: "colima", DiskTotal: 100, DiskUsed: 85}
	snap.Health = map[string]model.HealthResult{"pg": {OK: false, Detail: "refused"}}
	svcs := Aggregate(cfgWith(orderSvc, config.Service{Name: "pg", Compose: "postgresql", Health: "tcp://x:1"}), snap)

	got := Problems(svcs, snap)
	want := []string{"order-service: 0/2 ready", "pg: health: refused", "node n1: DiskPressure", "colima disk: 85% used"}
	if len(got) != len(want) {
		t.Fatalf("problems = %v", got)
	}
	for i := range want {
		if got[i].String() != want[i] {
			t.Errorf("problem[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestRollupIgnoresUnknown(t *testing.T) {
	if got := rollup(component{Unknown, "x"}, component{Healthy, ""}); got.status != Healthy {
		t.Errorf("rollup = %+v", got)
	}
	if got := rollup(component{Unknown, "cluster unavailable"}); got.status != Unknown || got.reason != "cluster unavailable" {
		t.Errorf("rollup = %+v", got)
	}
	if got := rollup(component{Completed, "completed"}, component{Healthy, ""}); got.status != Healthy {
		t.Errorf("healthy should outrank completed: %+v", got)
	}
}
