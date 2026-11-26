package history

import (
	"errors"
	"strings"
	"sync"
	"time"
)

var (
	// ErrOutOfCoverage: the asked point is older than the oldest retained state.
	ErrOutOfCoverage = errors.New("before the start of history coverage")
	// ErrInGap: the asked time falls in a gap, where the state is unknown.
	ErrInGap = errors.New("in a history gap: the state at that time is unknown")
	// ErrEmpty: nothing is recorded yet.
	ErrEmpty = errors.New("history is empty")
)

// Store keeps history records and checkpoints. MemoryStore is the default
// (ADR-0002); a bbolt store on a PVC implements the same interface.
type Store interface {
	// AddCheckpoint starts a new segment at cp.
	AddCheckpoint(cp Checkpoint)
	// Append adds a record after the latest checkpoint and numbers its events.
	Append(rec Record)
	// Base returns the nearest checkpoint at or before rev and the change
	// records after it, up to and including rev.
	Base(rev int64) (Checkpoint, []Record, error)
	// Resolve maps a wall-clock time to the revision current at that time.
	Resolve(t time.Time) (int64, error)
	// Events lists events newest first.
	Events(q EventQuery) []Event
	// Coverage describes what the store holds.
	Coverage() Coverage
	// SinceCheckpoint is the record count and estimated bytes after the latest
	// checkpoint, for the recorder's checkpoint cadence.
	SinceCheckpoint() (records int, bytes int64)
}

// EventQuery filters Events. Zero fields do not filter.
type EventQuery struct {
	Since, Until time.Time
	Kind         string // exact kind, or a prefix ending at a dot ("port" matches port.bound)
	Object       string
	Before       int64 // only events with Seq < Before (pagination)
	Limit        int
}

func (q EventQuery) match(e Event) bool {
	switch {
	case q.Before > 0 && e.Seq >= q.Before:
		return false
	case !q.Since.IsZero() && e.Time.Before(q.Since):
		return false
	case !q.Until.IsZero() && e.Time.After(q.Until):
		return false
	case q.Object != "" && e.Object != q.Object:
		return false
	case q.Kind != "" && e.Kind != q.Kind && !strings.HasPrefix(e.Kind, q.Kind+"."):
		return false
	}
	return true
}

type segment struct {
	cp      Checkpoint
	records []Record
	bytes   int64 // records only
}

// MemoryStore is the bounded in-memory store: segments (a checkpoint and the
// records after it) are dropped oldest first once a limit is exceeded, so a
// limit can be overshot by at most the newest segment.
type MemoryStore struct {
	mu       sync.RWMutex
	limits   Limits
	epoch    string
	segments []segment
	gaps     []Gap
	seq      int64
	now      func() time.Time
}

// NewMemoryStore returns an empty store for epoch with limits.
func NewMemoryStore(epoch string, limits Limits) *MemoryStore {
	return &MemoryStore{epoch: epoch, limits: limits, now: time.Now}
}

func (m *MemoryStore) AddCheckpoint(cp Checkpoint) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.segments = append(m.segments, segment{cp: cp})
	m.prune()
}

func (m *MemoryStore) Append(rec Record) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.segments) == 0 {
		return // nothing to replay from: the recorder checkpoints first
	}
	for i := range rec.Events {
		m.seq++
		rec.Events[i].Seq = m.seq
	}
	switch rec.Kind {
	case KindGap:
		m.gaps = append(m.gaps, Gap{Start: rec.Time, Revision: rec.Revision, Reason: rec.Reason})
	case KindCoalesced:
		if n := len(m.gaps); n > 0 && m.gaps[n-1].End.IsZero() {
			m.gaps[n-1].End = rec.Time
		}
	}
	head := &m.segments[len(m.segments)-1]
	head.records = append(head.records, rec)
	head.bytes += rec.bytes
	m.prune()
}

func (m *MemoryStore) SinceCheckpoint() (int, int64) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.segments) == 0 {
		return 0, 0
	}
	head := m.segments[len(m.segments)-1]
	return len(head.records), head.bytes
}

