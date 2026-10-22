package docker

import (
	"slices"
	"testing"

	"github.com/docker/docker/api/types/container"

	"github.com/swehacker/lazydev/internal/model"
)

func TestConvertContainersParsesStatus(t *testing.T) {
	got := convertContainers([]container.Summary{
		{ID: "bbbbbbbbbbbbbbbb", Names: []string{"/kafka"}, State: "running", Status: "Up 3 hours (unhealthy)",
			Labels: map[string]string{labelComposeProject: "infra", labelComposeService: "kafka"},
			Ports:  []container.Port{{PublicPort: 9092, PrivatePort: 9092, Type: "tcp"}, {PublicPort: 9092, PrivatePort: 9092, Type: "tcp"}}},
		{ID: "aaaaaaaaaaaaaaaa", Names: []string{"/setup"}, State: "exited", Status: "Exited (0) 2 hours ago"},
		{ID: "cccccccccccccccc", Names: []string{"/db"}, State: "running", Status: "Up 1 minute (health: starting)"},
	})
	if len(got) != 3 || got[0].Name != "db" || got[1].Name != "kafka" || got[2].Name != "setup" {
		t.Fatalf("unexpected order/names: %+v", got)
	}
	if got[0].Health != "starting" || got[1].Health != "unhealthy" {
		t.Errorf("health = %q, %q", got[0].Health, got[1].Health)
	}
	if got[1].ComposeProject != "infra" || got[1].ComposeService != "kafka" || len(got[1].Ports) != 1 {
		t.Errorf("compose/ports = %+v", got[1])
	}
	if got[2].ExitCode != 0 || got[2].Running() {
		t.Errorf("setup = %+v", got[2])
	}
}

func TestDeriveCluster(t *testing.T) {
	node := func(name, cluster, role, state string) model.Container {
		return model.Container{Name: name, State: state, Labels: map[string]string{labelK3dCluster: cluster, labelK3dRole: role}}
	}
	containers := []model.Container{
		node("k3d-dev-server-0", "dev", "server", "running"),
		node("k3d-dev-agent-0", "dev", "agent", "running"),
		node("k3d-dev-agent-1", "dev", "agent", "exited"),
		node("k3d-dev-serverlb", "dev", "loadbalancer", "running"),
		{Name: "postgres", State: "running"},
	}

	cs := deriveCluster(containers, "")
	if !cs.Found || cs.Name != "dev" || cs.Servers != 1 || cs.ServersRunning != 1 || cs.Agents != 2 || cs.AgentsRunning != 1 {
		t.Fatalf("cluster = %+v", cs)
	}
	if len(cs.NodeContainers) != 2 || cs.NodeContainers[0] != "k3d-dev-agent-0" {
		t.Errorf("node containers = %v", cs.NodeContainers)
	}

	if cs := deriveCluster(containers, "other"); cs.Found || cs.Name != "other" {
		t.Errorf("missing cluster should not be found: %+v", cs)
	}
	two := append(slices.Clone(containers), node("k3d-x-server-0", "x", "server", "running"))
	if cs := deriveCluster(two, ""); cs.Found {
		t.Errorf("ambiguous auto-detection should not pick a cluster: %+v", cs)
	}
}

func TestCPUAndMemoryFromStats(t *testing.T) {
	var s container.StatsResponse
	s.CPUStats.CPUUsage.TotalUsage = 3_000
	s.PreCPUStats.CPUUsage.TotalUsage = 1_000
	s.CPUStats.SystemUsage = 20_000
	s.PreCPUStats.SystemUsage = 10_000
	s.CPUStats.OnlineCPUs = 4
	if got := cpuMilli(s); got != 800 {
		t.Errorf("cpu = %v millicores, want 800", got)
	}
	s.MemoryStats.Usage = 1000
	s.MemoryStats.Stats = map[string]uint64{"inactive_file": 300}
	if got := memoryUsage(s); got != 700 {
		t.Errorf("memory = %d, want 700", got)
	}
}

func TestParseCrictlOutput(t *testing.T) {
	refs, err := parseCrictlImages([]byte(`{"images":[{"id":"sha256:n1","repoTags":["docker.io/library/order-service:local"]},{"id":"sha256:n2","repoTags":[]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if refs["docker.io/library/order-service:local"] != "sha256:n1" || len(refs) != 1 {
		t.Errorf("refs = %v", refs)
	}

	single := `{"status":{"id":"sha256:n1"},"info":{"imageSpec":{"created":"2026-10-13T14:46:33Z","rootfs":{"diff_ids":["sha256:l1","sha256:l2"]}}}}`
	for _, data := range []string{single, "[" + single + "," + single + "]"} {
		got, err := parseCrictlInspect([]byte(data))
		if err != nil {
			t.Fatal(err)
		}
		if got[0].Status.ID != "sha256:n1" || len(got[0].Info.ImageSpec.RootFS.DiffIDs) != 2 {
			t.Errorf("inspect = %+v", got[0])
		}
	}
}
