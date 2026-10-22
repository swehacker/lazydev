// Package app wires configuration, sources, state and the UI together.
package app

import (
	"context"
	"errors"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/go-logr/logr"
	"k8s.io/klog/v2"

	"github.com/swehacker/lazydev/internal/config"
	"github.com/swehacker/lazydev/internal/source/colima"
	"github.com/swehacker/lazydev/internal/source/docker"
	"github.com/swehacker/lazydev/internal/source/health"
	"github.com/swehacker/lazydev/internal/source/kube"
	"github.com/swehacker/lazydev/internal/state"
	"github.com/swehacker/lazydev/internal/ui"
)

// Run starts all sources and the terminal UI, returning when the user quits or ctx ends.
func Run(ctx context.Context, cfg *config.Config, version string) error {
	// client-go logs reconnect noise to stderr, which would corrupt the alternate screen.
	klog.SetLogger(logr.Discard())

	store := state.New()
	dockerSrc, err := docker.New(store, cfg.Cluster.K3dName)
	if err != nil {
		return fmt.Errorf("docker client: %w", err)
	}
	kubeSrc := kube.New(store, cfg.Cluster.KubeContext, cfg.Namespaces)
	engine := colima.New(store, docker.ResolveHost(), engineInfo(dockerSrc))

	var targets []health.Target
	for _, s := range cfg.Services {
		if s.Health != "" {
			targets = append(targets, health.Target{Service: s.Name, URL: s.Health})
		}
	}
	prober := health.New(store, targets, cfg.Health.Interval, cfg.Health.Timeout, cfg.Health.SkipVerify())

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	go dockerSrc.Run(ctx)
	go kubeSrc.Run(ctx)
	go engine.Run(ctx)
	go prober.Run(ctx)

	backend := &Backend{docker: dockerSrc, kube: kubeSrc, prober: prober}
	program := tea.NewProgram(ui.New(cfg, version, store, backend), tea.WithAltScreen(), tea.WithContext(ctx))
	_, err = program.Run()
	if errors.Is(err, tea.ErrProgramKilled) && ctx.Err() != nil {
		return nil
	}
	return err
}

func engineInfo(src *docker.Source) colima.InfoFunc {
	return func(ctx context.Context) (int, int64, error) {
		info, err := src.Client().Info(ctx)
		if err != nil {
			return 0, 0, err
		}
		return info.NCPU, info.MemTotal, nil
	}
}