// prune drops the oldest segments while a limit is exceeded. The newest
// segment always stays. Called with mu held.
func (m *MemoryStore) prune() {
	for len(m.segments) > 1 {
		records, bytes := m.totals()
		over := (m.limits.MaxRecords > 0 && records > m.limits.MaxRecords) ||
			(m.limits.MaxBytes > 0 && bytes > m.limits.MaxBytes) ||
			// the whole oldest segment is older than MaxAge once the next one is
			(m.limits.MaxAge > 0 && m.segments[1].cp.Time.Before(m.now().Add(-m.limits.MaxAge)))
		if !over {
			return
		}
		m.segments[0] = segment{} // release the snapshot and rows
		m.segments = m.segments[1:]
	}
	start := m.segments[0].cp.Time
	for len(m.gaps) > 0 && !m.gaps[0].End.IsZero() && m.gaps[0].End.Before(start) {
		m.gaps = m.gaps[1:]
	}
}

func (m *MemoryStore) totals() (records int, bytes int64) {
	for _, s := range m.segments {
		records += len(s.records)
		bytes += s.bytes + s.cp.bytes
	}
	return records, bytes
}

func (m *MemoryStore) Base(rev int64) (Checkpoint, []Record, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.segments) == 0 {
		return Checkpoint{}, nil, ErrEmpty
	}
	if rev < m.segments[0].cp.Revision {
		return Checkpoint{}, nil, ErrOutOfCoverage
	}
	i := len(m.segments) - 1
	for m.segments[i].cp.Revision > rev {
		i--
	}
	seg := m.segments[i]
	var recs []Record
	for _, r := range seg.records {
		if r.Revision > rev {
			break
		}
		if r.Kind != KindGap {
			recs = append(recs, r)
		}
	}
	return seg.cp, recs, nil
}

func (m *MemoryStore) Resolve(t time.Time) (int64, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if len(m.segments) == 0 {
		return 0, ErrEmpty
	}
	if t.Before(m.segments[0].cp.Time) {
		return 0, ErrOutOfCoverage
	}
	for _, g := range m.gaps {
		if !t.Before(g.Start) && (g.End.IsZero() || t.Before(g.End)) {
			return 0, ErrInGap
		}
	}
	// The latest checkpoint or change at or before t.
	for i := len(m.segments) - 1; i >= 0; i-- {
		seg := m.segments[i]
		for j := len(seg.records) - 1; j >= 0; j-- {
			if r := seg.records[j]; r.Kind != KindGap && !r.Time.After(t) {
				return r.Revision, nil
			}
		}
		if !seg.cp.Time.After(t) {
			return seg.cp.Revision, nil
		}
	}
	return 0, ErrOutOfCoverage
}

func (m *MemoryStore) Events(q EventQuery) []Event {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var out []Event
	for i := len(m.segments) - 1; i >= 0; i-- {
		recs := m.segments[i].records
		for j := len(recs) - 1; j >= 0; j-- {
			if !q.Since.IsZero() && recs[j].Time.Before(q.Since) {
				return out
			}
			evs := recs[j].Events
			for k := len(evs) - 1; k >= 0; k-- {
				if q.match(evs[k]) {
					out = append(out, evs[k])
					if q.Limit > 0 && len(out) >= q.Limit {
						return out
					}
				}
			}
		}
	}
	return out
}

func (m *MemoryStore) Coverage() Coverage {
	m.mu.RLock()
	defer m.mu.RUnlock()
	c := Coverage{Mode: "memory", Epoch: m.epoch, Limits: m.limits, Checkpoints: len(m.segments)}
	if len(m.segments) == 0 {
		return c
	}
	first := m.segments[0].cp
	c.Oldest = Point{Epoch: m.epoch, Revision: first.Revision, Time: first.Time}
	c.Newest = c.Oldest
	for _, s := range m.segments {
		c.Records += len(s.records)
		c.Bytes += s.bytes + s.cp.bytes
		if s.cp.Revision > c.Newest.Revision {
			c.Newest = Point{Epoch: m.epoch, Revision: s.cp.Revision, Time: s.cp.Time}
		}
		for _, r := range s.records {
			c.Events += len(r.Events)
			if r.Kind != KindGap && r.Revision >= c.Newest.Revision {
				c.Newest = Point{Epoch: m.epoch, Revision: r.Revision, Time: r.Time}
			}
		}
	}
	c.Gaps = append([]Gap(nil), m.gaps...)
	return c
}
