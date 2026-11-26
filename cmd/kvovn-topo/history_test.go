package main

import (
	"net/http/httptest"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/coulof/kvovn-topo/pkg/graph"
	"github.com/coulof/kvovn-topo/pkg/history"
	"github.com/coulof/kvovn-topo/pkg/ovsdb"
	"github.com/coulof/kvovn-topo/pkg/ovsdb/sbmodel"
	"github.com/coulof/kvovn-topo/pkg/server"
)

// pump runs cmd and every command its messages produce (batches included).
// It goes through update, not Update, so no flash timers are started.
func pump(m model, cmd tea.Cmd) model {
	for queue := []tea.Cmd{cmd}; len(queue) > 0; {
		c := queue[0]
		queue = queue[1:]
		if c == nil {
			continue
		}
		msg := c()
		if b, ok := msg.(tea.BatchMsg); ok {
			queue = append(queue, b...)
			continue
		}
		r, next := m.update(msg)
		m = r.(model)
		queue = append(queue, next)
	}
	return m
}

func pressRun(m model, keys ...string) model {
	for _, k := range keys {
		r, cmd := m.update(keyMsg(k))
		m = pump(r.(model), cmd)
	}
	return m
}

func command(m model, c string) model {
	m = pressRun(m, ":")
	for _, r := range c {
		m = pressRun(m, string(r))
	}
	return pressRun(m, "enter")
}

// historyDaemon serves the custom-vpc-ovn-nat scenario with history on (or
// off) and returns the live store.
func historyDaemon(t *testing.T, withHistory bool) (*httptest.Server, *graph.Store) {
	t.Helper()
	fix, err := ovsdb.LoadFixture("../../testdata/scenarios/custom-vpc-ovn-nat")
	if err != nil {
		t.Fatal(err)
	}
	store := graph.NewStore(nil)
	var h *history.History
	if withHistory {
		rec, _ := history.NewMemoryRecorder(history.DefaultConfig())
		h = history.New(rec, store)
	}
	store.Apply(func(d *graph.Snapshot) []string { *d = *fix.Clone(d.Revision); return []string{"*"} })
	store.SetReady(true)
	srv := server.NewServer(store, server.VersionConfig{APIVersion: "v1"}, nil)
	if h != nil {
		srv.SetHistory(h)
	}
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return ts, store
}

// failover moves the first gateway (cr-) port to another chassis; it returns
// the router port name.
func failover(t *testing.T, store *graph.Store) string {
	t.Helper()
	var lrp string
	store.Apply(func(d *graph.Snapshot) []string {
		var pbs []*sbmodel.PortBinding
		for _, pb := range d.PortBindings {
			if strings.HasPrefix(pb.LogicalPort, "cr-") && pb.Chassis != nil {
				pbs = append(pbs, pb)
			}
		}
		slices.SortFunc(pbs, func(a, b *sbmodel.PortBinding) int { return strings.Compare(a.LogicalPort, b.LogicalPort) })
		if len(pbs) == 0 {
			return nil
		}
		pb := pbs[0]
		for _, ch := range d.Chassis {
			if ch.UUID != *pb.Chassis {
				cp, uuid := *pb, ch.UUID
				cp.Chassis = &uuid
				d.PortBindings[pb.UUID] = &cp
				lrp = strings.TrimPrefix(pb.LogicalPort, "cr-")
				break
			}
		}
		return []string{"*"}
	})
	if lrp == "" {
		t.Fatal("scenario has no gateway port to fail over")
	}
	return lrp
}

func daemonModel(t *testing.T, url string) model {
	t.Helper()
	m := initialModel(url+"/api/v1", "", "default", false, nil, "")
	m = send(m, tea.WindowSizeMsg{Width: 160, Height: 50})
	m = pump(m, m.probeHistoryCmd())
	m = pump(m, m.fetchGraphCmd())
	if m.tree == nil {
		t.Fatalf("no tree: %v", m.err)
	}
	return expandAll(m)
}

func lrpBadge(m model, name string) string {
	for _, n := range m.tree.byID {
		if n.kind == kLRP && n.name == name {
			return n.badge
		}
	}
	return "<missing>"
}

