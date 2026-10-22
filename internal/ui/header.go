package ui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"

	"github.com/swehacker/lazydev/internal/model"
	"github.com/swehacker/lazydev/internal/service"
)

func percentStyle(p float64) lipgloss.Style {
	switch {
	case p >= 90:
		return styleErr
	case p >= service.EngineDiskWarnPercent:
		return styleWarn
	}
	return lipgloss.NewStyle()
}

func renderHeader(width int, version, composeProject string, snap model.Snapshot, svcs []service.LogicalService, problems []service.Problem) []string {
	sep := styleFaint.Render("  │  ")
	var parts []string
	parts = append(parts, styleBrand.Render("lazydev"))

	if snap.Kube.Available {
		parts = append(parts, styleDim.Render("ctx ")+snap.Kube.Context)
	} else {
		parts = append(parts, styleDim.Render("ctx ")+styleErr.Render(orDash(snap.Kube.Context)))
	}

	e := snap.Engine
	switch {
	case e.Available:
		s := styleDim.Render(e.Name+" ") + fmt.Sprintf("%dcpu", e.CPUs)
		if e.CPUPercent >= 0 {
			s += styleDim.Render(" cpu ") + percentStyle(e.CPUPercent).Render(fmt.Sprintf("%.0f%%", e.CPUPercent))
		}
		if e.MemUsed > 0 {
			memPct := float64(e.MemUsed) / float64(e.MemTotal) * 100
			s += styleDim.Render(" mem ") + percentStyle(memPct).Render(humanBytes(e.MemUsed)+"/"+humanBytes(e.MemTotal))
		} else if e.MemTotal > 0 {
			s += styleDim.Render(" mem ") + humanBytes(e.MemTotal)
		}
		if pct := e.DiskPercent(); pct >= 0 {
			s += styleDim.Render(" disk ") + percentStyle(pct).Render(fmt.Sprintf("%.0f%%", pct))
		}
		parts = append(parts, s)
	case e.Name != "":
		parts = append(parts, styleDim.Render(e.Name+" ")+styleErr.Render("down"))
	}

	c := snap.Cluster
	switch {
	case c.Found:
		total, running := c.Servers+c.Agents, c.ServersRunning+c.AgentsRunning
		st := styleOK
		if running < total {
			st = styleWarn
		}
		if running == 0 {
			st = styleErr
		}
		parts = append(parts, styleDim.Render("k3d ")+c.Name+" "+st.Render(fmt.Sprintf("%d/%d", running, total)))
	case c.Name != "":
		parts = append(parts, styleDim.Render("k3d ")+c.Name+" "+styleErr.Render("not running"))
	}

	if snap.Docker.Available {
		running, total := 0, 0
		for _, ctr := range snap.Docker.Containers {
			if ctr.ComposeService == "" || (composeProject != "" && ctr.ComposeProject != composeProject) {
				continue
			}
			if ctr.State == "exited" && ctr.ExitCode == 0 {
				continue
			}
			total++
			if ctr.Running() {
				running++
			}
		}
		if total > 0 {
			label := "compose"
			if composeProject != "" {
				label += " " + composeProject
			}
			st := styleOK
			if running < total {
				st = styleWarn
			}
			parts = append(parts, styleDim.Render(label+" ")+st.Render(fmt.Sprintf("%d/%d", running, total)))
		}
	}

	counts := service.Counts(svcs)
	parts = append(parts, styleOK.Render(fmt.Sprintf("● %d", counts[service.Healthy]+counts[service.Completed]))+" "+
		styleWarn.Render(fmt.Sprintf("◐ %d", counts[service.Degraded]))+" "+
		styleErr.Render(fmt.Sprintf("✖ %d", counts[service.Down]))+
		unknownCount(counts[service.Unknown]))

	line1 := " " + strings.Join(parts, sep)
	if version != "" && version != "dev" {
		line1 += styleFaint.Render("  " + version)
	}

	var line2 string
	var errs []string
	if !snap.Docker.Available && snap.Docker.Err != "" {
		errs = append(errs, "docker: "+snap.Docker.Err)
	}
	if !snap.Kube.Available && snap.Kube.Err != "" {
		errs = append(errs, "kube: "+snap.Kube.Err)
	}
	if e.Err != "" {
		errs = append(errs, e.Name+": "+e.Err)
	}
	switch {
	case len(errs) > 0:
		line2 = styleErr.Render(" ✖ " + strings.Join(errs, "  ·  "))
	case len(problems) > 0:
		texts := make([]string, 0, len(problems))
		for _, p := range problems {
			st := styleWarn
			if p.Severity == service.Down {
				st = styleErr
			}
			texts = append(texts, styleBold.Render(p.Subject)+st.Render(": "+p.Text))
		}
		label := "problems"
		if len(problems) == 1 {
			label = "problem"
		}
		line2 = styleWarn.Render(fmt.Sprintf(" ⚠ %d %s: ", len(problems), label)) + strings.Join(texts, styleFaint.Render("  ·  "))
	case len(svcs) > 0:
		line2 = styleOK.Render(" ✓ all services healthy")
	default:
		line2 = styleDim.Render(" waiting for data…")
	}
	return []string{fit(line1, width), fit(line2, width)}
}

func unknownCount(n int) string {
	if n == 0 {
		return ""
	}
	return " " + styleDim.Render(fmt.Sprintf("○ %d", n))
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
