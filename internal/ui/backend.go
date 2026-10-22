package ui

import (
	"context"

	"github.com/swehacker/lazydev/internal/logs"
	"github.com/swehacker/lazydev/internal/model"
)

type RefKind int

const (
	RefPod RefKind = iota
	RefDeployment
	RefContainer
	RefImage
	RefNode
)

// Ref identifies an object for on-demand lookups.
type Ref struct {
	Kind      RefKind
	Namespace string
	Name      string
	ID        string // Docker container or image ID
}

// Backend performs on-demand work for the UI.
type Backend interface {
	Streamer() logs.Streamer
	Describe(ctx context.Context, ref Ref) (string, error)
	Env(ctx context.Context, ref Ref) (string, error)
	RefreshHealth()
}

// Snapshots is the read side of the state store.
type Snapshots interface {
	Changed() <-chan struct{}
	Snapshot() model.Snapshot
}
