package history

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/coulof/kvovn-topo/pkg/graph"
)

// ErrBadPoint: the point string cannot be parsed or names another epoch.
var ErrBadPoint = errors.New("invalid history point")

// History answers questions about the past: state at a point, the change
// feed, coverage. It reconstructs with the current pkg/graph code (ADR-0002
// driver 1).
type History struct {
	rec  *Recorder
	live *graph.Store
	now  func() time.Time

	mu    sync.Mutex
	cache []*graph.Snapshot // small MRU of reconstructed snapshots
}

const cacheSize = 4

// New wires a recorder to live: it becomes the store's observer.
func New(rec *Recorder, live *graph.Store) *History {
	live.SetObserver(rec)
	return &History{rec: rec, live: live, now: time.Now}
}

// Store is the underlying record store.
func (h *History) Store() Store { return h.rec.store }

// Epoch is the current epoch.
func (h *History) Epoch() string { return h.rec.epoch }

// Coverage describes the retained history.
func (h *History) Coverage() Coverage { return h.rec.store.Coverage() }

// Events lists events newest first.
func (h *History) Events(q EventQuery) []Event { return h.rec.store.Events(q) }

// ParseTime reads an RFC3339 time or a negative duration from now ("-1h").
func ParseTime(s string, now time.Time) (time.Time, error) {
	if strings.HasPrefix(s, "-") {
		d, err := time.ParseDuration(s)
		if err != nil {
			return time.Time{}, fmt.Errorf("%w: %q: %v", ErrBadPoint, s, err)
		}
		return now.Add(d), nil
	}
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		return time.Time{}, fmt.Errorf("%w: %q: want RFC3339, <epoch>:<revision>, a revision or -<duration>", ErrBadPoint, s)
	}
	return t, nil
}

// Resolve turns a point string into a revision: "" or "now" (live), an RFC3339
// time, "<epoch>:<revision>", a bare revision, or "-<duration>".
func (h *History) Resolve(s string) (Point, error) {
	cur := h.live.Current()
	live := Point{Epoch: h.rec.epoch, Revision: cur.Revision, Time: cur.Timestamp}
	switch {
	case s == "" || s == "now":
		return live, nil
	case isEpochRev(s):
		epoch, rev, _ := strings.Cut(s, ":")
		if epoch != h.rec.epoch {
			return Point{}, fmt.Errorf("%w: epoch %q is not retained (current epoch %s)", ErrOutOfCoverage, epoch, h.rec.epoch)
		}
		return h.revPoint(rev, live)
	case isDigits(s):
		return h.revPoint(s, live)
	}
	t, err := ParseTime(s, h.now())
	if err != nil {
		return Point{}, err
	}
	if !t.Before(h.now()) {
		return live, nil
	}
	rev, err := h.rec.store.Resolve(t)
	if err != nil {
		return Point{}, err
	}
	return Point{Epoch: h.rec.epoch, Revision: rev, Time: t}, nil
}

// isEpochRev matches "<epoch>:<revision>": 8 lowercase hex digits (NewEpoch), a colon, digits.
func isEpochRev(s string) bool {
	epoch, rev, ok := strings.Cut(s, ":")
	if !ok || len(epoch) != 8 || !isDigits(rev) {
		return false
	}
	for _, c := range epoch {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

func isDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}

func (h *History) revPoint(s string, live Point) (Point, error) {
	rev, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return Point{}, fmt.Errorf("%w: revision %q", ErrBadPoint, s)
	}
	if rev >= live.Revision {
		return live, nil
	}
	cp, recs, err := h.rec.store.Base(rev)
	if err != nil {
		return Point{}, err
	}
	t := cp.Time
	if len(recs) > 0 {
		t = recs[len(recs)-1].Time
	}
	return Point{Epoch: h.rec.epoch, Revision: rev, Time: t}, nil
}

// At returns the snapshot at a point resolved by Resolve. The live snapshot is
// returned as is; a past one is the nearest checkpoint plus replayed rows,
// with the current Kubernetes enrichment (ADR-0002 §7).
func (h *History) At(p Point) (*graph.Snapshot, error) {
	cur := h.live.Current()
	if p.Revision >= cur.Revision {
		return cur, nil
	}
	h.mu.Lock()
	for _, s := range h.cache {
		if s.Revision == p.Revision {
			h.mu.Unlock()
			return s, nil
		}
	}
	h.mu.Unlock()

	cp, recs, err := h.rec.store.Base(p.Revision)
	if err != nil {
		return nil, err
	}
	s := Replay(cp.Snapshot, recs, p.Revision)
	s.Timestamp = p.Time
	s.NodeMapper, s.WorkloadResolver = cur.NodeMapper, cur.WorkloadResolver

	h.mu.Lock()
	h.cache = append([]*graph.Snapshot{s}, h.cache...)
	if len(h.cache) > cacheSize {
		h.cache = h.cache[:cacheSize]
	}
	h.mu.Unlock()
	return s, nil
}

// Replay applies the row changes of recs on a copy of base and reindexes.
func Replay(base *graph.Snapshot, recs []Record, rev int64) *graph.Snapshot {
	s := base.Clone(rev)
	for _, r := range recs {
		for _, rc := range r.Rows {
			t, ok := tableByName[rc.Table]
			if !ok {
				continue // a table this build no longer consumes
			}
			t.put(s, rc.Key, rc.New)
		}
	}
	s.Reindex()
	return s
}
