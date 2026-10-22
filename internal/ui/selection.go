package ui

import (
	"fmt"
	"strings"

	"github.com/swehacker/lazydev/internal/logs"
	"github.com/swehacker/lazydev/internal/model"
	"github.com/swehacker/lazydev/internal/service"
)

type selKind int

const (
	selNone selKind = iota
	selService
	selPod
	selDeployment
	selRelease
	selContainer
	selImage
	selNode
	selEvent
)

// selection is the object shown in the detail pane.
type selection struct {
	kind selKind
	key  string

	svc     *service.LogicalService
	pod     *model.Pod
	deploy  *model.Deployment
	release *model.Release
	ctr     *model.Container
	image   *model.Image
	node    *model.Node
	event   *model.Event
}

func (s selection) id() string { return fmt.Sprintf("%d:%s", s.kind, s.key) }

func (s selection) title() string {
	switch s.kind {
	case selService:
		return s.svc.Name
	case selPod:
		return s.pod.Name
	case selDeployment:
		return "deploy/" + s.deploy.Name
	case selRelease:
		return "helm/" + s.release.Name
	case selContainer:
		return s.ctr.Name
	case selImage:
		if len(s.image.Tags) > 0 {
			return s.image.Tags[0]
		}
		return shortID(s.image.ID)
	case selNode:
		return "node/" + s.node.Name
	case selEvent:
		return "event/" + s.event.Reason
	}
	return ""
}

func (s selection) hasLogs() bool {
	switch s.kind {
	case selService, selPod, selDeployment, selContainer:
		return true
	}
	return false
}

// logTargets lists the streams to follow for the selection.
func (s selection) logTargets(snap model.Snapshot) []logs.Target {
	var out []logs.Target
	addPod := func(p model.Pod) {
		var containers []model.PodContainer
		for _, c := range p.Containers {
			if !c.Init {
				containers = append(containers, c)
			}
		}
		for _, c := range containers {
			out = append(out, logs.Target{
				Label:     logs.TargetLabel(p.Name, c.Name, len(containers) > 1),
				Namespace: p.Namespace, Pod: p.Name, Container: c.Name,
			})
		}
	}
	addContainer := func(c model.Container) {
		out = append(out, logs.Target{Label: c.Name, ContainerID: c.ID})
	}
	switch s.kind {
	case selService:
		for _, p := range s.svc.Pods {
			addPod(p)
		}
		for _, c := range s.svc.Containers {
			addContainer(c)
		}
	case selPod:
		addPod(*s.pod)
	case selDeployment:
		for _, p := range snap.Kube.Pods {
			if p.Namespace == s.deploy.Namespace && p.OwnerKind == "Deployment" && p.OwnerName == s.deploy.Name {
				addPod(p)
			}
		}
	case selContainer:
		addContainer(*s.ctr)
	}
	return out
}

// ref returns the object to describe and read env from.
func (s selection) ref() (Ref, bool) {
	switch s.kind {
	case selService:
		switch {
		case s.svc.Deployment != nil:
			return Ref{Kind: RefDeployment, Namespace: s.svc.Deployment.Namespace, Name: s.svc.Deployment.Name}, true
		case len(s.svc.Pods) > 0:
			return Ref{Kind: RefPod, Namespace: s.svc.Pods[0].Namespace, Name: s.svc.Pods[0].Name}, true
		case len(s.svc.Containers) > 0:
			return Ref{Kind: RefContainer, ID: s.svc.Containers[0].ID, Name: s.svc.Containers[0].Name}, true
		}
	case selPod:
		return Ref{Kind: RefPod, Namespace: s.pod.Namespace, Name: s.pod.Name}, true
	case selDeployment:
		return Ref{Kind: RefDeployment, Namespace: s.deploy.Namespace, Name: s.deploy.Name}, true
	case selContainer:
		return Ref{Kind: RefContainer, ID: s.ctr.ID, Name: s.ctr.Name}, true
	case selImage:
		return Ref{Kind: RefImage, ID: s.image.ID}, true
	case selNode:
		return Ref{Kind: RefNode, Name: s.node.Name}, true
	}
	return Ref{}, false
}

// matchesEvent reports whether a Kubernetes event concerns the selection.
func (s selection) matchesEvent(e model.Event) bool {
	podMatch := func(pods []model.Pod) bool {
		for _, p := range pods {
			if e.Namespace == p.Namespace && e.Kind == "Pod" && e.Name == p.Name {
				return true
			}
		}
		return false
	}
	deployMatch := func(d *model.Deployment) bool {
		if d == nil || e.Namespace != d.Namespace {
			return false
		}
		return (e.Kind == "Deployment" && e.Name == d.Name) ||
			(e.Kind == "ReplicaSet" && strings.HasPrefix(e.Name, d.Name+"-")) ||
			(e.Kind == "Pod" && strings.HasPrefix(e.Name, d.Name+"-"))
	}
	switch s.kind {
	case selService:
		return podMatch(s.svc.Pods) || deployMatch(s.svc.Deployment)
	case selPod:
		return e.Namespace == s.pod.Namespace && e.Kind == "Pod" && e.Name == s.pod.Name
	case selDeployment:
		return deployMatch(s.deploy)
	case selNode:
		return e.Kind == "Node" && e.Name == s.node.Name
	case selEvent:
		return e == *s.event
	}
	return false
}

func (s selection) supportsEvents() bool {
	switch s.kind {
	case selService:
		return s.svc.Namespace != ""
	case selPod, selDeployment, selNode, selEvent:
		return true
	}
	return false
}

type historyRow struct {
	label    string
	key      string
	memLimit int64
}

// historyRows lists the CPU/memory series relevant to the selection.
func (s selection) historyRows(snap model.Snapshot) []historyRow {
	var rows []historyRow
	addPods := func(pods []model.Pod) {
		for _, p := range pods {
			rows = append(rows, historyRow{label: p.Name, key: model.HistoryKeyPod(p.Namespace, p.Name)})
		}
	}
	addContainers := func(cs []model.Container) {
		for _, c := range cs {
			rows = append(rows, historyRow{label: c.Name, key: model.HistoryKeyContainer(c.ID), memLimit: snap.Docker.Stats[c.ID].MemoryLimit})
		}
	}
	switch s.kind {
	case selService:
		addPods(s.svc.Pods)
		addContainers(s.svc.Containers)
	case selPod:
		addPods([]model.Pod{*s.pod})
	case selDeployment:
		var pods []model.Pod
		for _, p := range snap.Kube.Pods {
			if p.Namespace == s.deploy.Namespace && p.OwnerKind == "Deployment" && p.OwnerName == s.deploy.Name {
				pods = append(pods, p)
			}
		}
		addPods(pods)
	case selContainer:
		addContainers([]model.Container{*s.ctr})
	case selNode:
		rows = append(rows, historyRow{label: s.node.Name, key: model.HistoryKeyNode(s.node.Name), memLimit: s.node.MemoryBytes})
	}
	return rows
}

func shortID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}
