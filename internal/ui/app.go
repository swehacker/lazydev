// Package ui implements the lazydev terminal interface.
package ui

import (
	"cmp"
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/swehacker/lazydev/internal/config"
	"github.com/swehacker/lazydev/internal/model"
	"github.com/swehacker/lazydev/internal/service"
)

type focus int

const (
	focusServices focus = iota
	focusResources
	focusDetail
)

const (
	snapshotThrottle = 100 * time.Millisecond
	actionTimeout    = 45 * time.Second
	scaleTimeout     = 15 * time.Second
)

type snapshotMsg struct{ snap model.Snapshot }

type actionResultMsg struct{ text string }

type scalePrompt struct {
	input                  textinput.Model
	active                 bool
	err                    string
	namespace, name, label string
}

func newScalePrompt() scalePrompt {
	input := textinput.New()
	input.Prompt = "="
	return scalePrompt{input: input}
}

type Model struct {
	cfg     *config.Config
	version string
	store   Snapshots
	backend Backend

	snap     model.Snapshot
	svcs     []service.LogicalService
	problems []service.Problem

	services  *listModel
	resources *listModel
	kind      resourceKind
	namespace string
	focus     focus
	active    focus // list driving the detail pane

	detail  *detailModel
	palette palette
	scale   scalePrompt

	showHelp bool
	zoom     bool
	width    int
	height   int
	status   string
}

func New(cfg *config.Config, version string, store Snapshots, backend Backend) *Model {
	return &Model{
		cfg:       cfg,
		version:   version,
		store:     store,
		backend:   backend,
		services:  newList(serviceColumns, "no services yet"),
		resources: newList(resourceColumns(kindPods), "nothing here"),
		kind:      kindPods,
		detail:    newDetail(backend),
		palette:   newPalette(),
		scale:     newScalePrompt(),
	}
}

func (m *Model) Init() tea.Cmd {
	return tea.Batch(waitSnapshot(m.store), tea.SetWindowTitle("lazydev"))
}

func waitSnapshot(s Snapshots) tea.Cmd {
	return func() tea.Msg {
		<-s.Changed()
		time.Sleep(snapshotThrottle)
		return snapshotMsg{snap: s.Snapshot()}
	}
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		return m, nil
	case snapshotMsg:
		return m, tea.Batch(waitSnapshot(m.store), m.applySnapshot(msg.snap))
	case logLinesMsg:
		return m, m.detail.logs.handleLines(msg)
	case settleMsg:
		m.detail.settle(msg)
		return m, nil
	case fetchMsg:
		m.detail.handleFetch(msg)
		return m, nil
	case actionResultMsg:
		m.status = msg.text
		return m, nil
	case tea.KeyMsg:
		return m, m.handleKey(msg)
	}
	return m, nil
}

func (m *Model) applySnapshot(snap model.Snapshot) tea.Cmd {
	m.snap = snap
	m.svcs = service.Aggregate(m.cfg, snap)
	m.problems = service.Problems(m.svcs, snap)
	now := time.Now()
	m.services.setRows(serviceRows(m.svcs, now))
	m.resources.setRows(resourceRows(m.kind, snap, m.namespace, now))
	return m.syncSelection()
}

func (m *Model) syncSelection() tea.Cmd {
	var sel selection
	if m.active == focusServices {
		key := m.services.selectedKey()
		if svc := serviceByName(m.svcs, key); svc != nil {
			sel = selection{kind: selService, key: key, svc: svc}
		}
	} else if key := m.resources.selectedKey(); key != "" {
		sel = resolve(m.kind, key, m.snap)
	}
	return m.detail.setSelection(sel, m.snap)
}

func (m *Model) layout() (leftW, svcH, resH, bodyH int) {
	bodyH = max(4, m.height-3)
	if !m.zoom {
		leftW = min(max(44, m.width*48/100), m.width-30)
	}
	svcH = max(3, bodyH*55/100)
	resH = bodyH - svcH
	return leftW, svcH, resH, bodyH
}

