package ui

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/swehacker/lazydev/internal/config"
	"github.com/swehacker/lazydev/internal/logs"
	"github.com/swehacker/lazydev/internal/model"
)

var ansiEscape = regexp.MustCompile(`\x1b\[[0-9;]*m`)

type recordedAction struct {
	kind   string
	action Action
	id     string
	ns     string
	name   string
}

type fakeBackend struct {
	refreshed int
	actions   []recordedAction
	err       error
}

func (f *fakeBackend) Streamer() logs.Streamer { return logs.Streamer{} }
func (f *fakeBackend) Describe(context.Context, Ref) (string, error) {
	return "kind: Deployment", nil
}
func (f *fakeBackend) Env(context.Context, Ref) (string, error) { return "A=1", nil }
func (f *fakeBackend) RefreshHealth()                           { f.refreshed++ }
func (f *fakeBackend) ContainerAction(_ context.Context, id string, action Action) error {
	f.actions = append(f.actions, recordedAction{kind: "container", action: action, id: id})
	return f.err
}
func (f *fakeBackend) DeletePod(_ context.Context, namespace, name string) error {
	f.actions = append(f.actions, recordedAction{kind: "pod", ns: namespace, name: name})
	return f.err
}

type fakeStore struct{ snap model.Snapshot }

func (f fakeStore) Changed() <-chan struct{} { return make(chan struct{}) }
func (f fakeStore) Snapshot() model.Snapshot { return f.snap }

func testSnapshot() model.Snapshot {
	var snap model.Snapshot
	snap.Kube.Available = true
	snap.Kube.Context = "k3d-dev"
	for _, name := range []string{"alpha", "bravo", "charlie", "delta"} {
		snap.Kube.Deployments = append(snap.Kube.Deployments, model.Deployment{
			Namespace: "apps", Name: name, Replicas: 1, Ready: 1,
			Selector: map[string]string{"app": name}, TemplateLabels: map[string]string{"app": name},
		})
		snap.Kube.Pods = append(snap.Kube.Pods, model.Pod{
			Namespace: "apps", Name: name + "-1", Labels: map[string]string{"app": name},
			Status: "Running", Phase: "Running", Ready: 1, Total: 1, OwnerKind: "Deployment", OwnerName: name,
			Containers: []model.PodContainer{{Name: "app", Ready: true, State: "running"}},
		})
	}
	return snap
}

func newTestModel(t *testing.T) (*Model, *fakeBackend) {
	t.Helper()
	backend := &fakeBackend{}
	snap := testSnapshot()
	m := New(&config.Config{}, "test", fakeStore{snap}, backend)
	m.Update(tea.WindowSizeMsg{Width: 160, Height: 40})
	m.Update(snapshotMsg{snap: snap})
	return m, backend
}

func runes(s string) tea.KeyMsg { return tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(s)} }

func TestBurstOfKeysMovesCursorPerKey(t *testing.T) {
	m, _ := newTestModel(t)
	m.Update(runes("jj"))
	if got := m.services.selectedKey(); got != "charlie" {
		t.Fatalf("selected = %q, want charlie after two j presses delivered as one message", got)
	}
	if m.detail.sel.title() != "charlie" {
		t.Errorf("detail follows selection: %q", m.detail.sel.title())
	}
}

func TestJumpToEndAndStart(t *testing.T) {
	m, _ := newTestModel(t)
	m.Update(runes("G"))
	last := m.services.selectedKey()
	if last == "alpha" || last == "" {
		t.Fatalf("G should select the last service, got %q", last)
	}
	m.Update(runes("g"))
	if got := m.services.selectedKey(); got == last {
		t.Errorf("g should return to the first service, got %q", got)
	}
}

func TestPaletteSwitchesResourceKindAndNamespace(t *testing.T) {
	m, _ := newTestModel(t)
	m.Update(runes(":"))
	if !m.palette.active {
		t.Fatal("palette should open on ':'")
	}
	m.palette.input.SetValue("pods apps")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.kind != kindPods || m.namespace != "apps" || m.focus != focusResources {
		t.Fatalf("kind=%v ns=%q focus=%v", m.kind, m.namespace, m.focus)
	}
	if m.detail.sel.kind != selPod {
		t.Errorf("detail should show the selected pod, got kind %v", m.detail.sel.kind)
	}
	view := m.View()
	if !strings.Contains(view, "[2] pods") || !strings.Contains(view, "alpha-1") {
		t.Errorf("resources panel not rendered:\n%s", view)
	}
}

