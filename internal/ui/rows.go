package ui

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/swehacker/lazydev/internal/model"
	"github.com/swehacker/lazydev/internal/service"
)

var serviceColumns = []column{
	{title: "", width: 1},
	{title: "NAME", width: 14, flex: true},
	{title: "STATUS", width: 16},
	{title: "READY", width: 5, right: true},
	{title: "RST", width: 3, right: true, hideBelow: 56},
	{title: "CPU", width: 5, right: true, hideBelow: 66},
	{title: "MEM", width: 6, right: true, hideBelow: 66},
	{title: "HEALTH", width: 7, hideBelow: 76},
	{title: "IMAGE", width: 14, hideBelow: 80},
	{title: "AGE", width: 4, right: true, hideBelow: 84},
}

func serviceRows(svcs []service.LogicalService, now time.Time) []row {
	rows := make([]row, 0, len(svcs))
	for _, s := range svcs {
		st := statusStyle(s.Status)
		status := s.Status.String()
		if s.Reason != "" {
			status = s.Reason
		}
		ready := "-"
		if s.Desired > 0 || s.Ready > 0 {
			ready = fmt.Sprintf("%d/%d", s.Ready, s.Desired)
		}
		cpu, mem := "-", "-"
		if s.HasUsage {
			cpu, mem = humanCPU(s.Usage.CPUMilli), humanBytes(s.Usage.MemoryBytes)
		}
		health := styled("-", styleFaint)
		if s.Health != nil {
			if s.Health.OK {
				health = styled(fmt.Sprintf("✓ %dms", s.Health.Latency.Milliseconds()), styleOK)
			} else {
				health = styled("✗ fail", styleErr)
			}
		} else if s.HealthURL != "" {
			health = styled("…", styleDim)
		}
		image := styled("-", styleFaint)
		switch {
		case s.Drift == service.InSync:
			image = styled("in sync", styleOK)
		case s.Drift.Problem():
			image = styled(s.Drift.String(), styleWarn)
		}
		restarts := styled(fmt.Sprint(s.Restarts), styleDim)
		if s.Restarts > 0 {
			restarts = styled(fmt.Sprint(s.Restarts), styleWarn)
		}
		rows = append(rows, row{key: s.Name, cells: []cell{
			styled(statusIcon(s.Status), st),
			plain(s.Name),
			styled(status, st),
			plain(ready),
			restarts,
			plain(cpu),
			plain(mem),
			health,
			image,
			styled(humanAge(s.Created, now), styleDim),
		}})
	}
	return rows
}

