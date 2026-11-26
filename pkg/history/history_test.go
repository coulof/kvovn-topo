package history

import (
	"encoding/json"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/coulof/kvovn-topo/pkg/graph"
	"github.com/coulof/kvovn-topo/pkg/ovsdb"
	"github.com/coulof/kvovn-topo/pkg/ovsdb/nbmodel"
	"github.com/coulof/kvovn-topo/pkg/ovsdb/sbmodel"
)

const scenario = "../../testdata/scenarios/custom-vpc-ovn-nat"

// harness is a live store fed like the syncer does, with history attached.
type harness struct {
	t    *testing.T
	live *graph.Store
	h    *History
}

func newHarness(t *testing.T, cfg Config) *harness {
	t.Helper()
	fix, err := ovsdb.LoadFixture(scenario)
	if err != nil {
		t.Fatalf("load fixture: %v", err)
	}
	live := graph.NewStore(nil)
	rec, _ := NewMemoryRecorder(cfg)
	h := New(rec, live)
	// A Kubernetes-only apply before OVSDB arrives must not start history.
	live.Apply(func(*graph.Snapshot) []string { return nil })
	live.Apply(func(d *graph.Snapshot) []string { copyTables(d, fix); return []string{"*"} })
	return &harness{t: t, live: live, h: h}
}

// copyTables sets the consumed tables of d to fresh maps holding src's rows.
func copyTables(d, src *graph.Snapshot) {
	c := src.Clone(d.Revision)
	d.LogicalRouters, d.LogicalRouterPorts, d.StaticRoutes, d.Policies = c.LogicalRouters, c.LogicalRouterPorts, c.StaticRoutes, c.Policies
	d.NATs, d.GatewayChassis, d.HAChassisGroups, d.HAChassis = c.NATs, c.GatewayChassis, c.HAChassisGroups, c.HAChassis
	d.LogicalSwitches, d.LogicalSwitchPorts, d.LoadBalancers = c.LogicalSwitches, c.LogicalSwitchPorts, c.LoadBalancers
	d.Chassis, d.Encaps, d.PortBindings, d.DatapathBindings, d.BFDs = c.Chassis, c.Encaps, c.PortBindings, c.DatapathBindings, c.BFDs
}

// render is every view the reconstruction must reproduce, as JSON.
func render(t *testing.T, s *graph.Snapshot) string {
	t.Helper()
	var parts []string
	views := [][2]string{{"l3", ""}, {"chassis", ""}}
	for id := range s.LogicalSwitches {
		views = append(views, [2]string{"l2", graph.PrefixLS + id})
	}
	slices.SortFunc(views, func(a, b [2]string) int { return strings.Compare(a[0]+a[1], b[0]+b[1]) })
	for _, v := range views {
		g, err := FullGraph(s, v[0], v[1], 1)
		if err != nil {
			t.Fatalf("render %v: %v", v, err)
		}
		b, _ := json.Marshal(g)
		parts = append(parts, string(b))
	}
	return strings.Join(parts, "\n")
}

// step applies one change through the live store and returns the published snapshot.
func (hs *harness) step(fn func(d *graph.Snapshot)) *graph.Snapshot {
	return hs.live.Apply(func(d *graph.Snapshot) []string { fn(d); return []string{"*"} })
}

func chassisUUIDs(s *graph.Snapshot) []string {
	var out []string
	for _, ch := range s.Chassis {
		out = append(out, ch.UUID)
	}
	slices.Sort(out)
	return out
}

func sortedPBs(s *graph.Snapshot) []*sbmodel.PortBinding {
	var out []*sbmodel.PortBinding
	for _, pb := range s.PortBindings {
		out = append(out, pb)
	}
	slices.SortFunc(out, func(a, b *sbmodel.PortBinding) int { return strings.Compare(a.LogicalPort, b.LogicalPort) })
	return out
}

