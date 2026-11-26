package main

// Topology history (M8, ADR-0002): with a daemon that records history, the
// Changes view (`5`) reads its change feed, `H` shows one object's history,
// `:diff` compares two points and `:at` / --at browses the past.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/coulof/kvovn-topo/pkg/api"
	"github.com/coulof/kvovn-topo/pkg/graph"
)

// histEventsLimit is how many feed events view 5 loads.
const histEventsLimit = 500

type histCoverageMsg struct {
	cov *api.HistoryCoverage
	err error
}

type histEventsMsg struct {
	object, name string
	events       []api.HistoryEvent
	err          error
}

type diffLoadedMsg struct {
	title string
	diffs []viewDiff
	err   error
}

type viewDiff struct {
	label string
	d     api.GraphDiff
}

// diffState is the :diff screen.
type diffState struct {
	title   string
	diffs   []viewDiff
	loading bool
	err     error
	off     int
}

// probeHistoryCmd asks the daemon whether it records history.
func (m model) probeHistoryCmd() tea.Cmd {
	daemon, client := m.daemonURL, m.httpClient
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var cov api.HistoryCoverage
		if err := httpGetJSON(ctx, client, apiURL(daemon, "/history/coverage", nil), "", &cov); err != nil {
			return histCoverageMsg{err: err}
		}
		return histCoverageMsg{cov: &cov}
	}
}

// historyOff explains why history features are unavailable ("" if they are).
func (m model) historyOff() string {
	switch {
	case m.fixturePath != "":
		return "history needs the daemon: an offline fixture has none"
	case !m.histAvail:
		return "the daemon records no history (history.mode=off, or a daemon older than M8)"
	}
	return ""
}