func TestDescribeTabFetchesFromBackend(t *testing.T) {
	m, _ := newTestModel(t)
	_, cmd := m.Update(runes("]]"))
	if m.detail.tab != tabDescribe {
		t.Fatalf("tab = %v", m.detail.tab)
	}
	msg := findMsg[fetchMsg](t, cmd)
	m.Update(msg)
	if !strings.Contains(strings.Join(m.detail.describe.lines, "\n"), "kind: Deployment") {
		t.Errorf("describe lines = %q", m.detail.describe.lines)
	}
}

func TestReprobeAndHelp(t *testing.T) {
	m, backend := newTestModel(t)
	m.Update(runes("r"))
	if backend.refreshed != 1 || !strings.Contains(m.View(), "health checks re-probed") {
		t.Errorf("re-probe not triggered or not reported")
	}
	m.Update(runes("?"))
	if !strings.Contains(m.View(), "keyboard reference") {
		t.Error("help overlay missing")
	}
	m.Update(runes("x"))
	if m.showHelp {
		t.Error("any key should close help")
	}
}

func TestViewFitsTerminal(t *testing.T) {
	m, _ := newTestModel(t)
	lines := strings.Split(m.View(), "\n")
	if len(lines) != 40 {
		t.Errorf("view has %d lines, want 40", len(lines))
	}
}

func openResource(m *Model, command string) {
	m.Update(runes(":"))
	m.palette.input.SetValue(command)
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
}

func withContainers(t *testing.T) (*Model, *fakeBackend) {
	t.Helper()
	m, backend := newTestModel(t)
	snap := testSnapshot()
	snap.Docker.Containers = []model.Container{
		{ID: "abc", Name: "api", State: "running", ComposeProject: "dev", ComposeService: "api"},
		{ID: "def", Name: "redis", State: "exited"},
	}
	m.Update(snapshotMsg{snap: snap})
	return m, backend
}

func finishAction(t *testing.T, m *Model, cmd tea.Cmd) {
	t.Helper()
	m.Update(findMsg[actionResultMsg](t, cmd))
}

func TestContainerLifecycleKeys(t *testing.T) {
	m, backend := withContainers(t)
	openResource(m, "containers")

	checks := []struct {
		key     string
		action  Action
		pending string
		done    string
	}{
		{"s", ActionStop, "stopping api", "stopped api"},
		{"S", ActionStart, "starting api", "started api"},
		{"R", ActionRestart, "restarting api", "restarted api"},
	}
	for _, c := range checks {
		_, cmd := m.Update(runes(c.key))
		if !strings.Contains(m.View(), c.pending) {
			t.Fatalf("%s status = %q, want %q", c.key, m.status, c.pending)
		}
		finishAction(t, m, cmd)
		if !strings.Contains(m.View(), c.done) {
			t.Fatalf("%s result = %q, want %q", c.key, m.status, c.done)
		}
	}
	if len(backend.actions) != 3 {
		t.Fatalf("actions = %+v", backend.actions)
	}
	for i, c := range checks {
		got := backend.actions[i]
		if got.kind != "container" || got.action != c.action || got.id != "abc" {
			t.Errorf("action %d = %+v, want %s id abc", i, got, c.pending)
		}
	}
}

func TestComposeRowStopsContainer(t *testing.T) {
	m, backend := withContainers(t)
	openResource(m, "compose")
	_, cmd := m.Update(runes("s"))
	finishAction(t, m, cmd)
	if len(backend.actions) != 1 || backend.actions[0].id != "abc" || backend.actions[0].action != ActionStop {
		t.Fatalf("actions = %+v", backend.actions)
	}
	if !strings.Contains(m.View(), "stopped api") {
		t.Fatalf("status = %q", m.status)
	}
}

func TestContainerActionError(t *testing.T) {
	m, backend := withContainers(t)
	backend.err = errors.New("no such container")
	openResource(m, "containers")
	_, cmd := m.Update(runes("s"))
	finishAction(t, m, cmd)
	if !strings.Contains(m.View(), "stop api: no such container") {
		t.Fatalf("status = %q", m.status)
	}
}

func TestPodRestartDeletesPod(t *testing.T) {
	m, backend := newTestModel(t)
	openResource(m, "pods")
	_, cmd := m.Update(runes("R"))
	if !strings.Contains(m.View(), "restarting apps/alpha-1") {
		t.Fatalf("status = %q", m.status)
	}
	finishAction(t, m, cmd)
	if !strings.Contains(m.View(), "deleted apps/alpha-1") {
		t.Fatalf("status = %q", m.status)
	}
	if len(backend.actions) != 1 || backend.actions[0].ns != "apps" || backend.actions[0].name != "alpha-1" {
		t.Fatalf("actions = %+v", backend.actions)
	}
}

