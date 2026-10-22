package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/swehacker/lazydev/internal/model"
	"github.com/swehacker/lazydev/internal/service"
)

type tab int

const (
	tabLogs tab = iota
	tabEvents
	tabDescribe
	tabEnv
	tabStats
)

var tabNames = []string{"Logs", "Events", "Describe", "Env", "Stats"}

const (
	settleDelay  = 250 * time.Millisecond
	fetchTimeout = 10 * time.Second
)

type fetchMsg struct {
	id   string
	tab  tab
	text string
	err  error
}

type settleMsg struct{ gen int }

// textView is a scrollable block of pre-rendered lines.
type textView struct {
	lines  []string
	offset int
}

func (t *textView) set(lines []string) {
	t.lines = lines
	t.offset = min(t.offset, max(0, len(lines)-1))
}

func (t *textView) update(msg tea.KeyMsg, height int) bool {
	maxOffset := max(0, len(t.lines)-height)
	switch msg.String() {
	case "j", "down":
		t.offset++
	case "k", "up":
		t.offset--
	case "ctrl+d", "pgdown":
		t.offset += max(1, height/2)
	case "ctrl+u", "pgup":
		t.offset -= max(1, height/2)
	case "g", "home":
		t.offset = 0
	case "G", "end":
		t.offset = maxOffset
	default:
		return false
	}
	t.offset = min(max(0, t.offset), maxOffset)
	return true
}

func (t *textView) view(width, height int) []string {
	t.offset = min(t.offset, max(0, len(t.lines)-height))
	out := make([]string, 0, height)
	for i := t.offset; i < len(t.lines) && len(out) < height; i++ {
		out = append(out, fit(t.lines[i], width))
	}
	return out
}

type detailModel struct {
	backend Backend
	tab     tab
	sel     selection
	selGen  int
	snap    model.Snapshot

	logs        *logView
	logsStarted bool

	describe, env  textView
	describeLoaded string
	envLoaded      string
	describeText   string
	events, stats  textView
}

func newDetail(backend Backend) *detailModel {
	return &detailModel{backend: backend, logs: newLogView(backend)}
}

// setSelection switches the detail pane to sel. Same-object refreshes keep state.
func (d *detailModel) setSelection(sel selection, snap model.Snapshot) tea.Cmd {
	d.snap = snap
	if sel.id() == d.sel.id() {
		d.sel = sel
		return d.refresh()
	}
	d.sel = sel
	d.selGen++
	d.logsStarted = false
	d.describeLoaded, d.envLoaded = "", ""
	d.describe, d.env, d.events, d.stats = textView{}, textView{}, textView{}, textView{}
	cmds := []tea.Cmd{d.logs.reset()}
	if sel.hasLogs() {
		gen := d.selGen
		cmds = append(cmds, tea.Tick(settleDelay, func(time.Time) tea.Msg { return settleMsg{gen: gen} }))
	}
	cmds = append(cmds, d.refresh())
	return tea.Batch(cmds...)
}

// settle starts log streams once the cursor rests on a selection.
func (d *detailModel) settle(msg settleMsg) {
	if msg.gen != d.selGen || !d.sel.hasLogs() {
		return
	}
	d.logsStarted = true
	d.logs.setTargets(d.sel.logTargets(d.snap))
	d.logs.addEvents(d.matchingEvents())
}

// refresh updates derived views after a snapshot or selection change.
func (d *detailModel) refresh() tea.Cmd {
	if d.logsStarted {
		d.logs.setTargets(d.sel.logTargets(d.snap))
		d.logs.addEvents(d.matchingEvents())
	}
	d.events.set(d.eventLines())
	d.stats.set(d.statsLines())
	return d.load()
}

// load fetches describe/env text for the active tab when not loaded yet.
func (d *detailModel) load() tea.Cmd {
	id := d.sel.id()
	switch {
	case d.tab == tabDescribe && d.describeLoaded != id:
		d.describeLoaded = id
		d.describeText = ""
		d.describe.set(d.describeLines("loading…"))
		return d.fetch(tabDescribe)
	case d.tab == tabEnv && d.envLoaded != id:
		d.envLoaded = id
		d.env.set([]string{styleDim.Render("loading…")})
		return d.fetch(tabEnv)
	}
	return nil
}

func (d *detailModel) fetch(t tab) tea.Cmd {
	ref, ok := d.sel.ref()
	id := d.sel.id()
	if !ok {
		return func() tea.Msg { return fetchMsg{id: id, tab: t} }
	}
	backend := d.backend
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), fetchTimeout)
		defer cancel()
		var text string
		var err error
		if t == tabDescribe {
			text, err = backend.Describe(ctx, ref)
		} else {
			text, err = backend.Env(ctx, ref)
		}
		return fetchMsg{id: id, tab: t, text: text, err: err}
	}
}

