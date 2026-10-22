package logs

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"
)

// Target identifies one log stream: a Kubernetes container or a Docker container.
type Target struct {
	Label string

	Namespace string
	Pod       string
	Container string

	ContainerID string
}

func (t Target) Key() string {
	if t.ContainerID != "" {
		return "docker:" + t.ContainerID
	}
	return "k8s:" + t.Namespace + "/" + t.Pod + "/" + t.Container
}

func (t Target) IsKube() bool { return t.ContainerID == "" }

type Options struct {
	Tail     int64
	Previous bool
}

// Streamer opens log streams. Either client may be nil when that backend is unavailable.
type Streamer struct {
	Kube   kubernetes.Interface
	Docker *client.Client
}

// Stream copies lines from target into out until ctx ends or the stream closes.
// Failures and stream ends are reported as KindMeta lines.
func (s Streamer) Stream(ctx context.Context, t Target, opts Options, out chan<- Line) {
	var (
		rc  io.ReadCloser
		err error
	)
	if t.IsKube() {
		rc, err = s.kubeStream(ctx, t, opts)
	} else {
		rc, err = s.dockerStream(ctx, t, opts)
	}
	if err != nil {
		if ctx.Err() == nil {
			send(ctx, out, Line{Source: t.Label, Kind: KindMeta, Level: LevelError, Text: "lazydev: " + err.Error(), Time: time.Now()})
		}
		return
	}
	defer func() { _ = rc.Close() }()

	r := bufio.NewReaderSize(rc, 64<<10)
	for {
		raw, err := r.ReadString('\n')
		if raw = strings.TrimRight(raw, "\r\n"); raw != "" {
			ts, text := SplitTimestamp(raw)
			if !send(ctx, out, Line{Source: t.Label, Time: ts, Text: text}) {
				return
			}
			if hint := Hint(text); hint != "" {
				send(ctx, out, Line{Source: t.Label, Kind: KindMeta, Level: LevelWarn, Text: "lazydev: " + hint, Time: time.Now()})
			}
		}
		if err != nil {
			if ctx.Err() == nil {
				msg := "lazydev: log stream ended"
				if !errors.Is(err, io.EOF) {
					msg = "lazydev: " + err.Error()
				}
				send(ctx, out, Line{Source: t.Label, Kind: KindMeta, Text: msg, Time: time.Now()})
			}
			return
		}
	}
}

func send(ctx context.Context, out chan<- Line, l Line) bool {
	select {
	case out <- l:
		return true
	case <-ctx.Done():
		return false
	}
}

func (s Streamer) kubeStream(ctx context.Context, t Target, opts Options) (io.ReadCloser, error) {
	if s.Kube == nil {
		return nil, errors.New("cluster unavailable")
	}
	tail := opts.Tail
	return s.Kube.CoreV1().Pods(t.Namespace).GetLogs(t.Pod, &corev1.PodLogOptions{
		Container:  t.Container,
		Follow:     !opts.Previous,
		Previous:   opts.Previous,
		Timestamps: true,
		TailLines:  &tail,
	}).Stream(ctx)
}

func (s Streamer) dockerStream(ctx context.Context, t Target, opts Options) (io.ReadCloser, error) {
	if s.Docker == nil {
		return nil, errors.New("docker unavailable")
	}
	if opts.Previous {
		return nil, errors.New("previous logs are only available for Kubernetes pods")
	}
	info, err := s.Docker.ContainerInspect(ctx, t.ContainerID)
	if err != nil {
		return nil, err
	}
	rc, err := s.Docker.ContainerLogs(ctx, t.ContainerID, container.LogsOptions{
		ShowStdout: true, ShowStderr: true, Follow: true, Timestamps: true, Tail: strconv.FormatInt(opts.Tail, 10),
	})
	if err != nil {
		return nil, err
	}
	if info.Config != nil && info.Config.Tty {
		return rc, nil
	}
	pr, pw := io.Pipe()
	go func() {
		_, err := stdcopy.StdCopy(pw, pw, rc)
		_ = rc.Close()
		pw.CloseWithError(err)
	}()
	return readCloser{pr, func() error { _ = rc.Close(); return pr.Close() }}, nil
}

type readCloser struct {
	io.Reader
	close func() error
}

func (r readCloser) Close() error { return r.close() }

// Hint explains known environment failures that surface inside log streams.
func Hint(text string) string {
	if strings.Contains(text, "fsnotify watcher") && strings.Contains(text, "too many open files") {
		return "the kubelet ran out of inotify instances, so following stopped. Raise the limit in the engine VM, " +
			"e.g. `colima ssh -- sudo sysctl -w fs.inotify.max_user_instances=1024`, then reselect to resume"
	}
	return ""
}

// TargetLabel builds a short label for a pod container.
func TargetLabel(pod, container string, multiContainer bool) string {
	if multiContainer {
		return fmt.Sprintf("%s/%s", pod, container)
	}
	return pod
}
