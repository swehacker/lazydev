package kube

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"

	"github.com/swehacker/lazydev/internal/model"
)

const (
	pendingGrace  = 60 * time.Second
	oomRecentness = 10 * time.Minute
)

var problemReasons = map[string]bool{
	"CrashLoopBackOff":           true,
	"ImagePullBackOff":           true,
	"ErrImagePull":               true,
	"CreateContainerConfigError": true,
	"CreateContainerError":       true,
	"InvalidImageName":           true,
	"RunContainerError":          true,
	"ErrImageNeverPull":          true,
}

func convertPod(p *corev1.Pod, now time.Time) model.Pod {
	mp := model.Pod{
		Namespace: p.Namespace,
		Name:      p.Name,
		Labels:    p.Labels,
		Phase:     string(p.Status.Phase),
		Created:   p.CreationTimestamp.Time,
		Node:      p.Spec.NodeName,
		Total:     len(p.Spec.Containers),
	}
	if owner := controllerOf(p); owner != nil {
		mp.OwnerKind, mp.OwnerName = owner.Kind, owner.Name
		if owner.Kind == "ReplicaSet" {
			if hash := p.Labels["pod-template-hash"]; hash != "" && strings.HasSuffix(owner.Name, "-"+hash) {
				mp.OwnerKind, mp.OwnerName = "Deployment", strings.TrimSuffix(owner.Name, "-"+hash)
			}
		}
	}
	images := map[string]string{}
	for _, c := range p.Spec.Containers {
		images[c.Name] = c.Image
	}
	for _, c := range p.Spec.InitContainers {
		images[c.Name] = c.Image
	}
	for _, cs := range p.Status.InitContainerStatuses {
		mp.Containers = append(mp.Containers, convertContainerStatus(cs, images[cs.Name], true))
	}
	for _, cs := range p.Status.ContainerStatuses {
		if cs.Ready {
			mp.Ready++
		}
		mp.Restarts += int(cs.RestartCount)
		mp.Containers = append(mp.Containers, convertContainerStatus(cs, images[cs.Name], false))
	}
	mp.Status = podDisplayStatus(p)
	mp.Problem = podProblem(p, mp.Status, now)
	return mp
}

func controllerOf(p *corev1.Pod) *struct{ Kind, Name string } {
	for _, ref := range p.OwnerReferences {
		if ref.Controller != nil && *ref.Controller {
			return &struct{ Kind, Name string }{ref.Kind, ref.Name}
		}
	}
	return nil
}

func convertContainerStatus(cs corev1.ContainerStatus, specImage string, init bool) model.PodContainer {
	pc := model.PodContainer{
		Name:     cs.Name,
		Image:    cs.Image,
		ImageID:  cs.ImageID,
		Ready:    cs.Ready,
		Restarts: int(cs.RestartCount),
		Init:     init,
	}
	if pc.Image == "" {
		pc.Image = specImage
	}
	switch {
	case cs.State.Running != nil:
		pc.State = "running"
	case cs.State.Waiting != nil:
		pc.State, pc.Reason = "waiting", cs.State.Waiting.Reason
	case cs.State.Terminated != nil:
		pc.State, pc.Reason = "terminated", cs.State.Terminated.Reason
	}
	if t := cs.LastTerminationState.Terminated; t != nil {
		pc.LastTerminationReason = t.Reason
	}
	return pc
}