func resourceColumns(kind resourceKind) []column {
	switch kind {
	case kindPods:
		return []column{
			{title: "", width: 1},
			{title: "NAMESPACE", width: 10, hideBelow: 70},
			{title: "NAME", width: 20, flex: true},
			{title: "READY", width: 5, right: true},
			{title: "STATUS", width: 16},
			{title: "RST", width: 3, right: true},
			{title: "CPU", width: 5, right: true, hideBelow: 80},
			{title: "MEM", width: 6, right: true, hideBelow: 80},
			{title: "AGE", width: 4, right: true, hideBelow: 90},
		}
	case kindDeployments:
		return []column{
			{title: "", width: 1},
			{title: "NAMESPACE", width: 10, hideBelow: 70},
			{title: "NAME", width: 20, flex: true},
			{title: "READY", width: 5, right: true},
			{title: "UP", width: 3, right: true},
			{title: "AVL", width: 3, right: true},
			{title: "IMAGE", width: 24, hideBelow: 90},
			{title: "AGE", width: 4, right: true},
		}
	case kindHelm:
		return []column{
			{title: "", width: 1},
			{title: "NAMESPACE", width: 10},
			{title: "NAME", width: 16, flex: true},
			{title: "REV", width: 3, right: true},
			{title: "CHART", width: 22, hideBelow: 70},
			{title: "STATUS", width: 10},
			{title: "UPDATED", width: 7, right: true},
		}
	case kindCompose:
		return []column{
			{title: "", width: 1},
			{title: "PROJECT", width: 12, hideBelow: 70},
			{title: "SERVICE", width: 16, flex: true},
			{title: "STATE", width: 18},
			{title: "CPU", width: 5, right: true},
			{title: "MEM", width: 6, right: true},
			{title: "PORTS", width: 18, hideBelow: 90},
		}
	case kindContainers:
		return []column{
			{title: "", width: 1},
			{title: "NAME", width: 20, flex: true},
			{title: "IMAGE", width: 22, hideBelow: 80},
			{title: "STATE", width: 18},
			{title: "CPU", width: 5, right: true},
			{title: "MEM", width: 6, right: true},
		}
	case kindImages:
		return []column{
			{title: "", width: 1},
			{title: "IMAGE", width: 24, flex: true},
			{title: "ID", width: 12, hideBelow: 80},
			{title: "SIZE", width: 6, right: true},
			{title: "USED", width: 4, right: true},
			{title: "K3D", width: 12},
			{title: "AGE", width: 4, right: true},
		}
	case kindNodes:
		return []column{
			{title: "", width: 1},
			{title: "NAME", width: 20, flex: true},
			{title: "STATUS", width: 14},
			{title: "ROLES", width: 20, hideBelow: 80},
			{title: "CPU", width: 11, right: true},
			{title: "MEM", width: 13, right: true},
			{title: "VERSION", width: 14, hideBelow: 100},
		}
	case kindEvents:
		return []column{
			{title: "", width: 1},
			{title: "AGE", width: 4, right: true},
			{title: "REASON", width: 16},
			{title: "OBJECT", width: 26},
			{title: "MESSAGE", width: 20, flex: true},
		}
	}
	return nil
}

