package main

// The Problems view (`4`): everything the tree already knows is wrong, in one
// list, most severe first; Enter jumps to the object. Computed client-side
// from the tree and the routers' /node details, an interim for M4's /checks.

import (
	"fmt"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/coulof/ovn-topology/pkg/api"
)

const modeProblems mode = "problems"

type problem struct {
	sev    health
	n      *xnode
	reason string
}

func (m model) problems() []problem {
	if m.tree == nil {
		return nil
	}
	var out []problem
	add := func(sev health, n *xnode, reason string) { out = append(out, problem{sev, n, reason}) }
	for _, n := range m.tree.byID {
		p := props(n.gn)
		switch n.kind {
		case kLSP:
			switch n.badge {
			case "unbound":
				add(hBad, n, "unbound: no chassis has claimed this port")
			case "disabled":
				add(hWarn, n, "port disabled (enabled=false)")
			}
		case kChassis:
			switch pstr(p, "nodeMatch") {
			case "hostname":
				add(hWarn, n, "matched to a node by hostname only, not by ovn.kubernetes.io/chassis")
			case "fallback-hostname", "fallback-name":
				add(hWarn, n, "no Kubernetes node matches this chassis")
			}
			if c := pint(p, "hostnameShared"); c > 1 {
				add(hWarn, n, fmt.Sprintf("%d chassis share hostname %s (stale chassis after a reinstall?)", c, pstr(p, "hostname")))
			}
		case kLR:
			gws, _ := p["gateways"].([]interface{})
			for _, x := range gws {
				gw, _ := x.(map[string]interface{})
				if pstr(gw, "activeChassis") == "" {
					target := n
					if lrp := m.tree.byID["lrp:"+n.id+"/"+pstr(gw, "lrp")]; lrp != nil {
						target = lrp
					}
					add(hBad, target, "gateway port with no active chassis: the VPC has no egress")
				}
			}
			out = append(out, m.natReturnProblems(n)...)
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].sev != out[j].sev {
			return out[i].sev > out[j].sev
		}
		if out[i].n.kind != out[j].n.kind {
			return out[i].n.kind < out[j].n.kind
		}
		return out[i].n.name < out[j].n.name
	})
	return out
}

// natReturnProblems is the ADR-0004 asymmetric-return heuristic: a router
// with a NAT gateway on its subnet, a default route leaving elsewhere, and no
// src-ip route or reroute policy sending anything to the gateway. Replies to
// flows the gateway DNATs would then bypass it. It needs the router's /node.
func (m model) natReturnProblems(lr *xnode) []problem {
	gws := m.natGatewaysNear(lr)
	if len(gws) == 0 {
		return nil
	}
	d := m.nodeDetails[lr.id]
	if d == nil {
		if m.nodeErrs[lr.id] != nil {
			return []problem{{hWarn, lr, "NAT gateway return path not checked: " + m.nodeErrs[lr.id].Error()}}
		}
		return []problem{{hNone, lr, "checking the NAT gateway return path…"}}
	}
	var out []problem
	for _, gw := range gws {
		lan := ""
		for _, p := range gw.ports {
			if !hasLocalnet(m.switchOf(p)) {
				lan, _ = lspAddress(p.gn)
				break
			}
		}
		if lan == "" {
			continue
		}
		pinned, defaultElsewhere := false, ""
		if d.StaticRoutes != nil {
			for _, r := range *d.StaticRoutes {
				hop := extractCleanIP(r.Nexthop)
				switch {
				case r.Policy != nil && *r.Policy == api.SrcIp && hop == lan:
					pinned = true
				case r.IpPrefix == "0.0.0.0/0" && hop != lan:
					defaultElsewhere = hop
				case r.IpPrefix == "0.0.0.0/0" && hop == lan:
					pinned = true // all egress goes through the gateway
				}
			}
		}
		if d.Policies != nil {
			for _, p := range *d.Policies {
				if p.Action == api.Reroute && p.Nexthops != nil {
					for _, nh := range *p.Nexthops {
						if extractCleanIP(nh) == lan {
							pinned = true
						}
					}
				}
			}
		}
		if !pinned && defaultElsewhere != "" {
			out = append(out, problem{hWarn, lr, fmt.Sprintf(
				"replies may bypass NAT gateway %s: 0.0.0.0/0 goes to %s and no src-ip route or reroute leads to %s (heuristic)",
				gw.name, defaultElsewhere, lan)})
		}
	}
	return out
}

// problemsFetchCmd fetches the /node detail of every router with a NAT
// gateway, so the return-path check can run.
func (m *model) problemsFetchCmd() tea.Cmd {
	if m.tree == nil {
		return nil
	}
	var cmds []tea.Cmd
	for _, n := range m.tree.byID {
		if n.kind == kLR && len(m.natGatewaysNear(n)) > 0 &&
			m.nodeDetails[n.id] == nil && !m.nodeLoading[n.id] && m.nodeErrs[n.id] == nil {
			m.nodeLoading[n.id] = true
			cmds = append(cmds, m.fetchNodeDetailCmd(n.id))
		}
	}
	return tea.Batch(cmds...)
}

// problemRows lists the problems as rows; Enter on one jumps to its object.
func (m *model) problemRows() {
	for i, p := range m.problems() {
		if m.filter != "" && !containsFold(p.n.name+" "+p.reason, m.filter) {
			continue
		}
		m.rows = append(m.rows, row{n: p.n, key: fmt.Sprintf("%s/%d/%s", modeProblems, i, p.n.id), ref: true,
			problem: p.reason, sev: p.sev})
	}
}

// problemCount is the number of real problems (not pending checks).
func (m model) problemCount() int {
	c := 0
	for _, p := range m.problems() {
		if p.sev != hNone {
			c++
		}
	}
	return c
}

// problemLine renders one Problems row.
func (m model) problemLine(r row) string {
	st := m.styles
	reason := st.MutedText.Render(r.problem)
	if strings.HasPrefix(r.problem, "replies may bypass") || r.sev == hWarn {
		reason = st.Warn.Render(r.problem)
	}
	if r.sev == hBad {
		reason = st.Bad.Render(r.problem)
	}
	return " " + m.glyph(r.sev) + " " + m.kind(r.n) + " " + st.Kind[r.n.kind].Render(r.n.name) + "  " + reason
}
