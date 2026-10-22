package docker

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"

	"github.com/swehacker/lazydev/internal/model"
)

const (
	labelComposeProject = "com.docker.compose.project"
	labelComposeService = "com.docker.compose.service"
	labelK3dCluster     = "k3d.cluster"
	labelK3dRole        = "k3d.role"
)

var exitCodeRe = regexp.MustCompile(`^Exited \((-?\d+)\)`)

func convertContainers(in []container.Summary) []model.Container {
	out := make([]model.Container, 0, len(in))
	for _, c := range in {
		name := c.ID[:min(12, len(c.ID))]
		if len(c.Names) > 0 {
			name = strings.TrimPrefix(c.Names[0], "/")
		}
		mc := model.Container{
			ID:             c.ID,
			Name:           name,
			Image:          c.Image,
			ImageID:        c.ImageID,
			State:          c.State,
			Status:         c.Status,
			Health:         healthFromStatus(c.Status),
			Created:        time.Unix(c.Created, 0),
			Labels:         c.Labels,
			ComposeProject: c.Labels[labelComposeProject],
			ComposeService: c.Labels[labelComposeService],
		}
		if m := exitCodeRe.FindStringSubmatch(c.Status); m != nil {
			mc.ExitCode, _ = strconv.Atoi(m[1])
		}
		for _, p := range c.Ports {
			if p.PublicPort != 0 {
				mc.Ports = append(mc.Ports, fmt.Sprintf("%d->%d/%s", p.PublicPort, p.PrivatePort, p.Type))
			}
		}
		mc.Ports = dedupe(mc.Ports)
		out = append(out, mc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func healthFromStatus(status string) string {
	switch {
	case strings.Contains(status, "(healthy)"):
		return "healthy"
	case strings.Contains(status, "(unhealthy)"):
		return "unhealthy"
	case strings.Contains(status, "(health: starting)"):
		return "starting"
	}
	return ""
}

func convertImages(in []image.Summary) []model.Image {
	out := make([]model.Image, 0, len(in))
	for _, img := range in {
		var tags []string
		for _, t := range img.RepoTags {
			if t != "<none>:<none>" {
				tags = append(tags, t)
			}
		}
		out = append(out, model.Image{
			ID:       img.ID,
			Tags:     tags,
			Size:     img.Size,
			Created:  time.Unix(img.Created, 0),
			Dangling: len(tags) == 0,
			InUse:    int(max(img.Containers, 0)),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Created.After(out[j].Created) })
	return out
}

// deriveCluster finds the k3d cluster's node containers. With an empty name it picks
// the only cluster present, so auto-discovery works for the common single-cluster setup.
func deriveCluster(containers []model.Container, name string) model.ClusterState {
	if name == "" {
		clusters := map[string]bool{}
		for _, c := range containers {
			if n := c.Labels[labelK3dCluster]; n != "" {
				clusters[n] = true
			}
		}
		if len(clusters) != 1 {
			return model.ClusterState{}
		}
		for n := range clusters {
			name = n
		}
	}
	cs := model.ClusterState{Name: name}
	for _, c := range containers {
		if c.Labels[labelK3dCluster] != name {
			continue
		}
		cs.Found = true
		switch c.Labels[labelK3dRole] {
		case "server":
			cs.Servers++
			if c.Running() {
				cs.ServersRunning++
				cs.NodeContainers = append(cs.NodeContainers, c.Name)
			}
		case "agent":
			cs.Agents++
			if c.Running() {
				cs.AgentsRunning++
				cs.NodeContainers = append(cs.NodeContainers, c.Name)
			}
		}
	}
	sort.Strings(cs.NodeContainers)
	return cs
}

// cpuMilli computes CPU usage in millicores from a non-streaming stats sample (which includes precpu).
func cpuMilli(s container.StatsResponse) float64 {
	cpuDelta := float64(s.CPUStats.CPUUsage.TotalUsage) - float64(s.PreCPUStats.CPUUsage.TotalUsage)
	sysDelta := float64(s.CPUStats.SystemUsage) - float64(s.PreCPUStats.SystemUsage)
	if cpuDelta <= 0 || sysDelta <= 0 {
		return 0
	}
	cpus := float64(s.CPUStats.OnlineCPUs)
	if cpus == 0 {
		cpus = float64(len(s.CPUStats.CPUUsage.PercpuUsage))
	}
	return cpuDelta / sysDelta * cpus * 1000
}

// memoryUsage mirrors `docker stats`: usage minus page cache.
func memoryUsage(s container.StatsResponse) int64 {
	usage := s.MemoryStats.Usage
	if v, ok := s.MemoryStats.Stats["inactive_file"]; ok && v < usage {
		usage -= v
	} else if v, ok := s.MemoryStats.Stats["total_inactive_file"]; ok && v < usage {
		usage -= v
	}
	return int64(usage)
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	out := in[:0]
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}
