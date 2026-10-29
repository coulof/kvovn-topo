package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

type screen int

const (
	scrMain screen = iota
	scrDescribe
	scrHelp
)

// A changed row is painted solid for flashSolid, then keeps a ▌ mark in its
// gutter until the model's flashFor (--flash) runs out.
const (
	flashSolid      = 3 * time.Second
	defaultFlashFor = 15 * time.Second
)

func (m model) w() int { return m.width }

// changeAge is how long ago id changed, if that is still within flashFor.
func (m model) changeAge(id string) (time.Duration, bool) {
	t, ok := m.changed[id]
	if !ok {
		return 0, false
	}
	age := time.Since(t)
	return age, age < m.flashFor
}

func (m model) headerH() int {
	if m.height < 18 {
		return 1
	}
	return 2
}

// areaH is the height of the main box (borders included).
func (m model) areaH() int {
	h := m.height - m.headerH() - 1
	if m.cmdEdit {
		h -= 3
	}
	return max(h, 3)
}

// bodyH is the number of content rows inside the main box.
func (m model) bodyH() int { return max(m.areaH()-2, 1) }

func (m model) View() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	var out []string
	out = append(out, m.header()...)
	if m.cmdEdit {
		out = append(out, m.prompt()...)
	}
	switch {
	case m.diagModal:
		modal := strings.Split(m.renderDiagModal(), "\n")
		out = append(out, block(modal, m.w(), m.areaH())...)
	case m.scr == scrHelp:
		out = append(out, m.helpView()...)
	case m.scr == scrDescribe:
		out = append(out, m.describeView()...)
	case m.graphResp == nil:
		out = append(out, m.stateView()...)
	default:
		out = append(out, m.treeView()...)
	}
	out = append(out, m.crumbs())
	return strings.Join(block(out, m.w(), m.height), "\n")
}

func (m model) liveLabel() string {
	st := m.styles
	switch {
	case m.fixturePath != "":
		return st.MutedText.Render("○ offline fixture")
	case m.livePaused:
		return st.Warn.Render("‖ paused (L)")
	case m.live == liveSSE:
		return st.Ok.Render("● live")
	case m.live == livePolling:
		return st.Warn.Render("◌ polling")
	}
	return st.MutedText.Render("… connecting")
}

func (m model) diagLabel() string {
	st := m.styles
	switch {
	case !m.diagMode:
		return st.MutedText.Render("T0")
	case m.diagAvailable:
		return st.DiagBadgeActive.Render("DIAG") + " " + st.Ok.Render("ready")
	case m.diagExecutor == nil:
		return st.DiagBadgeDisabled.Render("DIAG") + " " + st.Bad.Render(m.diagReason)
	}
	return st.DiagBadgeDisabled.Render("DIAG") + " " + st.Warn.Render(firstNonEmpty(m.diagReason, "probing…"))
}

// hotkeys are the keys hinted in the header for the current screen; ? has them all.
func (m model) hotkeys() [][2]string {
	keys := [][2]string{{"1-5", "views"}, {"/", "filter"}, {"i", "pane"}, {"d", "describe"}}
	if m.diagMode {
		keys = append(keys, [2]string{"x", "diag"}, [2]string{"!", "run"})
	}
	return append(keys, [2]string{"b", "back"}, [2]string{"?", "help"}, [2]string{"q", "quit"})
}

