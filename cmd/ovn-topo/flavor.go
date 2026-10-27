package main

// The CNI layer of the details pane (ADR-0004): what a platform engineer calls
// an OVN object, where that reading came from ("← via …"), where traffic
// leaves OVN's view, and the command that shows more. Kube-OVN is the only
// flavor with a vocabulary here; anything else is shown as plain OVN. Interim:
// it moves behind the pkg/graph flavor adapter in M4 prep.

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/coulof/ovn-topology/pkg/diag"
	"github.com/coulof/ovn-topology/pkg/graph"
)

type flavor int

const (
	flavorOVN flavor = iota // no CNI conventions recognised
	flavorKubeOVN
	flavorOVNK
)

const kubeOVNVia = "external_ids:vendor=kube-ovn"

// natGWPrefix is Kube-OVN's StatefulSet/pod name prefix for a VPC NAT gateway.
const natGWPrefix = "vpc-nat-gw-"

// clusterFlavor: Kube-OVN when any router or switch carries vendor=kube-ovn;
// ovn-kubernetes when the TUI was started on one of its zones.
func (m model) clusterFlavor() flavor {
	for _, gn := range m.nodeMap {
		if gn.K8s != nil && deref(gn.K8s.Vendor) == "kube-ovn" {
			return flavorKubeOVN
		}
	}
	if m.zone != "" {
		return flavorOVNK
	}
	return flavorOVN
}

// cniLabel is the header's CNI row.
func (m model) cniLabel() string {
	switch m.clusterFlavor() {
	case flavorKubeOVN:
		return "Kube-OVN · central NB/SB"
	case flavorOVNK:
		return "ovn-kubernetes · zone " + m.zone
	}
	if m.graphResp == nil {
		return "—"
	}
	return "OVN (no CNI conventions found)"
}

// cniShort is the flavor alone, for the header.
func (m model) cniShort() string {
	switch m.clusterFlavor() {
	case flavorKubeOVN:
		return "Kube-OVN"
	case flavorOVNK:
		return "ovn-kubernetes zone " + m.zone
	}
	if m.graphResp == nil {
		return "—"
	}
	return "OVN"
}

// zoneOf names the ovn-kubernetes zone of a --fixture-dir capture (…/zone-<node>).
func zoneOf(fixtureDir string) string {
	if b := filepath.Base(filepath.Clean(fixtureDir)); strings.HasPrefix(b, "zone-") {
		return strings.TrimPrefix(b, "zone-")
	}
	return ""
}

// claimedByKubeOVN reports whether n carries Kube-OVN's vendor tag (a router
// port answers for its router).
func (m model) claimedByKubeOVN(n *xnode) bool {
	gn := n.gn
	if n.kind == kLRP && n.conn != nil {
		if r, ok := m.nodeMap[n.conn.RouterID]; ok {
			gn = &r
		}
	}
	return gn != nil && gn.K8s != nil && deref(gn.K8s.Vendor) == "kube-ovn"
}

// cniIdentity is the header in the platform engineer's words: term, parent,
// and the role row with where it was read from. ok is false for objects no
// flavor claims; they keep the plain OVN header.
type cniIdentity struct {
	term, parent string
	role, via    string
}

func (m model) cniIdentity(n *xnode) (cniIdentity, bool) {
	if !m.claimedByKubeOVN(n) && (n.kind != kLSP || m.natGatewayOf(n) == nil) {
		return cniIdentity{}, false
	}
	vpc := ""
	if n.gn != nil {
		vpc = strings.TrimPrefix(deref(n.gn.Parent), graph.PrefixVPC)
	}
	switch n.kind {
	case kLR:
		id := cniIdentity{term: "VPC router", role: "routes between the VPC's subnets and out of the VPC", via: kubeOVNVia}
		if vpc != "" && vpc != n.name {
			id.parent = "VPC " + vpc
		}
		return id, true
	case kLS:
		id := cniIdentity{term: "Subnet", role: "workload subnet", via: kubeOVNVia}
		if vpc != "" {
			id.parent = "VPC " + vpc
		}
		switch {
		case n.name == "join":
			id.term, id.role = "Join subnet", "node transit: every node's ovn0 reaches the cluster router here"
			id.via = "switch name join (Kube-OVN convention)"
		case m.hasLocalnet(n):
			id.term, id.role = "Underlay subnet", "provider network: a localnet port bridges it to the physical network"
			id.via = "localnet port · " + kubeOVNVia
		}
		return id, true
	case kLRP:
		c := n.conn
		term := map[string]string{"external gateway": "VPC external gateway", "interconnect": "Interconnect port",
			"node transit (join)": "Join port", "subnet gateway": "Subnet gateway"}[portRole(c)]
		return cniIdentity{term: firstNonEmpty(term, "Router port"), parent: "VPC " + c.VPCName}, true
	case kLSP:
		return m.portIdentity(n), true
	}
	return cniIdentity{}, false
}

