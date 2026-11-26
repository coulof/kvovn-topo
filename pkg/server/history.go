package server

// Topology history endpoints (M8, ADR-0002): at= on /graph and /node, /diff,
// /history/events and /history/coverage. All read-only.

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/coulof/kvovn-topo/pkg/api"
	"github.com/coulof/kvovn-topo/pkg/graph"
	"github.com/coulof/kvovn-topo/pkg/history"
)

// SetHistory enables the history endpoints. Without it they answer 501.
func (s *Server) SetHistory(h *history.History) { s.history = h }

func (s *Server) historyEnabled(w http.ResponseWriter) bool {
	if s.history == nil {
		writeError(w, http.StatusNotImplemented, "HISTORY_DISABLED", "topology history is disabled on this daemon (history.mode=off)")
		return false
	}
	return true
}

// resolve maps a point string to a history point, writing the error response
// on failure.
func (s *Server) resolve(w http.ResponseWriter, at string) (history.Point, bool) {
	p, err := s.history.Resolve(at)
	if err != nil {
		writeHistoryError(w, err)
		return history.Point{}, false
	}
	return p, true
}

func writeHistoryError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, history.ErrBadPoint):
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	case errors.Is(err, history.ErrInGap):
		writeError(w, http.StatusUnprocessableEntity, "IN_GAP", err.Error())
	case errors.Is(err, history.ErrOutOfCoverage), errors.Is(err, history.ErrEmpty):
		writeError(w, http.StatusUnprocessableEntity, "OUT_OF_COVERAGE", err.Error())
	default:
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", err.Error())
	}
}

// snapshotAt is the live snapshot, or the reconstructed one for ?at=.
func (s *Server) snapshotAt(w http.ResponseWriter, r *http.Request) (*graph.Snapshot, bool) {
	at := r.URL.Query().Get("at")
	if at == "" || at == "now" {
		return s.store.Current(), true
	}
	if !s.historyEnabled(w) {
		return nil, false
	}
	p, ok := s.resolve(w, at)
	if !ok {
		return nil, false
	}
	snap, err := s.history.At(p)
	if err != nil {
		writeHistoryError(w, err)
		return nil, false
	}
	return snap, true
}

func (s *Server) handleDiff(w http.ResponseWriter, r *http.Request) {
	if !s.store.IsReady() {
		writeError(w, http.StatusServiceUnavailable, "SERVICE_UNAVAILABLE", "OVSDB monitors are synchronizing initial snapshot")
		return
	}
	if !s.historyEnabled(w) {
		return
	}
	q := r.URL.Query()
	if q.Get("from") == "" {
		writeError(w, http.StatusBadRequest, "BAD_REQUEST", "query parameter from is required")
		return
	}
	from, ok := s.resolve(w, q.Get("from"))
	if !ok {
		return
	}
	to, ok := s.resolve(w, q.Get("to"))
	if !ok {
		return
	}
	if from.Revision > to.Revision {
		from, to = to, from
	}
	depth, _ := strconv.Atoi(q.Get("depth"))
	d, err := s.history.Diff(from, to, q.Get("view"), q.Get("focus"), depth)
	if err != nil {
		writeHistoryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, d)
}

func (s *Server) handleHistoryEvents(w http.ResponseWriter, r *http.Request) {
	if !s.historyEnabled(w) {
		return
	}
	q := r.URL.Query()
	eq := history.EventQuery{Kind: q.Get("kind"), Object: q.Get("object"), Limit: 100}
	now := time.Now()
	for _, f := range []struct {
		name string
		dst  *time.Time
	}{{"since", &eq.Since}, {"until", &eq.Until}} {
		if v := q.Get(f.name); v != "" {
			t, err := history.ParseTime(v, now)
			if err != nil {
				writeError(w, http.StatusBadRequest, "BAD_REQUEST", f.name+": "+err.Error())
				return
			}
			*f.dst = t
		}
	}
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "BAD_REQUEST", "limit must be a positive integer")
			return
		}
		eq.Limit = min(n, 1000)
	}
	if v := q.Get("before"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n <= 0 {
			writeError(w, http.StatusBadRequest, "BAD_REQUEST", "before must be a positive integer")
			return
		}
		eq.Before = n
	}

	evs := s.history.Events(eq)
	out := api.HistoryEventList{Events: make([]api.HistoryEvent, 0, len(evs))}
	for _, e := range evs {
		out.Events = append(out.Events, toAPIEvent(e))
	}
	if len(evs) == eq.Limit {
		next := evs[len(evs)-1].Seq
		out.NextBefore = &next
	}
	writeJSON(w, http.StatusOK, out)
}

func toAPIEvent(e history.Event) api.HistoryEvent {
	ae := api.HistoryEvent{Seq: e.Seq, Revision: e.Revision, Time: e.Time, Kind: e.Kind, Summary: e.Summary}
	if e.Object != "" {
		ae.Object = &e.Object
	}
	if e.Name != "" {
		ae.Name = &e.Name
	}
	if e.Coalesced {
		ae.Coalesced = &e.Coalesced
	}
	return ae
}

func (s *Server) handleHistoryCoverage(w http.ResponseWriter, _ *http.Request) {
	if !s.historyEnabled(w) {
		return
	}
	c := s.history.Coverage()
	out := api.HistoryCoverage{
		Mode:        api.HistoryCoverageMode(c.Mode),
		Epoch:       c.Epoch,
		Records:     c.Records,
		Events:      c.Events,
		Checkpoints: c.Checkpoints,
		Bytes:       c.Bytes,
		Gaps:        make([]api.HistoryGap, 0, len(c.Gaps)),
	}
	out.Limits.MaxRecords = c.Limits.MaxRecords
	out.Limits.MaxBytes = c.Limits.MaxBytes
	out.Limits.MaxAgeSeconds = int64(c.Limits.MaxAge / time.Second)
	if c.Oldest.Revision > 0 {
		out.Oldest = &api.HistoryPoint{Epoch: c.Oldest.Epoch, Revision: c.Oldest.Revision, Time: c.Oldest.Time}
		out.Newest = &api.HistoryPoint{Epoch: c.Newest.Epoch, Revision: c.Newest.Revision, Time: c.Newest.Time}
	}
	for _, g := range c.Gaps {
		ag := api.HistoryGap{Start: g.Start, Revision: g.Revision, Reason: g.Reason}
		if !g.End.IsZero() {
			end := g.End
			ag.End = &end
		}
		out.Gaps = append(out.Gaps, ag)
	}
	writeJSON(w, http.StatusOK, out)
}