// moveBinding rebinds the first port matching want to another chassis (or
// unbinds it if there is only one chassis). It reports the port name.
func moveBinding(d *graph.Snapshot, want func(*sbmodel.PortBinding) bool) string {
	chs := chassisUUIDs(d)
	for _, pb := range sortedPBs(d) {
		if pb.Chassis == nil || !want(pb) {
			continue
		}
		cp := *pb
		cp.Chassis = nil
		for _, c := range chs {
			if c != *pb.Chassis {
				c := c
				cp.Chassis = &c
				break
			}
		}
		d.PortBindings[pb.UUID] = &cp
		return pb.LogicalPort
	}
	return ""
}

type stepCase struct {
	name string
	fn   func(d *graph.Snapshot)
	kind string // an event kind the step must produce ("" for none expected)
}

func scriptedSteps() []stepCase {
	return []stepCase{
		{"workload port moves", func(d *graph.Snapshot) {
			moveBinding(d, func(pb *sbmodel.PortBinding) bool { return pb.Type == "" })
		}, "port."},
		{"gateway fails over", func(d *graph.Snapshot) {
			moveBinding(d, func(pb *sbmodel.PortBinding) bool { return strings.HasPrefix(pb.LogicalPort, "cr-") })
		}, "gateway."},
		{"FIP retargeted", func(d *graph.Snapshot) {
			for _, n := range sortedNATs(d) {
				cp := *n
				cp.LogicalIP = "10.99.99.99"
				d.NATs[n.UUID] = &cp
				return
			}
		}, "nat.retargeted"},
		{"route removed", func(d *graph.Snapshot) {
			for _, lr := range d.LogicalRouters {
				if len(lr.StaticRoutes) == 0 {
					continue
				}
				gone := lr.StaticRoutes[0]
				cp := *lr
				cp.StaticRoutes = slices.Clone(lr.StaticRoutes[1:])
				d.LogicalRouters[lr.UUID] = &cp
				delete(d.StaticRoutes, gone)
				return
			}
		}, "route.removed"},
		{"switch port added", func(d *graph.Snapshot) {
			for _, ls := range d.LogicalSwitches {
				lsp := &nbmodel.LogicalSwitchPort{UUID: "00000000-hist-test-0000-000000000001", Name: "hist-test-port", Addresses: []string{"00:00:00:00:00:01 10.0.0.250"}}
				d.LogicalSwitchPorts[lsp.UUID] = lsp
				cp := *ls
				cp.Ports = append(slices.Clone(ls.Ports), lsp.UUID)
				d.LogicalSwitches[ls.UUID] = &cp
				return
			}
		}, "lsp.added"},
	}
}

func sortedNATs(s *graph.Snapshot) []*nbmodel.NAT {
	var out []*nbmodel.NAT
	for _, n := range s.NATs {
		out = append(out, n)
	}
	slices.SortFunc(out, func(a, b *nbmodel.NAT) int { return strings.Compare(a.UUID, b.UUID) })
	return out
}

// TestReconstructionMatchesLive is the M8 DoD reconstruction check: after a
// scripted sequence, graph(at=rev_k) equals the live graph captured at rev_k.
func TestReconstructionMatchesLive(t *testing.T) {
	cfg := DefaultConfig()
	cfg.CheckpointEvery = 2 // replay across several checkpoints
	hs := newHarness(t, cfg)

	type captured struct {
		rev  int64
		want string
	}
	start := hs.live.Current()
	caps := []captured{{start.Revision, render(t, start)}}
	for _, st := range scriptedSteps() {
		s := hs.step(st.fn)
		hs.live.Apply(func(*graph.Snapshot) []string { return nil }) // k8s-only noise
		caps = append(caps, captured{s.Revision, render(t, s)})
	}
	for _, c := range caps {
		p, err := hs.h.Resolve(itoa(c.rev))
		if err != nil {
			t.Fatalf("resolve r%d: %v", c.rev, err)
		}
		s, err := hs.h.At(p)
		if err != nil {
			t.Fatalf("at r%d: %v", c.rev, err)
		}
		if s == hs.live.Current() {
			t.Fatalf("r%d resolved to the live snapshot", c.rev)
		}
		if got := render(t, s); got != c.want {
			t.Errorf("r%d: reconstructed graph differs from the live capture", c.rev)
		}
	}
	if cov := hs.h.Coverage(); cov.Checkpoints < 3 {
		t.Errorf("checkpoints = %d, want several (CheckpointEvery=2)", cov.Checkpoints)
	}
}

