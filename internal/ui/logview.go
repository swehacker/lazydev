package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/swehacker/lazydev/internal/logs"
	"github.com/swehacker/lazydev/internal/model"
)

const (
	logTail        = 500
	logBatchMax    = 2000
	logBatchDelay  = 30 * time.Millisecond
	maxSourceWidth = 28
	eventSource    = "event"
)

type logLinesMsg struct {
	gen   int
	lines []logs.Line
}

type renderKey struct {
	entry    *logs.Entry
	stackLen int
}

type logView struct {
	backend Backend

	buf     *logs.Buffer
	gen     int
	ctx     context.Context
	cancel  context.CancelFunc
	out     chan logs.Line
	streams map[string]logs.Target
	targets []logs.Target
	opts    logs.Options
	events  map[string]bool
	sources map[string]bool

	view     []int
	follow   bool
	top      int
	topEntry *logs.Entry
	unseen   int

	minLevel    logs.Level
	search      string
	searchInput textinput.Model
	searching   bool
	onlyMatches bool
	expand      bool
	timestamps  bool
	wrap        bool
	showEvents  bool

	width, height int
	cache         map[renderKey][]string
	cacheWidth    int
}

func newLogView(backend Backend) *logView {
	ti := textinput.New()
	ti.Prompt = "search: "
	ti.CharLimit = 120
	v := &logView{
		backend:     backend,
		buf:         logs.NewBuffer(logs.DefaultCapacity),
		searchInput: ti,
		timestamps:  true,
		showEvents:  true,
		opts:        logs.Options{Tail: logTail},
	}
	v.reset()
	return v
}

// reset stops all streams and clears the buffer for a new selection.
func (v *logView) reset() tea.Cmd {
	if v.cancel != nil {
		v.cancel()
	}
	v.gen++
	v.ctx, v.cancel = context.WithCancel(context.Background())
	v.out = make(chan logs.Line, 4096)
	v.streams = map[string]logs.Target{}
	v.targets = nil
	v.events = map[string]bool{}
	v.sources = map[string]bool{}
	v.buf.Reset()
	v.view = nil
	v.follow, v.top, v.topEntry, v.unseen = true, 0, nil, 0
	v.cache = map[renderKey][]string{}
	return v.waitCmd()
}

func (v *logView) waitCmd() tea.Cmd {
	gen, out, ctx := v.gen, v.out, v.ctx
	return func() tea.Msg {
		select {
		case <-ctx.Done():
			return nil
		case l := <-out:
			lines := []logs.Line{l}
			timer := time.NewTimer(logBatchDelay)
			defer timer.Stop()
			for len(lines) < logBatchMax {
				select {
				case l := <-out:
					lines = append(lines, l)
				case <-timer.C:
					return logLinesMsg{gen: gen, lines: lines}
				case <-ctx.Done():
					return nil
				}
			}
			return logLinesMsg{gen: gen, lines: lines}
		}
	}
}

// setTargets starts streams for targets not yet followed. Ended streams keep their lines.
func (v *logView) setTargets(targets []logs.Target) {
	v.targets = targets
	streamer := v.backend.Streamer()
	for _, t := range targets {
		if _, ok := v.streams[t.Key()]; ok {
			continue
		}
		v.streams[t.Key()] = t
		v.sources[t.Label] = true
		go streamer.Stream(v.ctx, t, v.opts, v.out)
	}
}

func (v *logView) restart() tea.Cmd {
	targets := v.targets
	cmd := v.reset()
	v.setTargets(targets)
	return cmd
}

func (v *logView) handleLines(msg logLinesMsg) tea.Cmd {
	if msg.gen != v.gen {
		return nil
	}
	added := 0
	for _, l := range msg.lines {
		if v.buf.Add(l) {
			added++
		}
	}
	if !v.follow {
		v.unseen += added
	}
	v.recompute()
	return v.waitCmd()
}

func (v *logView) addEvents(events []model.Event) {
	added := false
	for _, e := range events {
		key := eventKey(e) + fmt.Sprint(e.Count)
		if v.events[key] {
			continue
		}
		v.events[key] = true
		lvl := logs.LevelInfo
		if e.Type == "Warning" {
			lvl = logs.LevelWarn
		}
		v.buf.Add(logs.Line{
			Source: eventSource, Kind: logs.KindEvent, Level: lvl, Time: e.Last,
			Text: fmt.Sprintf("%s %s/%s: %s", e.Reason, strings.ToLower(e.Kind), e.Name, e.Message),
		})
		added = true
	}
	if added {
		v.recompute()
	}
}