// portIdentity names a switch port by what is plugged into it.
func (m model) portIdentity(n *xnode) cniIdentity {
	typ := pstr(props(n.gn), "type")
	if gw := m.natGatewayOf(n); gw != nil {
		side := "LAN"
		if m.hasLocalnet(m.switchOf(n)) {
			side = "WAN"
		}
		return cniIdentity{term: "NAT gateway " + side + " interface", parent: "gateway " + gw.name,
			role: "an interface of VPC NAT gateway " + gw.name + "; its NAT is iptables in the pod, not OVN",
			via:  "pod name " + natGWPrefix + "<name> (Kube-OVN convention)"}
	}
	switch {
	case typ == "router":
		return cniIdentity{term: "Router link", role: "the switch side of a router port", via: kubeOVNVia}
	case typ == "localnet":
		return cniIdentity{term: "Underlay uplink", role: "bridges the switch to the physical network", via: kubeOVNVia}
	case strings.HasPrefix(n.name, "node-"):
		return cniIdentity{term: "Node join port", parent: "node " + strings.TrimPrefix(n.name, "node-"),
			role: "the node's ovn0 interface on the join subnet", via: "port name node-<node> (Kube-OVN convention)"}
	}
	if kind, name, _, ok := workloadOf(n.gn); ok {
		term := map[string]string{"VirtualMachine": "VM NIC"}[kind]
		if term == "" {
			term = "Pod interface"
		}
		return cniIdentity{term: term, parent: kind + " " + name, role: "workload interface", via: "external_ids:pod"}
	}
	return cniIdentity{term: "Port", via: kubeOVNVia}
}

func (m model) hasLocalnet(ls *xnode) bool { return hasLocalnet(ls) }

func hasLocalnet(ls *xnode) bool {
	if ls == nil {
		return false
	}
	for _, r := range ls.refs {
		if r.label == "localnet" {
			return true
		}
	}
	return false
}

func (m model) switchOf(lsp *xnode) *xnode { return switchOfIn(m.tree, lsp) }

func switchOfIn(t *xtree, lsp *xnode) *xnode {
	if t == nil {
		return nil
	}
	for _, r := range lsp.refs {
		if r.label == "subnet" {
			return t.byID[r.to]
		}
	}
	return nil
}

// natGateway is a Kube-OVN VPC NAT gateway pod and its OVN switch ports: an
// opaque hop (ADR-0004). OVN sees its interfaces, not its NAT. ports lists the
// LAN side (a VPC subnet) before the WAN side (a localnet underlay).
type natGateway struct {
	name, namespace, pod string
	ports                []*xnode
}

func (m model) natGateways() []*natGateway { return natGatewaysIn(m.tree) }

// natGatewaysIn finds VPC NAT gateway pods in t from their ports' pod identity.
func natGatewaysIn(t *xtree) []*natGateway {
	if t == nil {
		return nil
	}
	byPod := map[string]*natGateway{}
	for _, n := range t.byID {
		if n.kind != kLSP || n.gn == nil || n.gn.K8s == nil {
			continue
		}
		pod := deref(n.gn.K8s.Pod)
		if !strings.HasPrefix(pod, natGWPrefix) {
			continue
		}
		key := deref(n.gn.K8s.Namespace) + "/" + pod
		gw := byPod[key]
		if gw == nil {
			name := strings.TrimPrefix(pod, natGWPrefix)
			if i := strings.LastIndex(name, "-"); i > 0 {
				name = name[:i] // StatefulSet ordinal
			}
			gw = &natGateway{name: name, namespace: deref(n.gn.K8s.Namespace), pod: pod}
			byPod[key] = gw
		}
		gw.ports = append(gw.ports, n)
	}
	var out []*natGateway
	for _, gw := range byPod {
		sort.Slice(gw.ports, func(i, j int) bool {
			wi, wj := hasLocalnet(switchOfIn(t, gw.ports[i])), hasLocalnet(switchOfIn(t, gw.ports[j]))
			if wi != wj {
				return wj // LAN first
			}
			return gw.ports[i].name < gw.ports[j].name
		})
		out = append(out, gw)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].pod < out[j].pod })
	return out
}

