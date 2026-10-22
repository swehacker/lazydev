// Package service joins configuration and observed state into logical services.
package service

import (
	"fmt"
	"sort"
	"time"

	"k8s.io/apimachinery/pkg/labels"

	"github.com/swehacker/lazydev/internal/config"
	"github.com/swehacker/lazydev/internal/model"
)

type LogicalService struct {
	Name   string
	Status Status
	Reason string

	Namespace  string
	Selector   string
	Deployment *model.Deployment
	Pods       []model.Pod
	Containers []model.Container

	Desired  int
	Ready    int
	Restarts int
	Created  time.Time
	Usage    model.Usage
	HasUsage bool

	HealthURL string
	Health    *model.HealthResult

	Image       string // normalized reference
	Drift       Drift
	DriftDetail string

	Auto bool // discovered rather than configured
}

// Aggregate builds logical services from the config, or discovers them when none are configured.
func Aggregate(cfg *config.Config, snap model.Snapshot) []LogicalService {
	defs := cfg.Services
	auto := cfg.AutoDiscover()
	if auto {
		defs = discover(cfg, snap)
	}
	out := make([]LogicalService, 0, len(defs))
	for i := range defs {
		svc := build(cfg, &defs[i], snap)
		svc.Auto = auto
		out = append(out, svc)
	}
	return out
}

