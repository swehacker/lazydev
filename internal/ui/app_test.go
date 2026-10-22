package ui

import (
	"context"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/swehacker/lazydev/internal/config"
	"github.com/swehacker/lazydev/internal/logs"
	"github.com/swehacker/lazydev/internal/model"
)

type fakeBackend struct{ refreshed int }

func (f *fakeBackend) Streamer() logs.Streamer { return logs.Streamer{} }
func (f *fakeBackend) Describe(context.Context, Ref) (string, error) {
	return "kind: Deployment", nil
}
func (f *fakeBackend) Env(context.Context, Ref) (string, error) { return "A=1", nil }
func (f *fakeBackend) RefreshHealth()                           { f.refreshed++ }

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