// header is one status line (source, revision, CNI, size, diag) and one line
// of key hints; a single compact line on short terminals.
func (m model) header() []string {
	st := m.styles
	daemon := firstNonEmpty(m.via, m.daemonURL)
	if m.fixturePath != "" {
		daemon = "fixture: " + m.fixturePath
	}
	clip := func(s string) string { // before the first WindowSizeMsg the width is unknown
		if m.w() <= 0 {
			return s
		}
		return fit(s, m.w())
	}
	rev := "rev —"
	if m.graphResp != nil {
		rev = fmt.Sprintf("rev %d", m.graphResp.Revision)
	}
	if m.height < 18 {
		return []string{clip(" " + st.Label.Render("kvovn-topo") + " " + st.MutedText.Render(daemon) + "  " + rev + " " + m.liveLabel() + "  " + st.MutedText.Render("? help"))}
	}
	sep := st.Faint.Render(" │ ")
	parts := []string{st.Pill.Render(" kvovn-topo "), rev + " " + m.liveLabel(), st.Value.Render(m.cniShort())}
	if m.tree != nil {
		s := m.tree.stats
		parts = append(parts, st.MutedText.Render(fmt.Sprintf("%d VPC %d LS %d LSP %d ch", s.vpcs, s.switches, s.ports, s.chassis)))
	}
	if c := m.problemCount(); c > 0 {
		parts = append(parts, st.Warn.Render(fmt.Sprintf("! %s (4)", plural(c, "problem"))))
	}
	parts = append(parts, m.diagLabel())
	var hints []string
	for _, k := range m.hotkeys() {
		hints = append(hints, st.Key.Render(k[0])+" "+st.MutedText.Render(k[1]))
	}
	// The state on the left; the source right-aligned, cut first when space runs out.
	status := " " + strings.Join(parts, sep)
	if room := m.w() - lipgloss.Width(status) - 3; m.w() > 0 && room >= 8 {
		src := shortSource(daemon)
		src = fit(src, min(lipgloss.Width(src), room))
		status = pad(status, m.w()-lipgloss.Width(src)-1) + st.MutedText.Render(src)
	}
	return []string{clip(status), clip(" " + strings.Join(hints, "  "))}
}

// shortSource drops the scheme and the API path from the daemon address shown.
func shortSource(s string) string {
	for _, p := range []string{"http://", "https://"} {
		s = strings.Replace(s, p, "", 1)
	}
	return strings.TrimSuffix(s, "/api/v1")
}

// viewTitle names what a view contains.
var viewTitle = map[mode]string{modeVPC: "VPCs", modeChassis: "Chassis", modeWorkload: "Workloads",
	modeProblems: "Problems", modeChanges: "Changes"}

func (m model) crumbs() string {
	st := m.styles
	pill := func(s string, style lipgloss.Style) string { return style.Render(" " + s + " ") }
	c := pill(firstNonEmpty(viewTitle[m.mode], string(m.mode)), st.Pill)
	switch {
	case m.diagModal:
		c += " " + pill("diagnostics", st.DiagBadgeActive)
	case m.scr == scrDescribe:
		c += " " + pill("describe", st.PillAlt)
	case m.scr == scrHelp:
		c += " " + pill("help", st.PillAlt)
	}
	if n := m.selectedXNode(); n != nil && m.scr == scrMain && !m.diagModal {
		c += " " + st.MutedText.Render(n.id)
	}
	if m.flash != "" && time.Since(m.flashAt) < 6*time.Second {
		fs := st.CIDR
		if m.flashErr {
			fs = st.Bad
		}
		room := m.w() - lipgloss.Width(c) - 2
		if room > 10 {
			c = pad(c, m.w()-min(lipgloss.Width(m.flash), room)-1) + fit(fs.Render(m.flash), min(lipgloss.Width(m.flash), room))
		}
	}
	return c
}

func (m model) prompt() []string {
	st := m.styles
	sug := suggest(m.cmd)
	ghost := ""
	if sug != "" {
		ghost = st.Faint.Render(strings.TrimPrefix(sug, m.cmd))
	}
	body := []string{" " + st.CIDR.Render("▶ ") + m.cmd + ghost + lipgloss.NewStyle().Reverse(true).Render(" ") +
		st.MutedText.Render("   tab: complete · enter: run · esc: cancel")}
	return box(body, m.w(), 3, "", st.Border)
}

func (m model) filterTitle() string {
	if m.filter == "" && !m.filterEdit {
		return ""
	}
	cur := ""
	if m.filterEdit {
		cur = "▏"
	}
	return " " + m.styles.Warn.Render("</"+m.filter+cur+">")
}

func (m model) glyph(h health) string {
	switch h {
	case hOK:
		return m.styles.Ok.Render("●")
	case hWarn:
		return m.styles.Warn.Render("!")
	case hBad:
		return m.styles.Bad.Render("✗")
	}
	return m.styles.Faint.Render("·")
}