func resourceRows(kind resourceKind, snap model.Snapshot, namespace string, now time.Time) []row {
	nsOK := func(ns string) bool { return namespace == "" || ns == namespace }
	var rows []row
	switch kind {
	case kindPods:
		for _, p := range snap.Kube.Pods {
			if !nsOK(p.Namespace) {
				continue
			}
			st := podStatusStyle(p)
			cpu, mem := "-", "-"
			if u, ok := snap.Kube.PodMetrics[p.Key()]; ok {
				cpu, mem = humanCPU(u.CPUMilli), humanBytes(u.MemoryBytes)
			}
			rows = append(rows, row{key: p.Key(), cells: []cell{
				styled(statusIcon(podServiceStatus(p)), st), styled(p.Namespace, styleDim), plain(p.Name),
				plain(fmt.Sprintf("%d/%d", p.Ready, p.Total)), styled(p.Status, st), restartsCell(p.Restarts),
				plain(cpu), plain(mem), styled(humanAge(p.Created, now), styleDim),
			}})
		}
	case kindDeployments:
		for _, d := range snap.Kube.Deployments {
			if !nsOK(d.Namespace) {
				continue
			}
			status := service.Healthy
			switch {
			case d.Replicas == 0 || d.Ready == 0:
				status = service.Down
			case d.Ready < d.Replicas:
				status = service.Degraded
			}
			rows = append(rows, row{key: d.Key(), cells: []cell{
				styled(statusIcon(status), statusStyle(status)), styled(d.Namespace, styleDim), plain(d.Name),
				plain(fmt.Sprintf("%d/%d", d.Ready, d.Replicas)), plain(fmt.Sprint(d.Updated)), plain(fmt.Sprint(d.Available)),
				styled(strings.Join(d.Images, ","), styleDim), styled(humanAge(d.Created, now), styleDim),
			}})
		}
	case kindHelm:
		for _, r := range snap.Kube.Releases {
			if !nsOK(r.Namespace) {
				continue
			}
			status := service.Healthy
			if r.Status != "deployed" {
				status = service.Degraded
				if strings.HasPrefix(r.Status, "failed") {
					status = service.Down
				}
			}
			rows = append(rows, row{key: r.Namespace + "/" + r.Name, cells: []cell{
				styled(statusIcon(status), statusStyle(status)), styled(r.Namespace, styleDim), plain(r.Name),
				plain(fmt.Sprint(r.Revision)), styled(r.Chart, styleDim), styled(r.Status, statusStyle(status)),
				styled(humanAge(r.Updated, now), styleDim),
			}})
		}
	case kindCompose, kindContainers:
		for _, c := range snap.Docker.Containers {
			if kind == kindCompose && c.ComposeService == "" {
				continue
			}
			status := containerStatus(c)
			cpu, mem := "-", "-"
			if u, ok := snap.Docker.Stats[c.ID]; ok {
				cpu, mem = humanCPU(u.CPUMilli), humanBytes(u.MemoryBytes)
			}
			state := c.State
			if c.Health != "" {
				state += " (" + c.Health + ")"
			} else if c.State == "exited" {
				state = fmt.Sprintf("exited (%d)", c.ExitCode)
			}
			icon := styled(statusIcon(status), statusStyle(status))
			if kind == kindCompose {
				rows = append(rows, row{key: c.ID, cells: []cell{
					icon, styled(c.ComposeProject, styleDim), plain(c.ComposeService), styled(state, statusStyle(status)),
					plain(cpu), plain(mem), styled(strings.Join(c.Ports, ","), styleDim),
				}})
			} else {
				rows = append(rows, row{key: c.ID, cells: []cell{
					icon, plain(c.Name), styled(c.Image, styleDim), styled(state, statusStyle(status)), plain(cpu), plain(mem),
				}})
			}
		}
	case kindImages:
		for _, img := range snap.Docker.Images {
			name := "<none>"
			if len(img.Tags) > 0 {
				name = strings.Join(img.Tags, ", ")
			}
			k3d, k3dStyle := imageClusterState(img, snap)
			nameCell := plain(name)
			if img.Dangling {
				nameCell = styled(name, styleFaint)
			}
			rows = append(rows, row{key: img.ID, cells: []cell{
				styled(" ", styleDim), nameCell, styled(shortID(img.ID), styleDim), plain(humanBytes(img.Size)),
				plain(fmt.Sprint(img.InUse)), styled(k3d, k3dStyle), styled(humanAge(img.Created, now), styleDim),
			}})
		}
	case kindNodes:
		for _, n := range snap.Kube.Nodes {
			status, text := service.Healthy, "Ready"
			if !n.Ready {
				status, text = service.Down, "NotReady"
			}
			if len(n.Pressure) > 0 {
				status, text = max(status, service.Degraded), text+","+strings.Join(n.Pressure, ",")
			}
			cpu, mem := "-", "-"
			if u, ok := snap.Kube.NodeMetrics[n.Name]; ok {
				cpu = fmt.Sprintf("%s/%s", humanCPU(u.CPUMilli), humanCPU(float64(n.CPUMilli)))
				mem = fmt.Sprintf("%s/%s", humanBytes(u.MemoryBytes), humanBytes(n.MemoryBytes))
			}
			rows = append(rows, row{key: n.Name, cells: []cell{
				styled(statusIcon(status), statusStyle(status)), plain(n.Name), styled(text, statusStyle(status)),
				styled(strings.Join(n.Roles, ","), styleDim), plain(cpu), plain(mem), styled(n.Version, styleDim),
			}})
		}
	case kindEvents:
		for _, e := range snap.Kube.Events {
			if !nsOK(e.Namespace) {
				continue
			}
			st := styleDim
			icon := " "
			if e.Type == "Warning" {
				st, icon = styleWarn, "!"
			}
			reason := e.Reason
			if e.Count > 1 {
				reason = fmt.Sprintf("%s ×%d", e.Reason, e.Count)
			}
			rows = append(rows, row{key: eventKey(e), cells: []cell{
				styled(icon, st), styled(humanAge(e.Last, now), styleDim), styled(reason, st),
				plain(strings.ToLower(e.Kind) + "/" + e.Name), plain(e.Message),
			}})
		}
	}
	return rows
}