// podDisplayStatus follows the logic of `kubectl get pods` STATUS column.
func podDisplayStatus(p *corev1.Pod) string {
	reason := string(p.Status.Phase)
	if p.Status.Reason != "" {
		reason = p.Status.Reason
	}

	initializing := false
	for i, cs := range p.Status.InitContainerStatuses {
		switch {
		case cs.State.Terminated != nil && cs.State.Terminated.ExitCode == 0:
			continue
		case cs.State.Terminated != nil:
			switch {
			case cs.State.Terminated.Reason != "":
				reason = "Init:" + cs.State.Terminated.Reason
			case cs.State.Terminated.Signal != 0:
				reason = fmt.Sprintf("Init:Signal:%d", cs.State.Terminated.Signal)
			default:
				reason = fmt.Sprintf("Init:ExitCode:%d", cs.State.Terminated.ExitCode)
			}
		case cs.State.Waiting != nil && cs.State.Waiting.Reason != "" && cs.State.Waiting.Reason != "PodInitializing":
			reason = "Init:" + cs.State.Waiting.Reason
		default:
			reason = fmt.Sprintf("Init:%d/%d", i, len(p.Spec.InitContainers))
		}
		initializing = true
		break
	}

	if !initializing {
		hasRunning := false
		for i := len(p.Status.ContainerStatuses) - 1; i >= 0; i-- {
			cs := p.Status.ContainerStatuses[i]
			switch {
			case cs.State.Waiting != nil && cs.State.Waiting.Reason != "":
				reason = cs.State.Waiting.Reason
			case cs.State.Terminated != nil && cs.State.Terminated.Reason != "":
				reason = cs.State.Terminated.Reason
			case cs.State.Terminated != nil && cs.State.Terminated.Signal != 0:
				reason = fmt.Sprintf("Signal:%d", cs.State.Terminated.Signal)
			case cs.State.Terminated != nil:
				reason = fmt.Sprintf("ExitCode:%d", cs.State.Terminated.ExitCode)
			case cs.Ready && cs.State.Running != nil:
				hasRunning = true
			}
		}
		if reason == "Completed" && hasRunning {
			reason = "Running"
			if !podReady(p) {
				reason = "NotReady"
			}
		}
	}

	if p.Status.Reason == "Evicted" {
		return "Evicted"
	}
	if p.DeletionTimestamp != nil {
		if p.Status.Reason == "NodeLost" {
			return "Unknown"
		}
		return "Terminating"
	}
	return reason
}

func podReady(p *corev1.Pod) bool {
	for _, c := range p.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func podProblem(p *corev1.Pod, status string, now time.Time) string {
	if p.Status.Reason == "Evicted" {
		return "Evicted"
	}
	if problemReasons[strings.TrimPrefix(status, "Init:")] {
		return status
	}
	statuses := append(append([]corev1.ContainerStatus{}, p.Status.InitContainerStatuses...), p.Status.ContainerStatuses...)
	for _, cs := range statuses {
		if cs.State.Waiting != nil && problemReasons[cs.State.Waiting.Reason] {
			return cs.State.Waiting.Reason
		}
		if cs.State.Terminated != nil && cs.State.Terminated.Reason == "OOMKilled" {
			return "OOMKilled"
		}
		if t := cs.LastTerminationState.Terminated; t != nil && t.Reason == "OOMKilled" && now.Sub(t.FinishedAt.Time) < oomRecentness {
			return "OOMKilled"
		}
	}
	if p.Status.Phase == corev1.PodPending && p.DeletionTimestamp == nil && now.Sub(p.CreationTimestamp.Time) > pendingGrace {
		for _, c := range p.Status.Conditions {
			if c.Type == corev1.PodScheduled && c.Status == corev1.ConditionFalse && c.Reason != "" {
				return "Pending: " + c.Reason
			}
		}
		return "Pending"
	}
	if p.Status.Phase == corev1.PodFailed {
		return "Failed"
	}
	return ""
}

func convertDeployment(d *appsv1.Deployment) model.Deployment {
	md := model.Deployment{
		Namespace:      d.Namespace,
		Name:           d.Name,
		Labels:         d.Labels,
		TemplateLabels: d.Spec.Template.Labels,
		Replicas:       1,
		Ready:          d.Status.ReadyReplicas,
		Updated:        d.Status.UpdatedReplicas,
		Available:      d.Status.AvailableReplicas,
		Created:        d.CreationTimestamp.Time,
	}
	if d.Spec.Replicas != nil {
		md.Replicas = *d.Spec.Replicas
	}
	if d.Spec.Selector != nil {
		md.Selector = d.Spec.Selector.MatchLabels
	}
	for _, c := range d.Spec.Template.Spec.Containers {
		md.Images = append(md.Images, c.Image)
	}
	return md
}

func convertNode(n *corev1.Node) model.Node {
	mn := model.Node{
		Name:        n.Name,
		Version:     n.Status.NodeInfo.KubeletVersion,
		CPUMilli:    n.Status.Allocatable.Cpu().MilliValue(),
		MemoryBytes: n.Status.Allocatable.Memory().Value(),
		Created:     n.CreationTimestamp.Time,
	}
	for k := range n.Labels {
		if role, ok := strings.CutPrefix(k, "node-role.kubernetes.io/"); ok && role != "" {
			mn.Roles = append(mn.Roles, role)
		}
	}
	sort.Strings(mn.Roles)
	for _, c := range n.Status.Conditions {
		switch {
		case c.Type == corev1.NodeReady:
			mn.Ready = c.Status == corev1.ConditionTrue
		case c.Status == corev1.ConditionTrue:
			mn.Pressure = append(mn.Pressure, string(c.Type))
		}
	}
	return mn
}

func convertEvent(e *corev1.Event) model.Event {
	last := e.LastTimestamp.Time
	if last.IsZero() {
		last = e.EventTime.Time
	}
	if last.IsZero() {
		last = e.FirstTimestamp.Time
	}
	if last.IsZero() {
		last = e.CreationTimestamp.Time
	}
	count := e.Count
	if count == 0 && e.Series != nil {
		count = e.Series.Count
	}
	return model.Event{
		Namespace: e.Namespace,
		Kind:      e.InvolvedObject.Kind,
		Name:      e.InvolvedObject.Name,
		Type:      e.Type,
		Reason:    e.Reason,
		Message:   strings.TrimSpace(e.Message),
		Count:     max(count, 1),
		Last:      last,
	}
}

type helmRelease struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
	Version   int    `json:"version"`
	Info      struct {
		Status       string    `json:"status"`
		LastDeployed time.Time `json:"last_deployed"`
		Notes        string    `json:"notes"`
	} `json:"info"`
	Chart struct {
		Metadata struct {
			Name       string `json:"name"`
			Version    string `json:"version"`
			AppVersion string `json:"appVersion"`
		} `json:"metadata"`
	} `json:"chart"`
}