var kindTag = map[string]string{
	kVPC: "VPC", kLR: "LR", kLRP: "LRP", kLS: "LS", kLSP: "LSP", kChassis: "CH", kExt: "EXT", kNS: "NS",
}

func (m model) kind(n *xnode) string {
	tag := kindTag[n.kind]
	if n.kind == kWL {
		tag = map[string]string{"VirtualMachine": "VM", "Pod": "POD", "Node": "NODE"}[n.wlKind]
		if tag == "" {
			tag = strings.ToUpper(n.wlKind)
		}
	}
	return m.styles.Kind[n.kind].Bold(true).Render(pad(tag, 4))
}

func (m model) badge(n *xnode) string {
	if n.badge == "" {
		return ""
	}
	st := m.styles.Ok
	switch {
	case strings.HasPrefix(n.badge, "GW"), n.badge == "gateway":
		st = m.styles.ActiveGateway
	case strings.HasPrefix(n.badge, "shared"), n.badge == "default":
		st = m.styles.CIDR
	case n.health == hBad:
		st = m.styles.Bad
	case n.health == hWarn:
		st = m.styles.Warn
	}
	return st.Render("[" + n.badge + "]")
}

func (m model) treeView() []string {
	st := m.styles
	tw := m.treeW()
	inner := tw - 2
	h := m.bodyH()
	off := window(m.cursor, m.offset, len(m.rows), h)
	var body []string
	for i := off; i < min(off+h, len(m.rows)); i++ {
		r := m.rows[i]
		var line string
		if r.problem != "" {
			line = m.problemLine(r)
		} else if r.change != nil {
			line = m.changeLine(r)
		} else if r.ref {
			line = " " + st.Faint.Render(r.prefix) + st.MutedText.Render(r.relLabel+" ⇢ ") + m.kind(r.n) + " " +
				st.Kind[r.n.kind].Render(r.n.name) + " " + m.badge(r.n)
		} else {
			fold := "  "
			if r.hasKids {
				fold = st.MutedText.Render("▸ ")
				if m.isExpanded(r) || m.filter != "" && len(r.n.children) > 0 {
					fold = st.MutedText.Render("▾ ")
				}
			}
			name := st.Value.Render(r.n.name)
			if r.n.kind == kGroup {
				name = st.Value.Render(strings.ToUpper(r.n.name)) + st.MutedText.Render(fmt.Sprintf(" (%d)", len(r.n.children)))
			}
			g := m.glyph(r.n.health) + " "
			if r.n.kind == kGroup {
				g = ""
			}
			summary := r.n.summary
			if m.showPane() && (r.n.kind == kLSP || r.n.kind == kLRP) {
				summary = "" // the detail pane carries it; keep the tree calm
			}
			line = " " + st.Faint.Render(r.prefix) + fold + g + m.kind(r.n) + " " + name + "  " +
				st.MutedText.Render(summary) + " " + m.badge(r.n)
		}
		// A recent change keeps a gutter mark, also under the cursor.
		age, changed := m.changeAge(r.n.id)
		mark, w := changed && (i == m.cursor || age >= flashSolid), inner
		if mark {
			line, w = strings.TrimPrefix(line, " "), inner-1
		}
		switch {
		case i == m.cursor:
			line = solid(line, w, st.Selected)
		case changed && age < flashSolid:
			line = solid(line, w, st.Flash)
		}
		if mark {
			line = st.ChangeMark.Render("▌") + line
		}
		body = append(body, line)
	}
	if len(m.rows) == 0 {
		msg := "   nothing to show in this view yet"
		if m.filter != "" {
			msg = "   nothing matches “" + m.filter + "”  (esc clears the filter)"
		} else if m.mode == modeChassis {
			msg = "   no chassis reported by the daemon (view=chassis is empty)"
		} else if m.mode == modeWorkload {
			msg = "   no Kubernetes workloads resolved from LSP external_ids"
		} else if m.mode == modeProblems {
			msg = "   no problems found"
		} else if m.mode == modeChanges {
			msg = m.noChangesText()
		}
		body = append(body, "", st.MutedText.Render(msg))
	}
	title := st.Label.Render(firstNonEmpty(viewTitle[m.mode], string(m.mode))) + st.MutedText.Render(fmt.Sprintf(" [%d]", len(m.rows))) + m.filterTitle()
	tree := box(body, tw, m.areaH(), title, st.Border)
	if !m.showPane() {
		return tree
	}
	pane := m.detailPane(m.areaH())
	for i := range tree {
		tree[i] += pane[i]
	}
	return tree
}