func (m *Model) handleKey(msg tea.KeyMsg) tea.Cmd {
	// Bubble Tea delivers fast key repeats as one multi-rune message; bindings expect single keys.
	if msg.Type == tea.KeyRunes && len(msg.Runes) > 1 && !msg.Paste && !m.textInputActive() {
		cmds := make([]tea.Cmd, 0, len(msg.Runes))
		for _, r := range msg.Runes {
			cmds = append(cmds, m.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}, Alt: msg.Alt}))
		}
		return tea.Batch(cmds...)
	}
	m.status = ""
	key := msg.String()
	if key == "ctrl+c" {
		return tea.Quit
	}
	if m.showHelp {
		m.showHelp = false
		return nil
	}
	if m.palette.active {
		return m.handlePaletteKey(msg)
	}
	if m.scale.active {
		return m.handleScaleKey(msg)
	}

	_, svcH, resH, bodyH := m.layout()
	if !m.textInputActive() {
		switch key {
		case "q":
			return tea.Quit
		case "?":
			m.showHelp = true
			return nil
		case ":":
			m.openPalette()
			return nil
		case "=":
			if m.focus != focusDetail || m.detail.tab != tabLogs {
				m.openScalePrompt()
			}
			return nil
		case "1":
			m.zoom = false
			return m.focusList(focusServices)
		case "2":
			m.zoom = false
			return m.focusList(focusResources)
		case "tab":
			return m.cycleFocus(1)
		case "shift+tab":
			return m.cycleFocus(-1)
		case "]":
			return m.detail.cycleTab(1)
		case "[":
			return m.detail.cycleTab(-1)
		case "z":
			m.zoom = !m.zoom
			if m.zoom {
				m.focus = focusDetail
			}
			return nil
		case "r":
			m.backend.RefreshHealth()
			m.status = "health checks re-probed"
			return nil
		}
	}

	switch m.focus {
	case focusServices, focusResources:
		list, height := m.services, svcH-3
		if m.focus == focusResources {
			list, height = m.resources, resH-3
		}
		before := list.selectedKey()
		handled, cmd := list.update(msg, height)
		if handled {
			if list.selectedKey() != before {
				return tea.Batch(cmd, m.syncSelection())
			}
			return cmd
		}
		if key == "enter" || key == "l" || key == "right" {
			m.focus = focusDetail
			return m.detail.setTab(m.detail.tab)
		}
	case focusDetail:
		handled, cmd := m.detail.update(msg, bodyH-2)
		if handled {
			return cmd
		}
		if key == "esc" || key == "h" || key == "left" {
			m.zoom = false
			m.focus = m.active
		}
	}
	return m.handleAction(key)
}

func (m *Model) handleAction(key string) tea.Cmd {
	action, ok := actionForKey(key)
	if !ok || (m.focus == focusDetail && m.detail.tab == tabLogs) {
		return nil
	}
	sel, ok := m.lifecycleTarget()
	if !ok {
		return nil
	}
	label := lifecycleLabel(sel)
	if sel.kind == selPod && action != ActionRestart {
		m.status = "pods can only be restarted (R)"
		return nil
	}
	immediate, success, verb := actionText(action)
	if sel.kind == selPod {
		immediate, success, verb = "restarting", "deleted", "restart"
	}
	m.status = immediate + " " + label + "…"

	kind := sel.kind
	var id, namespace, name string
	if kind == selContainer {
		id = sel.ctr.ID
	} else {
		namespace, name = sel.pod.Namespace, sel.pod.Name
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), actionTimeout)
		defer cancel()
		var err error
		if kind == selPod {
			err = m.backend.DeletePod(ctx, namespace, name)
		} else {
			err = m.backend.ContainerAction(ctx, id, action)
		}
		if err != nil {
			return actionResultMsg{text: verb + " " + label + ": " + err.Error()}
		}
		return actionResultMsg{text: success + " " + label}
	}
}

