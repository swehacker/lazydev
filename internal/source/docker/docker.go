// Package docker observes the Docker engine: containers, images, stats, k3d nodes and node images.
package docker

import (
	"context"
	"encoding/json"
	"slices"
	"sync"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/events"
	"github.com/docker/docker/api/types/filters"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"

	"github.com/swehacker/lazydev/internal/model"
	"github.com/swehacker/lazydev/internal/state"
)

const (
	retryInterval       = 5 * time.Second
	statsInterval       = 5 * time.Second
	fingerprintInterval = 30 * time.Second
	safetyRefresh       = 15 * time.Second
	refreshDebounce     = 300 * time.Millisecond
	statsParallelism    = 8
)

type Source struct {
	cli        *client.Client
	store      *state.Store
	k3dCluster string

	mu         sync.Mutex
	containers []model.Container
	images     []model.Image

	// Fingerprint caches keyed by image ID; images are immutable so entries never go stale.
	fpMu    sync.Mutex
	localFP map[string]model.ImageFingerprint
	nodeFP  map[string]model.ImageFingerprint
}

func New(store *state.Store, k3dCluster string) (*Source, error) {
	opts := []client.Opt{client.FromEnv, client.WithAPIVersionNegotiation()}
	if host := ResolveHost(); host != "" {
		opts = append(opts, client.WithHost(host))
	}
	cli, err := client.NewClientWithOpts(opts...)
	if err != nil {
		return nil, err
	}
	return &Source{
		cli:        cli,
		store:      store,
		k3dCluster: k3dCluster,
		localFP:    map[string]model.ImageFingerprint{},
		nodeFP:     map[string]model.ImageFingerprint{},
	}, nil
}

// Client exposes the SDK client for on-demand operations (logs, inspect).
func (s *Source) Client() *client.Client { return s.cli }

func (s *Source) Run(ctx context.Context) {
	for ctx.Err() == nil {
		if _, err := s.cli.Ping(ctx); err != nil {
			s.setUnavailable(err)
			sleep(ctx, retryInterval)
			continue
		}
		err := s.watch(ctx)
		if ctx.Err() != nil {
			return
		}
		s.setUnavailable(err)
		sleep(ctx, retryInterval)
	}
}

func (s *Source) setUnavailable(err error) {
	msg := "connection lost"
	if err != nil {
		msg = err.Error()
	}
	s.store.Apply(func(snap *model.Snapshot) {
		snap.Docker = model.DockerState{Available: false, Err: msg}
		snap.Cluster = model.ClusterState{Name: s.k3dCluster}
	})
}

// watch refreshes on Docker events until the event stream fails.
func (s *Source) watch(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	f := filters.NewArgs(filters.Arg("type", string(events.ContainerEventType)), filters.Arg("type", string(events.ImageEventType)))
	msgs, errs := s.cli.Events(ctx, events.ListOptions{Filters: f})

	imagesChanged := make(chan struct{}, 1)
	if err := s.refresh(ctx, imagesChanged); err != nil {
		return err
	}
	go s.statsLoop(ctx)
	go s.fingerprintLoop(ctx, imagesChanged)

	debounce := time.NewTimer(time.Hour)
	debounce.Stop()
	safety := time.NewTicker(safetyRefresh)
	defer safety.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-errs:
			return err
		case m := <-msgs:
			if isNoise(m) {
				continue
			}
			debounce.Reset(refreshDebounce)
		case <-debounce.C:
			if err := s.refresh(ctx, imagesChanged); err != nil {
				return err
			}
		case <-safety.C:
			if err := s.refresh(ctx, imagesChanged); err != nil {
				return err
			}
		}
	}
}

// isNoise filters out high-frequency events (exec probes, healthchecks) that do not change listings.
func isNoise(m events.Message) bool {
	switch m.Action {
	case events.ActionExecCreate, events.ActionExecStart, events.ActionExecDie, events.ActionAttach, events.ActionResize:
		return true
	}
	return false
}

