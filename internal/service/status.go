package service

// Status is the rolled-up state of a logical service. Higher values are worse,
// except Unknown which is ignored when other components report.
type Status int

const (
	Unknown Status = iota
	Completed
	Healthy
	Degraded
	Down
)

func (s Status) String() string {
	switch s {
	case Completed:
		return "Completed"
	case Healthy:
		return "Healthy"
	case Degraded:
		return "Degraded"
	case Down:
		return "Down"
	}
	return "Unknown"
}

// component is the status contribution of one part of a service (k8s, compose, health).
type component struct {
	status Status
	reason string
}

// rollup returns the worst known component; Unknown only when nothing is known.
func rollup(parts ...component) component {
	var worst component
	known := false
	for _, p := range parts {
		if p.status == Unknown {
			if !known && worst.reason == "" {
				worst.reason = p.reason
			}
			continue
		}
		if !known || p.status > worst.status {
			worst = p
			known = true
		}
	}
	return worst
}
