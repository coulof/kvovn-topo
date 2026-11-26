package history

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/coulof/kvovn-topo/pkg/graph"
)

// Config tunes the recorder. Zero values take the ADR-0002 defaults.
type Config struct {
	Limits Limits
	// CheckpointEvery and CheckpointInterval: a checkpoint is taken after that
	// many change records or that much time, whichever comes first.
	CheckpointEvery    int
	CheckpointInterval time.Duration
	// Ignore lists, per table, columns whose changes alone are not recorded
	// (noise control). Inserts and removes are always recorded.
	Ignore map[string][]string
}

// DefaultConfig is the ADR-0002 default: 200k records / 256 MiB / 24 h,
// a checkpoint every 5 000 changes or 15 min.
func DefaultConfig() Config {
	return Config{
		Limits:             Limits{MaxRecords: 200_000, MaxBytes: 256 << 20, MaxAge: 24 * time.Hour},
		CheckpointEvery:    5000,
		CheckpointInterval: 15 * time.Minute,
		Ignore:             map[string][]string{"Chassis": {"nb_cfg"}},
	}
}

// Recorder turns published snapshots into history. It is the graph.Store
// observer (graph.Store.SetObserver).
type Recorder struct {
	cfg   Config
	store Store
	epoch string

	mu        sync.Mutex
	started   bool
	lastCP    time.Time
	gapOpen   bool
	lastRev   int64
	now       func() time.Time
	ignoreSet map[string]map[string]bool
}

// NewRecorder records into store under epoch ("" picks a random one).
func NewRecorder(cfg Config, store Store, epoch string) *Recorder {
	def := DefaultConfig()
	if cfg.CheckpointEvery <= 0 {
		cfg.CheckpointEvery = def.CheckpointEvery
	}
	if cfg.CheckpointInterval <= 0 {
		cfg.CheckpointInterval = def.CheckpointInterval
	}
	if epoch == "" {
		epoch = NewEpoch()
	}
	ig := make(map[string]map[string]bool, len(cfg.Ignore))
	for t, cols := range cfg.Ignore {
		ig[t] = make(map[string]bool, len(cols))
		for _, c := range cols {
			ig[t][c] = true
		}
	}
	return &Recorder{cfg: cfg, store: store, epoch: epoch, now: time.Now, ignoreSet: ig}
}

// NewMemoryRecorder is a recorder over a new MemoryStore bounded by cfg.Limits.
func NewMemoryRecorder(cfg Config) (*Recorder, *MemoryStore) {
	epoch := NewEpoch()
	st := NewMemoryStore(epoch, cfg.Limits)
	return NewRecorder(cfg, st, epoch), st
}

// NewEpoch returns a random epoch ID (8 hex digits).
func NewEpoch() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// Epoch is the recorder's epoch.
func (r *Recorder) Epoch() string { return r.epoch }

// Store is where the recorder writes.
func (r *Recorder) Store() Store { return r.store }

// Applied implements graph.Observer.
func (r *Recorder) Applied(prev, next *graph.Snapshot) {
	r.mu.Lock()
	defer r.mu.Unlock()
	now := next.Timestamp
	if now.IsZero() {
		now = r.now()
	}
	r.lastRev = next.Revision
	if !r.started {
		// History starts with the first non-empty state (the initial OVSDB
		// snapshot), so the initial load is not thousands of "added" events.
		if rowCount(next) == 0 {
			return
		}
		r.started = true
		r.checkpoint(next, now)
		r.store.Append(Record{Kind: KindChange, Revision: next.Revision, Time: now, Events: []Event{{
			Revision: next.Revision, Time: now, Kind: "history.started",
			Summary: fmt.Sprintf("history starts: %d rows in %d consumed tables", rowCount(next), len(tables)),
		}}})
		return
	}

	rows := r.diff(prev, next)
	coalesced := r.gapOpen
	if len(rows) == 0 && !coalesced {
		return // a Kubernetes-only change: nothing in OVSDB moved
	}
	kind := KindChange
	if coalesced {
		kind = KindCoalesced
		r.gapOpen = false
	}
	events := summarize(prev, next, rows)
	for i := range events {
		events[i].Revision, events[i].Time, events[i].Coalesced = next.Revision, now, coalesced
	}
	if coalesced {
		events = append([]Event{{
			Revision: next.Revision, Time: now, Kind: "gap.end", Coalesced: true,
			Summary: fmt.Sprintf("resynchronized: %d row changes during the gap, merged", len(rows)),
		}}, events...)
	}
	rec := Record{Kind: kind, Revision: next.Revision, Time: now, Rows: rows, Events: events}
	rec.bytes = recordBytes(rows, events)
	r.store.Append(rec)

	n, b := r.store.SinceCheckpoint()
	switch {
	case coalesced, // replay never crosses a gap
		n >= r.cfg.CheckpointEvery,
		now.Sub(r.lastCP) >= r.cfg.CheckpointInterval,
		r.cfg.Limits.MaxBytes > 0 && b >= r.cfg.Limits.MaxBytes/8:
		r.checkpoint(next, now)
	}
}

// Gap implements graph.Observer.
func (r *Recorder) Gap(reason string, at time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.started || r.gapOpen {
		return
	}
	r.gapOpen = true
	rev := r.lastRev
	r.store.Append(Record{Kind: KindGap, Revision: rev, Time: at, Reason: reason, Events: []Event{{
		Revision: rev, Time: at, Kind: "gap.start", Summary: "history gap: " + reason + " (changes until the resync are merged)",
	}}})
}

func (r *Recorder) checkpoint(s *graph.Snapshot, at time.Time) {
	r.lastCP = at
	// Map entries only: the rows are shared with the live snapshots and records.
	r.store.AddCheckpoint(Checkpoint{Revision: s.Revision, Time: at, Snapshot: s, bytes: int64(rowCount(s)) * 64})
}

func (r *Recorder) diff(prev, next *graph.Snapshot) []RowChange {
	var rows []RowChange
	for _, t := range tables {
		t.diff(prev, next, func(key string, old, new any) {
			rc := RowChange{DB: t.db, Table: t.name, Key: key, Old: old, New: new}
			switch {
			case old == nil:
				rc.Op = OpInsert
			case new == nil:
				rc.Op = OpRemove
			default:
				rc.Op = OpModify
				rc.Columns = changedColumns(old, new)
				if r.ignored(t.name, rc.Columns) {
					return
				}
			}
			rows = append(rows, rc)
		})
	}
	return rows
}

func (r *Recorder) ignored(table string, cols []string) bool {
	ig := r.ignoreSet[table]
	if len(ig) == 0 {
		return false
	}
	for _, c := range cols {
		if !ig[c] {
			return false
		}
	}
	return true
}

// recordBytes estimates what a record keeps alive: the new row versions (old
// ones are counted by the record that introduced them, or the checkpoint).
func recordBytes(rows []RowChange, events []Event) int64 {
	n := int64(64)
	for _, rc := range rows {
		n += 96
		if rc.New != nil {
			if b, err := json.Marshal(rc.New); err == nil {
				n += int64(len(b))
			}
		}
	}
	for _, e := range events {
		n += int64(96 + len(e.Summary) + len(e.Object) + len(e.Name))
	}
	return n
}