// TestHistoryFailoverInTUI is the M8 TUI DoD on a gateway failover: 5 lists
// it, Enter diffs it, H on the router port lists it, and :at before it shows
// the old active chassis with the HISTORICAL badge.
func TestHistoryFailoverInTUI(t *testing.T) {
	ts, store := historyDaemon(t, true)
	before := daemonModel(t, ts.URL)
	beforeRev := before.graphResp.Revision
	lrp := failover(t, store)
	beforeBadge := lrpBadge(before, lrp)

	m := daemonModel(t, ts.URL)
	if !m.histAvail {
		t.Fatal("history not detected")
	}
	liveBadge := lrpBadge(m, lrp)
	if liveBadge == beforeBadge {
		t.Fatalf("failover not visible live: %q", liveBadge)
	}

	// 5: the daemon's feed.
	m = pressRun(m, "5")
	idx := slices.IndexFunc(m.rows, func(r row) bool { return r.change != nil && strings.Contains(r.change.what, "failed over") })
	if idx < 0 {
		t.Fatalf("no failover in view 5:\n%s", ansi.Strip(m.View()))
	}
	if v := ansi.Strip(m.View()); !strings.Contains(v, "daemon history") {
		t.Errorf("view 5 does not say it shows daemon history")
	}

	// Enter: the diff of that revision.
	m.cursor = idx
	m = pressRun(m, "enter")
	if m.scr != scrDiff || m.diff == nil || m.diff.loading || m.diff.err != nil {
		t.Fatalf("diff screen: scr=%v diff=%+v", m.scr, m.diff)
	}
	v := ansi.Strip(m.View())
	if !strings.Contains(v, "~ ") || !strings.Contains(v, "r"+strconv.Itoa(beforeRev)) {
		t.Errorf("diff shows no change from r%d:\n%s", beforeRev, v)
	}
	m = pressRun(m, "esc")
	if m.scr != scrMain {
		t.Fatalf("esc did not close the diff")
	}

	// H on the router port.
	m = pressRun(m, "1")
	m = expandAll(m)
	m = selectRow(t, m, kLRP, lrp)
	m = pressRun(m, "H")
	if m.mode != modeChanges || m.histName != lrp {
		t.Fatalf("H: mode %v name %q", m.mode, m.histName)
	}
	if len(m.rows) == 0 || !strings.Contains(m.rows[0].change.what, "failed over") {
		t.Errorf("H on %s: rows %v", lrp, ansi.Strip(m.View()))
	}
	if m = pressRun(m, "b"); m.mode != modeVPC {
		t.Errorf("b after H: mode %v", m.mode)
	}

	// :at before the failover.
	m = command(m, "at "+strconv.Itoa(beforeRev))
	if m.at == "" || m.graphResp == nil || m.graphResp.Revision != beforeRev {
		t.Fatalf(":at r%d: at=%q graph=%v err=%v", beforeRev, m.at, m.graphResp != nil, m.err)
	}
	if got := lrpBadge(m, lrp); got != beforeBadge {
		t.Errorf("historical badge of %s = %q, want %q", lrp, got, beforeBadge)
	}
	if h := ansi.Strip(strings.Join(m.header(), "\n")); !strings.Contains(h, "HISTORICAL r"+strconv.Itoa(beforeRev)) {
		t.Errorf("no HISTORICAL badge: %s", h)
	}
	if m = pressRun(m, "x"); !strings.Contains(m.flash, "live cluster") {
		t.Errorf("x in a historical view: flash %q", m.flash)
	}

	// Back to live: nothing logged locally for the jumps.
	m = command(m, "at now")
	if m.at != "" || lrpBadge(m, lrp) != liveBadge {
		t.Errorf(":at now: at=%q badge=%q", m.at, lrpBadge(m, lrp))
	}
	if len(m.changes) != 0 {
		t.Errorf("switching points logged %d local changes", len(m.changes))
	}
}

func TestHistoryUnavailable(t *testing.T) {
	ts, _ := historyDaemon(t, false)
	m := daemonModel(t, ts.URL)
	if m.histAvail {
		t.Fatal("history detected on a daemon without it")
	}
	m = selectRow(t, m, kLR, firstRowName(m, kLR))
	for _, c := range []func(model) model{
		func(m model) model { return pressRun(m, "H") },
		func(m model) model { return command(m, "at -1h") },
		func(m model) model { return command(m, "diff -1h") },
	} {
		got := c(m)
		if !strings.Contains(got.flash, "records no history") || got.at != "" || got.scr != scrMain {
			t.Errorf("flash %q at %q scr %v", got.flash, got.at, got.scr)
		}
	}
	// Offline fixtures say so too.
	f := loaded(t, 140, 40, false)
	if f = pressRun(f, "H"); !strings.Contains(f.flash, "offline fixture") {
		t.Errorf("fixture H: %q", f.flash)
	}
}

func firstRowName(m model, kind string) string {
	for _, r := range m.rows {
		if r.n.kind == kind {
			return r.n.name
		}
	}
	return ""
}

func TestNormalizePoint(t *testing.T) {
	now := time.Date(2026, 11, 6, 14, 30, 0, 0, time.Local)
	for in, want := range map[string]string{
		"-1h":                  now.Add(-time.Hour).UTC().Format(time.RFC3339),
		"120":                  "120",
		"r120":                 "120",
		"a1b2c3d4:120":         "a1b2c3d4:120",
		"10:15":                time.Date(2026, 11, 6, 10, 15, 0, 0, time.Local).UTC().Format(time.RFC3339),
		"2026-11-05 22:00":     time.Date(2026, 11, 5, 22, 0, 0, 0, time.Local).UTC().Format(time.RFC3339),
		"2026-11-05T22:00:00Z": "2026-11-05T22:00:00Z",
	} {
		got, label, err := normalizePoint(in, now)
		if err != nil || got != want || label == "" {
			t.Errorf("%q: got %q %q %v, want %q", in, got, label, err, want)
		}
	}
	for _, bad := range []string{"", "yesterday", "-1x", "zz:12"} {
		if _, _, err := normalizePoint(bad, now); err == nil {
			t.Errorf("%q: no error", bad)
		}
	}
}

func TestDiffCLI(t *testing.T) {
	ts, store := historyDaemon(t, true)
	from := store.Current().Revision
	failover(t, store)
	var out []string
	err := runDiffCLI(ts.URL+"/api/v1", nil, "", strconv.FormatInt(from, 10), "", "chassis", "", "", func(s string) { out = append(out, s) })
	if err != nil {
		t.Fatal(err)
	}
	text := strings.Join(out, "\n")
	if !strings.Contains(text, "chassis · ") || !strings.Contains(text, "~ ") {
		t.Errorf("diff output:\n%s", text)
	}
	if err := runDiffCLI("", nil, "dev-fixture", "-1h", "", "l3", "", "", nil); err == nil {
		t.Error("diff on a fixture must fail")
	}
}
