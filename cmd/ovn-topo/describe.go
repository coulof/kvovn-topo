package main

// Describe: everything known about one object — graph properties, Kubernetes
// identity, relations, and the daemon's /node/{id} detail (external_ids, raw columns).

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/coulof/ovn-topology/pkg/api"
)

type descState struct {
	node    *xnode
	detail  *api.NodeDetail
	err     error
	loading bool
	raw     bool
	off     int
}

func (m *model) openDescribe(n *xnode, raw bool) tea.Cmd {
	if n == nil {
		return nil
	}
	m.scr = scrDescribe
	m.desc = &descState{node: n, raw: raw}
	if n.gn != nil && n.kind != kVPC {
		m.desc.loading = true
		return m.fetchNodeDetailCmd(n.gn.Id)
	}
	return nil
}

func fmtValue(v interface{}) string {
	switch x := v.(type) {
	case nil:
		return "—"
	case string:
		if x == "" {
			return "—"
		}
		return x
	case []interface{}:
		var parts []string
		for _, e := range x {
			parts = append(parts, fmtValue(e))
		}
		if len(parts) == 0 {
			return "—"
		}
		return strings.Join(parts, ", ")
	case map[string]interface{}:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		var parts []string
		for _, k := range keys {
			parts = append(parts, k+"="+fmtValue(x[k]))
		}
		return "{" + strings.Join(parts, " ") + "}"
	case float64:
		if x == float64(int64(x)) {
			return fmt.Sprintf("%d", int64(x))
		}
		return fmt.Sprintf("%g", x)
	}
	return fmt.Sprintf("%v", v)
}

func sortedKeys[V any](mp map[string]V) []string {
	keys := make([]string, 0, len(mp))
	for k := range mp {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func (m model) describeLines() []string {
	st := m.styles
	d := m.desc
	n := d.node
	kv := func(k, v string) string { return "   " + st.Label.Render(pad(k, 20)) + v }
	section := func(s string) string { return " " + st.Value.Render(s) }

	if d.raw {
		var out []string
		var raw map[string]interface{}
		switch {
		case d.detail != nil:
			raw = d.detail.Raw
		case n.gn != nil:
			raw = props(n.gn)
		}
		if n.conn != nil && raw == nil {
			b, _ := json.Marshal(n.conn)
			_ = json.Unmarshal(b, &raw)
		}
		if raw == nil {
			return []string{"", "   " + st.MutedText.Render("no raw columns for this object")}
		}
		for _, k := range sortedKeys(raw) {
			b, _ := json.Marshal(raw[k])
			out = append(out, " "+st.CIDR.Render(k)+": "+string(b))
		}
		if d.loading {
			out = append(out, "", "   "+st.MutedText.Render("loading /node detail…"))
		}
		return out
	}

	out := []string{" " + m.glyph(n.health) + " " + m.kind(n) + " " + st.Value.Render(n.name) + "  " + m.badge(n)}
	if n.summary != "" {
		out = append(out, "   "+st.MutedText.Render(n.summary))
	}
	out = append(out, "", kv("ID", n.id))
	if c := n.conn; c != nil {
		out = append(out,
			kv("Router", c.RouterName+"  (VPC "+c.VPCName+")"),
			kv("Networks", st.CIDR.Render(strings.Join(c.Networks, ", "))),
			kv("MAC", firstNonEmpty(c.MAC, "—")),
			kv("Peer switch", firstNonEmpty(c.SwitchName, "—")),
			kv("Peer LSP", firstNonEmpty(c.PeerPortName, "—")),
			kv("Active gateway", firstNonEmpty(m.resolveNodeName(c.ActiveGateway), "— (distributed)")),
			kv("Role", portRole(c)))
	}
	if p := props(n.gn); len(p) > 0 {
		out = append(out, "", section("Properties"))
		for _, k := range sortedKeys(p) {
			out = append(out, kv(k, fmtValue(p[k])))
		}
	}
	if n.gn != nil && n.gn.K8s != nil {
		k := n.gn.K8s
		var rows []string
		for _, f := range []struct{ k, v string }{
			{"Namespace", deref(k.Namespace)}, {"Pod", deref(k.Pod)}, {"Workload", strings.TrimSpace(deref(k.WorkloadKind) + " " + deref(k.WorkloadName))},
			{"Owner", deref(k.Owner)}, {"VMI", deref(k.Vmi)}, {"Node", deref(k.Node)}, {"Subnet", deref(k.Subnet)},
			{"VPC", deref(k.Vpc)}, {"NAD", deref(k.NetworkAttachment)}, {"Vendor", deref(k.Vendor)},
		} {
			if f.v != "" {
				rows = append(rows, kv(f.k, f.v))
			}
		}
		if len(rows) > 0 {
			out = append(out, "", section("Kubernetes"))
			out = append(out, rows...)
		}
	}
	if len(n.refs) > 0 && m.tree != nil {
		out = append(out, "", section("Related"))
		for _, r := range n.refs {
			if t := m.tree.byID[r.to]; t != nil {
				out = append(out, "   "+st.MutedText.Render(pad(r.label, 20))+m.kind(t)+" "+t.name+" "+m.badge(t))
			}
		}
	}
	if len(n.children) > 0 {
		out = append(out, "", section(fmt.Sprintf("Children (%d)", len(n.children))))
		for i, c := range n.children {
			if i == 50 {
				out = append(out, "   "+st.MutedText.Render(fmt.Sprintf("… %d more (use the xray tree)", len(n.children)-50)))
				break
			}
			out = append(out, "   "+m.glyph(c.health)+" "+m.kind(c)+" "+c.name+"  "+st.MutedText.Render(c.summary))
		}
	}
	switch {
	case d.loading:
		out = append(out, "", "   "+st.MutedText.Render("loading /node detail…"))
	case d.err != nil:
		out = append(out, "", "   "+st.Bad.Render("node detail unavailable: "+d.err.Error()))
	case d.detail != nil:
		if len(d.detail.ExternalIds) > 0 {
			out = append(out, "", section("External IDs"))
			for _, k := range sortedKeys(d.detail.ExternalIds) {
				out = append(out, kv(k, d.detail.ExternalIds[k]))
			}
		}
		if d.detail.Related != nil && len(*d.detail.Related) > 0 {
			out = append(out, "", section("Related objects (daemon)"))
			for _, r := range *d.detail.Related {
				out = append(out, "   "+st.MutedText.Render(pad(r.Relationship, 20))+r.Type+" "+firstNonEmpty(deref(r.Name), r.Id))
			}
		}
	}
	return out
}

func (m model) describeView() []string {
	st := m.styles
	if m.desc == nil || m.desc.node == nil {
		return box(nil, m.w(), m.areaH(), "", st.Border)
	}
	lines := m.describeLines()
	off := min(m.desc.off, max(len(lines)-m.bodyH(), 0))
	verb := "Describe"
	if m.desc.raw {
		verb = "Raw"
	}
	title := st.Label.Render(verb) + st.CIDR.Render("("+m.desc.node.kind+")") + " " + m.desc.node.name +
		st.MutedText.Render("  esc: back · y: raw · j/k: scroll")
	return box(lines[off:], m.w(), m.areaH(), title, st.Border)
}
