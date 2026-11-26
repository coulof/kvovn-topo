package main

// "--view all": the whole topology in one document, for offline debugging.

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
	"time"

	"github.com/coulof/kvovn-topo/pkg/api"
	"github.com/coulof/kvovn-topo/pkg/graph"
)

// topologyDump is the "--view all" document: the L3 and chassis views plus the
// L2 view of every logical switch, with every port (all pages merged).
type topologyDump struct {
	Revision int                           `json:"revision" yaml:"revision"`
	L3       *api.GraphResponse            `json:"l3" yaml:"l3"`
	Chassis  *api.GraphResponse            `json:"chassis" yaml:"chassis"`
	L2       map[string]*api.GraphResponse `json:"l2" yaml:"l2"`
}

// fetchGraphView loads one page of one view from the snapshot (fixture) or the daemon.
func fetchGraphView(snap *graph.Snapshot, daemon string, client *http.Client, view, focus, at string, page, pageSize int) (*api.GraphResponse, error) {
	if snap != nil {
		r, err := snap.BuildGraph(view, focus, 1, page, pageSize)
		if err == nil {
			err = viaJSON(r)
		}
		return r, err
	}
	q := url.Values{"view": {view}, "page": {strconv.Itoa(page)}, "pageSize": {strconv.Itoa(pageSize)}}
	if focus != "" {
		q.Set("focus", focus)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	var r api.GraphResponse
	if err := httpGetJSON(ctx, client, apiURL(daemon, "/graph", withAt(q, at)), "", &r); err != nil {
		return nil, err
	}
	return &r, nil
}

// buildDump assembles the full topology. Exactly one of snap and daemon is used.
func buildDump(snap *graph.Snapshot, daemon string, client *http.Client, at string) (*topologyDump, error) {
	l3, err := fetchGraphView(snap, daemon, client, "l3", "", at, 1, 100)
	if err != nil {
		return nil, err
	}
	ch, err := fetchGraphView(snap, daemon, client, "chassis", "", at, 1, 100)
	if err != nil {
		return nil, err
	}
	d := &topologyDump{Revision: l3.Revision, L3: l3, Chassis: ch, L2: map[string]*api.GraphResponse{}}
	for _, n := range l3.Nodes {
		if n.Type != api.GraphNodeTypeLS {
			continue
		}
		var merged *api.GraphResponse
		for page := 1; ; page++ {
			r, err := fetchGraphView(snap, daemon, client, "l2", n.Id, at, page, l2PageSize)
			if err != nil {
				return nil, err
			}
			if merged == nil {
				merged = r
			} else {
				merged.Nodes = append(merged.Nodes, r.Nodes...)
				merged.Edges = append(merged.Edges, r.Edges...)
			}
			if r.Pagination == nil || page >= r.Pagination.TotalPages {
				break
			}
		}
		merged.Pagination = nil
		d.L2[n.Id] = merged
	}
	return d, nil
}
