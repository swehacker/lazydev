package service

import (
	"fmt"
	"strings"

	"github.com/swehacker/lazydev/internal/model"
)

// EngineDiskWarnPercent is the engine VM disk usage at which lazydev warns.
const EngineDiskWarnPercent = 80

type Problem struct {
	Severity Status // Degraded or Down
	Subject  string
	Text     string
}

func (p Problem) String() string { return p.Subject + ": " + p.Text }

// Problems lists everything needing attention, most severe first.
func Problems(svcs []LogicalService, snap model.Snapshot) []Problem {
	var down, degraded []Problem
	for _, s := range svcs {
		switch s.Status {
		case Down:
			down = append(down, Problem{Down, s.Name, orDefault(s.Reason, "down")})
		case Degraded:
			degraded = append(degraded, Problem{Degraded, s.Name, orDefault(s.Reason, "degraded")})
		}
		if s.Drift.Problem() {
			degraded = append(degraded, Problem{Degraded, s.Name, "image " + s.Drift.String()})
		}
	}
	for _, n := range snap.Kube.Nodes {
		if !n.Ready {
			down = append(down, Problem{Down, "node " + n.Name, "NotReady"})
		}
		if len(n.Pressure) > 0 {
			degraded = append(degraded, Problem{Degraded, "node " + n.Name, strings.Join(n.Pressure, ", ")})
		}
	}
	if pct := snap.Engine.DiskPercent(); pct >= EngineDiskWarnPercent {
		degraded = append(degraded, Problem{Degraded, snap.Engine.Name + " disk", fmt.Sprintf("%.0f%% used", pct)})
	}
	return append(down, degraded...)
}

func orDefault(s, def string) string {
	if s == "" {
		return def
	}
	return s
}