func (d *detailModel) handleFetch(msg fetchMsg) {
	if msg.id != d.sel.id() {
		return
	}
	body := msg.text
	if msg.err != nil {
		body = styleErr.Render("error: " + msg.err.Error())
	}
	switch msg.tab {
	case tabDescribe:
		d.describeText = body
		d.describe.set(d.describeLines(body))
	case tabEnv:
		if body == "" {
			body = styleDim.Render("no environment for this selection")
		}
		d.env.set(strings.Split(body, "\n"))
	}
}

func (d *detailModel) setTab(t tab) tea.Cmd {
	d.tab = t
	return d.load()
}

func (d *detailModel) cycleTab(delta int) tea.Cmd {
	return d.setTab(tab((int(d.tab) + delta + len(tabNames)) % len(tabNames)))
}

// inputActive reports whether a text input inside the detail pane has focus.
func (d *detailModel) inputActive() bool { return d.tab == tabLogs && d.logs.searching }

func (d *detailModel) update(msg tea.KeyMsg, height int) (bool, tea.Cmd) {
	switch d.tab {
	case tabLogs:
		return d.logs.update(msg)
	case tabEvents:
		return d.events.update(msg, height), nil
	case tabDescribe:
		return d.describe.update(msg, height), nil
	case tabEnv:
		return d.env.update(msg, height), nil
	case tabStats:
		return d.stats.update(msg, height), nil
	}
	return false, nil
}

func (d *detailModel) view(width, height int, focused bool) string {
	var tabs []string
	for i, name := range tabNames {
		if tab(i) == d.tab {
			tabs = append(tabs, styleTabActive.Render(name))
		} else {
			tabs = append(tabs, styleTab.Render(name))
		}
	}
	title := strings.Join(tabs, styleFaint.Render(" │ "))
	if t := d.sel.title(); t != "" {
		title = styleTitle.Render(t) + styleFaint.Render("  ") + title
	}
	innerW, innerH := width-2, height-2
	var lines []string
	if d.sel.kind == selNone {
		lines = []string{styleDim.Render(" select a service or resource")}
	} else {
		switch d.tab {
		case tabLogs:
			if d.sel.hasLogs() {
				lines = d.logs.lines(innerW, innerH)
			} else {
				lines = []string{styleDim.Render(" logs are not available for this kind of resource")}
			}
		case tabEvents:
			lines = d.events.view(innerW, innerH)
		case tabDescribe:
			lines = d.describe.view(innerW, innerH)
		case tabEnv:
			lines = d.env.view(innerW, innerH)
		case tabStats:
			lines = d.stats.view(innerW, innerH)
		}
	}
	return box(title, lines, width, height, focused)
}

func (d *detailModel) matchingEvents() []model.Event {
	if !d.sel.supportsEvents() {
		return nil
	}
	var out []model.Event
	for _, e := range d.snap.Kube.Events {
		if d.sel.matchesEvent(e) {
			out = append(out, e)
		}
	}
	return out
}

func (d *detailModel) eventLines() []string {
	if !d.sel.supportsEvents() {
		return []string{styleDim.Render(" Kubernetes events are not available for this selection")}
	}
	events := d.matchingEvents()
	if len(events) == 0 {
		return []string{styleDim.Render(" no recent events")}
	}
	now := time.Now()
	lines := make([]string, 0, len(events))
	for _, e := range events {
		st := styleDim
		if e.Type == "Warning" {
			st = styleWarn
		}
		reason := e.Reason
		if e.Count > 1 {
			reason += fmt.Sprintf(" ×%d", e.Count)
		}
		lines = append(lines, fmt.Sprintf(" %s %s %s %s",
			styleFaint.Render(fitRight(humanAge(e.Last, now), 4)),
			st.Render(fit(reason, 22)),
			styleDim.Render(fit(strings.ToLower(e.Kind)+"/"+e.Name, 34)),
			e.Message))
	}
	return lines
}

func (d *detailModel) statsLines() []string {
	rows := d.sel.historyRows(d.snap)
	if len(rows) == 0 {
		return []string{styleDim.Render(" no resource metrics for this selection")}
	}
	var lines []string
	if !d.snap.Kube.MetricsAvailable && d.snap.Kube.MetricsErr != "" {
		lines = append(lines, styleWarn.Render(" metrics API unavailable: "+d.snap.Kube.MetricsErr), "")
	}
	for _, r := range rows {
		h := d.snap.History[r.key]
		lines = append(lines, " "+styleBold.Render(r.label))
		if len(h) == 0 {
			lines = append(lines, styleDim.Render("   no samples yet"), "")
			continue
		}
		cpu := make([]float64, len(h))
		mem := make([]float64, len(h))
		var peakCPU float64
		for i, s := range h {
			cpu[i], mem[i] = s.CPUMilli, float64(s.MemoryBytes)
			peakCPU = max(peakCPU, s.CPUMilli)
		}
		last := h[len(h)-1]
		lines = append(lines,
			fmt.Sprintf("   %s %s %s %s", styleDim.Render("CPU"), styleAccent.Render(sparkline(cpu, 40, 0)),
				fitRight(humanCPU(last.CPUMilli), 6), styleFaint.Render("peak "+humanCPU(peakCPU))))
		memText := humanBytes(last.MemoryBytes)
		if r.memLimit > 0 {
			memText += styleFaint.Render(" / " + humanBytes(r.memLimit))
		}
		lines = append(lines,
			fmt.Sprintf("   %s %s %s", styleDim.Render("MEM"), styleOK.Render(sparkline(mem, 40, float64(r.memLimit))), fitRight(memText, 6)), "")
	}
	return lines
}

