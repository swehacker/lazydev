package state

import (
	"fmt"
	"testing"
	"time"

	"github.com/swehacker/lazydev/internal/model"
)

func TestSnapshotIsIsolatedFromLaterUpdates(t *testing.T) {
	s := New()
	s.Apply(SetHealth("a", model.HealthResult{OK: true}))
	before := s.Snapshot()
	s.Apply(SetHealth("a", model.HealthResult{OK: false}))
	s.Apply(SetHealth("b", model.HealthResult{OK: true}))

	if !before.Health["a"].OK || len(before.Health) != 1 {
		t.Fatalf("earlier snapshot was mutated: %+v", before.Health)
	}
	after := s.Snapshot()
	if after.Health["a"].OK || len(after.Health) != 2 {
		t.Fatalf("unexpected current health: %+v", after.Health)
	}
}

func TestChangedCoalescesBursts(t *testing.T) {
	s := New()
	for range 10 {
		s.Apply(func(*model.Snapshot) {})
	}
	<-s.Changed()
	select {
	case <-s.Changed():
		t.Fatal("expected a single coalesced notification")
	default:
	}
}

func TestAppendHistoryCapsAndPrunes(t *testing.T) {
	s := New()
	for i := range HistoryLen + 5 {
		s.Apply(AppendHistory(map[string]model.Sample{
			"pod:a": {At: time.Unix(int64(i), 0), CPUMilli: float64(i)},
			"ctr:x": {CPUMilli: 1},
		}, "pod:", true))
	}
	snap := s.Snapshot()
	h := snap.History["pod:a"]
	if len(h) != HistoryLen {
		t.Fatalf("history len = %d", len(h))
	}
	if h[len(h)-1].CPUMilli != float64(HistoryLen+4) || h[0].CPUMilli != 5 {
		t.Fatalf("history window wrong: first=%v last=%v", h[0].CPUMilli, h[len(h)-1].CPUMilli)
	}

	s.Apply(AppendHistory(map[string]model.Sample{"pod:b": {}}, "pod:", true))
	snap2 := s.Snapshot()
	if _, ok := snap2.History["pod:a"]; ok {
		t.Error("stale pod history should be pruned")
	}
	if _, ok := snap2.History["ctr:x"]; !ok {
		t.Error("other prefixes must be kept")
	}
	if len(snap.History["pod:a"]) != HistoryLen {
		t.Error("earlier snapshot history was mutated")
	}
}

func BenchmarkApply(b *testing.B) {
	s := New()
	for i := range b.N {
		s.Apply(SetHealth(fmt.Sprint(i%50), model.HealthResult{}))
	}
}
