// Package kube observes a Kubernetes cluster with informers and the metrics API.
package kube

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/informers"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	"k8s.io/client-go/tools/clientcmd"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"

	"github.com/swehacker/lazydev/internal/model"
	"github.com/swehacker/lazydev/internal/state"
)

const (
	maxBackoff       = 30 * time.Second
	healthInterval   = 10 * time.Second
	metricsInterval  = 5 * time.Second
	rebuildDebounce  = 200 * time.Millisecond
	maxEvents        = 1000
	failuresToReset  = 2
	pingTimeout      = 5 * time.Second
	helmOwnerLabel   = "owner=helm"
	allNamespacesKey = ""
)

// Clients bundles the API clients of the current connection.
type Clients struct {
	Kube    kubernetes.Interface
	Metrics metricsclient.Interface
	Context string
}

type Source struct {
	store       *state.Store
	kubeContext string
	namespaces  []string

	mu      sync.RWMutex
	clients *Clients
}

func New(store *state.Store, kubeContext string, namespaces []string) *Source {
	return &Source{store: store, kubeContext: kubeContext, namespaces: namespaces}
}

// Clients returns the current API clients, or nil while disconnected.
func (s *Source) Clients() *Clients {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.clients
}

func (s *Source) Run(ctx context.Context) {
	backoff := time.Second
	for ctx.Err() == nil {
		started := time.Now()
		err := s.session(ctx)
		if ctx.Err() != nil {
			return
		}
		if time.Since(started) > time.Minute {
			backoff = time.Second
		}
		s.mu.Lock()
		s.clients = nil
		s.mu.Unlock()
		s.store.Apply(func(snap *model.Snapshot) {
			snap.Kube = model.KubeState{Available: false, Err: errString(err), Context: snap.Kube.Context}
		})
		sleep(ctx, backoff)
		backoff = min(backoff*2, maxBackoff)
	}
}

func errString(err error) string {
	if err == nil {
		return "disconnected"
	}
	return err.Error()
}

func (s *Source) restConfig() (*rest.Config, string, error) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	loader := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{CurrentContext: s.kubeContext})
	raw, err := loader.RawConfig()
	if err != nil {
		return nil, "", fmt.Errorf("load kubeconfig: %w", err)
	}
	name := s.kubeContext
	if name == "" {
		name = raw.CurrentContext
	}
	if _, ok := raw.Contexts[name]; !ok {
		return nil, name, fmt.Errorf("context %q not found in kubeconfig", name)
	}
	cfg, err := loader.ClientConfig()
	if err != nil {
		return nil, name, err
	}
	cfg.QPS, cfg.Burst = 50, 100
	return cfg, name, nil
}

// session connects, runs informers until the cluster stops answering, then returns.
func (s *Source) session(ctx context.Context) error {
	cfg, contextName, err := s.restConfig()
	s.store.Apply(func(snap *model.Snapshot) { snap.Kube.Context = contextName })
	if err != nil {
		return err
	}
	kc, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return err
	}
	mc, err := metricsclient.NewForConfig(cfg)
	if err != nil {
		return err
	}
	pingCfg := rest.CopyConfig(cfg)
	pingCfg.Timeout = pingTimeout
	pinger, err := kubernetes.NewForConfig(pingCfg)
	if err != nil {
		return err
	}
	if _, err := pinger.Discovery().ServerVersion(); err != nil {
		return fmt.Errorf("cluster unreachable: %w", err)
	}

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	w := newWatcher(kc, s.namespaces)
	w.start(ctx)
	if !w.waitForSync(ctx) {
		return errors.New("informer cache sync cancelled")
	}

	s.mu.Lock()
	s.clients = &Clients{Kube: kc, Metrics: mc, Context: contextName}
	s.mu.Unlock()

	rebuild := func() {
		ks := w.snapshot(time.Now())
		ks.Available, ks.Context = true, contextName
		s.store.Apply(func(snap *model.Snapshot) {
			ks.PodMetrics, ks.NodeMetrics = snap.Kube.PodMetrics, snap.Kube.NodeMetrics
			ks.MetricsAvailable, ks.MetricsErr = snap.Kube.MetricsAvailable, snap.Kube.MetricsErr
			snap.Kube = ks
		})
	}
	rebuild()

	go s.metricsLoop(ctx, mc)

	debounce := time.NewTimer(time.Hour)
	debounce.Stop()
	health := time.NewTicker(healthInterval)
	defer health.Stop()
	// Pending-for-too-long problems depend on time, not only on watch events.
	periodic := time.NewTicker(15 * time.Second)
	defer periodic.Stop()
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-w.changed:
			debounce.Reset(rebuildDebounce)
		case <-debounce.C:
			rebuild()
		case <-periodic.C:
			rebuild()
		case <-health.C:
			if _, err := pinger.Discovery().ServerVersion(); err != nil {
				failures++
				if failures >= failuresToReset {
					return fmt.Errorf("cluster unreachable: %w", err)
				}
				continue
			}
			failures = 0
		}
	}
}