// describeLines prefixes the fetched text with a locally computed overview.
func (d *detailModel) describeLines(body string) []string {
	lines := d.overview()
	if body != "" {
		if len(lines) > 0 {
			lines = append(lines, "")
		}
		lines = append(lines, strings.Split(body, "\n")...)
	}
	return lines
}

func (d *detailModel) overview() []string {
	kv := func(k, v string) string { return " " + styleDim.Render(fit(k, 12)) + v }
	now := time.Now()
	switch d.sel.kind {
	case selService:
		s := d.sel.svc
		lines := []string{kv("status", statusStyle(s.Status).Render(s.Status.String()))}
		if s.Reason != "" {
			lines = append(lines, kv("reason", s.Reason))
		}
		lines = append(lines, kv("ready", fmt.Sprintf("%d/%d", s.Ready, s.Desired)))
		if s.Namespace != "" {
			lines = append(lines, kv("namespace", s.Namespace), kv("selector", s.Selector))
		}
		for _, c := range s.Containers {
			lines = append(lines, kv("container", c.Name+styleFaint.Render(" "+c.Status)))
		}
		if s.Image != "" {
			drift := s.Drift.String()
			if s.DriftDetail != "" {
				drift += styleFaint.Render(" (" + s.DriftDetail + ")")
			}
			lines = append(lines, kv("image", s.Image), kv("drift", orDash(drift)))
		}
		if s.HealthURL != "" {
			result := styleDim.Render("pending")
			if s.Health != nil {
				st := styleOK
				if !s.Health.OK {
					st = styleErr
				}
				result = st.Render(s.Health.Detail) + styleFaint.Render(fmt.Sprintf(" %dms, %s ago", s.Health.Latency.Milliseconds(), humanAge(s.Health.Checked, now)))
			}
			lines = append(lines, kv("health", s.HealthURL), kv("", result))
		}
		if s.Auto {
			lines = append(lines, "", styleFaint.Render(" auto-discovered; add a lazydev.yaml to group resources and add health checks"))
		}
		return lines
	case selRelease:
		r := d.sel.release
		lines := []string{
			kv("release", r.Name), kv("namespace", r.Namespace), kv("chart", r.Chart), kv("app version", orDash(r.AppVersion)),
			kv("status", r.Status), kv("revision", fmt.Sprint(r.Revision)), kv("updated", r.Updated.Local().Format(time.DateTime)),
		}
		if r.Notes != "" {
			lines = append(lines, "", styleDim.Render(" NOTES"))
			lines = append(lines, strings.Split(r.Notes, "\n")...)
		}
		return lines
	case selEvent:
		e := d.sel.event
		return []string{
			kv("type", e.Type), kv("reason", e.Reason), kv("object", e.Namespace+" "+strings.ToLower(e.Kind)+"/"+e.Name),
			kv("count", fmt.Sprint(e.Count)), kv("last seen", e.Last.Local().Format(time.DateTime)), "", " " + e.Message,
		}
	case selImage:
		img := d.sel.image
		state, st := imageClusterState(*img, d.snap)
		lines := []string{
			kv("tags", orDash(strings.Join(img.Tags, ", "))), kv("id", img.ID), kv("size", humanBytes(img.Size)),
			kv("created", img.Created.Local().Format(time.DateTime)), kv("k3d", st.Render(state)),
		}
		for _, dep := range d.snap.Kube.Deployments {
			for _, ref := range dep.Images {
				for _, tag := range img.Tags {
					if model.NormalizeImageRef(ref) == model.NormalizeImageRef(tag) {
						lines = append(lines, kv("used by", dep.Namespace+"/"+dep.Name))
					}
				}
			}
		}
		return lines
	case selPod:
		p := d.sel.pod
		lines := []string{kv("status", p.Status), kv("node", p.Node)}
		if p.Problem != "" {
			lines = append(lines, kv("problem", styleErr.Render(p.Problem)))
		}
		for _, c := range p.Containers {
			state := c.State
			if c.Reason != "" {
				state += " (" + c.Reason + ")"
			}
			if c.LastTerminationReason != "" {
				state += styleFaint.Render(" last: " + c.LastTerminationReason)
			}
			kind := "container"
			if c.Init {
				kind = "init"
			}
			lines = append(lines, kv(kind, c.Name+" "+styleFaint.Render(c.Image)+" "+state))
		}
		return lines
	}
	return nil
}

// serviceByName finds a logical service in a slice.
func serviceByName(svcs []service.LogicalService, name string) *service.LogicalService {
	for i := range svcs {
		if svcs[i].Name == name {
			return &svcs[i]
		}
	}
	return nil
}