func (v *logView) passes(e *logs.Entry) bool {
	if e.Kind == logs.KindEvent && !v.showEvents {
		return false
	}
	if e.Kind == logs.KindLog && v.minLevel != logs.LevelUnknown {
		if e.Level == logs.LevelUnknown {
			if v.minLevel > logs.LevelInfo {
				return false
			}
		} else if e.Level < v.minLevel {
			return false
		}
	}
	if v.onlyMatches && v.search != "" && !v.matches(e) {
		return false
	}
	return true
}

func (v *logView) matches(e *logs.Entry) bool {
	term := strings.ToLower(v.search)
	if term == "" {
		return false
	}
	if strings.Contains(strings.ToLower(e.Message), term) || strings.Contains(strings.ToLower(e.Logger), term) ||
		strings.Contains(strings.ToLower(e.Source), term) {
		return true
	}
	for _, s := range e.Stack {
		if strings.Contains(strings.ToLower(s), term) {
			return true
		}
	}
	return false
}

func (v *logView) recompute() {
	entries := v.buf.Entries()
	v.view = v.view[:0]
	newTop := -1
	for i, e := range entries {
		if v.passes(e) {
			if e == v.topEntry {
				newTop = len(v.view)
			}
			v.view = append(v.view, i)
		}
	}
	if !v.follow {
		if newTop >= 0 {
			v.top = newTop
		}
		v.top = min(max(0, v.top), max(0, len(v.view)-1))
		v.setTopEntry()
	}
	if len(v.cache) > 3*logs.DefaultCapacity {
		v.cache = map[renderKey][]string{}
	}
}

func (v *logView) setTopEntry() {
	if v.top >= 0 && v.top < len(v.view) {
		v.topEntry = v.buf.At(v.view[v.top])
	} else {
		v.topEntry = nil
	}
}

func (v *logView) invalidate() {
	v.cache = map[renderKey][]string{}
}

func (v *logView) bodyHeight() int {
	h := v.height - 1
	if v.searching {
		h--
	}
	return max(0, h)
}

// followTop returns the index of the first entry visible when following.
func (v *logView) followTop() int {
	rows := 0
	h := v.bodyHeight()
	for i := len(v.view) - 1; i >= 0; i-- {
		rows += len(v.rows(v.buf.At(v.view[i])))
		if rows >= h {
			return i
		}
	}
	return 0
}

func (v *logView) pause() {
	if v.follow {
		v.follow = false
		v.top = v.followTop()
		v.unseen = 0
		v.setTopEntry()
	}
}

func (v *logView) scroll(delta int) {
	v.pause()
	v.top = min(max(0, v.top+delta), max(0, len(v.view)-1))
	if delta > 0 && v.top >= v.followTop() {
		v.follow, v.unseen = true, 0
	}
	v.setTopEntry()
}

func (v *logView) jumpMatch(forward bool) {
	if v.search == "" || len(v.view) == 0 {
		return
	}
	v.pause()
	n := len(v.view)
	for step := 1; step <= n; step++ {
		i := v.top - step
		if forward {
			i = v.top + step
		}
		i = ((i % n) + n) % n
		if v.matches(v.buf.At(v.view[i])) {
			v.top = i
			v.setTopEntry()
			return
		}
	}
}

func (v *logView) cycleLevel() {
	switch v.minLevel {
	case logs.LevelUnknown:
		v.minLevel = logs.LevelDebug
	case logs.LevelDebug:
		v.minLevel = logs.LevelInfo
	case logs.LevelInfo:
		v.minLevel = logs.LevelWarn
	case logs.LevelWarn:
		v.minLevel = logs.LevelError
	default:
		v.minLevel = logs.LevelUnknown
	}
	v.recompute()
}