func actionForKey(key string) (Action, bool) {
	switch key {
	case "s":
		return ActionStop, true
	case "S":
		return ActionStart, true
	case "R":
		return ActionRestart, true
	default:
		return 0, false
	}
}

func actionText(action Action) (immediate, success, verb string) {
	switch action {
	case ActionStop:
		return "stopping", "stopped", "stop"
	case ActionStart:
		return "starting", "started", "start"
	default:
		return "restarting", "restarted", "restart"
	}
}

func lifecycleLabel(sel selection) string {
	if sel.kind == selPod {
		return sel.pod.Namespace + "/" + sel.pod.Name
	}
	return sel.ctr.Name
}

// lifecycleTarget is the container or pod owned by the focused pane.
func (m *Model) lifecycleTarget() (selection, bool) {
	var sel selection
	switch m.focus {
	case focusResources:
		if key := m.resources.selectedKey(); key != "" {
			sel = resolve(m.kind, key, m.snap)
		}
	case focusDetail:
		sel = m.detail.sel
	default:
		return selection{}, false
	}
	if sel.kind == selContainer && sel.ctr != nil {
		return sel, true
	}
	if sel.kind == selPod && sel.pod != nil {
		return sel, true
	}
	return selection{}, false
}

func (m *Model) actionHints() [][2]string {
	sel, ok := m.lifecycleTarget()
	if !ok {
		return nil
	}
	if sel.kind == selPod {
		return [][2]string{{"R", "restart"}}
	}
	return [][2]string{{"s", "stop"}, {"S", "start"}, {"R", "restart"}}
}

func (m *Model) scaleHints() [][2]string {
	if _, _, _, _, ok := m.scaleTarget(); !ok {
		return nil
	}
	return [][2]string{{"=", "scale"}}
}

func (m *Model) textInputActive() bool {
	return m.palette.active || m.scale.active ||
		(m.focus == focusServices && m.services.filtering) ||
		(m.focus == focusResources && m.resources.filtering) ||
		(m.focus == focusDetail && m.detail.inputActive())
}

func (m *Model) openScalePrompt() {
	namespace, name, label, replicas, ok := m.scaleTarget()
	if !ok {
		return
	}
	m.scale.active, m.scale.err = true, ""
	m.scale.namespace, m.scale.name, m.scale.label = namespace, name, label
	m.scale.input.Placeholder = strconv.FormatInt(int64(replicas), 10)
	m.scale.input.SetValue("")
	m.scale.input.Focus()
}

func (m *Model) handleScaleKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.scale.active = false
		m.scale.input.Blur()
		return nil
	case "enter":
		replicas, err := parseReplicas(m.scale.input.Value())
		if err != nil {
			m.scale.err = "enter a whole number from 0"
			return nil
		}
		namespace, name, label := m.scale.namespace, m.scale.name, m.scale.label
		m.scale.active = false
		m.scale.input.Blur()
		m.status = fmt.Sprintf("scaling %s to %d…", label, replicas)
		return func() tea.Msg {
			ctx, cancel := context.WithTimeout(context.Background(), scaleTimeout)
			defer cancel()
			if err := m.backend.ScaleDeployment(ctx, namespace, name, replicas); err != nil {
				return actionResultMsg{text: fmt.Sprintf("scale %s: %v", label, err)}
			}
			return actionResultMsg{text: fmt.Sprintf("scaled %s to %d", label, replicas)}
		}
	}
	m.scale.err = ""
	var cmd tea.Cmd
	m.scale.input, cmd = m.scale.input.Update(msg)
	return cmd
}

func parseReplicas(input string) (int32, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return 0, fmt.Errorf("empty replica count")
	}
	for _, r := range input {
		if r < '0' || r > '9' {
			return 0, fmt.Errorf("invalid replica count")
		}
	}
	replicas, err := strconv.ParseInt(input, 10, 32)
	if err != nil {
		return 0, err
	}
	return int32(replicas), nil
}