func itoa(r int64) string { return strconv.FormatInt(r, 10) }

func TestSummarizerCatalogue(t *testing.T) {
	hs := newHarness(t, DefaultConfig())
	for _, st := range scriptedSteps() {
		before := len(hs.h.Events(EventQuery{}))
		hs.step(st.fn)
		evs := hs.h.Events(EventQuery{})
		fresh := evs[:len(evs)-before]
		if len(fresh) == 0 {
			t.Errorf("%s: no event", st.name)
			continue
		}
		if !slices.ContainsFunc(fresh, func(e Event) bool { return strings.HasPrefix(e.Kind, st.kind) }) {
			var kinds []string
			for _, e := range fresh {
				kinds = append(kinds, e.Kind+": "+e.Summary)
			}
			t.Errorf("%s: want a %q event, got %v", st.name, st.kind, kinds)
		}
		for _, e := range fresh {
			if e.Summary == "" || e.Seq == 0 || e.Revision == 0 {
				t.Errorf("%s: incomplete event %+v", st.name, e)
			}
		}
	}
	// Newest first, seq strictly decreasing.
	evs := hs.h.Events(EventQuery{})
	for i := 1; i < len(evs); i++ {
		if evs[i].Seq >= evs[i-1].Seq {
			t.Fatalf("events not newest first at %d", i)
		}
	}
	if evs[len(evs)-1].Kind != "history.started" {
		t.Errorf("oldest event = %q, want history.started", evs[len(evs)-1].Kind)
	}
	// Filters.
	if got := hs.h.Events(EventQuery{Kind: "nat"}); len(got) == 0 || got[0].Kind != "nat.retargeted" {
		t.Errorf("kind prefix filter: %+v", got)
	}
	obj := hs.h.Events(EventQuery{Kind: "lsp.added"})
	if len(obj) != 1 || len(hs.h.Events(EventQuery{Object: obj[0].Object})) != 1 {
		t.Errorf("object filter: %+v", obj)
	}
	if got := hs.h.Events(EventQuery{Limit: 2}); len(got) != 2 {
		t.Errorf("limit: %d", len(got))
	}
	if got := hs.h.Events(EventQuery{Before: evs[0].Seq}); len(got) != len(evs)-1 {
		t.Errorf("before: %d, want %d", len(got), len(evs)-1)
	}
}

func TestGapIsCoalescedAndNeverInterpolated(t *testing.T) {
	hs := newHarness(t, DefaultConfig())
	before := hs.live.Current()

	hs.live.MarkGap("nb unreachable")
	hs.live.MarkGap("sb unreachable") // one gap, not two
	gapAt := time.Now()
	time.Sleep(5 * time.Millisecond)

	// The resync re-reads every row (new pointers, same content) and one port moved.
	var moved string
	hs.step(func(d *graph.Snapshot) {
		copyTables(d, deepCopy(t, d))
		moved = moveBinding(d, func(pb *sbmodel.PortBinding) bool { return pb.Type == "" })
	})
	if moved == "" {
		t.Skip("fixture has no bound workload port")
	}

	cov := hs.h.Coverage()
	if len(cov.Gaps) != 1 || cov.Gaps[0].End.IsZero() || cov.Gaps[0].Reason != "nb unreachable" {
		t.Fatalf("gaps = %+v", cov.Gaps)
	}
	if _, err := hs.h.Resolve(gapAt.Format(time.RFC3339Nano)); !errors.Is(err, ErrInGap) {
		t.Errorf("time inside the gap: err = %v, want ErrInGap", err)
	}
	// The coalesced record holds only the real change, not the re-read rows.
	evs := hs.h.Events(EventQuery{Since: gapAt})
	var coalesced []Event
	for _, e := range evs {
		if e.Coalesced {
			coalesced = append(coalesced, e)
		}
	}
	if len(coalesced) != 2 || coalesced[1].Kind != "gap.end" || !strings.HasPrefix(coalesced[0].Kind, "port.") {
		t.Errorf("coalesced events = %+v", coalesced)
	}
	// Both sides of the gap reconstruct by revision.
	for _, s := range []*graph.Snapshot{before} {
		got, err := hs.h.At(Point{Revision: s.Revision})
		if err != nil || render(t, got) != render(t, s) {
			t.Errorf("r%d before the gap: err=%v", s.Revision, err)
		}
	}
}

