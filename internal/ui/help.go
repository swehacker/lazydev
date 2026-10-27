package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var helpSections = []struct {
	title string
	keys  [][2]string
}{
	{"Global", [][2]string{
		{"q / ctrl+c", "quit"}, {"?", "toggle help"}, {":", "command palette (tab completes)"},
		{"1 / 2", "focus Services / Resources"}, {"tab", "cycle focus"}, {"[ / ]", "previous / next detail tab"},
		{"z", "zoom detail pane"}, {"r", "re-run health checks"},
	}},
	{"Lists", [][2]string{
		{"j/k ↑/↓", "move"}, {"g / G", "top / bottom"}, {"ctrl+d / ctrl+u", "half page"},
		{"/", "filter (esc clears)"}, {"enter / l", "open detail"},
	}},
	{"Actions", [][2]string{
		{"s", "stop the selected container"},
		{"S", "start the selected container"},
		{"R", "restart the selected container or pod"},
		{"=", "scale the selected service or deployment (0 stops its pods)"},
	}},
	{"Logs", [][2]string{
		{"space", "follow / pause"}, {"G", "jump to end and follow"}, {"/", "search"}, {"n / N", "next / previous match"},
		{"F", "only matching lines"}, {"l", "cycle minimum level"}, {"s", "expand stack traces and fields"},
		{"p", "previous container logs"}, {"t", "timestamps"}, {"w", "wrap"}, {"e", "Kubernetes events"},
		{"c", "clear"}, {"esc", "clear search / back"},
	}},
	{"Commands", [][2]string{
		{":services", "logical services"}, {":pods :deploy :helm", "Kubernetes resources"},
		{":compose :containers", "Docker containers"}, {":images :nodes :events", "images, nodes, events"},
		{":ns <name|all>", "namespace filter"}, {":pods <ns>", "switch kind and namespace"},
	}},
}

func helpView() string {
	var b strings.Builder
	b.WriteString(styleBrand.Render("lazydev") + styleDim.Render("  keyboard reference") + "\n")
	for _, s := range helpSections {
		b.WriteString("\n" + styleTitle.Render(s.title) + "\n")
		for _, k := range s.keys {
			b.WriteString("  " + styleKey.Render(fit(k[0], 24)) + k[1] + "\n")
		}
	}
	b.WriteString("\n" + styleFaint.Render("press any key to close"))
	return lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colorAccent).Padding(0, 2).Render(b.String())
}