func (m *Model) scaleTarget() (namespace, name, label string, replicas int32, ok bool) {
	var sel selection
	switch m.focus {
	case focusServices:
		if svc := serviceByName(m.svcs, m.services.selectedKey()); svc != nil {
			sel = selection{kind: selService, svc: svc}
		}
	case focusResources:
		if m.kind == kindDeployments {
			sel = resolve(m.kind, m.resources.selectedKey(), m.snap)
		}
	case focusDetail:
		sel = m.detail.sel
	default:
		return "", "", "", 0, false
	}
	switch sel.kind {
	case selService:
		if sel.svc == nil || sel.svc.Deployment == nil {
			return "", "", "", 0, false
		}
		deployment := sel.svc.Deployment
		return deployment.Namespace, deployment.Name, sel.svc.Name, deployment.Replicas, true
	case selDeployment:
		if sel.deploy == nil {
			return "", "", "", 0, false
		}
		return sel.deploy.Namespace, sel.deploy.Name, sel.deploy.Namespace + "/" + sel.deploy.Name, sel.deploy.Replicas, true
	default:
		return "", "", "", 0, false
	}
}

func (m *Model) focusList(f focus) tea.Cmd {
	m.focus = f
	if m.active != f {
		m.active = f
		return m.syncSelection()
	}
	return nil
}

func (m *Model) cycleFocus(delta int) tea.Cmd {
	order := []focus{focusServices, focusResources, focusDetail}
	if m.zoom {
		order = []focus{focusDetail}
	}
	idx := 0
	for i, f := range order {
		if f == m.focus {
			idx = i
		}
	}
	next := order[(idx+delta+len(order))%len(order)]
	if next == focusDetail {
		m.focus = focusDetail
		return nil
	}
	return m.focusList(next)
}

func (m *Model) knownNamespaces() []string {
	set := map[string]bool{}
	for _, p := range m.snap.Kube.Pods {
		set[p.Namespace] = true
	}
	for _, d := range m.snap.Kube.Deployments {
		set[d.Namespace] = true
	}
	out := make([]string, 0, len(set))
	for ns := range set {
		out = append(out, ns)
	}
	sort.Strings(out)
	return out
}

func (m *Model) openPalette() {
	m.palette.open()
	suggestions := commandNames()
	for _, ns := range append([]string{"all"}, m.knownNamespaces()...) {
		suggestions = append(suggestions, "ns "+ns)
	}
	m.palette.input.SetSuggestions(suggestions)
}

func (m *Model) handlePaletteKey(msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.palette.close()
		return nil
	case "enter":
		input := m.palette.input.Value()
		cmd, err := parseCommand(input)
		if err != nil {
			m.palette.err = err.Error()
			return nil
		}
		m.palette.close()
		return m.execute(cmd)
	case "tab":
		if c := completions(m.palette.input.Value(), m.knownNamespaces()); len(c) > 0 {
			m.palette.input.SetValue(c[0])
			m.palette.input.CursorEnd()
		}
		return nil
	}
	m.palette.err = ""
	var cmd tea.Cmd
	m.palette.input, cmd = m.palette.input.Update(msg)
	return cmd
}

func (m *Model) execute(c command) tea.Cmd {
	switch c.typ {
	case cmdQuit:
		return tea.Quit
	case cmdHelp:
		m.showHelp = true
	case cmdServices:
		m.zoom = false
		return m.focusList(focusServices)
	case cmdNamespace:
		m.setNamespace(c.arg)
		return m.syncSelection()
	case cmdResource:
		if c.arg != "" {
			m.setNamespace(c.arg)
		}
		m.kind = c.kind
		m.resources.setColumns(resourceColumns(c.kind))
		m.resources.filter.SetValue("")
		m.resources.cursor, m.resources.offset = 0, 0
		m.resources.setRows(resourceRows(m.kind, m.snap, m.namespace, time.Now()))
		m.zoom = false
		m.focus = focusResources
		m.active = focusResources
		return m.syncSelection()
	}
	return nil
}

