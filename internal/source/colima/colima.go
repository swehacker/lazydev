// Package colima observes the Docker engine host: the Colima VM when available,
// otherwise the totals reported by `docker info`.
package colima

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/swehacker/lazydev/internal/model"
	"github.com/swehacker/lazydev/internal/state"
)

const (
	usageInterval  = 5 * time.Second
	statusInterval = 30 * time.Second
	commandTimeout = 10 * time.Second
	usageTimeout   = 4 * time.Second
	// maxUsageFailures consecutive usage failures are reported as an engine error.
	maxUsageFailures = 3
	usageScript      = `head -1 /proc/stat; grep -E '^(MemTotal|MemAvailable):' /proc/meminfo; df -B1 /var/lib/docker 2>/dev/null | tail -1`
)

var profileRe = regexp.MustCompile(`/\.colima/([^/]+)/docker\.sock`)

// InfoFunc reports engine totals (CPUs, memory bytes) when Colima is not in use.
type InfoFunc func(ctx context.Context) (cpus int, memBytes int64, err error)

type Source struct {
	store   *state.Store
	profile string
	info    InfoFunc
	run     func(ctx context.Context, name string, args ...string) ([]byte, error)

	prevCPU       cpuTimes
	usageFailures int
}

// New observes the Colima profile behind dockerHost, or falls back to info when
// the host is not a Colima socket or the colima CLI is missing.
func New(store *state.Store, dockerHost string, info InfoFunc) *Source {
	s := &Source{store: store, info: info, run: runCommand}
	if m := profileRe.FindStringSubmatch(dockerHost); m != nil {
		if _, err := exec.LookPath("colima"); err == nil {
			s.profile = m[1]
		}
	}
	return s
}

func runCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, commandTimeout)
	defer cancel()
	var stderr bytes.Buffer
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%s: %s", name, msg)
		}
		return nil, err
	}
	return out, nil
}

func (s *Source) Run(ctx context.Context) {
	usage := time.NewTicker(usageInterval)
	defer usage.Stop()
	var engine model.EngineState
	var lastStatus time.Time
	for {
		if s.profile == "" {
			engine = s.dockerInfo(ctx)
		} else {
			if time.Since(lastStatus) >= statusInterval || !engine.Available {
				engine = s.colimaStatus(ctx)
				lastStatus = time.Now()
			}
			if engine.Available {
				engine = s.colimaUsage(ctx, engine)
			}
		}
		if ctx.Err() != nil {
			return
		}
		e := engine
		s.store.Apply(func(snap *model.Snapshot) { snap.Engine = e })
		if e.CPUPercent >= 0 && e.Available {
			s.store.Apply(state.AppendHistory(map[string]model.Sample{
				model.HistoryKeyEngine: {At: time.Now(), CPUMilli: e.CPUPercent / 100 * float64(e.CPUs) * 1000, MemoryBytes: e.MemUsed},
			}, model.HistoryKeyEngine, false))
		}
		select {
		case <-ctx.Done():
			return
		case <-usage.C:
		}
	}
}

func (s *Source) dockerInfo(ctx context.Context) model.EngineState {
	e := model.EngineState{Kind: "docker", Name: "docker", CPUPercent: -1}
	if s.info == nil {
		e.Err = "no engine info"
		return e
	}
	cpus, mem, err := s.info(ctx)
	if err != nil {
		e.Err = err.Error()
		return e
	}
	e.Available, e.CPUs, e.MemTotal = true, cpus, mem
	return e
}

type colimaStatus struct {
	CPU    int   `json:"cpu"`
	Memory int64 `json:"memory"`
	Disk   int64 `json:"disk"`
}

func (s *Source) colimaStatus(ctx context.Context) model.EngineState {
	name := "colima"
	if s.profile != "default" {
		name += ":" + s.profile
	}
	e := model.EngineState{Kind: "colima", Name: name, CPUPercent: -1}
	out, err := s.run(ctx, "colima", "status", "--json", "--profile", s.profile)
	if err != nil {
		e.Err = "colima not running: " + err.Error()
		return e
	}
	var st colimaStatus
	if err := json.Unmarshal(out, &st); err != nil {
		e.Err = "parse colima status: " + err.Error()
		return e
	}
	e.Available, e.CPUs, e.MemTotal, e.DiskTotal = true, st.CPU, st.Memory, st.Disk
	return e
}

func (s *Source) colimaUsage(ctx context.Context, e model.EngineState) model.EngineState {
	ctx, cancel := context.WithTimeout(ctx, usageTimeout)
	defer cancel()
	out, err := s.run(ctx, "colima", "ssh", "--profile", s.profile, "--", "sh", "-c", usageScript)
	if err != nil {
		// colima ssh occasionally hangs; keep the last sample unless it keeps failing.
		if s.usageFailures++; s.usageFailures >= maxUsageFailures {
			e.Err = "colima ssh: " + err.Error()
		}
		return e
	}
	s.usageFailures = 0
	u, err := parseUsage(out)
	if err != nil {
		e.Err = err.Error()
		return e
	}
	e.Err = ""
	e.CPUPercent = cpuPercent(s.prevCPU, u.cpu)
	s.prevCPU = u.cpu
	if u.memTotal > 0 {
		e.MemTotal, e.MemUsed = u.memTotal, u.memTotal-u.memAvailable
	}
	if u.diskTotal > 0 {
		e.DiskTotal, e.DiskUsed = u.diskTotal, u.diskUsed
	}
	return e
}

type cpuTimes struct{ total, idle uint64 }

type vmUsage struct {
	cpu                    cpuTimes
	memTotal, memAvailable int64
	diskTotal, diskUsed    int64
}

func parseUsage(out []byte) (vmUsage, error) {
	var u vmUsage
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 0 {
			continue
		}
		switch {
		case fields[0] == "cpu":
			for i, f := range fields[1:] {
				v, _ := strconv.ParseUint(f, 10, 64)
				u.cpu.total += v
				if i == 3 || i == 4 { // idle, iowait
					u.cpu.idle += v
				}
			}
		case fields[0] == "MemTotal:" && len(fields) >= 2:
			u.memTotal = kib(fields[1])
		case fields[0] == "MemAvailable:" && len(fields) >= 2:
			u.memAvailable = kib(fields[1])
		case strings.HasPrefix(fields[0], "/") && len(fields) >= 4:
			u.diskTotal, _ = strconv.ParseInt(fields[1], 10, 64)
			u.diskUsed, _ = strconv.ParseInt(fields[2], 10, 64)
		}
	}
	if u.cpu.total == 0 && u.memTotal == 0 {
		return u, fmt.Errorf("unexpected VM usage output: %q", strings.TrimSpace(string(out)))
	}
	return u, nil
}

func kib(s string) int64 {
	v, _ := strconv.ParseInt(s, 10, 64)
	return v * 1024
}

// cpuPercent returns busy CPU percent between two samples, or -1 without a previous sample.
func cpuPercent(prev, cur cpuTimes) float64 {
	if prev.total == 0 || cur.total <= prev.total {
		return -1
	}
	dTotal := float64(cur.total - prev.total)
	dIdle := float64(cur.idle - prev.idle)
	return (1 - dIdle/dTotal) * 100
}