// decodeHelmRelease decodes the "release" field of a Helm storage secret:
// base64 text wrapping (usually gzipped) JSON.
func decodeHelmRelease(data []byte) (model.Release, error) {
	raw, err := base64.StdEncoding.DecodeString(string(data))
	if err != nil {
		return model.Release{}, fmt.Errorf("decode helm release: %w", err)
	}
	if len(raw) > 2 && raw[0] == 0x1f && raw[1] == 0x8b {
		zr, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return model.Release{}, fmt.Errorf("decode helm release: %w", err)
		}
		raw, err = io.ReadAll(zr)
		if err != nil {
			return model.Release{}, fmt.Errorf("decode helm release: %w", err)
		}
	}
	var r helmRelease
	if err := json.Unmarshal(raw, &r); err != nil {
		return model.Release{}, fmt.Errorf("decode helm release: %w", err)
	}
	return model.Release{
		Name:       r.Name,
		Namespace:  r.Namespace,
		Chart:      r.Chart.Metadata.Name + "-" + r.Chart.Metadata.Version,
		AppVersion: r.Chart.Metadata.AppVersion,
		Status:     r.Info.Status,
		Revision:   r.Version,
		Updated:    r.Info.LastDeployed,
		Notes:      r.Info.Notes,
	}, nil
}

// latestReleases keeps the highest revision per release from Helm secret labels,
// decoding only those secrets.
func latestReleases(secrets []*corev1.Secret) []model.Release {
	latest := map[string]*corev1.Secret{}
	rev := func(s *corev1.Secret) int {
		var v int
		_, _ = fmt.Sscanf(s.Labels["version"], "%d", &v)
		return v
	}
	for _, s := range secrets {
		key := s.Namespace + "/" + s.Labels["name"]
		if cur, ok := latest[key]; !ok || rev(s) > rev(cur) {
			latest[key] = s
		}
	}
	out := make([]model.Release, 0, len(latest))
	for _, s := range latest {
		r, err := decodeHelmRelease(s.Data["release"])
		if err != nil {
			r = model.Release{Name: s.Labels["name"], Namespace: s.Namespace, Status: "undecodable: " + err.Error(), Revision: rev(s)}
		}
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Namespace != out[j].Namespace {
			return out[i].Namespace < out[j].Namespace
		}
		return out[i].Name < out[j].Name
	})
	return out
}