func (s *Source) metricsLoop(ctx context.Context, mc metricsclient.Interface) {
	ticker := time.NewTicker(metricsInterval)
	defer ticker.Stop()
	for {
		s.collectMetrics(ctx, mc)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func (s *Source) collectMetrics(ctx context.Context, mc metricsclient.Interface) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()

	pods := map[string]model.Usage{}
	nodes := map[string]model.Usage{}
	var metricsErr error
	namespaces := s.namespaces
	if len(namespaces) == 0 {
		namespaces = []string{allNamespacesKey}
	}
	for _, ns := range namespaces {
		list, err := mc.MetricsV1beta1().PodMetricses(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			metricsErr = err
			break
		}
		for _, pm := range list.Items {
			var u model.Usage
			for _, c := range pm.Containers {
				u.CPUMilli += float64(c.Usage.Cpu().MilliValue())
				u.MemoryBytes += c.Usage.Memory().Value()
			}
			pods[pm.Namespace+"/"+pm.Name] = u
		}
	}
	if metricsErr == nil {
		list, err := mc.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
		if err != nil {
			metricsErr = err
		} else {
			for _, nm := range list.Items {
				nodes[nm.Name] = model.Usage{CPUMilli: float64(nm.Usage.Cpu().MilliValue()), MemoryBytes: nm.Usage.Memory().Value()}
			}
		}
	}
	if ctx.Err() != nil && metricsErr == nil {
		return
	}

	if metricsErr != nil {
		msg := metricsErr.Error()
		s.store.Apply(func(snap *model.Snapshot) {
			snap.Kube.MetricsAvailable, snap.Kube.MetricsErr = false, msg
			snap.Kube.PodMetrics, snap.Kube.NodeMetrics = nil, nil
		})
		return
	}

	now := time.Now()
	samples := make(map[string]model.Sample, len(pods)+len(nodes))
	for key, u := range pods {
		samples["pod:"+key] = model.Sample{At: now, CPUMilli: u.CPUMilli, MemoryBytes: u.MemoryBytes}
	}
	for name, u := range nodes {
		samples[model.HistoryKeyNode(name)] = model.Sample{At: now, CPUMilli: u.CPUMilli, MemoryBytes: u.MemoryBytes}
	}
	s.store.Apply(func(snap *model.Snapshot) {
		snap.Kube.MetricsAvailable, snap.Kube.MetricsErr = true, ""
		snap.Kube.PodMetrics, snap.Kube.NodeMetrics = pods, nodes
	})
	s.store.Apply(state.AppendHistory(samples, "pod:", true))
}

// watcher owns the informers of one connection.
type watcher struct {
	factories   []informers.SharedInformerFactory
	pods        []cache.SharedIndexInformer
	deployments []cache.SharedIndexInformer
	events      []cache.SharedIndexInformer
	secrets     []cache.SharedIndexInformer
	nodes       cache.SharedIndexInformer
	changed     chan struct{}
}

func newWatcher(kc kubernetes.Interface, namespaces []string) *watcher {
	w := &watcher{changed: make(chan struct{}, 1)}
	if len(namespaces) == 0 {
		namespaces = []string{allNamespacesKey}
	}
	for _, ns := range namespaces {
		f := informers.NewSharedInformerFactoryWithOptions(kc, 0, informers.WithNamespace(ns))
		helm := informers.NewSharedInformerFactoryWithOptions(kc, 0, informers.WithNamespace(ns),
			informers.WithTweakListOptions(func(o *metav1.ListOptions) { o.LabelSelector = helmOwnerLabel }))
		w.factories = append(w.factories, f, helm)
		w.pods = append(w.pods, w.track(f.Core().V1().Pods().Informer()))
		w.deployments = append(w.deployments, w.track(f.Apps().V1().Deployments().Informer()))
		w.events = append(w.events, w.track(f.Core().V1().Events().Informer()))
		w.secrets = append(w.secrets, w.track(helm.Core().V1().Secrets().Informer()))
	}
	cluster := informers.NewSharedInformerFactory(kc, 0)
	w.factories = append(w.factories, cluster)
	w.nodes = w.track(cluster.Core().V1().Nodes().Informer())
	return w
}

func (w *watcher) track(inf cache.SharedIndexInformer) cache.SharedIndexInformer {
	notify := func() {
		select {
		case w.changed <- struct{}{}:
		default:
		}
	}
	_, _ = inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { notify() },
		UpdateFunc: func(any, any) { notify() },
		DeleteFunc: func(any) { notify() },
	})
	return inf
}