func (s *Source) refresh(ctx context.Context, imagesChanged chan<- struct{}) error {
	rawContainers, err := s.cli.ContainerList(ctx, container.ListOptions{All: true})
	if err != nil {
		return err
	}
	rawImages, err := s.cli.ImageList(ctx, image.ListOptions{})
	if err != nil {
		return err
	}
	containers := convertContainers(rawContainers)
	images := convertImages(rawImages)
	cluster := deriveCluster(containers, s.k3dCluster)

	s.mu.Lock()
	changed := !sameImageIDs(s.images, images) || !slices.Equal(clusterNodes(s.containers, s.k3dCluster), cluster.NodeContainers)
	s.containers, s.images = containers, images
	s.mu.Unlock()

	s.store.Apply(func(snap *model.Snapshot) {
		stats := snap.Docker.Stats
		snap.Docker = model.DockerState{Available: true, Containers: containers, Images: images, Stats: stats}
		snap.Cluster = cluster
	})
	if changed {
		select {
		case imagesChanged <- struct{}{}:
		default:
		}
	}
	return nil
}

func clusterNodes(containers []model.Container, name string) []string {
	return deriveCluster(containers, name).NodeContainers
}

func sameImageIDs(a, b []model.Image) bool {
	if len(a) != len(b) {
		return false
	}
	ids := make(map[string][]string, len(a))
	for _, img := range a {
		ids[img.ID] = img.Tags
	}
	for _, img := range b {
		tags, ok := ids[img.ID]
		if !ok || !slices.Equal(tags, img.Tags) {
			return false
		}
	}
	return true
}

func (s *Source) fingerprintLoop(ctx context.Context, imagesChanged <-chan struct{}) {
	ticker := time.NewTicker(fingerprintInterval)
	defer ticker.Stop()
	run := func() {
		s.mu.Lock()
		images := s.images
		nodes := deriveCluster(s.containers, s.k3dCluster).NodeContainers
		s.mu.Unlock()
		s.refreshFingerprints(ctx, images, nodes)
	}
	run()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		case <-imagesChanged:
			run()
		}
	}
}

func (s *Source) statsLoop(ctx context.Context) {
	ticker := time.NewTicker(statsInterval)
	defer ticker.Stop()
	for {
		s.collectStats(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Source) collectStats(ctx context.Context) {
	s.mu.Lock()
	var running []string
	for _, c := range s.containers {
		if c.Running() {
			running = append(running, c.ID)
		}
	}
	s.mu.Unlock()

	var (
		mu    sync.Mutex
		wg    sync.WaitGroup
		sem   = make(chan struct{}, statsParallelism)
		stats = make(map[string]model.Usage, len(running))
	)
	for _, id := range running {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			u, err := s.containerStats(ctx, id)
			if err != nil {
				return
			}
			mu.Lock()
			stats[id] = u
			mu.Unlock()
		}()
	}
	wg.Wait()
	if ctx.Err() != nil {
		return
	}

	now := time.Now()
	samples := make(map[string]model.Sample, len(stats))
	for id, u := range stats {
		samples[model.HistoryKeyContainer(id)] = model.Sample{At: now, CPUMilli: u.CPUMilli, MemoryBytes: u.MemoryBytes}
	}
	s.store.Apply(func(snap *model.Snapshot) {
		if snap.Docker.Available {
			snap.Docker.Stats = stats
		}
	})
	s.store.Apply(state.AppendHistory(samples, "ctr:", true))
}

func (s *Source) containerStats(ctx context.Context, id string) (model.Usage, error) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	resp, err := s.cli.ContainerStats(ctx, id, false)
	if err != nil {
		return model.Usage{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	var st container.StatsResponse
	if err := json.NewDecoder(resp.Body).Decode(&st); err != nil {
		return model.Usage{}, err
	}
	return model.Usage{CPUMilli: cpuMilli(st), MemoryBytes: memoryUsage(st), MemoryLimit: int64(st.MemoryStats.Limit)}, nil
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
