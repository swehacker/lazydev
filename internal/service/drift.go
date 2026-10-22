package service

import (
	"slices"
	"strings"

	"github.com/swehacker/lazydev/internal/model"
)

// Drift describes whether the cluster runs the locally built image.
type Drift int

const (
	DriftNA Drift = iota // not applicable or not determinable
	InSync
	Missing
	NotImported
	StaleImport
	NotRolledOut
)

func (d Drift) String() string {
	switch d {
	case InSync:
		return "in sync"
	case Missing:
		return "missing"
	case NotImported:
		return "not imported"
	case StaleImport:
		return "stale import"
	case NotRolledOut:
		return "not rolled out"
	}
	return ""
}

// Problem reports whether the drift needs a rebuild, import or rollout.
func (d Drift) Problem() bool { return d >= Missing }

// ClassifyDrift compares a local image with the k3d nodes and the pods running it.
// explicit marks images named in the config, which must exist locally.
func ClassifyDrift(ref string, explicit bool, drift model.DriftState, pods []model.Pod, clusterFound bool) (Drift, string) {
	if ref == "" {
		return DriftNA, ""
	}
	local, ok := drift.Local[ref]
	if !ok {
		if explicit {
			return Missing, "image not built locally"
		}
		return DriftNA, ""
	}
	if !clusterFound || len(drift.Nodes) == 0 {
		return DriftNA, drift.Err
	}

	nodeNames := make([]string, 0, len(drift.Nodes))
	for n := range drift.Nodes {
		nodeNames = append(nodeNames, n)
	}
	slices.Sort(nodeNames)

	nodeIDs := map[string]bool{}
	for _, n := range nodeNames {
		if _, ok := drift.Nodes[n][ref]; !ok {
			return NotImported, "not imported into " + n
		}
	}
	for _, n := range nodeNames {
		fp := drift.Nodes[n][ref]
		if !slices.Equal(fp.Layers, local.Layers) {
			return StaleImport, "local image rebuilt; " + n + " has an older import"
		}
		nodeIDs[fp.ID] = true
	}

	for _, p := range pods {
		for _, c := range p.Containers {
			if c.Init || model.NormalizeImageRef(c.Image) != ref {
				continue
			}
			// Registry-pulled images report a repo digest; only bare config digests come from imports.
			if !strings.HasPrefix(c.ImageID, "sha256:") {
				continue
			}
			if !nodeIDs[c.ImageID] {
				return NotRolledOut, p.Name + " runs an older image"
			}
		}
	}
	return InSync, ""
}