func (m model) natGatewayOf(lsp *xnode) *natGateway {
	for _, gw := range m.natGateways() {
		for _, p := range gw.ports {
			if p.id == lsp.id {
				return gw
			}
		}
	}
	return nil
}

func (m model) natGatewaysNear(n *xnode) []*natGateway { return natGatewaysNearIn(m.tree, n) }

// natGatewaysNearIn lists the gateways with a port on switch ls, or with their
// LAN side on a switch of router lr. A shared underlay (the WAN side) does not
// count: the gateway serves the VPC whose subnet it sits on, not every router
// on the provider network.
func natGatewaysNearIn(t *xtree, n *xnode) []*natGateway {
	if t == nil {
		return nil
	}
	on := map[string]bool{}
	switch n.kind {
	case kLS:
		on[n.id] = true
	case kLR:
		for _, c := range n.children {
			if c.conn != nil && !hasLocalnet(t.byID[c.conn.SwitchID]) {
				on[c.conn.SwitchID] = true
			}
		}
	case kLSP:
		for _, gw := range natGatewaysIn(t) {
			for _, p := range gw.ports {
				if p.id == n.id {
					return []*natGateway{gw}
				}
			}
		}
		return nil
	}
	var out []*natGateway
	for _, gw := range natGatewaysIn(t) {
		for _, p := range gw.ports {
			if ls := switchOfIn(t, p); ls != nil && on[ls.id] {
				out = append(out, gw)
				break
			}
		}
	}
	return out
}

// interfaces is the gateway's addresses, LAN first, one per line:
// "LAN 192.168.54.46 on subnet-onewater", "WAN 10.144.124.15 on external".
func (gw *natGateway) interfaces(t *xtree) []string {
	var ifs []string
	for _, p := range gw.ports {
		ls := switchOfIn(t, p)
		if ls == nil {
			continue
		}
		side := "LAN"
		if hasLocalnet(ls) {
			side = "WAN"
		}
		ip, _ := lspAddress(p.gn)
		ifs = append(ifs, side+" "+firstNonEmpty(ip, "?")+" on "+ls.name)
	}
	return ifs
}

// inspectCmd is one Inspect line. argv is set when the TUI can run it (`!`,
// Tier 1): it is exactly the text shown, built by pkg/diag's typed builders.
type inspectCmd struct {
	text string
	argv []string
}

// inspectCommands is the next command for n, in the CNI's own tooling. Only
// Kube-OVN's (kubectl ko, kubectl exec) are runnable from a laptop; plain
// ovn-nbctl lines are shown for copying.
func (m model) inspectCommands(n *xnode) []inspectCmd {
	kubeOVN := m.clusterFlavor() == flavorKubeOVN
	var out []inspectCmd
	add := func(sb bool, query, obj string) {
		build, tool := diag.BuildKoNbctlCommand, "ovn-nbctl"
		if sb {
			build, tool = diag.BuildKoSbctlCommand, "ovn-sbctl"
		}
		if kubeOVN {
			if argv, err := build(query, obj); err == nil {
				out = append(out, inspectCmd{text: strings.Join(argv, " "), argv: argv})
				return
			}
		}
		words := strings.Fields(query)
		if words[0] == "find" {
			words[len(words)-1] += "=" + obj
		} else {
			words = append(words, obj)
		}
		out = append(out, inspectCmd{text: tool + " " + strings.Join(words, " ")})
	}
	switch n.kind {
	case kLR:
		for _, q := range []string{"lr-route-list", "lr-policy-list", "lr-nat-list"} {
			add(false, q, n.name)
		}
	case kLS:
		add(false, "show", n.name)
	case kLRP:
		add(false, "list Logical_Router_Port", n.name)
	case kLSP:
		add(false, "list Logical_Switch_Port", n.name)
		add(true, "find Port_Binding logical_port", n.name)
	case kChassis:
		add(true, "list Chassis", firstNonEmpty(pstr(props(n.gn), "name"), n.name))
	}
	if n.kind == kLR || n.kind == kLS || n.kind == kLSP {
		for _, gw := range m.natGatewaysNear(n) {
			if argv, err := diag.BuildNATGatewayIptablesCommand(gw.namespace, gw.pod); err == nil {
				out = append(out, inspectCmd{text: strings.Join(argv, " "), argv: argv})
			}
		}
	}
	return out
}
