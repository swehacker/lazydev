package colima

import (
	"context"
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/swehacker/lazydev/internal/model"
	"github.com/swehacker/lazydev/internal/state"
)

const sample = `cpu  6380047 0 2575363 64829724 20826 0 385615 0 0 0
MemTotal:       16343420 kB
MemAvailable:    4917812 kB
/dev/root      102888095744 79170543616 23700774912  77% /
`

func TestParseUsage(t *testing.T) {
	u, err := parseUsage([]byte(sample))
	if err != nil {
		t.Fatal(err)
	}
	if u.memTotal != 16343420*1024 || u.memAvailable != 4917812*1024 {
		t.Errorf("mem = %d / %d", u.memTotal, u.memAvailable)
	}
	if u.diskTotal != 102888095744 || u.diskUsed != 79170543616 {
		t.Errorf("disk = %d / %d", u.diskUsed, u.diskTotal)
	}
	if u.cpu.idle != 64829724+20826 || u.cpu.total != 6380047+2575363+64829724+20826+385615 {
		t.Errorf("cpu = %+v", u.cpu)
	}
	if _, err := parseUsage([]byte("garbage")); err == nil {
		t.Error("expected error for unexpected output")
	}
}

func TestCPUPercent(t *testing.T) {
	if got := cpuPercent(cpuTimes{}, cpuTimes{total: 100, idle: 50}); got != -1 {
		t.Errorf("first sample should be unknown, got %v", got)
	}
	got := cpuPercent(cpuTimes{total: 1000, idle: 800}, cpuTimes{total: 2000, idle: 1550})
	if math.Abs(got-25) > 0.001 {
		t.Errorf("cpu percent = %v, want 25", got)
	}
}

func TestColimaUsageCombinesStatusAndSSH(t *testing.T) {
	s := &Source{store: state.New(), profile: "default"}
	calls := 0
	s.run = func(_ context.Context, name string, args ...string) ([]byte, error) {
		calls++
		if args[0] == "status" {
			return []byte(`{"cpu":4,"memory":17179869184,"disk":107374182400}`), nil
		}
		// Second ssh call advances only the user counter, so the interval is 100% busy.
		if calls > 2 {
			return []byte(strings.Replace(sample, "cpu  6380047", "cpu  6380547", 1) + "cpu-extra\n"), nil
		}
		return []byte(sample), nil
	}
	ctx := context.Background()
	e := s.colimaStatus(ctx)
	if !e.Available || e.CPUs != 4 {
		t.Fatalf("status = %+v", e)
	}
	e = s.colimaUsage(ctx, e)
	if e.CPUPercent != -1 || e.MemUsed == 0 || e.DiskPercent() < 76 || e.DiskPercent() > 78 {
		t.Errorf("first usage = %+v", e)
	}
	e = s.colimaUsage(ctx, e)
	if e.CPUPercent != 100 {
		t.Errorf("second usage cpu = %v", e.CPUPercent)
	}
}

func TestColimaUsageToleratesTransientSSHFailures(t *testing.T) {
	s := &Source{store: state.New(), profile: "default"}
	fail := false
	s.run = func(context.Context, string, ...string) ([]byte, error) {
		if fail {
			return nil, errors.New("signal: killed")
		}
		return []byte(sample), nil
	}
	ctx := context.Background()
	e := s.colimaUsage(ctx, model.EngineState{Available: true, CPUs: 4})
	fail = true
	for i := 1; i < maxUsageFailures; i++ {
		if e = s.colimaUsage(ctx, e); e.Err != "" || e.MemUsed == 0 {
			t.Fatalf("failure %d should keep the last sample, got %+v", i, e)
		}
	}
	if e = s.colimaUsage(ctx, e); e.Err == "" {
		t.Fatal("persistent failures should be reported")
	}
	fail = false
	if e = s.colimaUsage(ctx, e); e.Err != "" {
		t.Errorf("recovery should clear the error, got %q", e.Err)
	}
}

func TestDockerInfoFallback(t *testing.T) {
	s := New(state.New(), "unix:///var/run/docker.sock", func(context.Context) (int, int64, error) { return 8, 1 << 30, nil })
	if s.profile != "" {
		t.Fatalf("non-colima host should not use colima, got profile %q", s.profile)
	}
	e := s.dockerInfo(context.Background())
	if !e.Available || e.CPUs != 8 || e.Kind != "docker" {
		t.Errorf("engine = %+v", e)
	}
	s.info = func(context.Context) (int, int64, error) { return 0, 0, errors.New("down") }
	if e := s.dockerInfo(context.Background()); e.Available || e.Err != "down" {
		t.Errorf("engine = %+v", e)
	}
}