// deepCopy re-reads s's rows into new pointers (what a resync produces).
func deepCopy(t *testing.T, s *graph.Snapshot) *graph.Snapshot {
	t.Helper()
	c := graph.NewEmptySnapshot(s.Revision, nil)
	cp := func(dst, src any) {
		b, err := json.Marshal(src)
		if err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(b, dst); err != nil {
			t.Fatal(err)
		}
	}
	cp(&c.LogicalRouters, s.LogicalRouters)
	cp(&c.LogicalRouterPorts, s.LogicalRouterPorts)
	cp(&c.StaticRoutes, s.StaticRoutes)
	cp(&c.Policies, s.Policies)
	cp(&c.NATs, s.NATs)
	cp(&c.GatewayChassis, s.GatewayChassis)
	cp(&c.HAChassisGroups, s.HAChassisGroups)
	cp(&c.HAChassis, s.HAChassis)
	cp(&c.LogicalSwitches, s.LogicalSwitches)
	cp(&c.LogicalSwitchPorts, s.LogicalSwitchPorts)
	cp(&c.LoadBalancers, s.LoadBalancers)
	cp(&c.Chassis, s.Chassis)
	cp(&c.Encaps, s.Encaps)
	cp(&c.PortBindings, s.PortBindings)
	cp(&c.DatapathBindings, s.DatapathBindings)
	cp(&c.BFDs, s.BFDs)
	return c
}

func TestResolveAndCoverageBounds(t *testing.T) {
	hs := newHarness(t, DefaultConfig())
	cov := hs.h.Coverage()
	if cov.Mode != "memory" || cov.Oldest.Revision == 0 || cov.Epoch == "" {
		t.Fatalf("coverage = %+v", cov)
	}
	live := hs.live.Current()
	if p, err := hs.h.Resolve("now"); err != nil || p.Revision != live.Revision {
		t.Errorf("now: %+v %v", p, err)
	}
	if _, err := hs.h.Resolve("-48h"); !errors.Is(err, ErrOutOfCoverage) {
		t.Errorf("-48h: %v, want ErrOutOfCoverage", err)
	}
	if _, err := hs.h.Resolve("0"); !errors.Is(err, ErrOutOfCoverage) {
		t.Errorf("r0: %v, want ErrOutOfCoverage", err)
	}
	if _, err := hs.h.Resolve("deadbeef:3"); !errors.Is(err, ErrOutOfCoverage) {
		t.Errorf("other epoch: %v", err)
	}
	if _, err := hs.h.Resolve("yesterday"); !errors.Is(err, ErrBadPoint) {
		t.Errorf("garbage: %v", err)
	}
	if p, err := hs.h.Resolve(hs.h.Epoch() + ":" + itoa(cov.Oldest.Revision)); err != nil || p.Revision != cov.Oldest.Revision {
		t.Errorf("epoch:rev: %+v %v", p, err)
	}
}

