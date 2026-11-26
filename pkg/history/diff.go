package history

import (
	"encoding/json"
	"sort"

	"github.com/coulof/kvovn-topo/pkg/api"
	"github.com/coulof/kvovn-topo/pkg/graph"
)

// DiffGraphs compares two renderings of the same view by node and edge ID,
// with field-level changes. Lists are sorted by ID.
func DiffGraphs(from, to *api.GraphResponse) api.GraphDiff {
	d := api.GraphDiff{
		View:         api.GraphDiffView(to.View),
		AddedNodes:   []api.GraphNode{},
		RemovedNodes: []api.GraphNode{},
		ChangedNodes: []api.NodeChange{},
		AddedEdges:   []api.GraphEdge{},
		RemovedEdges: []api.GraphEdge{},
		ChangedEdges: []api.EdgeChange{},
	}
	oldN := make(map[string]api.GraphNode, len(from.Nodes))
	for _, n := range from.Nodes {
		oldN[n.Id] = n
	}
	seen := make(map[string]bool, len(to.Nodes))
	for _, n := range to.Nodes {
		seen[n.Id] = true
		o, ok := oldN[n.Id]
		if !ok {
			d.AddedNodes = append(d.AddedNodes, n)
			continue
		}
		if f := fieldChanges(o, n); len(f) > 0 {
			d.ChangedNodes = append(d.ChangedNodes, api.NodeChange{Id: n.Id, Type: string(n.Type), Name: n.Name, Fields: f})
		}
	}
	for _, n := range from.Nodes {
		if !seen[n.Id] {
			d.RemovedNodes = append(d.RemovedNodes, n)
		}
	}

	oldE := make(map[string]api.GraphEdge, len(from.Edges))
	for _, e := range from.Edges {
		oldE[e.Id] = e
	}
	seenE := make(map[string]bool, len(to.Edges))
	for _, e := range to.Edges {
		seenE[e.Id] = true
		o, ok := oldE[e.Id]
		if !ok {
			d.AddedEdges = append(d.AddedEdges, e)
			continue
		}
		if f := fieldChanges(o, e); len(f) > 0 {
			d.ChangedEdges = append(d.ChangedEdges, api.EdgeChange{Id: e.Id, Source: e.Source, Target: e.Target, Type: string(e.Type), Fields: f})
		}
	}
	for _, e := range from.Edges {
		if !seenE[e.Id] {
			d.RemovedEdges = append(d.RemovedEdges, e)
		}
	}

	sort.Slice(d.AddedNodes, func(i, j int) bool { return d.AddedNodes[i].Id < d.AddedNodes[j].Id })
	sort.Slice(d.RemovedNodes, func(i, j int) bool { return d.RemovedNodes[i].Id < d.RemovedNodes[j].Id })
	sort.Slice(d.ChangedNodes, func(i, j int) bool { return d.ChangedNodes[i].Id < d.ChangedNodes[j].Id })
	sort.Slice(d.AddedEdges, func(i, j int) bool { return d.AddedEdges[i].Id < d.AddedEdges[j].Id })
	sort.Slice(d.RemovedEdges, func(i, j int) bool { return d.RemovedEdges[i].Id < d.RemovedEdges[j].Id })
	sort.Slice(d.ChangedEdges, func(i, j int) bool { return d.ChangedEdges[i].Id < d.ChangedEdges[j].Id })
	return d
}

// fieldChanges flattens both values through JSON to dotted leaves and lists
// the leaves that differ, sorted by path. Arrays are compared whole.
func fieldChanges(old, new any) []api.FieldChange {
	o, n := flatten(old), flatten(new)
	keys := make(map[string]bool, len(o)+len(n))
	for k := range o {
		keys[k] = true
	}
	for k := range n {
		keys[k] = true
	}
	var out []api.FieldChange
	for _, k := range sortedKeys(keys) {
		ov, ook := o[k]
		nv, nok := n[k]
		if ook && nok && ov == nv {
			continue
		}
		fc := api.FieldChange{Field: k}
		if ook {
			fc.From = &ov
		}
		if nok {
			fc.To = &nv
		}
		out = append(out, fc)
	}
	return out
}

func flatten(v any) map[string]string {
	b, err := json.Marshal(v)
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return nil
	}
	out := map[string]string{}
	var walk func(prefix string, v any)
	walk = func(prefix string, v any) {
		if mm, ok := v.(map[string]any); ok && len(mm) > 0 {
			for k, x := range mm {
				p := k
				if prefix != "" {
					p = prefix + "." + k
				}
				walk(p, x)
			}
			return
		}
		if prefix == "id" {
			return
		}
		b, _ := json.Marshal(v)
		out[prefix] = string(b)
	}
	walk("", m)
	return out
}

// FullGraph renders a view of s with every page (view=l2 is paginated).
func FullGraph(s *graph.Snapshot, view, focus string, depth int) (*api.GraphResponse, error) {
	g, err := s.BuildGraph(view, focus, depth, 1, 200)
	if err != nil {
		return nil, err
	}
	for p := g.Pagination; p != nil && p.Page < p.TotalPages; {
		next, err := s.BuildGraph(view, focus, depth, p.Page+1, 200)
		if err != nil {
			return nil, err
		}
		g.Nodes = append(g.Nodes, next.Nodes...)
		g.Edges = append(g.Edges, next.Edges...)
		p = next.Pagination
	}
	g.Pagination = nil
	// Pages repeat their shared context nodes (the switch, chassis).
	g.Nodes = dedupe(g.Nodes, func(n api.GraphNode) string { return n.Id })
	g.Edges = dedupe(g.Edges, func(e api.GraphEdge) string { return e.Id })
	return g, nil
}

func dedupe[T any](xs []T, id func(T) string) []T {
	seen := make(map[string]bool, len(xs))
	out := xs[:0]
	for _, x := range xs {
		if !seen[id(x)] {
			seen[id(x)] = true
			out = append(out, x)
		}
	}
	return out
}

// Diff compares a view between two points (see Resolve).
func (h *History) Diff(from, to Point, view, focus string, depth int) (*api.GraphDiff, error) {
	render := func(p Point) (*api.GraphResponse, error) {
		s, err := h.At(p)
		if err != nil {
			return nil, err
		}
		return FullGraph(s, view, focus, depth)
	}
	a, err := render(from)
	if err != nil {
		return nil, err
	}
	b, err := render(to)
	if err != nil {
		return nil, err
	}
	d := DiffGraphs(a, b)
	d.From = api.HistoryPoint{Epoch: from.Epoch, Revision: from.Revision, Time: from.Time}
	d.To = api.HistoryPoint{Epoch: to.Epoch, Revision: to.Revision, Time: to.Time}
	return &d, nil
}
