package history

import (
	"time"

	"github.com/coulof/kvovn-topo/pkg/graph"
)

// Op is what happened to a row.
type Op string

const (
	OpInsert Op = "insert"
	OpModify Op = "modify"
	OpRemove Op = "remove"
)

// RowChange is one OVSDB row change of a consumed table. Old and New are the
// immutable model rows the snapshots hold (nil for an insert / a remove).
type RowChange struct {
	DB, Table string
	Key       string // the snapshot key: row UUID, or name for SB Chassis
	Op        Op
	Columns   []string // changed columns of a modify
	Old, New  any
}

// RecordKind tells a change record from a gap marker.
type RecordKind string

const (
	// KindChange is a set of row changes published as one revision.
	KindChange RecordKind = "change"
	// KindCoalesced is the first change after a gap: the resync diffed against
	// the last known state. Changes inside the gap are merged into it.
	KindCoalesced RecordKind = "coalesced"
	// KindGap marks that changes may be missing from Time until the next
	// coalesced record. Nothing is interpolated across it.
	KindGap RecordKind = "gap"
)

// Record is one entry of the history log.
type Record struct {
	Kind     RecordKind
	Revision int64 // the revision it produced (a gap: the last revision before it)
	Time     time.Time
	Reason   string // gap only
	Rows     []RowChange
	Events   []Event
	bytes    int64 // estimated retained size
}

// Checkpoint is the full state of the consumed tables at Revision. In memory
// it is the published snapshot itself: immutable, and sharing its rows with
// the snapshots around it.
type Checkpoint struct {
	Revision int64
	Time     time.Time
	Snapshot *graph.Snapshot
	bytes    int64
}

// Event is a human-readable change derived from row changes (the summarizer).
type Event struct {
	Seq       int64 // monotonic across the store; newest has the highest
	Revision  int64
	Time      time.Time
	Kind      string // e.g. port.bound, gateway.failover, row.modified, gap
	Object    string // graph node ID the event is about ("" if none)
	Name      string // display name of Object
	Summary   string
	Coalesced bool // derived from a coalesced record: may merge several changes
}

// Point names a state in history.
type Point struct {
	Epoch    string
	Revision int64
	Time     time.Time
}

// Gap is a span with possibly missing changes: [Start, End). End is zero
// while the gap is still open.
type Gap struct {
	Start, End time.Time
	Revision   int64 // last revision before the gap
	Reason     string
}

// Coverage describes what history holds.
type Coverage struct {
	Mode           string
	Epoch          string
	Oldest, Newest Point
	Records        int
	Events         int
	Checkpoints    int
	Bytes          int64
	Gaps           []Gap
	Limits         Limits
}

// Limits bound the store; the first limit reached wins. Zero means unbounded.
type Limits struct {
	MaxRecords int
	MaxBytes   int64
	MaxAge     time.Duration
}
