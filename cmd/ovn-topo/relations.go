package main

// Troubleshooting relations, built TUI-side until M4's IP resolver: who owns
// an IP, where a router's routes and policies lead, which port a NAT rule
// serves, and which router a NAT gateway serves. Route, policy and NAT links
// come from the router's /node detail, so /graph stays aggregated.

import (
	"fmt"
	"strings"

	"github.com/coulof/ovn-topology/pkg/api"
	"github.com/coulof/ovn-topology/pkg/graph"
)

// relations is everything n links to: its graph refs, then the links read
// from its /node detail (routes, policies, NAT) once fetched.
func (n *xnode) relations() []xref {
	if len(n.detailRefs) == 0 {
		return n.refs
	}
	return append(append([]xref{}, n.refs...), n.detailRefs...)
}

// indexIPs maps every IP the tree knows to its owner: switch-port addresses
// and router-port networks.
func indexIPs(t *xtree) {
	t.byIP = map[string]*xnode{}
	for _, n := range t.byID {
		switch n.kind {
		case kLSP:
			addrs, _ := props(n.gn)["addresses"].([]interface{})
			for _, a := range addrs {
				s, _ := a.(string)
				for _, f := range strings.Fields(s) {
					if ip := extractCleanIP(f); ip != "" && t.byIP[ip] == nil {
						t.byIP[ip] = n
					}
				}
			}
		case kLRP:
			if n.conn != nil {
				for _, cidr := range n.conn.Networks {
					if ip := extractCleanIP(cidr); ip != "" {
						t.byIP[ip] = n // a router port owns its address over any port claiming it
					}
				}
			}
		}
	}
}

// ownerLabel names an IP's owner the way the pane does.
func (m model) ownerLabel(n *xnode) string {
	switch n.kind {
	case kLRP:
		if n.conn != nil {
			return n.name + " " + n.conn.RouterName
		}
	case kLSP:
		if gw := m.natGatewayOf(n); gw != nil {
			return "NAT gateway " + gw.name
		}
		if strings.HasPrefix(n.name, "node-") {
			return "node " + strings.TrimPrefix(n.name, "node-")
		}
		if kind, name, _, ok := workloadOf(n.gn); ok {
			switch kind {
			case "VirtualMachine":
				return "vm " + name
			case "Node":
				return "node " + name
			}
			return "pod " + firstNonEmpty(deref(n.gn.K8s.Pod), name)
		}
	}
	return n.name
}

// ipOwner is the object that owns ip, if the tree knows it.
func (m model) ipOwner(ip string) *xnode {
	if m.tree == nil || m.tree.byIP == nil {
		return nil
	}
	return m.tree.byIP[extractCleanIP(ip)]
}

// formatNexthop annotates an IP with its owner when the tree knows it.
func (m model) formatNexthop(ip string) string {
	if o := m.ipOwner(ip); o != nil {
		return fmt.Sprintf("%s (%s)", ip, m.ownerLabel(o))
	}
	return ip
}

// detailRefsFor turns a router's routes, reroute policies and NAT rules into
// relations to the objects they lead to.
func (m model) detailRefsFor(d *api.NodeDetail) []xref {
	if d == nil || m.tree == nil {
		return nil
	}
	var out []xref
	seen := map[string]bool{}
	add := func(label string, to *xnode) {
		if to != nil && !seen[label+to.id] {
			seen[label+to.id] = true
			out = append(out, xref{label, to.id})
		}
	}
	if d.StaticRoutes != nil {
		for _, r := range *d.StaticRoutes {
			label := "route " + r.IpPrefix
			if r.Policy != nil && *r.Policy == api.SrcIp {
				label += " src"
			}
			add(label, m.ipOwner(r.Nexthop))
		}
	}
	if d.Policies != nil {
		for _, p := range *d.Policies {
			if p.Nexthops != nil {
				for _, nh := range *p.Nexthops {
					add(fmt.Sprintf("policy %d", p.Priority), m.ipOwner(nh))
				}
			}
		}
	}
	if d.Nats != nil {
		for _, nat := range *d.Nats {
			if nat.LogicalPort != nil {
				add("nat "+nat.ExternalIp, m.tree.lspByName[*nat.LogicalPort])
			}
		}
	}
	return out
}

// applyDetailRefs attaches the links of every cached router detail to the
// current tree (after a fetch, and after the tree is rebuilt).
func (m *model) applyDetailRefs() {
	if m.tree == nil {
		return
	}
	for id, d := range m.nodeDetails {
		if n := m.tree.byID[id]; n != nil {
			n.detailRefs = m.detailRefsFor(d)
		}
	}
}

// linkNATGateways relates each router to the NAT gateways serving it (by the
// gateway's LAN port) and back.
func linkNATGateways(t *xtree) {
	for _, n := range t.byID {
		if n.kind != kLR {
			continue
		}
		for _, gw := range natGatewaysNearIn(t, n) {
			for _, p := range gw.ports {
				if !hasLocalnet(switchOfIn(t, p)) {
					n.refs = append(n.refs, xref{"nat gateway", p.id})
					p.refs = append(p.refs, xref{"serves router", n.id})
					break
				}
			}
		}
	}
}

// sameHostname lists the other chassis rows reported with this chassis's
// hostname (the daemon's hostnameSharedWith).
func sameHostname(ch *xnode) []string {
	raw, _ := props(ch.gn)["hostnameSharedWith"].([]interface{})
	var out []string
	for _, x := range raw {
		if s, _ := x.(string); s != "" {
			out = append(out, s)
		}
	}
	return out
}

// authoritative reports whether the node's own annotation (or a static
// override) names this chassis: then it is the node's live ovn-controller.
func authoritative(ch *xnode) bool {
	how := pstr(props(ch.gn), "nodeMatch")
	return how == "annotation" || how == "override"
}

// linkSameHostname relates chassis rows that share a hostname.
func linkSameHostname(t *xtree) {
	for _, n := range t.byID {
		if n.kind != kChassis {
			continue
		}
		for _, name := range sameHostname(n) {
			if o := t.byID[graph.PrefixChassis+name]; o != nil {
				n.refs = append(n.refs, xref{"same hostname", o.id})
			}
		}
	}
}