func (w *watcher) start(ctx context.Context) {
	for _, f := range w.factories {
		f.Start(ctx.Done())
	}
}

func (w *watcher) waitForSync(ctx context.Context) bool {
	var synced []cache.InformerSynced
	for _, group := range [][]cache.SharedIndexInformer{w.pods, w.deployments, w.events, w.secrets, {w.nodes}} {
		for _, inf := range group {
			synced = append(synced, inf.HasSynced)
		}
	}
	return cache.WaitForCacheSync(ctx.Done(), synced...)
}

func (w *watcher) snapshot(now time.Time) model.KubeState {
	var ks model.KubeState
	for _, obj := range listAll[*corev1.Pod](w.pods) {
		ks.Pods = append(ks.Pods, convertPod(obj, now))
	}
	sort.Slice(ks.Pods, func(i, j int) bool { return ks.Pods[i].Key() < ks.Pods[j].Key() })

	for _, obj := range listAll[*appsv1.Deployment](w.deployments) {
		ks.Deployments = append(ks.Deployments, convertDeployment(obj))
	}
	sort.Slice(ks.Deployments, func(i, j int) bool { return ks.Deployments[i].Key() < ks.Deployments[j].Key() })

	for _, obj := range listAll[*corev1.Node]([]cache.SharedIndexInformer{w.nodes}) {
		ks.Nodes = append(ks.Nodes, convertNode(obj))
	}
	sort.Slice(ks.Nodes, func(i, j int) bool { return ks.Nodes[i].Name < ks.Nodes[j].Name })

	for _, obj := range listAll[*corev1.Event](w.events) {
		ks.Events = append(ks.Events, convertEvent(obj))
	}
	sort.Slice(ks.Events, func(i, j int) bool { return ks.Events[i].Last.After(ks.Events[j].Last) })
	if len(ks.Events) > maxEvents {
		ks.Events = ks.Events[:maxEvents]
	}

	ks.Releases = latestReleases(listAll[*corev1.Secret](w.secrets))
	return ks
}

func listAll[T any](infs []cache.SharedIndexInformer) []T {
	var out []T
	for _, inf := range infs {
		for _, obj := range inf.GetStore().List() {
			if t, ok := obj.(T); ok {
				out = append(out, t)
			}
		}
	}
	return out
}

func sleep(ctx context.Context, d time.Duration) {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
	case <-t.C:
	}
}
