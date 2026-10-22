package kube

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

var now = time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)

func pod(mutate func(*corev1.Pod)) *corev1.Pod {
	ctrl := true
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "platform", Name: "order-service-684698f6cf-dwn5m",
			Labels:            map[string]string{"pod-template-hash": "684698f6cf"},
			CreationTimestamp: metav1.NewTime(now.Add(-time.Hour)),
			OwnerReferences:   []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "order-service-684698f6cf", Controller: &ctrl}},
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "order-service:local"}}},
		Status: corev1.PodStatus{
			Phase:      corev1.PodRunning,
			Conditions: []corev1.PodCondition{{Type: corev1.PodReady, Status: corev1.ConditionTrue}},
			ContainerStatuses: []corev1.ContainerStatus{{
				Name: "app", Ready: true, Image: "docker.io/library/order-service:local", ImageID: "sha256:abc",
				State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
			}},
		},
	}
	if mutate != nil {
		mutate(p)
	}
	return p
}

func TestConvertPodHealthy(t *testing.T) {
	got := convertPod(pod(nil), now)
	if got.Status != "Running" || got.Problem != "" || got.Ready != 1 || got.Total != 1 {
		t.Fatalf("pod = %+v", got)
	}
	if got.OwnerKind != "Deployment" || got.OwnerName != "order-service" {
		t.Errorf("owner = %s/%s", got.OwnerKind, got.OwnerName)
	}
	if got.Containers[0].ImageID != "sha256:abc" {
		t.Errorf("container = %+v", got.Containers[0])
	}
}

func TestConvertPodProblems(t *testing.T) {
	cases := map[string]struct {
		mutate        func(*corev1.Pod)
		status, probl string
	}{
		"crashloop": {func(p *corev1.Pod) {
			p.Status.ContainerStatuses[0].Ready = false
			p.Status.ContainerStatuses[0].RestartCount = 7
			p.Status.ContainerStatuses[0].State = corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}
		}, "CrashLoopBackOff", "CrashLoopBackOff"},
		"image pull": {func(p *corev1.Pod) {
			p.Status.Phase = corev1.PodPending
			p.Status.ContainerStatuses[0].State = corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ImagePullBackOff"}}
		}, "ImagePullBackOff", "ImagePullBackOff"},
		"recent oom": {func(p *corev1.Pod) {
			p.Status.ContainerStatuses[0].LastTerminationState = corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
				Reason: "OOMKilled", ExitCode: 137, FinishedAt: metav1.NewTime(now.Add(-time.Minute)),
			}}
		}, "Running", "OOMKilled"},
		"old oom is ignored": {func(p *corev1.Pod) {
			p.Status.ContainerStatuses[0].LastTerminationState = corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{
				Reason: "OOMKilled", FinishedAt: metav1.NewTime(now.Add(-time.Hour)),
			}}
		}, "Running", ""},
		"evicted": {func(p *corev1.Pod) {
			p.Status.Phase, p.Status.Reason = corev1.PodFailed, "Evicted"
			p.Status.ContainerStatuses[0].State = corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 137}}
		}, "Evicted", "Evicted"},
		"pending unschedulable": {func(p *corev1.Pod) {
			p.Status.Phase = corev1.PodPending
			p.Status.ContainerStatuses = nil
			p.Status.Conditions = []corev1.PodCondition{{Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: "Unschedulable"}}
		}, "Pending", "Pending: Unschedulable"},
		"fresh pending is fine": {func(p *corev1.Pod) {
			p.CreationTimestamp = metav1.NewTime(now.Add(-10 * time.Second))
			p.Status.Phase = corev1.PodPending
			p.Status.ContainerStatuses = nil
		}, "Pending", ""},
		"init crash": {func(p *corev1.Pod) {
			p.Spec.InitContainers = []corev1.Container{{Name: "migrate"}}
			p.Status.Phase = corev1.PodPending
			p.Status.InitContainerStatuses = []corev1.ContainerStatus{{Name: "migrate",
				State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}}}
		}, "Init:CrashLoopBackOff", "Init:CrashLoopBackOff"},
		"terminating": {func(p *corev1.Pod) {
			ts := metav1.NewTime(now)
			p.DeletionTimestamp = &ts
		}, "Terminating", ""},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := convertPod(pod(tc.mutate), now)
			if got.Status != tc.status || got.Problem != tc.probl {
				t.Errorf("status=%q problem=%q, want %q/%q", got.Status, got.Problem, tc.status, tc.probl)
			}
		})
	}
}

func encodeRelease(t *testing.T, js string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	if _, err := zw.Write([]byte(js)); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return []byte(base64.StdEncoding.EncodeToString(buf.Bytes()))
}

func TestLatestReleasesDecodesNewestRevision(t *testing.T) {
	secret := func(rev, status string) *corev1.Secret {
		return &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{Namespace: "apps", Labels: map[string]string{"owner": "helm", "name": "platform", "version": rev}},
			Data: map[string][]byte{"release": encodeRelease(t, `{"name":"platform","namespace":"apps","version":`+rev+
				`,"info":{"status":"`+status+`","last_deployed":"2026-10-21T10:00:00Z"},"chart":{"metadata":{"name":"platform","version":"0.3.0","appVersion":"1.0"}}}`)},
		}
	}
	got := latestReleases([]*corev1.Secret{secret("9", "superseded"), secret("10", "deployed"), secret("2", "superseded")})
	if len(got) != 1 {
		t.Fatalf("releases = %+v", got)
	}
	r := got[0]
	if r.Revision != 10 || r.Status != "deployed" || r.Chart != "platform-0.3.0" || r.AppVersion != "1.0" {
		t.Errorf("release = %+v", r)
	}
}

func TestConvertNode(t *testing.T) {
	n := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "k3d-dev-server-0", Labels: map[string]string{"node-role.kubernetes.io/control-plane": "true", "node-role.kubernetes.io/master": "true"}},
		Status: corev1.NodeStatus{Conditions: []corev1.NodeCondition{
			{Type: corev1.NodeReady, Status: corev1.ConditionTrue},
			{Type: corev1.NodeDiskPressure, Status: corev1.ConditionTrue},
			{Type: corev1.NodeMemoryPressure, Status: corev1.ConditionFalse},
		}},
	}
	got := convertNode(n)
	if !got.Ready || len(got.Pressure) != 1 || got.Pressure[0] != "DiskPressure" || len(got.Roles) != 2 || got.Roles[0] != "control-plane" {
		t.Errorf("node = %+v", got)
	}
}
