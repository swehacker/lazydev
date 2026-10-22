// Package state holds the latest observed Snapshot and notifies subscribers when it changes.
package state

import (
	"maps"
	"strings"
	"sync"

	"github.com/swehacker/lazydev/internal/model"
)

// HistoryLen is the number of samples kept per history key.
const HistoryLen = 60

// Update modifies a snapshot. Implementations must assign new values to fields
// (slices, maps, structs) rather than mutating the existing ones in place,
// because previously returned snapshots share them.
type Update func(*model.Snapshot)

type Store struct {
	mu      sync.RWMutex
	snap    model.Snapshot
	changed chan struct{}
}

func New() *Store {
	return &Store{changed: make(chan struct{}, 1)}
}

// Apply runs u under the store lock and signals a change.
func (s *Store) Apply(u Update) {
	s.mu.Lock()
	u(&s.snap)
	s.mu.Unlock()
	select {
	case s.changed <- struct{}{}:
	default:
	}
}

// Snapshot returns a copy of the current state that is safe to read without locking.
func (s *Store) Snapshot() model.Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.snap
}

// Changed delivers at most one pending notification; bursts of updates coalesce.
func (s *Store) Changed() <-chan struct{} { return s.changed }

// SetHealth returns an update that records a health result for one service.
func SetHealth(service string, r model.HealthResult) Update {
	return func(s *model.Snapshot) {
		next := maps.Clone(s.Health)
		if next == nil {
			next = map[string]model.HealthResult{}
		}
		next[service] = r
		s.Health = next
	}
}

// AppendHistory returns an update that appends samples, keeping the last HistoryLen per key.
// Keys not present in samples are dropped when prune is true, so history follows live objects.
func AppendHistory(samples map[string]model.Sample, prefix string, prune bool) Update {
	return func(s *model.Snapshot) {
		next := make(map[string][]model.Sample, len(s.History)+len(samples))
		for k, v := range s.History {
			if prune && strings.HasPrefix(k, prefix) {
				if _, live := samples[k]; !live {
					continue
				}
			}
			next[k] = v
		}
		for k, sample := range samples {
			prev := next[k]
			start := 0
			if len(prev) >= HistoryLen {
				start = len(prev) - HistoryLen + 1
			}
			h := make([]model.Sample, 0, HistoryLen)
			h = append(h, prev[start:]...)
			next[k] = append(h, sample)
		}
		s.History = next
	}
}