func TestPodStartAndStopDoNotCallBackend(t *testing.T) {
	m, backend := newTestModel(t)
	openResource(m, "pods")
	for _, key := range []string{"s", "S"} {
		_, cmd := m.Update(runes(key))
		if cmd != nil {
			t.Fatalf("%s returned a command", key)
		}
		if !strings.Contains(m.View(), "pods can only be restarted (R)") {
			t.Fatalf("%s status = %q", key, m.status)
		}
	}
	if len(backend.actions) != 0 {
		t.Fatalf("actions = %+v", backend.actions)
	}
}

func TestDeploymentIgnoresLifecycleKeys(t *testing.T) {
	m, backend := newTestModel(t)
	openResource(m, "deploy")
	for _, key := range []string{"s", "S", "R"} {
		_, cmd := m.Update(runes(key))
		if cmd != nil {
			t.Fatalf("%s returned a command", key)
		}
	}
	if len(backend.actions) != 0 || m.status != "" {
		t.Fatalf("status %q actions %+v", m.status, backend.actions)
	}
}

func TestServiceRowIgnoresLifecycleKeys(t *testing.T) {
	m, backend := newTestModel(t)
	for _, key := range []string{"s", "S", "R"} {
		m.Update(runes(key))
	}
	if len(backend.actions) != 0 {
		t.Fatalf("actions = %+v", backend.actions)
	}
}

func TestLogsTabKeepsStackTraceKey(t *testing.T) {
	m, backend := newTestModel(t)
	openResource(m, "pods")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if m.focus != focusDetail || m.detail.tab != tabLogs {
		t.Fatalf("focus %v tab %v", m.focus, m.detail.tab)
	}
	m.Update(runes("s"))
	if !m.detail.logs.expand {
		t.Fatal("s should expand stack traces on the logs tab")
	}
	_, cmd := m.Update(runes("R"))
	if cmd != nil || len(backend.actions) != 0 {
		t.Fatalf("logs tab ran an action: cmd %v actions %+v", cmd != nil, backend.actions)
	}
	if strings.Contains(m.status, "restarting") {
		t.Fatalf("status = %q", m.status)
	}
}

func TestDetailTabStartsContainer(t *testing.T) {
	m, backend := withContainers(t)
	openResource(m, "containers")
	m.Update(tea.KeyMsg{Type: tea.KeyEnter})
	m.Update(runes("]"))
	if m.focus != focusDetail || m.detail.tab == tabLogs {
		t.Fatalf("focus %v tab %v", m.focus, m.detail.tab)
	}
	_, cmd := m.Update(runes("S"))
	finishAction(t, m, cmd)
	if len(backend.actions) != 1 || backend.actions[0].action != ActionStart || backend.actions[0].id != "abc" {
		t.Fatalf("actions = %+v", backend.actions)
	}
	if !strings.Contains(m.View(), "started api") {
		t.Fatalf("status = %q", m.status)
	}
}

func TestFilterDoesNotStopContainer(t *testing.T) {
	m, backend := withContainers(t)
	openResource(m, "containers")
	m.Update(runes("/"))
	m.Update(runes("s"))
	if len(backend.actions) != 0 || strings.Contains(m.status, "stopping") {
		t.Fatalf("filter ran an action: status %q actions %+v", m.status, backend.actions)
	}
}

func TestActionFooterHints(t *testing.T) {
	m, _ := withContainers(t)
	openResource(m, "containers")
	footer := plainFooter(m.View())
	for _, want := range []string{"stop", " start", "restart"} {
		if !strings.Contains(footer, want) {
			t.Errorf("container footer %q missing %q", footer, want)
		}
	}

	openResource(m, "pods")
	footer = plainFooter(m.View())
	if !strings.Contains(footer, "restart") || strings.Contains(footer, "stop") || strings.Contains(footer, " start") {
		t.Errorf("pod footer = %q", footer)
	}
}

func plainFooter(view string) string {
	lines := strings.Split(view, "\n")
	return ansiEscape.ReplaceAllString(lines[len(lines)-1], "")
}

// findMsg runs cmd (and nested batches) until it produces a message of type T.
func findMsg[T any](t *testing.T, cmd tea.Cmd) T {
	t.Helper()
	var zero T
	queue := []tea.Cmd{cmd}
	deadline := time.Now().Add(2 * time.Second)
	for len(queue) > 0 && time.Now().Before(deadline) {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		done := make(chan tea.Msg, 1)
		go func() { done <- c() }()
		select {
		case msg := <-done:
			if v, ok := msg.(T); ok {
				return v
			}
			if batch, ok := msg.(tea.BatchMsg); ok {
				queue = append(queue, batch...)
			}
		case <-time.After(300 * time.Millisecond):
		}
	}
	t.Fatalf("no %T produced", zero)
	return zero
}