// update handles log view keys; it returns true when the key was consumed.
func (v *logView) update(msg tea.KeyMsg) (bool, tea.Cmd) {
	if v.searching {
		switch msg.String() {
		case "enter":
			v.searching = false
			v.searchInput.Blur()
			v.search = strings.TrimSpace(v.searchInput.Value())
			v.invalidate()
			v.recompute()
			if !v.onlyMatches {
				v.jumpMatch(false)
			}
			return true, nil
		case "esc":
			v.searching = false
			v.searchInput.Blur()
			return true, nil
		}
		var cmd tea.Cmd
		v.searchInput, cmd = v.searchInput.Update(msg)
		return true, cmd
	}
	half := max(1, v.bodyHeight()/2)
	switch msg.String() {
	case "j", "down":
		v.scroll(1)
	case "k", "up":
		v.scroll(-1)
	case "ctrl+d", "pgdown":
		v.scroll(half)
	case "ctrl+u", "pgup":
		v.scroll(-half)
	case "g", "home":
		v.pause()
		v.top = 0
		v.setTopEntry()
	case "G", "end":
		v.follow, v.unseen = true, 0
	case " ":
		if v.follow {
			v.pause()
		} else {
			v.follow, v.unseen = true, 0
		}
	case "/":
		v.searching = true
		v.searchInput.SetValue(v.search)
		v.searchInput.CursorEnd()
		v.searchInput.Focus()
		return true, textinput.Blink
	case "n":
		v.jumpMatch(true)
	case "N":
		v.jumpMatch(false)
	case "F":
		v.onlyMatches = !v.onlyMatches
		v.recompute()
	case "l":
		v.cycleLevel()
	case "s":
		v.expand = !v.expand
		v.invalidate()
	case "t":
		v.timestamps = !v.timestamps
		v.invalidate()
	case "w":
		v.wrap = !v.wrap
		v.invalidate()
	case "e":
		v.showEvents = !v.showEvents
		v.recompute()
	case "p":
		v.opts.Previous = !v.opts.Previous
		return true, v.restart()
	case "c":
		v.buf.Reset()
		v.events = map[string]bool{}
		v.recompute()
	case "esc":
		if v.search != "" {
			v.search, v.onlyMatches = "", false
			v.invalidate()
			v.recompute()
			return true, nil
		}
		return false, nil
	default:
		return false, nil
	}
	return true, nil
}

func (v *logView) statusLine(width int) string {
	var parts []string
	if v.follow {
		parts = append(parts, styleOK.Render("● follow"))
	} else {
		p := styleWarn.Render("❚❚ paused")
		if v.unseen > 0 {
			p += styleWarn.Render(fmt.Sprintf(" (+%d)", v.unseen))
		}
		parts = append(parts, p)
	}
	if v.opts.Previous {
		parts = append(parts, styleWarn.Render("previous container"))
	}
	if v.minLevel != logs.LevelUnknown {
		parts = append(parts, "level≥"+levelStyle(v.minLevel).Render(v.minLevel.String()))
	}
	if v.search != "" {
		n := 0
		for _, i := range v.view {
			if v.matches(v.buf.At(i)) {
				n++
			}
		}
		s := fmt.Sprintf("/%s (%d)", v.search, n)
		if v.onlyMatches {
			s += " only"
		}
		parts = append(parts, styleAccent.Render(s))
	}
	if !v.showEvents {
		parts = append(parts, styleDim.Render("events off"))
	}
	if v.wrap {
		parts = append(parts, styleDim.Render("wrap"))
	}
	if v.expand {
		parts = append(parts, styleDim.Render("expanded"))
	}
	parts = append(parts, styleDim.Render(fmt.Sprintf("%s · %s", plural(len(v.streams), "source"), plural(len(v.view), "line"))))
	return fit(" "+strings.Join(parts, styleFaint.Render(" · ")), width)
}

// lines renders the log view into exactly height lines.
func (v *logView) lines(width, height int) []string {
	if width != v.cacheWidth {
		v.invalidate()
		v.cacheWidth = width
	}
	v.width, v.height = width, height
	out := make([]string, 0, height)
	out = append(out, v.statusLine(width))
	body := v.bodyHeight()

	switch {
	case len(v.view) == 0:
		msg := "waiting for log lines…"
		if len(v.streams) == 0 {
			msg = "no log streams for this selection"
		}
		out = append(out, styleDim.Render(" "+msg))
	case v.follow:
		var rows []string
		for i := len(v.view) - 1; i >= 0 && len(rows) < body; i-- {
			rows = append(v.rows(v.buf.At(v.view[i])), rows...)
		}
		if len(rows) > body {
			rows = rows[len(rows)-body:]
		}
		out = append(out, rows...)
	default:
		for i := v.top; i < len(v.view) && len(out) < 1+body; i++ {
			for _, r := range v.rows(v.buf.At(v.view[i])) {
				if len(out) >= 1+body {
					break
				}
				out = append(out, r)
			}
		}
	}
	for len(out) < 1+body {
		out = append(out, "")
	}
	if v.searching {
		out = append(out, v.searchInput.View())
	}
	return out
}

func (v *logView) sourceWidth() int {
	w := 0
	for s := range v.sources {
		w = max(w, ansi.StringWidth(s))
	}
	return min(w, maxSourceWidth)
}