// fetchHistEventsCmd loads view 5 from the daemon's feed: every object, or
// the one H picked (by graph ID, else by name).
func (m model) fetchHistEventsCmd() tea.Cmd {
	daemon, client, object, name := m.daemonURL, m.httpClient, m.histObject, m.histName
	return func() tea.Msg {
		q := url.Values{"limit": {strconv.Itoa(histEventsLimit)}}
		if object != "" {
			q.Set("object", object)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		var list api.HistoryEventList
		if err := httpGetJSON(ctx, client, apiURL(daemon, "/history/events", q), "", &list); err != nil {
			return histEventsMsg{object: object, name: name, err: err}
		}
		evs := list.Events
		if object == "" && name != "" { // an object the graph has no ID for: match by name
			var kept []api.HistoryEvent
			for _, e := range evs {
				if deref(e.Name) == name {
					kept = append(kept, e)
				}
			}
			evs = kept
		}
		return histEventsMsg{object: object, name: name, events: evs}
	}
}

// histChangesFrom turns feed events (newest first) into Changes rows.
func histChangesFrom(evs []api.HistoryEvent) []change {
	out := make([]change, 0, len(evs))
	for _, e := range evs {
		id := deref(e.Object)
		kind, _, _ := strings.Cut(id, ":")
		c := change{
			seq: int(e.Seq), at: e.Time.Local(), rev: int(e.Revision), id: id, kind: kind,
			name: firstNonEmpty(deref(e.Name), e.Kind), what: e.Summary, hist: true,
			removed: strings.HasSuffix(e.Kind, ".removed"), afterHlth: hOK,
		}
		switch {
		case strings.HasPrefix(e.Kind, "gap."), e.Coalesced != nil && *e.Coalesced:
			c.afterHlth = hWarn
		case e.Kind == "port.unbound", e.Kind == "gateway.lost":
			c.afterHlth = hBad
		}
		out = append(out, c)
	}
	return out
}

// histNode is the tree node an event is about, matching router ports by name
// (the tree keys them by router and port name, the feed by row UUID).
func (m model) histNode(c *change) *xnode {
	if n := m.tree.byID[c.id]; n != nil {
		return n
	}
	if c.kind == kLRP {
		for _, n := range m.tree.byID {
			if n.kind == kLRP && n.name == c.name {
				return n
			}
		}
	}
	return nil
}

// objectHistory is H: view 5 narrowed to the selected object.
func (m model) objectHistory() (tea.Model, tea.Cmd) {
	if why := m.historyOff(); why != "" {
		m.setFlash(why, true)
		return m, nil
	}
	n := m.selectedXNode()
	if n == nil || n.kind == kGroup {
		m.setFlash("select an object first", false)
		return m, nil
	}
	m.history = append(m.history, histEntry{m.mode, m.selKey()})
	m.mode, m.filter, m.scr = modeChanges, "", scrMain
	m.histObject, m.histName = "", n.name
	switch {
	case n.gn != nil && strings.Contains(n.gn.Id, ":"):
		m.histObject = n.gn.Id
	case n.kind != kLRP && strings.Contains(n.id, ":"):
		m.histObject = n.id
	}
	m.histChanges, m.cursor, m.offset = nil, 0, 0
	m.rebuildRows()
	return m, m.fetchHistEventsCmd()
}

// openRevisionDiff is Enter on a feed event: what that revision changed.
func (m model) openRevisionDiff(c *change) (tea.Model, tea.Cmd) {
	from, to := strconv.Itoa(c.rev-1), strconv.Itoa(c.rev)
	views := []diffReq{{"l3", "", "VPCs (l3)"}, {"chassis", "", "chassis"}}
	if n := m.histNode(c); n != nil && n.kind == kLSP {
		for _, r := range n.refs {
			if r.label == "subnet" {
				views = append(views, diffReq{"l2", r.to, "ports of " + m.nameOf(r.to)})
			}
		}
	}
	return m.openDiff(from, to, fmt.Sprintf("r%d → r%d · %s", c.rev-1, c.rev, c.what), views)
}

func (m model) nameOf(id string) string {
	if n := m.tree.byID[id]; n != nil {
		return n.name
	}
	return id
}

type diffReq struct{ view, focus, label string }

// openDiff shows the diff screen and loads the diffs of views from → to.
func (m model) openDiff(from, to, title string, views []diffReq) (tea.Model, tea.Cmd) {
	if why := m.historyOff(); why != "" {
		m.setFlash(why, true)
		return m, nil
	}
	m.scr, m.diff = scrDiff, &diffState{title: title, loading: true}
	daemon, client := m.daemonURL, m.httpClient
	return m, func() tea.Msg {
		diffs, err := fetchDiffs(daemon, client, from, to, views)
		return diffLoadedMsg{title: title, diffs: diffs, err: err}
	}
}

func fetchDiffs(daemon string, client *http.Client, from, to string, views []diffReq) ([]viewDiff, error) {
	var out []viewDiff
	for _, v := range views {
		q := url.Values{"from": {from}, "view": {v.view}}
		if to != "" {
			q.Set("to", to)
		}
		if v.focus != "" {
			q.Set("focus", v.focus)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		var d api.GraphDiff
		err := httpGetJSON(ctx, client, apiURL(daemon, "/diff", q), "", &d)
		cancel()
		if err != nil {
			return nil, err
		}
		out = append(out, viewDiff{label: v.label, d: d})
	}
	return out, nil
}

// runDiffCmd is `:diff <from> [<to>]`.
func (m model) runDiffCmd(args string) (tea.Model, tea.Cmd) {
	f := strings.Fields(args)
	if len(f) == 0 || len(f) > 2 {
		m.setFlash("usage: :diff <from> [<to>]  — a time (15:04, 2026-11-06 15:04, RFC3339), -1h, or a revision", true)
		return m, nil
	}
	now := time.Now()
	from, _, err := normalizePoint(f[0], now)
	if err != nil {
		m.setFlash(err.Error(), true)
		return m, nil
	}
	to, toLabel := "", "now"
	if len(f) == 2 {
		if to, toLabel, err = normalizePoint(f[1], now); err != nil {
			m.setFlash(err.Error(), true)
			return m, nil
		}
	}
	return m.openDiff(from, to, f[0]+" → "+toLabel, []diffReq{{"l3", "", "VPCs (l3)"}, {"chassis", "", "chassis"}})
}

// normalizePoint turns user input into a stable `at` value and a label. A
// relative or local time becomes an absolute RFC3339 time, so a historical
// view does not drift as the clock moves.
func normalizePoint(s string, now time.Time) (point, label string, err error) {
	s = strings.TrimSpace(s)
	switch {
	case s == "":
		return "", "", errors.New("empty history point")
	case strings.HasPrefix(s, "-"):
		d, err := time.ParseDuration(s)
		if err != nil {
			return "", "", fmt.Errorf("bad duration %q", s)
		}
		t := now.Add(d)
		return t.UTC().Format(time.RFC3339), t.Local().Format("2006-01-02 15:04:05"), nil
	case isDigits(strings.TrimPrefix(s, "r")):
		r := strings.TrimPrefix(s, "r")
		return r, "r" + r, nil
	}
	if epoch, rev, ok := strings.Cut(s, ":"); ok && len(epoch) == 8 && isHex(epoch) && isDigits(rev) {
		return s, "r" + rev, nil
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t.UTC().Format(time.RFC3339), t.Local().Format("2006-01-02 15:04:05"), nil
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02 15:04", "2006-01-02T15:04"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			return t.UTC().Format(time.RFC3339), t.Format("2006-01-02 15:04:05"), nil
		}
	}
	for _, layout := range []string{"15:04:05", "15:04"} {
		if t, err := time.ParseInLocation(layout, s, time.Local); err == nil {
			y, mo, d := now.Date()
			t = time.Date(y, mo, d, t.Hour(), t.Minute(), t.Second(), 0, time.Local)
			return t.UTC().Format(time.RFC3339), t.Format("2006-01-02 15:04:05"), nil
		}
	}
	return "", "", fmt.Errorf("unknown point %q: use 15:04, 2026-11-06 15:04, RFC3339, -1h or a revision", s)
}

func isDigits(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return s != ""
}

func isHex(s string) bool {
	for _, c := range s {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return s != ""
}

// setAt is `:at <point>`: browse the topology as it was ("now" returns to
// live). Everything is refetched; nothing is logged as a change.
func (m model) setAt(arg string) (tea.Model, tea.Cmd) {
	arg = strings.TrimSpace(arg)
	switch arg {
	case "":
		if m.at == "" {
			m.setFlash("usage: :at <15:04 | 2026-11-06 15:04 | RFC3339 | -1h | revision>   ·   :at now returns to live", false)
		} else {
			m.setFlash("historical view at "+m.atLabel+"   ·   :at now returns to live", false)
		}
		return m, nil
	case "now", "live":
		if m.at == "" {
			return m, nil
		}
		m.at, m.atLabel = "", ""
		m.resetGraph()
		m.setFlash("back to the live topology", false)
		return m, m.fetchGraphCmd()
	}
	if why := m.historyOff(); why != "" {
		m.setFlash(why, true)
		return m, nil
	}
	at, label, err := normalizePoint(arg, time.Now())
	if err != nil {
		m.setFlash(err.Error(), true)
		return m, nil
	}
	m.at, m.atLabel = at, label
	m.resetGraph()
	m.setFlash("HISTORICAL view at "+label+": Kubernetes names are current and may differ from then · :at now returns to live", false)
	return m, m.fetchGraphCmd()
}

// resetGraph drops everything loaded, so the next graph is a first load: no
// flashes and no Changes entries between live and historical states.
func (m *model) resetGraph() {
	m.graphResp, m.tree, m.err = nil, nil, nil
	m.chassisResp, m.chassisNodes = nil, nil
	m.switchWorkloads = make(map[string][]api.GraphNode)
	m.nodeDetails = make(map[string]*api.NodeDetail)
	m.nodeErrs = make(map[string]error)
	m.nodeLoading = make(map[string]bool)
	m.changed = make(map[string]time.Time)
	m.pendingIDs = nil
	m.detailsRev = -1
	m.rows = m.rows[:0]
}

// historicalLabel is the status badge of a historical view.
func (m model) historicalLabel() string {
	return m.styles.Warn.Render("⏸ HISTORICAL " + m.atLabel)
}

// --- diff screen

type diffLine struct {
	mark   byte // '+', '-', '~', or 0 for headings and field lines
	indent bool
	text   string
}

// diffText renders diffs as lines; the screen styles them, the CLI prints them.
func diffText(diffs []viewDiff) []diffLine {
	var out []diffLine
	for i, vd := range diffs {
		d := vd.d
		if i == 0 {
			out = append(out, diffLine{text: fmt.Sprintf("r%d (%s) → r%d (%s)", d.From.Revision, d.From.Time.Local().Format("2006-01-02 15:04:05"), d.To.Revision, d.To.Time.Local().Format("2006-01-02 15:04:05"))}, diffLine{})
		}
		n := len(d.AddedNodes) + len(d.RemovedNodes) + len(d.ChangedNodes) + len(d.AddedEdges) + len(d.RemovedEdges) + len(d.ChangedEdges)
		head := vd.label
		if n == 0 {
			head += " · nothing changed"
		} else {
			head += fmt.Sprintf(" · %d added · %d removed · %d changed", len(d.AddedNodes)+len(d.AddedEdges), len(d.RemovedNodes)+len(d.RemovedEdges), len(d.ChangedNodes)+len(d.ChangedEdges))
		}
		out = append(out, diffLine{text: head})
		for _, x := range d.AddedNodes {
			out = append(out, diffLine{mark: '+', text: nodeText(string(x.Type), x.Name, x.Id)})
		}
		for _, x := range d.RemovedNodes {
			out = append(out, diffLine{mark: '-', text: nodeText(string(x.Type), x.Name, x.Id)})
		}
		for _, x := range d.ChangedNodes {
			out = append(out, diffLine{mark: '~', text: nodeText(x.Type, x.Name, x.Id)})
			out = append(out, fieldLines(x.Fields)...)
		}
		for _, x := range d.AddedEdges {
			out = append(out, diffLine{mark: '+', text: edgeText(string(x.Type), x.Source, x.Target, deref(x.Label))})
		}
		for _, x := range d.RemovedEdges {
			out = append(out, diffLine{mark: '-', text: edgeText(string(x.Type), x.Source, x.Target, deref(x.Label))})
		}
		for _, x := range d.ChangedEdges {
			out = append(out, diffLine{mark: '~', text: edgeText(x.Type, x.Source, x.Target, "")})
			out = append(out, fieldLines(x.Fields)...)
		}
		out = append(out, diffLine{})
	}
	return out
}

func nodeText(typ, name, id string) string {
	return fmt.Sprintf("%-8s %s  %s", typ, name, id)
}

func edgeText(typ, src, dst, label string) string {
	s := fmt.Sprintf("edge %-12s %s → %s", typ, src, dst)
	if label != "" {
		s += "  " + label
	}
	return s
}

func fieldLines(fs []api.FieldChange) []diffLine {
	var out []diffLine
	for _, f := range fs {
		from, to := "—", "—"
		if f.From != nil {
			from = *f.From
		}
		if f.To != nil {
			to = *f.To
		}
		out = append(out, diffLine{indent: true, text: f.Field + ": " + from + " → " + to})
	}
	return out
}

func (m model) diffLines() []string {
	st := m.styles
	d := m.diff
	switch {
	case d == nil:
		return nil
	case d.loading:
		return []string{"", st.MutedText.Render("   loading diff…")}
	case d.err != nil:
		return []string{"", st.Bad.Render("   " + d.err.Error())}
	}
	var out []string
	for _, l := range diffText(d.diffs) {
		switch {
		case l.indent:
			out = append(out, "       "+st.MutedText.Render(l.text))
		case l.mark == '+':
			out = append(out, " "+st.Ok.Render("+ "+l.text))
		case l.mark == '-':
			out = append(out, " "+st.Bad.Render("- "+l.text))
		case l.mark == '~':
			out = append(out, " "+st.Warn.Render("~ "+l.text))
		case l.text != "":
			out = append(out, " "+st.Label.Render(l.text))
		default:
			out = append(out, "")
		}
	}
	return out
}

func (m model) diffView() []string {
	st := m.styles
	lines := m.diffLines()
	h := m.bodyH()
	off := min(m.diff.off, max(len(lines)-h, 0))
	body := lines[off:min(off+h, len(lines))]
	title := st.Label.Render("Diff") + st.MutedText.Render(" "+m.diff.title)
	return box(body, m.w(), m.areaH(), title, st.Border)
}

func (m model) updateDiff(s string) (tea.Model, tea.Cmd) {
	switch s {
	case "esc", "q", "b", "backspace":
		m.scr, m.diff = scrMain, nil
		return m, nil
	case "j", "down":
		m.diff.off++
	case "k", "up":
		m.diff.off = max(m.diff.off-1, 0)
	case "ctrl+d", "pgdown":
		m.diff.off += m.bodyH() / 2
	case "ctrl+u", "pgup":
		m.diff.off = max(m.diff.off-m.bodyH()/2, 0)
	case "g":
		m.diff.off = 0
	case "G":
		m.diff.off = 1 << 20
	}
	m.diff.off = min(m.diff.off, max(len(m.diffLines())-m.bodyH(), 0))
	return m, nil
}

// --- kvovn-topo diff (CLI)

// runDiffCLI prints the diff of view between two points: text, or json/yaml.
func runDiffCLI(daemonURL string, client *http.Client, fixturePath, from, to, view, focus, format string, w func(string)) error {
	if fixturePath != "" {
		return errors.New("diff needs the daemon's history; an offline fixture has none")
	}
	if from == "" {
		return errors.New("diff needs --from (a time, -1h, or a revision)")
	}
	now := time.Now()
	f, _, err := normalizePoint(from, now)
	if err != nil {
		return err
	}
	t := ""
	if to != "" {
		if t, _, err = normalizePoint(to, now); err != nil {
			return err
		}
	}
	if view == "l2" && !strings.HasPrefix(focus, graph.PrefixLS) {
		return errors.New("view l2 needs --focus ls:<uuid>")
	}
	diffs, err := fetchDiffs(daemonURL, client, f, t, []diffReq{{view, focus, view}})
	if err != nil {
		return err
	}
	if format != "" {
		return writeMachine(diffs[0].d, format)
	}
	for _, l := range diffText(diffs) {
		switch {
		case l.indent:
			w("      " + l.text)
		case l.mark != 0:
			w(string(l.mark) + " " + l.text)
		default:
			w(l.text)
		}
	}
	return nil
}
