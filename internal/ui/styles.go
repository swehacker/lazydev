package ui

import (
	"hash/fnv"

	"github.com/charmbracelet/lipgloss"

	"github.com/swehacker/lazydev/internal/logs"
	"github.com/swehacker/lazydev/internal/service"
)

var (
	colorGreen  = lipgloss.Color("42")
	colorYellow = lipgloss.Color("214")
	colorRed    = lipgloss.Color("196")
	colorGray   = lipgloss.Color("244")
	colorDim    = lipgloss.Color("240")
	colorAccent = lipgloss.Color("39")
	colorBlue   = lipgloss.Color("75")

	styleBorder        = lipgloss.NewStyle().Foreground(colorDim)
	styleBorderFocused = lipgloss.NewStyle().Foreground(colorAccent)
	styleTitle         = lipgloss.NewStyle().Bold(true)
	styleDim           = lipgloss.NewStyle().Foreground(colorGray)
	styleFaint         = lipgloss.NewStyle().Foreground(colorDim)
	styleAccent        = lipgloss.NewStyle().Foreground(colorAccent)
	styleBold          = lipgloss.NewStyle().Bold(true)
	styleOK            = lipgloss.NewStyle().Foreground(colorGreen)
	styleWarn          = lipgloss.NewStyle().Foreground(colorYellow)
	styleErr           = lipgloss.NewStyle().Foreground(colorRed)
	styleHeader        = lipgloss.NewStyle().Foreground(colorGray).Bold(true)
	styleSelected      = lipgloss.NewStyle().Background(lipgloss.Color("237")).Bold(true)
	styleSelectedFocus = lipgloss.NewStyle().Background(lipgloss.Color("24")).Foreground(lipgloss.Color("231")).Bold(true)
	styleTabActive     = lipgloss.NewStyle().Foreground(colorAccent).Bold(true).Underline(true)
	styleTab           = lipgloss.NewStyle().Foreground(colorGray)
	styleMatch         = lipgloss.NewStyle().Background(colorYellow).Foreground(lipgloss.Color("16"))
	styleKey           = lipgloss.NewStyle().Foreground(colorAccent).Bold(true)
	styleBrand         = lipgloss.NewStyle().Foreground(lipgloss.Color("16")).Background(colorAccent).Bold(true).Padding(0, 1)

	sourcePalette = []lipgloss.Color{"75", "114", "180", "141", "80", "209", "147", "222", "117", "174"}
)

func statusStyle(s service.Status) lipgloss.Style {
	switch s {
	case service.Healthy:
		return styleOK
	case service.Degraded:
		return styleWarn
	case service.Down:
		return styleErr
	}
	return styleDim
}

func statusIcon(s service.Status) string {
	switch s {
	case service.Healthy:
		return "●"
	case service.Degraded:
		return "◐"
	case service.Down:
		return "✖"
	case service.Completed:
		return "✓"
	}
	return "○"
}

func levelStyle(l logs.Level) lipgloss.Style {
	switch l {
	case logs.LevelError:
		return styleErr.Bold(true)
	case logs.LevelWarn:
		return styleWarn
	case logs.LevelInfo:
		return styleOK
	case logs.LevelDebug:
		return lipgloss.NewStyle().Foreground(colorBlue)
	case logs.LevelTrace:
		return styleFaint
	}
	return styleDim
}

func sourceStyle(source string) lipgloss.Style {
	h := fnv.New32a()
	_, _ = h.Write([]byte(source))
	return lipgloss.NewStyle().Foreground(sourcePalette[int(h.Sum32())%len(sourcePalette)])
}