func (m *Model) setNamespace(ns string) {
	if ns == "all" || ns == "*" {
		ns = ""
	}
	m.namespace = ns
	m.resources.setRows(resourceRows(m.kind, m.snap, m.namespace, time.Now()))
	m.status = "namespace: " + orDash(ns)
}

func (m *Model) View() string {
	if m.width == 0 || m.height == 0 {
		return "starting lazydev…"
	}
	if m.width < 60 || m.height < 12 {
		return "lazydev needs at least 60x12 cells"
	}
	header := renderHeader(m.width, m.version, m.cfg.Compose.Project, m.snap, m.svcs, m.problems)
	leftW, svcH, resH, bodyH := m.layout()

	var body string
	if m.showHelp {
		body = lipgloss.Place(m.width, bodyH, lipgloss.Center, lipgloss.Center, helpView())
	} else {
		right := m.detail.view(m.width-leftW, bodyH, m.focus == focusDetail)
		if leftW > 0 {
			svcTitle := styleTitle.Render("[1] Services") + styleDim.Render(fmt.Sprintf(" %d", len(m.svcs)))
			if m.cfg.AutoDiscover() {
				svcTitle += styleFaint.Render(" auto")
			}
			resTitle := styleTitle.Render("[2] "+m.kind.String()) + styleDim.Render(fmt.Sprintf(" %d", len(m.resources.visible)))
			if m.kind.namespaced() {
				resTitle += styleDim.Render(" · ns ") + cmp.Or(m.namespace, "all")
			}
			svcBox := box(svcTitle, m.services.view(leftW-2, svcH-2, m.focus == focusServices), leftW, svcH, m.focus == focusServices)
			resBox := box(resTitle, m.resources.view(leftW-2, resH-2, m.focus == focusResources), leftW, resH, m.focus == focusResources)
			body = joinHorizontal(svcBox+"\n"+resBox, right)
		} else {
			body = right
		}
	}
	return strings.Join(append(header, body, m.footer()), "\n")
}

func (m *Model) footer() string {
	if m.palette.active {
		line := m.palette.input.View()
		if m.palette.err != "" {
			line += "  " + styleErr.Render(m.palette.err)
		}
		return fit(line, m.width)
	}
	if m.scale.active {
		line := m.scale.input.View()
		if m.scale.err != "" {
			line += "  " + styleErr.Render(m.scale.err)
		}
		return fit(line, m.width)
	}
	if m.status != "" {
		return fit(" "+styleAccent.Render(m.status), m.width)
	}
	var hints [][2]string
	switch {
	case m.focus == focusDetail && m.detail.tab == tabLogs:
		hints = [][2]string{{"space", "follow"}, {"/", "search"}, {"n/N", "match"}, {"F", "only"}, {"l", "level"}, {"s", "stack"},
			{"p", "previous"}, {"t", "time"}, {"w", "wrap"}, {"e", "events"}, {"[ ]", "tabs"}, {"z", "zoom"}, {"esc", "back"}}
	case m.focus == focusDetail:
		hints = append(append(m.scaleHints(), m.actionHints()...), [][2]string{{"j/k", "scroll"}, {"[ ]", "tabs"}, {"z", "zoom"}, {"esc", "back"}, {"?", "help"}}...)
	default:
		hints = append(append(m.scaleHints(), m.actionHints()...), [][2]string{{"j/k", "move"}, {"enter", "details"}, {"/", "filter"}, {":", "command"}, {"1/2", "panels"},
			{"[ ]", "tabs"}, {"z", "zoom"}, {"r", "re-probe"}, {"?", "help"}, {"q", "quit"}}...)
	}
	var parts []string
	for _, h := range hints {
		parts = append(parts, styleKey.Render(h[0])+" "+styleDim.Render(h[1]))
	}
	return fit(" "+strings.Join(parts, "  "), m.width)
}
