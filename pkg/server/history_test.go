package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"

	"github.com/coulof/kvovn-topo/pkg/api"
	"github.com/coulof/kvovn-topo/pkg/graph"
	"github.com/coulof/kvovn-topo/pkg/history"
)

// setupHistoryServer is a ready server with history, a first state, and one
// router renamed after it (r1 → r2).
func setupHistoryServer(t *testing.T) (*Server, *graph.Store, int64, string) {
	t.Helper()
	snap := graph.LoadTestSnapshot(t)
	store := graph.NewStore(snap.NodeMapper)
	rec, _ := history.NewMemoryRecorder(history.DefaultConfig())
	h := history.New(rec, store)
	first := store.Apply(func(d *graph.Snapshot) []string {
		rev := d.Revision
		*d = *snap.Clone(rev)
		return []string{"*"}
	})
	var lrID string
	store.Apply(func(d *graph.Snapshot) []string {
		for id, lr := range d.LogicalRouters {
			cp := *lr
			cp.Name = "renamed-router"
			d.LogicalRouters[id] = &cp
			lrID = graph.PrefixLR + id
			break
		}
		return []string{lrID}
	})
	store.SetReady(true)
	srv := NewServer(store, VersionConfig{APIVersion: "v1"}, nil)
	srv.SetHistory(h)
	return srv, store, first.Revision, lrID
}

func get(t *testing.T, srv *Server, url string, out any) int {
	t.Helper()
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, httptest.NewRequest("GET", url, nil))
	if out != nil && w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
			t.Fatalf("%s: decode: %v", url, err)
		}
	}
	return w.Code
}

func TestHistoryDisabled(t *testing.T) {
	srv, store := setupTestServer(t)
	store.SetReady(true)
	for _, u := range []string{"/api/v1/history/events", "/api/v1/history/coverage", "/api/v1/diff?from=-1h", "/api/v1/graph?at=-1h"} {
		if code := get(t, srv, u, nil); code != http.StatusNotImplemented {
			t.Errorf("%s: %d, want 501", u, code)
		}
	}
	if code := get(t, srv, "/api/v1/graph?at=now", nil); code != http.StatusOK {
		t.Errorf("at=now without history: %d", code)
	}
}

func TestGraphAndNodeAt(t *testing.T) {
	srv, _, first, lrID := setupHistoryServer(t)
	var node api.NodeDetail
	if code := get(t, srv, "/api/v1/node/"+lrID+"?at="+strconv.FormatInt(first, 10), &node); code != http.StatusOK {
		t.Fatalf("node at: %d", code)
	}
	if node.Name == "renamed-router" {
		t.Errorf("node at r%d shows the later name", first)
	}
	if get(t, srv, "/api/v1/node/"+lrID, &node); node.Name != "renamed-router" {
		t.Errorf("live node name = %q", node.Name)
	}
	var g api.GraphResponse
	if code := get(t, srv, "/api/v1/graph?at="+strconv.FormatInt(first, 10), &g); code != http.StatusOK || int64(g.Revision) != first {
		t.Errorf("graph at: code %d revision %d, want r%d", code, g.Revision, first)
	}
	for at, want := range map[string]int{"-48h": 422, "0": 422, "garbage": 400} {
		if code := get(t, srv, "/api/v1/graph?at="+at, nil); code != want {
			t.Errorf("at=%s: %d, want %d", at, code, want)
		}
	}
}

func TestDiffEndpoint(t *testing.T) {
	srv, _, first, lrID := setupHistoryServer(t)
	var d api.GraphDiff
	if code := get(t, srv, "/api/v1/diff?view=l3&from="+strconv.FormatInt(first, 10), &d); code != http.StatusOK {
		t.Fatalf("diff: %d", code)
	}
	found := false
	for _, c := range d.ChangedNodes {
		for _, f := range c.Fields {
			if c.Id == lrID && f.Field == "name" && f.To != nil && *f.To == `"renamed-router"` {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("diff does not show the rename of %s: %+v", lrID, d.ChangedNodes)
	}
	if code := get(t, srv, "/api/v1/diff", nil); code != http.StatusBadRequest {
		t.Errorf("diff without from: %d", code)
	}
}

func TestHistoryEventsAndCoverage(t *testing.T) {
	srv, _, _, lrID := setupHistoryServer(t)
	var list api.HistoryEventList
	if code := get(t, srv, "/api/v1/history/events?object="+lrID, &list); code != http.StatusOK {
		t.Fatalf("events: %d", code)
	}
	if len(list.Events) != 1 || list.Events[0].Kind != "lr.changed" {
		t.Errorf("events for %s: %+v", lrID, list.Events)
	}
	if get(t, srv, "/api/v1/history/events?limit=1", &list); len(list.Events) != 1 || list.NextBefore == nil {
		t.Fatalf("page 1: %+v", list)
	}
	var page2 api.HistoryEventList
	get(t, srv, "/api/v1/history/events?limit=1&before="+strconv.FormatInt(*list.NextBefore, 10), &page2)
	if len(page2.Events) != 1 || page2.Events[0].Seq >= list.Events[0].Seq {
		t.Errorf("page 2: %+v", page2)
	}
	if code := get(t, srv, "/api/v1/history/events?since=yesterday", nil); code != http.StatusBadRequest {
		t.Errorf("bad since: %d", code)
	}

	var cov api.HistoryCoverage
	if code := get(t, srv, "/api/v1/history/coverage", &cov); code != http.StatusOK {
		t.Fatalf("coverage: %d", code)
	}
	if cov.Mode != "memory" || cov.Oldest == nil || cov.Newest == nil || cov.Newest.Revision <= cov.Oldest.Revision || cov.Limits.MaxRecords != 200000 {
		t.Errorf("coverage = %+v", cov)
	}
}
