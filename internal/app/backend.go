package app

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/docker/docker/api/types/container"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/yaml"

	"github.com/swehacker/lazydev/internal/logs"
	"github.com/swehacker/lazydev/internal/source/docker"
	"github.com/swehacker/lazydev/internal/source/health"
	"github.com/swehacker/lazydev/internal/source/kube"
	"github.com/swehacker/lazydev/internal/ui"
)

var errClusterUnavailable = errors.New("cluster unavailable")

// Backend implements ui.Backend on top of the live sources.
type Backend struct {
	docker *docker.Source
	kube   *kube.Source
	prober *health.Prober
}

func (b *Backend) Streamer() logs.Streamer {
	s := logs.Streamer{Docker: b.docker.Client()}
	if c := b.kube.Clients(); c != nil {
		s.Kube = c.Kube
	}
	return s
}

func (b *Backend) RefreshHealth() { b.prober.Trigger() }

func (b *Backend) ContainerAction(ctx context.Context, id string, action ui.Action) error {
	cli := b.docker.Client()
	switch action {
	case ui.ActionStop:
		return cli.ContainerStop(ctx, id, container.StopOptions{})
	case ui.ActionStart:
		return cli.ContainerStart(ctx, id, container.StartOptions{})
	case ui.ActionRestart:
		return cli.ContainerRestart(ctx, id, container.StopOptions{})
	default:
		return fmt.Errorf("unknown container action %d", action)
	}
}

func (b *Backend) DeletePod(ctx context.Context, namespace, name string) error {
	c := b.kube.Clients()
	if c == nil {
		return errClusterUnavailable
	}
	return c.Kube.CoreV1().Pods(namespace).Delete(ctx, name, metav1.DeleteOptions{})
}

func (b *Backend) Describe(ctx context.Context, ref ui.Ref) (string, error) {
	switch ref.Kind {
	case ui.RefPod:
		pod, err := b.pod(ctx, ref)
		if err != nil {
			return "", err
		}
		pod.APIVersion, pod.Kind = "v1", "Pod"
		pod.ManagedFields = nil
		return toYAML(pod)
	case ui.RefDeployment:
		d, err := b.deployment(ctx, ref)
		if err != nil {
			return "", err
		}
		d.APIVersion, d.Kind = "apps/v1", "Deployment"
		d.ManagedFields = nil
		return toYAML(d)
	case ui.RefNode:
		c := b.kube.Clients()
		if c == nil {
			return "", errClusterUnavailable
		}
		n, err := c.Kube.CoreV1().Nodes().Get(ctx, ref.Name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		n.APIVersion, n.Kind = "v1", "Node"
		n.ManagedFields = nil
		n.Status.Images = nil
		return toYAML(n)
	case ui.RefContainer:
		info, err := b.docker.Client().ContainerInspect(ctx, ref.ID)
		if err != nil {
			return "", err
		}
		return toYAML(info)
	case ui.RefImage:
		info, err := b.docker.Client().ImageInspect(ctx, ref.ID)
		if err != nil {
			return "", err
		}
		return toYAML(info)
	}
	return "", nil
}

func (b *Backend) Env(ctx context.Context, ref ui.Ref) (string, error) {
	switch ref.Kind {
	case ui.RefPod:
		pod, err := b.pod(ctx, ref)
		if err != nil {
			return "", err
		}
		return formatPodEnv(pod.Spec.Containers), nil
	case ui.RefDeployment:
		d, err := b.deployment(ctx, ref)
		if err != nil {
			return "", err
		}
		return formatPodEnv(d.Spec.Template.Spec.Containers), nil
	case ui.RefContainer:
		info, err := b.docker.Client().ContainerInspect(ctx, ref.ID)
		if err != nil {
			return "", err
		}
		if info.Config == nil {
			return "", nil
		}
		return formatPlainEnv(info.Config.Env), nil
	case ui.RefImage:
		info, err := b.docker.Client().ImageInspect(ctx, ref.ID)
		if err != nil {
			return "", err
		}
		if info.Config == nil {
			return "", nil
		}
		return formatPlainEnv(info.Config.Env), nil
	}
	return "", nil
}

func (b *Backend) pod(ctx context.Context, ref ui.Ref) (*corev1.Pod, error) {
	c := b.kube.Clients()
	if c == nil {
		return nil, errClusterUnavailable
	}
	return c.Kube.CoreV1().Pods(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
}

func (b *Backend) deployment(ctx context.Context, ref ui.Ref) (*appsv1.Deployment, error) {
	c := b.kube.Clients()
	if c == nil {
		return nil, errClusterUnavailable
	}
	return c.Kube.AppsV1().Deployments(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
}

func toYAML(v any) (string, error) {
	out, err := yaml.Marshal(v)
	if err != nil {
		return "", err
	}
	return strings.TrimRight(string(out), "\n"), nil
}

// formatPodEnv lists env vars per container; references to secrets and config maps are shown, not resolved.
func formatPodEnv(containers []corev1.Container) string {
	var b strings.Builder
	for i, c := range containers {
		if i > 0 {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "# container %s\n", c.Name)
		for _, from := range c.EnvFrom {
			switch {
			case from.ConfigMapRef != nil:
				fmt.Fprintf(&b, "%s<- configMap %s\n", from.Prefix, from.ConfigMapRef.Name)
			case from.SecretRef != nil:
				fmt.Fprintf(&b, "%s<- secret %s\n", from.Prefix, from.SecretRef.Name)
			}
		}
		for _, e := range c.Env {
			switch {
			case e.ValueFrom == nil:
				fmt.Fprintf(&b, "%s=%s\n", e.Name, e.Value)
			case e.ValueFrom.SecretKeyRef != nil:
				fmt.Fprintf(&b, "%s <- secret %s/%s\n", e.Name, e.ValueFrom.SecretKeyRef.Name, e.ValueFrom.SecretKeyRef.Key)
			case e.ValueFrom.ConfigMapKeyRef != nil:
				fmt.Fprintf(&b, "%s <- configMap %s/%s\n", e.Name, e.ValueFrom.ConfigMapKeyRef.Name, e.ValueFrom.ConfigMapKeyRef.Key)
			case e.ValueFrom.FieldRef != nil:
				fmt.Fprintf(&b, "%s <- field %s\n", e.Name, e.ValueFrom.FieldRef.FieldPath)
			case e.ValueFrom.ResourceFieldRef != nil:
				fmt.Fprintf(&b, "%s <- resource %s\n", e.Name, e.ValueFrom.ResourceFieldRef.Resource)
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

func formatPlainEnv(env []string) string {
	sorted := append([]string(nil), env...)
	sort.Strings(sorted)
	return strings.Join(sorted, "\n")
}
