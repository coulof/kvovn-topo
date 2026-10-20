package graph

import (
	"sync"
	"sync/atomic"
	"time"

	"github.com/coulof/ovn-topology/pkg/api"
)

// Store manages thread-safe, immutable snapshot storage with atomic pointer swaps
// and pub-sub notifications for live SSE consumers.
type Store struct {
	current atomic.Pointer[Snapshot]
	ready   atomic.Bool

	subMu       sync.Mutex
	subscribers map[chan api.GraphEvent]struct{}
	nodeMapper  NodeMapper
}

// NewStore initializes an empty Store with revision 0.
func NewStore(mapper NodeMapper) *Store {
	if mapper == nil {
		mapper = &StaticNodeMapper{}
	}
	s := &Store{
		subscribers: make(map[chan api.GraphEvent]struct{}),
		nodeMapper:  mapper,
	}
	initial := NewEmptySnapshot(0, mapper)
	s.current.Store(initial)
	return s
}

// Current returns the immutable current snapshot. Never blocks.
func (s *Store) Current() *Snapshot {
	return s.current.Load()
}

// IsReady indicates whether initial sync with NB and SB has completed.
func (s *Store) IsReady() bool {
	return s.ready.Load()
}

// SetReady marks the store ready after initial NB and SB snapshots have synchronized.
func (s *Store) SetReady(ready bool) {
	s.ready.Store(ready)
}

// Apply executes a mutation function on a draft snapshot, increments the revision,
// reindexes, swaps the snapshot pointer atomically, and notifies all subscribers.
func (s *Store) Apply(fn func(draft *Snapshot) []string) *Snapshot {
	s.subMu.Lock()
	defer s.subMu.Unlock()

	cur := s.current.Load()
	nextRev := cur.Revision + 1
	draft := cur.Clone(nextRev)
	draft.Timestamp = time.Now()

	changedIDs := fn(draft)
	draft.Reindex()

	s.current.Store(draft)

	if len(changedIDs) > 0 {
		event := api.GraphEvent{
			Revision: int(nextRev),
			Changed:  changedIDs,
		}
		for ch := range s.subscribers {
			select {
			case ch <- event:
			default:
				// Degrade gracefully if subscriber channel is slow
			}
		}
	}

	return draft
}

// Subscribe returns an event channel and an unsubscribe func.
func (s *Store) Subscribe() (<-chan api.GraphEvent, func()) {
	s.subMu.Lock()
	defer s.subMu.Unlock()

	ch := make(chan api.GraphEvent, 32)
	s.subscribers[ch] = struct{}{}

	unsubscribe := func() {
		s.subMu.Lock()
		defer s.subMu.Unlock()
		delete(s.subscribers, ch)
		close(ch)
	}

	return ch, unsubscribe
}