// discover derives one service per deployment and per compose service.
func discover(cfg *config.Config, snap model.Snapshot) []config.Service {
	var defs []config.Service
	names := map[string]int{}
	for _, d := range snap.Kube.Deployments {
		names[d.Name]++
	}
	for _, d := range snap.Kube.Deployments {
		if len(d.Selector) == 0 {
			continue
		}
		name := d.Name
		if names[d.Name] > 1 {
			name = d.Namespace + "/" + d.Name
		}
		defs = append(defs, config.Service{
			Name: name,
			K8s:  &config.K8sRef{Namespace: d.Namespace, Selector: labels.SelectorFromSet(d.Selector).String(), Deployment: d.Name},
		})
	}
	seen := map[string]bool{}
	for _, c := range snap.Docker.Containers {
		if c.ComposeService == "" || (cfg.Compose.Project != "" && c.ComposeProject != cfg.Compose.Project) || seen[c.ComposeService] {
			continue
		}
		seen[c.ComposeService] = true
		name := c.ComposeService
		if names[name] > 0 {
			name = c.ComposeProject + "/" + name
		}
		names[name]++
		defs = append(defs, config.Service{Name: name, Compose: c.ComposeService})
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	return defs
}

func build(cfg *config.Config, def *config.Service, snap model.Snapshot) LogicalService {
	svc := LogicalService{Name: def.Name, HealthURL: def.Health}
	var parts []component

	if def.K8s != nil {
		svc.Namespace, svc.Selector = def.K8s.Namespace, def.K8s.Selector
		parts = append(parts, svc.attachK8s(def.K8s, snap))
	}
	if def.Compose != "" {
		parts = append(parts, svc.attachCompose(cfg.Compose.Project, def.Compose, snap))
	}
	if def.Health != "" {
		if r, ok := snap.Health[def.Name]; ok {
			svc.Health = &r
			if r.OK {
				parts = append(parts, component{Healthy, ""})
			} else {
				parts = append(parts, component{Degraded, "health: " + r.Detail})
			}
		}
	}
	result := rollup(parts...)
	svc.Status, svc.Reason = result.status, result.reason

	explicit := def.Image != ""
	ref := model.NormalizeImageRef(def.Image)
	if ref == "" && svc.Deployment != nil && len(svc.Deployment.Images) > 0 {
		ref = model.NormalizeImageRef(svc.Deployment.Images[0])
	}
	svc.Image = ref
	svc.Drift, svc.DriftDetail = ClassifyDrift(ref, explicit, snap.Drift, svc.Pods, snap.Cluster.Found)
	return svc
}

func (svc *LogicalService) attachK8s(ref *config.K8sRef, snap model.Snapshot) component {
	if !snap.Kube.Available {
		return component{Unknown, "cluster unavailable"}
	}
	sel, err := labels.Parse(ref.Selector)
	if err != nil {
		return component{Unknown, "invalid selector"}
	}
	for i := range snap.Kube.Deployments {
		d := &snap.Kube.Deployments[i]
		if d.Namespace != ref.Namespace {
			continue
		}
		if (ref.Deployment != "" && d.Name == ref.Deployment) || (ref.Deployment == "" && sel.Matches(labels.Set(d.TemplateLabels))) {
			svc.Deployment = d
			break
		}
	}
	var problem string
	ready := 0
	for _, p := range snap.Kube.Pods {
		if p.Namespace != ref.Namespace || !sel.Matches(labels.Set(p.Labels)) {
			continue
		}
		svc.Pods = append(svc.Pods, p)
		svc.Restarts += p.Restarts
		if svc.Created.IsZero() || p.Created.Before(svc.Created) {
			svc.Created = p.Created
		}
		if p.Status != "Terminating" {
			if p.Total > 0 && p.Ready == p.Total {
				ready++
			}
			if problem == "" && p.Problem != "" {
				problem = p.Problem
			}
		}
		if u, ok := snap.Kube.PodMetrics[p.Key()]; ok {
			svc.Usage.CPUMilli += u.CPUMilli
			svc.Usage.MemoryBytes += u.MemoryBytes
			svc.HasUsage = true
		}
	}

	switch {
	case svc.Deployment != nil:
		svc.Desired, svc.Ready = int(svc.Deployment.Replicas), int(svc.Deployment.Ready)
		if svc.Created.IsZero() {
			svc.Created = svc.Deployment.Created
		}
	case len(svc.Pods) > 0:
		svc.Desired, svc.Ready = len(svc.Pods), ready
	default:
		return component{Down, "not deployed"}
	}

	switch {
	case svc.Desired == 0:
		return component{Down, "scaled to 0"}
	case problem != "" && svc.Ready == 0:
		return component{Down, problem}
	case problem != "":
		return component{Degraded, problem}
	case svc.Ready >= svc.Desired:
		return component{Healthy, ""}
	case svc.Ready == 0:
		return component{Down, fmt.Sprintf("0/%d ready", svc.Desired)}
	default:
		return component{Degraded, fmt.Sprintf("%d/%d ready", svc.Ready, svc.Desired)}
	}
}

func (svc *LogicalService) attachCompose(project, service string, snap model.Snapshot) component {
	if !snap.Docker.Available {
		return component{Unknown, "docker unavailable"}
	}
	for _, c := range snap.Docker.Containers {
		if c.ComposeService == service && (project == "" || c.ComposeProject == project) {
			svc.Containers = append(svc.Containers, c)
		}
	}
	if len(svc.Containers) == 0 {
		return component{Down, "container not found"}
	}
	parts := make([]component, 0, len(svc.Containers))
	for _, c := range svc.Containers {
		completed := c.State == "exited" && c.ExitCode == 0
		if !completed {
			svc.Desired++
		}
		if svc.Created.IsZero() || c.Created.Before(svc.Created) {
			svc.Created = c.Created
		}
		if u, ok := snap.Docker.Stats[c.ID]; ok {
			svc.Usage.CPUMilli += u.CPUMilli
			svc.Usage.MemoryBytes += u.MemoryBytes
			svc.HasUsage = true
		}
		switch {
		case c.Running() && c.Health == "unhealthy":
			svc.Ready++
			parts = append(parts, component{Degraded, "unhealthy"})
		case c.Running() && c.Health == "starting":
			parts = append(parts, component{Degraded, "starting"})
		case c.Running():
			svc.Ready++
			parts = append(parts, component{Healthy, ""})
		case completed:
			parts = append(parts, component{Completed, ""})
		case c.State == "exited":
			parts = append(parts, component{Down, fmt.Sprintf("exited (%d)", c.ExitCode)})
		default:
			parts = append(parts, component{Down, c.State})
		}
	}
	return rollup(parts...)
}

// Counts tallies services by status for the header.
func Counts(svcs []LogicalService) map[Status]int {
	out := map[Status]int{}
	for _, s := range svcs {
		out[s.Status]++
	}
	return out
}
