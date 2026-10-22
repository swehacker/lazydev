package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type column struct {
	title     string
	width     int  // fixed width; ignored for flex columns
	flex      bool // takes remaining space (at least width)
	right     bool
	hideBelow int // hide the column when the list is narrower than this
}

type cell struct {
	text  string
	style lipgloss.Style
}

type row struct {
	key   string
	cells []cell
}

func plain(s string) cell                     { return cell{text: s, style: lipgloss.NewStyle()} }
func styled(s string, st lipgloss.Style) cell { return cell{text: s, style: st} }

// listModel is a filterable, scrollable table.
type listModel struct {
	cols    []column
	rows    []row
	visible []int
	cursor  int
	offset  int

	filter    textinput.Model
	filtering bool
	empty     string
}

func newList(cols []column, empty string) *listModel {
	ti := textinput.New()
	ti.Prompt = "/"
	ti.CharLimit = 64
	return &listModel{cols: cols, filter: ti, empty: empty}
}

// setRows replaces the rows, keeping the cursor on the same key when possible.
func (l *listModel) setRows(rows []row) {
	selected := ""
	if r, ok := l.selected(); ok {
		selected = r.key
	}
	l.rows = rows
	l.applyFilter()
	if selected != "" {
		for i, idx := range l.visible {
			if l.rows[idx].key == selected {
				l.cursor = i
				return
			}
		}
	}
	l.cursor = min(l.cursor, max(0, len(l.visible)-1))
}

func (l *listModel) setColumns(cols []column) { l.cols = cols }

func (l *listModel) applyFilter() {
	term := strings.ToLower(strings.TrimSpace(l.filter.Value()))
	l.visible = l.visible[:0]
	for i, r := range l.rows {
		if term == "" || l.matches(r, term) {
			l.visible = append(l.visible, i)
		}
	}
	l.cursor = min(l.cursor, max(0, len(l.visible)-1))
}

func (l *listModel) matches(r row, term string) bool {
	if strings.Contains(strings.ToLower(r.key), term) {
		return true
	}
	for _, c := range r.cells {
		if strings.Contains(strings.ToLower(c.text), term) {
			return true
		}
	}
	return false
}

func (l *listModel) selected() (row, bool) {
	if l.cursor < 0 || l.cursor >= len(l.visible) {
		return row{}, false
	}
	return l.rows[l.visible[l.cursor]], true
}

func (l *listModel) selectedKey() string {
	r, _ := l.selected()
	return r.key
}

func (l *listModel) filterText() string { return strings.TrimSpace(l.filter.Value()) }

// update handles navigation and filter keys; it returns true when the key was consumed.
func (l *listModel) update(msg tea.KeyMsg, pageSize int) (bool, tea.Cmd) {
	if l.filtering {
		switch msg.String() {
		case "enter":
			l.filtering = false
			l.filter.Blur()
			return true, nil
		case "esc":
			l.filtering = false
			l.filter.Blur()
			l.filter.SetValue("")
			l.applyFilter()
			return true, nil
		}
		var cmd tea.Cmd
		l.filter, cmd = l.filter.Update(msg)
		l.applyFilter()
		return true, cmd
	}
	switch msg.String() {
	case "j", "down":
		l.move(1)
	case "k", "up":
		l.move(-1)
	case "g", "home":
		l.cursor = 0
	case "G", "end":
		l.cursor = max(0, len(l.visible)-1)
	case "ctrl+d", "pgdown":
		l.move(max(1, pageSize/2))
	case "ctrl+u", "pgup":
		l.move(-max(1, pageSize/2))
	case "/":
		l.filtering = true
		l.filter.Focus()
		return true, textinput.Blink
	case "esc":
		if l.filter.Value() == "" {
			return false, nil
		}
		l.filter.SetValue("")
		l.applyFilter()
	default:
		return false, nil
	}
	return true, nil
}

func (l *listModel) move(delta int) {
	l.cursor = min(max(0, l.cursor+delta), max(0, len(l.visible)-1))
}

// view renders header and rows into exactly height lines of width cells.
func (l *listModel) view(width, height int, focused bool) []string {
	if height <= 0 {
		return nil
	}
	cols, widths := l.layout(width)
	lines := make([]string, 0, height)
	lines = append(lines, styleHeader.Render(l.renderCells(cols, widths, func(c column, i int) cell { return plain(c.title) }, nil)))

	bodyHeight := height - 1
	if l.filtering || l.filterText() != "" {
		bodyHeight--
	}
	if bodyHeight < 0 {
		bodyHeight = 0
	}
	if l.cursor < l.offset {
		l.offset = l.cursor
	}
	if l.cursor >= l.offset+bodyHeight {
		l.offset = l.cursor - bodyHeight + 1
	}
	l.offset = max(0, min(l.offset, max(0, len(l.visible)-bodyHeight)))

	if len(l.visible) == 0 {
		msg := l.empty
		if l.filterText() != "" {
			msg = "no matches"
		}
		lines = append(lines, styleDim.Render(" "+msg))
	}
	for i := l.offset; i < len(l.visible) && len(lines) < 1+bodyHeight; i++ {
		r := l.rows[l.visible[i]]
		get := func(_ column, ci int) cell {
			if ci < len(r.cells) {
				return r.cells[ci]
			}
			return plain("")
		}
		if i == l.cursor {
			sel := styleSelected
			if focused {
				sel = styleSelectedFocus
			}
			lines = append(lines, sel.Render(fit(l.renderCells(cols, widths, get, &sel), width)))
		} else {
			lines = append(lines, l.renderCells(cols, widths, get, nil))
		}
	}
	for len(lines) < height-1 {
		lines = append(lines, "")
	}
	if l.filtering || l.filterText() != "" {
		for len(lines) < height-1 {
			lines = append(lines, "")
		}
		if l.filtering {
			lines = append(lines, l.filter.View())
		} else {
			lines = append(lines, styleAccent.Render("/"+l.filterText())+styleDim.Render("  (esc clears)"))
		}
	}
	return lines
}

func (l *listModel) layout(width int) ([]int, []int) {
	var cols []int
	for i, c := range l.cols {
		if c.hideBelow == 0 || width >= c.hideBelow {
			cols = append(cols, i)
		}
	}
	widths := make([]int, len(cols))
	used, flexCount := 0, 0
	for i, ci := range cols {
		c := l.cols[ci]
		widths[i] = c.width
		used += c.width + 1
		if c.flex {
			flexCount++
		}
	}
	extra := width - used
	if flexCount > 0 && extra > 0 {
		for i, ci := range cols {
			if l.cols[ci].flex {
				widths[i] += extra / flexCount
			}
		}
	}
	return cols, widths
}

// renderCells joins cells into one line. With sel set, cell styles are replaced by the selection style.
func (l *listModel) renderCells(cols, widths []int, get func(column, int) cell, sel *lipgloss.Style) string {
	var b strings.Builder
	for i, ci := range cols {
		c := l.cols[ci]
		cl := get(c, ci)
		text := cl.text
		if c.right {
			text = fitRight(text, widths[i])
		} else {
			text = fit(text, widths[i])
		}
		if sel == nil {
			text = cl.style.Render(text)
		}
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(text)
	}
	return b.String()
}