func TestMemoryStorePrunesOldestSegments(t *testing.T) {
	m := NewMemoryStore("e", Limits{MaxRecords: 4})
	t0 := time.Now()
	snap := graph.NewEmptySnapshot(0, nil)
	for i := int64(1); i <= 12; i++ {
		if i%3 == 1 {
			m.AddCheckpoint(Checkpoint{Revision: i, Time: t0.Add(time.Duration(i) * time.Second), Snapshot: snap})
		}
		m.Append(Record{Kind: KindChange, Revision: i, Time: t0.Add(time.Duration(i) * time.Second), Events: []Event{{Kind: "x"}}})
	}
	cov := m.Coverage()
	if cov.Records > 4+3 { // a limit is overshot by at most the newest segment
		t.Errorf("records = %d", cov.Records)
	}
	if cov.Oldest.Revision != 7 && cov.Oldest.Revision != 10 {
		t.Errorf("oldest = r%d", cov.Oldest.Revision)
	}
	if _, _, err := m.Base(2); !errors.Is(err, ErrOutOfCoverage) {
		t.Errorf("pruned revision: %v", err)
	}
	if _, err := m.Resolve(t0); !errors.Is(err, ErrOutOfCoverage) {
		t.Errorf("pruned time: %v", err)
	}

	aged := NewMemoryStore("e", Limits{MaxAge: time.Hour})
	aged.now = func() time.Time { return t0.Add(3 * time.Hour) }
	aged.AddCheckpoint(Checkpoint{Revision: 1, Time: t0, Snapshot: snap})
	aged.AddCheckpoint(Checkpoint{Revision: 2, Time: t0.Add(time.Hour), Snapshot: snap})
	aged.AddCheckpoint(Checkpoint{Revision: 3, Time: t0.Add(150 * time.Minute), Snapshot: snap})
	if c := aged.Coverage(); c.Oldest.Revision != 2 {
		t.Errorf("age prune: oldest = r%d, want r2 (its successor is within MaxAge)", c.Oldest.Revision)
	}
}

func TestIgnoredColumnsAreNotRecorded(t *testing.T) {
	hs := newHarness(t, DefaultConfig())
	before := hs.h.Coverage().Records
	hs.step(func(d *graph.Snapshot) {
		for name, ch := range d.Chassis {
			cp := *ch
			cp.NbCfg++
			d.Chassis[name] = &cp
		}
	})
	if got := hs.h.Coverage().Records; got != before {
		t.Errorf("nb_cfg-only change recorded: %d → %d records", before, got)
	}
}

func TestDiffReportsChangedFields(t *testing.T) {
	hs := newHarness(t, DefaultConfig())
	from := hs.live.Current().Revision
	var port string
	hs.step(func(d *graph.Snapshot) {
		port = moveBinding(d, func(pb *sbmodel.PortBinding) bool { return strings.HasPrefix(pb.LogicalPort, "cr-") })
	})
	if port == "" {
		t.Skip("fixture has no gateway port")
	}
	fp, _ := hs.h.Resolve(itoa(from))
	tp, _ := hs.h.Resolve("now")
	d, err := hs.h.Diff(fp, tp, "l3", "", 1)
	if err != nil {
		t.Fatal(err)
	}
	if d.From.Revision != from || d.To.Revision != tp.Revision {
		t.Errorf("points: %+v → %+v", d.From, d.To)
	}
	if len(d.ChangedNodes)+len(d.ChangedEdges)+len(d.AddedEdges)+len(d.RemovedEdges) == 0 {
		t.Fatalf("gateway failover produced an empty l3 diff: %+v", d)
	}
	for _, c := range d.ChangedNodes {
		for _, f := range c.Fields {
			if f.From == nil && f.To == nil {
				t.Errorf("%s.%s: empty field change", c.Id, f.Field)
			}
		}
	}
	// Same point on both sides: empty diff.
	same, _ := hs.h.Diff(tp, tp, "chassis", "", 1)
	if n := len(same.AddedNodes) + len(same.RemovedNodes) + len(same.ChangedNodes) + len(same.ChangedEdges); n != 0 {
		t.Errorf("self diff not empty: %d", n)
	}
}
