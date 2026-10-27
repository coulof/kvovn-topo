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
	if !m.claimedByKubeOVN(n) && !(n.kind == kLSP && m.natGatewayOf(n) != nil) {
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

func (m model) hasLocalnet(ls *xnode) bool {
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

func (m model) switchOf(lsp *xnode) *xnode {
	for _, r := range lsp.refs {
		if r.label == "subnet" && m.tree != nil {
			return m.tree.byID[r.to]
		}
	}
	return nil
}

// natGateway is a Kube-OVN VPC NAT gateway pod and its OVN switch ports: an
// opaque hop (ADR-0004). OVN sees its interfaces, not its NAT.
type natGateway struct {
	name, namespace, pod string
	ports                []*xnode
}

// natGateways finds VPC NAT gateway pods from their ports' pod identity.
func (m model) natGateways() []*natGateway {
	if m.tree == nil {
		return nil
	}
	byPod := map[string]*natGateway{}
	for _, n := range m.tree.byID {
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
		sort.Slice(gw.ports, func(i, j int) bool { return gw.ports[i].name < gw.ports[j].name })
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

// natGatewaysNear lists the gateways with a port on switch ls, or with their
// LAN side on a switch of router lr. A shared underlay (the WAN side) does not
// count: the gateway serves the VPC whose subnet it sits on, not every router
// on the provider network.
func (m model) natGatewaysNear(n *xnode) []*natGateway {
	on := map[string]bool{}
	switch n.kind {
	case kLS:
		on[n.id] = true
	case kLR:
		for _, c := range m.routerConns(n) {
			if m.tree != nil && !m.hasLocalnet(m.tree.byID[c.SwitchID]) {
				on[c.SwitchID] = true
			}
		}
	case kLSP:
		if gw := m.natGatewayOf(n); gw != nil {
			return []*natGateway{gw}
		}
		return nil
	}
	var out []*natGateway
	for _, gw := range m.natGateways() {
		for _, p := range gw.ports {
			if ls := m.switchOf(p); ls != nil && on[ls.id] {
				out = append(out, gw)
				break
			}
		}
	}
	return out
}

// inspectCommands is the next command for n, in the CNI's own tooling.
func (m model) inspectCommands(n *xnode) []string {
	nb, sb := "ovn-nbctl", "ovn-sbctl"
	if m.clusterFlavor() == flavorKubeOVN {
		nb, sb = "kubectl ko nbctl", "kubectl ko sbctl"
	}
	var out []string
	switch n.kind {
	case kLR:
		for _, c := range []string{"lr-route-list", "lr-policy-list", "lr-nat-list"} {
			out = append(out, nb+" "+c+" "+n.name)
		}
	case kLS:
		out = append(out, nb+" show "+n.name)
	case kLRP:
		out = append(out, nb+" list Logical_Router_Port "+n.name)
	case kLSP:
		out = append(out, nb+" list Logical_Switch_Port "+n.name, sb+" find Port_Binding logical_port="+n.name)
	case kChassis:
		out = append(out, sb+" list Chassis "+firstNonEmpty(pstr(props(n.gn), "name"), n.name))
	}
	if n.kind == kLR || n.kind == kLS || n.kind == kLSP {
		for _, gw := range m.natGatewaysNear(n) {
			out = append(out, "kubectl -n "+gw.namespace+" exec "+gw.pod+" -- iptables-save -t nat")
		}
	}
	return out
}