// stateView covers loading and "daemon unreachable" before the first graph arrives.
func (m model) stateView() []string {
	st := m.styles
	var body []string
	title := st.Label.Render("Loading")
	if m.err != nil {
		title = st.Bad.Render("Daemon unreachable")
		body = []string{""}
		for i, l := range strings.Split(m.err.Error(), "\n") {
			if i == 0 {
				body = append(body, "  "+st.Bad.Render("✗ ")+l)
			} else {
				body = append(body, "    "+st.Warn.Render(l))
			}
		}
		body = append(body, "")
		if m.httpClient != nil { // service proxy
			body = append(body,
				"  Reached through the Kubernetes "+st.CIDR.Render(m.via)+".",
				"  Check that the daemon pod is ready and that your kubeconfig user has",
				"  "+st.CIDR.Render("get services/proxy")+" on it (chart value rbac.viewer.create),")
		} else {
			body = append(body,
				"  Check that kvovn-topod is running and reachable at "+st.CIDR.Render(m.daemonURL)+",",
				"  or drop --daemon-url to go through the Kubernetes service proxy,")
		}
		body = append(body,
			"  or explore the bundled dataset offline with "+st.CIDR.Render("kvovn-topo --fixture")+".", "",
			"  "+st.Key.Render("<r>")+" retry   "+st.Key.Render("<q>")+" quit",
		)
	} else {
		body = []string{"", "  Loading topology from " + st.CIDR.Render(firstNonEmpty(m.fixturePath, m.via, m.daemonURL)) + " …"}
	}
	return box(body, m.w(), m.areaH(), title, st.Border)
}

func (m model) helpKeyLines() []string {
	st := m.styles
	b := st.Value.Render
	left := []string{
		" " + b("XRAY ROOTS"),
		"   1  :xray vpc       VPC → LR → LRP · LS → LSP",
		"   2  :xray chassis   chassis → bound ports",
		"   3  :xray workload  namespace → VM/Pod → NICs",
		"   4  :problems       what is wrong, most severe first",
		"   5  :changes        what changed since start, newest first",
		"",
		" " + b("INSPECT"),
		"   i  details pane",
		"   d  describe (/node detail, external_ids)",
		"   y  raw columns",
		"   x  Tier 1 diagnostics (--diag)",
	}
	right := []string{
		" " + b("NAVIGATION"),
		"   j/k ↑/↓   move            g/G    top/bottom",
		"   h/l ←/→   fold / unfold   ^d/^u  half page",
		"   space     toggle          e/E    expand/fold all",
		"   enter     toggle · on a ⇢ relation: jump there",
		"   b         back after a jump",
		" " + b("FIND"),
		"   /text     filter (keeps ancestors) · esc clears",
		"   r refresh · L live on/off · t theme · q quit",
	}
	var l []string
	if m.w() >= 110 {
		for i := range max(len(left), len(right)) {
			a, c := "", ""
			if i < len(left) {
				a = left[i]
			}
			if i < len(right) {
				c = right[i]
			}
			l = append(l, pad(a, 52)+c)
		}
	} else {
		l = append(append(left, ""), right...)
	}
	l = append(l, "",
		" "+b("LEGEND")+"  "+st.Ok.Render("●")+" ok  "+st.Warn.Render("!")+" degraded  "+st.Bad.Render("✗")+" unbound  "+
			st.Faint.Render("·")+" infra port  ├╌ ⇢ related object (graph edge)",
		"   !  run an Inspect command (--diag)",
	)
	return l
}

var commands = []string{"xray vpc", "xray chassis", "xray workload", "vpc", "chassis", "workload", "problems", "changes", "theme", "quit"}

func suggest(prefix string) string {
	if prefix == "" {
		return ""
	}
	for _, c := range commands {
		if strings.HasPrefix(c, prefix) && c != prefix {
			return c
		}
	}
	return ""
}