func eventKey(e model.Event) string {
	return fmt.Sprintf("%s/%s/%s/%s/%d", e.Namespace, e.Kind, e.Name, e.Reason, e.Last.UnixNano())
}

func restartsCell(n int) cell {
	if n > 0 {
		return styled(fmt.Sprint(n), styleWarn)
	}
	return styled("0", styleDim)
}

func podServiceStatus(p model.Pod) service.Status {
	switch {
	case p.Problem != "":
		if p.Ready == 0 {
			return service.Down
		}
		return service.Degraded
	case p.Phase == "Succeeded":
		return service.Completed
	case p.Status == "Running" && p.Ready == p.Total:
		return service.Healthy
	case p.Status == "Terminating":
		return service.Unknown
	default:
		return service.Degraded
	}
}

func podStatusStyle(p model.Pod) lipgloss.Style { return statusStyle(podServiceStatus(p)) }

func containerStatus(c model.Container) service.Status {
	switch {
	case c.Running() && (c.Health == "unhealthy" || c.Health == "starting"):
		return service.Degraded
	case c.Running():
		return service.Healthy
	case c.State == "exited" && c.ExitCode == 0:
		return service.Completed
	default:
		return service.Down
	}
}

// imageClusterState describes whether an image is imported into all k3d nodes and current.
func imageClusterState(img model.Image, snap model.Snapshot) (string, lipgloss.Style) {
	if img.Dangling || !snap.Cluster.Found || len(snap.Drift.Nodes) == 0 {
		return "-", styleFaint
	}
	for _, tag := range img.Tags {
		ref := model.NormalizeImageRef(tag)
		local, ok := snap.Drift.Local[ref]
		if !ok {
			continue
		}
		imported, stale := 0, false
		for _, imgs := range snap.Drift.Nodes {
			if fp, ok := imgs[ref]; ok {
				imported++
				if !slices.Equal(fp.Layers, local.Layers) {
					stale = true
				}
			}
		}
		switch {
		case stale:
			return "stale import", styleWarn
		case imported == len(snap.Drift.Nodes):
			return "imported", styleOK
		case imported > 0:
			return "partial", styleWarn
		}
	}
	return "-", styleFaint
}

// resolve finds the object behind a row key.
func resolve(kind resourceKind, key string, snap model.Snapshot) selection {
	sel := selection{key: key}
	switch kind {
	case kindPods:
		for i := range snap.Kube.Pods {
			if snap.Kube.Pods[i].Key() == key {
				sel.kind, sel.pod = selPod, &snap.Kube.Pods[i]
			}
		}
	case kindDeployments:
		for i := range snap.Kube.Deployments {
			if snap.Kube.Deployments[i].Key() == key {
				sel.kind, sel.deploy = selDeployment, &snap.Kube.Deployments[i]
			}
		}
	case kindHelm:
		for i := range snap.Kube.Releases {
			r := &snap.Kube.Releases[i]
			if r.Namespace+"/"+r.Name == key {
				sel.kind, sel.release = selRelease, r
			}
		}
	case kindCompose, kindContainers:
		for i := range snap.Docker.Containers {
			if snap.Docker.Containers[i].ID == key {
				sel.kind, sel.ctr = selContainer, &snap.Docker.Containers[i]
			}
		}
	case kindImages:
		for i := range snap.Docker.Images {
			if snap.Docker.Images[i].ID == key {
				sel.kind, sel.image = selImage, &snap.Docker.Images[i]
			}
		}
	case kindNodes:
		for i := range snap.Kube.Nodes {
			if snap.Kube.Nodes[i].Name == key {
				sel.kind, sel.node = selNode, &snap.Kube.Nodes[i]
			}
		}
	case kindEvents:
		for i := range snap.Kube.Events {
			if eventKey(snap.Kube.Events[i]) == key {
				sel.kind, sel.event = selEvent, &snap.Kube.Events[i]
			}
		}
	}
	return sel
}
