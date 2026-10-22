package logs

import (
	"sort"
	"strings"
)

// DefaultCapacity is the number of entries kept per log view.
const DefaultCapacity = 5000

// Buffer holds entries ordered by time and attaches continuation lines to the
// previous entry of the same source.
type Buffer struct {
	capacity int
	entries  []*Entry
	last     map[string]*Entry
	dropped  int
}

func NewBuffer(capacity int) *Buffer {
	if capacity <= 0 {
		capacity = DefaultCapacity
	}
	return &Buffer{capacity: capacity, last: map[string]*Entry{}}
}

// Add parses and stores a line. It returns false when the line was blank or merged into an existing entry.
func (b *Buffer) Add(l Line) bool {
	if l.Kind == KindLog && strings.TrimSpace(l.Text) == "" {
		return false
	}
	if l.Kind == KindLog && IsContinuation(l.Text) {
		if prev := b.last[l.Source]; prev != nil {
			prev.Stack = append(prev.Stack, Clean(l.Text))
			return false
		}
	}
	e := Parse(l)
	b.insert(&e)
	if l.Kind == KindLog {
		b.last[l.Source] = &e
	}
	if len(b.entries) > b.capacity {
		drop := len(b.entries) - b.capacity + b.capacity/10
		b.entries = append([]*Entry(nil), b.entries[drop:]...)
		b.dropped += drop
	}
	return true
}

// insert keeps entries sorted by time. Entries without a time go to the end; streams
// mostly append, so the search starts from the back.
func (b *Buffer) insert(e *Entry) {
	n := len(b.entries)
	if e.Time.IsZero() || n == 0 || !e.Time.Before(b.entries[n-1].Time) {
		b.entries = append(b.entries, e)
		return
	}
	i := sort.Search(n, func(i int) bool {
		t := b.entries[i].Time
		return !t.IsZero() && t.After(e.Time)
	})
	b.entries = append(b.entries, nil)
	copy(b.entries[i+1:], b.entries[i:])
	b.entries[i] = e
}

func (b *Buffer) Len() int          { return len(b.entries) }
func (b *Buffer) At(i int) *Entry   { return b.entries[i] }
func (b *Buffer) Entries() []*Entry { return b.entries }
func (b *Buffer) Dropped() int      { return b.dropped }

func (b *Buffer) Reset() {
	b.entries = nil
	b.last = map[string]*Entry{}
	b.dropped = 0
}