// rows renders one entry, using the cache keyed by entry and stack length.
func (v *logView) rows(e *logs.Entry) []string {
	key := renderKey{e, len(e.Stack)}
	if r, ok := v.cache[key]; ok {
		return r
	}
	r := v.render(e, v.width)
	v.cache[key] = r
	return r
}

func (v *logView) render(e *logs.Entry, width int) []string {
	var prefix strings.Builder
	if len(v.sources) > 1 || e.Kind == logs.KindEvent {
		sw := max(v.sourceWidth(), len(eventSource))
		st := sourceStyle(e.Source)
		if e.Kind == logs.KindEvent {
			st = styleAccent
		}
		prefix.WriteString(st.Render(fit(e.Source, sw)) + " ")
	}
	if v.timestamps {
		ts := "            "
		if !e.Time.IsZero() {
			ts = e.Time.Local().Format("15:04:05.000")
		}
		prefix.WriteString(styleFaint.Render(ts) + " ")
	}
	switch {
	case e.Kind == logs.KindMeta:
		prefix.WriteString(styleDim.Render("---- "))
	case e.Level != logs.LevelUnknown:
		prefix.WriteString(levelStyle(e.Level).Render(fit(e.Level.String(), 5)) + " ")
	}
	if e.Logger != "" {
		prefix.WriteString(styleFaint.Render(shortLogger(e.Logger)) + " ")
	}
	pre := prefix.String()
	pw := ansi.StringWidth(pre)
	msgStyle := lipgloss.NewStyle()
	if e.Kind == logs.KindMeta {
		msgStyle = styleDim
	}

	var rows []string
	if (v.wrap || e.Kind == logs.KindMeta) && width-pw > 10 {
		wrapped := strings.Split(ansi.Hardwrap(e.Message, width-pw, true), "\n")
		for i, w := range wrapped {
			p := strings.Repeat(" ", pw)
			if i == 0 {
				p = pre
			}
			rows = append(rows, p+v.highlight(w, msgStyle))
		}
	} else {
		rows = append(rows, fit(pre+v.highlight(e.Message, msgStyle), width))
	}

	if len(e.Stack) > 0 || len(e.Fields) > 0 {
		indent := strings.Repeat(" ", min(pw, 8))
		if v.expand {
			for _, f := range e.Fields {
				rows = append(rows, fit(indent+styleFaint.Render(f), width))
			}
			for _, s := range e.Stack {
				st := styleFaint
				if strings.HasPrefix(strings.TrimSpace(s), "Caused by") || !strings.HasPrefix(s, " ") {
					st = styleErr
				}
				if v.wrap && width-len(indent) > 10 {
					for _, w := range strings.Split(ansi.Hardwrap(s, width-len(indent), true), "\n") {
						rows = append(rows, indent+v.highlight(w, st))
					}
				} else {
					rows = append(rows, fit(indent+v.highlight(s, st), width))
				}
			}
		} else {
			var parts []string
			if len(e.Stack) > 0 {
				parts = append(parts, fmt.Sprintf("%d stack lines", len(e.Stack)))
			}
			if len(e.Fields) > 0 {
				parts = append(parts, fmt.Sprintf("%d fields", len(e.Fields)))
			}
			rows = append(rows, fit(indent+styleFaint.Render("⤷ "+strings.Join(parts, " · ")+" (s to expand)"), width))
		}
	}
	return rows
}

func (v *logView) highlight(text string, base lipgloss.Style) string {
	if v.search == "" {
		return base.Render(text)
	}
	lower, term := strings.ToLower(text), strings.ToLower(v.search)
	if len(lower) != len(text) {
		return base.Render(text)
	}
	var b strings.Builder
	i := 0
	for {
		j := strings.Index(lower[i:], term)
		if j < 0 {
			b.WriteString(base.Render(text[i:]))
			return b.String()
		}
		b.WriteString(base.Render(text[i : i+j]))
		b.WriteString(styleMatch.Render(text[i+j : i+j+len(term)]))
		i += j + len(term)
	}
}

// shortLogger abbreviates a dotted logger name: com.example.orders.OrderService -> c.e.o.OrderService.
func shortLogger(name string) string {
	parts := strings.Split(name, ".")
	if len(parts) <= 2 {
		return name
	}
	for i := range parts[:len(parts)-1] {
		if parts[i] != "" {
			parts[i] = parts[i][:1]
		}
	}
	return strings.Join(parts, ".")
}
