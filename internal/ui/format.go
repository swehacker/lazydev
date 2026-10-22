package ui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func humanBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	v := float64(b) / float64(div)
	suffix := "KMGTPE"[exp : exp+1]
	if v >= 100 {
		return fmt.Sprintf("%.0f%s", v, suffix)
	}
	return fmt.Sprintf("%.1f%s", v, suffix)
}

func humanCPU(milli float64) string {
	if milli < 1000 {
		return fmt.Sprintf("%.0fm", milli)
	}
	return fmt.Sprintf("%.2f", milli/1000)
}

func humanAge(t time.Time, now time.Time) string {
	if t.IsZero() {
		return "-"
	}
	d := now.Sub(t)
	switch {
	case d < 0:
		return "0s"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

// fit truncates or pads s (which may contain ANSI styles) to exactly width cells.
func fit(s string, width int) string {
	if width <= 0 {
		return ""
	}
	w := ansi.StringWidth(s)
	if w > width {
		return ansi.Truncate(s, width, "…")
	}
	return s + strings.Repeat(" ", width-w)
}

// fitRight right-aligns s within width cells.
func fitRight(s string, width int) string {
	w := ansi.StringWidth(s)
	if w >= width {
		return fit(s, width)
	}
	return strings.Repeat(" ", width-w) + s
}

// box draws a rounded border of exactly width x height with a title in the top edge.
func box(title string, lines []string, width, height int, focused bool) string {
	if width < 4 || height < 2 {
		return ""
	}
	border := styleBorder
	if focused {
		border = styleBorderFocused
	}
	inner := width - 2
	t := ""
	if title != "" {
		t = " " + title + " "
	}
	t = ansi.Truncate(t, inner-1, "…")
	top := border.Render("╭─") + t + border.Render(strings.Repeat("─", max(0, inner-1-ansi.StringWidth(t)))+"╮")

	var b strings.Builder
	b.WriteString(top)
	side := border.Render("│")
	for i := range height - 2 {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}
		b.WriteString("\n" + side + fit(line, inner) + side)
	}
	b.WriteString("\n" + border.Render("╰"+strings.Repeat("─", inner)+"╯"))
	return b.String()
}

// sparkline renders values as block characters, scaled to 0..ceiling when ceiling > 0,
// otherwise to the observed min..max so small variations stay visible.
func sparkline(values []float64, width int, ceiling float64) string {
	runes := []rune("▁▂▃▄▅▆▇█")
	if width <= 0 {
		return ""
	}
	if len(values) > width {
		values = values[len(values)-width:]
	}
	lo, hi := 0.0, ceiling
	if ceiling <= 0 && len(values) > 0 {
		lo, hi = values[0], values[0]
		for _, v := range values {
			lo, hi = min(lo, v), max(hi, v)
		}
	}
	var b strings.Builder
	b.WriteString(strings.Repeat(" ", width-len(values)))
	for _, v := range values {
		idx := 0
		switch {
		case hi > lo:
			idx = int((v - lo) / (hi - lo) * float64(len(runes)-1))
		case v > 0:
			idx = len(runes)/2 - 1
		}
		b.WriteRune(runes[min(max(idx, 0), len(runes)-1)])
	}
	return b.String()
}

func plural(n int, word string) string {
	if n == 1 {
		return "1 " + word
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func joinHorizontal(left, right string) string {
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}
