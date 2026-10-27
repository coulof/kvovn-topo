package main

// Concept help (`?`): what the selected object is in OVN and in Kube-OVN, how
// to read its pane, and what usually goes wrong; what each view is for; and a
// glossary across Kube-OVN, OVN and ovn-kubernetes. The long form of each
// entry is in docs/concepts.md (linked, never fetched: air-gap safe).

import "strings"

const conceptsURL = "https://github.com/coulof/ovn-topology/blob/main/docs/concepts.md"

type concept struct {
	title, anchor string
	ovn, kubeOVN  string
	read          string
	problems      string
}

// concepts is keyed by kind, or kind/role where the role changes the meaning.
var concepts = map[string]concept{
	"lr": {
		title: "Logical router (LR)", anchor: "logical-router",
		ovn:     "Routes between the switches attached to its ports. Static routes, routing policies, NAT rules and load balancers are configured on it.",
		kubeOVN: "One router per VPC; `ovn-cluster` is the default VPC.",
		read: "Ports & interconnections are its router ports. The route, policy and NAT rows come from the NB database. " +
			"OVN routes first: the longest matching prefix wins across `dst-ip` and `src-ip` routes, and `dst-ip` wins a tie. " +
			"Policies then apply by priority and can allow, drop or reroute.",
		problems: "A `src-ip` route pins traffic *from* a prefix. Without one, replies to traffic a VPC NAT gateway forwarded in follow 0.0.0.0/0 and bypass the gateway. " +
			"`nat 0` does not mean no NAT: a VPC NAT gateway does its NAT in iptables, and centralized natOutgoing uses a policy reroute plus node iptables.",
	},
	"lrp/gateway": {
		title: "Gateway router port (centralized)", anchor: "gateway-router-port",
		ovn:     "A router port with gateway chassis (or an HA chassis group). Its traffic is centralized: it leaves through the one chassis that holds its `cr-<port>` redirect port.",
		kubeOVN: "The VPC's external gateway port on the external (underlay) subnet; EIPs, SNAT and FIPs leave here.",
		read:    "`GW active <node>` is the chassis that holds `cr-<port>`. *Routes out this port* lists the routes whose next hop sits on this port's network, typically 0.0.0.0/0.",
		problems: "No active chassis means the VPC has no egress (Problems lists it). " +
			"A failover moves `cr-<port>` to the next chassis by priority; with BFD this is automatic.",
	},
	"lrp/subnet": {
		title: "Subnet gateway port (distributed)", anchor: "subnet-gateway-port",
		ovn:      "A router port with no gateway chassis: every chassis routes it locally, so traffic between subnets never leaves the host.",
		kubeOVN:  "The subnet's gateway IP, the default gateway of its pods and VMs.",
		read:     "Networks is the gateway IP and prefix; the peer switch is the subnet.",
		problems: "A workload with the wrong gateway, or a subnet attached to the wrong router, shows here first.",
	},
	"lrp/join": {
		title: "Join port", anchor: "join",
		ovn:      "A router port on the `join` switch.",
		kubeOVN:  "Connects the default VPC router to every node's `ovn0` interface over 100.64.0.0/16: node ↔ pod traffic, and centralized natOutgoing.",
		read:     "The join switch carries one `node-<name>` port per node.",
		problems: "A node without its `node-<name>` port cannot reach pods.",
	},
	"lrp/interconnect": {
		title: "Interconnect port", anchor: "interconnect-port",
		ovn:      "A router port on a switch that several routers share; traffic between those routers crosses the switch.",
		kubeOVN:  "Typically the external (underlay) switch shared by several VPC gateways.",
		read:     "Routers on LS lists every router on the shared switch.",
		problems: "Shared does not mean peered: each VPC still routes by its own table.",
	},
	"ls/subnet": {
		title: "Logical switch (subnet)", anchor: "logical-switch",
		ovn:      "A layer-2 broadcast domain. Its ports are workloads, router links and possibly a localnet uplink.",
		kubeOVN:  "A Subnet: the CIDR comes from its router port, the ports are pods and VM NICs.",
		read:     "Connected routers says which VPC router serves it. Ports gives totals and the unbound count.",
		problems: "Unbound ports are workloads no chassis has claimed (see a port's help).",
	},
	"ls/join": {
		title: "Join switch", anchor: "join",
		ovn:      "A switch attached to the default router.",
		kubeOVN:  "The join network (100.64.0.0/16): one `node-<name>` port per node, the node's `ovn0`.",
		read:     "Each node join port shows its node.",
		problems: "A missing node port means that node's pods are unreachable from the host.",
	},
	"ls/underlay": {
		title: "Underlay switch (provider network)", anchor: "underlay",
		ovn:      "A switch with a `localnet` port: traffic leaves OVN onto a physical bridge, mapped per chassis by `ovn-bridge-mappings`.",
		kubeOVN:  "An underlay or external subnet on a provider network; VPC gateway ports and VPC NAT gateways attach here.",
		read:     "Underlay names the provider network. A switch shared by several routers is listed under Shared & standalone switches.",
		problems: "A chassis without the bridge mapping drops traffic for this network.",
	},
	"lsp/workload": {
		title: "Workload port (pod or VM NIC)", anchor: "workload-port",
		ovn:      "A switch port with the workload's MAC and IP. A `Port_Binding` in the SB database says which chassis claimed it.",
		kubeOVN:  "Named `<pod>.<ns>`, or `<vm>.<ns>.<nad>.<subnet>.ovn` for a NIC attached through a NetworkAttachmentDefinition.",
		read:     "Bound on is the chassis that claimed it. Path out follows switch → gateway → router.",
		problems: "Unbound: no `ovn-controller` claimed it. Usually the pod is not running yet, `ovs-ovn` is down on its node, or the port is pinned to another chassis.",
	},
	"lsp/router": {
		title: "Router link", anchor: "router-link",
		ovn:      "A switch port of type `router`, the switch side of a router port (`options:router-port`).",
		kubeOVN:  "Created with each subnet's gateway.",
		read:     "Its peer is the router port.",
		problems: "Never bound to a chassis by design (no unbound badge).",
	},
	"lsp/localnet": {
		title: "Localnet port", anchor: "underlay",
		ovn:      "Bridges the switch to a physical network on every chassis that maps it.",
		kubeOVN:  "The provider network's uplink.",
		read:     "The switch it belongs to is an underlay switch.",
		problems: "Traffic is dropped on chassis without the bridge mapping.",
	},
	"lsp/node": {
		title: "Node join port", anchor: "join",
		ovn:      "A switch port on the join switch.",
		kubeOVN:  "`node-<name>`: the node's `ovn0` interface, used for host ↔ pod traffic and as a reroute target (`100.64.x.x`).",
		read:     "Policies that reroute to its IP send traffic through that node.",
		problems: "If it is unbound, the node's `ovs-ovn` is not running.",
	},
	"lsp/natgw": {
		title: "VPC NAT gateway interface", anchor: "vpc-nat-gateway",
		ovn:      "An ordinary switch port. OVN sees the interface, not what happens behind it.",
		kubeOVN:  "A VPC NAT gateway is a pod (`vpc-nat-gw-<name>`) with a LAN interface on the VPC subnet and a WAN interface on the external network. Its EIPs, DNAT and SNAT are iptables in the pod.",
		read:     "Outside OVN shows both interfaces. `!` (with --diag) runs `iptables-save -t nat` in the pod.",
		problems: "Replies must come back through the gateway: the router needs a `src-ip` route (or 0.0.0.0/0) to its LAN IP. Problems flags a router where they may not.",
	},
	"chassis": {
		title: "Chassis", anchor: "chassis",
		ovn:      "One `ovn-controller`, identified by the host's OVS system-id. It claims ports (Port_Binding) and builds the Geneve tunnels to the other chassis.",
		kubeOVN:  "Normally one per node. The node annotation `ovn.kubernetes.io/chassis` names it (ovn-kubernetes: `k8s.ovn.org/node-chassis-id`).",
		read:     "Node and the rule that matched it, encaps, bound ports, and the gateways it holds.",
		problems: "Matched by hostname only, or a stale chassis left by a reinstall. An interconnect chassis is not a host of this cluster.",
	},
	"vpc": {
		title: "VPC", anchor: "vpc",
		ovn:      "Not an OVN object: the TUI groups a router with its switches.",
		kubeOVN:  "A Vpc: one router, its subnets, and optionally a NAT gateway and an external gateway.",
		read:     "Egress lists the gateway ports and their active chassis.",
		problems: "No egress listed means the VPC only routes internally.",
	},
	"wl": {
		title: "Workload", anchor: "workload",
		ovn:      "Not an OVN object: the TUI groups switch ports by their pod.",
		kubeOVN:  "A pod, or a VM resolved through its virt-launcher pod and VMI, with one NIC per network.",
		read:     "Each NIC with its IP, switch and chassis.",
		problems: "A broken NIC marks the whole workload.",
	},
	"ext": {
		title: "External network", anchor: "underlay",
		ovn:      "Outside OVN: known only through the localnet port attached to it.",
		kubeOVN:  "The provider network (physical underlay).",
		read:     "Localnet from lists the switches that reach it.",
		problems: "OVN cannot see past it; the upstream router and firewall decide what happens next.",
	},
}

// viewHelp is what each view is for.
var viewHelp = map[mode]string{
	modeVPC:      "VPCs: VPCs → routers → router ports → switches → switch ports. Read it to see how a VPC is wired. ⇢ rows are cross-links (peer switch, gateway chassis, routes, NAT gateway); Enter follows them and b comes back.",
	modeChassis:  "Chassis: each ovn-controller (usually one per node) and the ports bound on it. Read it to see where things run and which chassis holds a gateway.",
	modeWorkload: "Workloads: namespaces → pods and VMs → NICs. Start here from a Kubernetes name.",
	modeProblems: "Problems: what the tree already knows is wrong, most severe first. Enter jumps to the object.",
	modeChanges:  "Changes: what moved between revisions since ovn-topo started, newest first. Enter jumps to the object.",
}

type glossaryEntry struct{ term, kubeOVN, ovn, ovnk, text string }

var glossary = []glossaryEntry{
	{"VPC", "Vpc", "Logical_Router + its switches", "network / UDN", "An isolated routing domain with its own router."},
	{"Subnet", "Subnet", "Logical_Switch", "node switch / UDN switch", "A layer-2 segment with a CIDR; its gateway is a router port."},
	{"Router port", "subnet gateway, VPC gateway", "Logical_Router_Port", "rtos-<node>, GR ports", "Distributed unless it has a gateway chassis."},
	{"Switch port", "pod / VM NIC", "Logical_Switch_Port", "<ns>_<pod>", "A workload's attachment, or a router link or localnet uplink."},
	{"Chassis", "node", "Chassis (ovn-controller)", "node (one zone each)", "The per-host agent that claims ports and builds tunnels."},
	{"Port binding", "—", "SB Port_Binding", "same", "Which chassis claimed a port. No chassis: unbound."},
	{"cr- port", "VPC gateway", "chassisredirect Port_Binding", "same", "Pins a gateway port's traffic to one chassis."},
	{"Join network", "join, 100.64.0.0/16", "a switch", "join; transit_switch across zones", "Connects the cluster router to every node."},
	{"Underlay", "provider network, external subnet", "localnet port", "localnet (secondary networks)", "Where traffic leaves OVN onto a physical network."},
	{"src-ip route", "policySrc static route", "Static_Route policy=src-ip", "same", "Routes by source prefix; pins replies to a gateway."},
	{"Policy", "policy route", "Logical_Router_Policy", "same (EgressIP reroutes)", "allow, drop or reroute by match, after routing."},
	{"FIP / EIP", "OvnFip / OvnEip; IptablesEIP in a NAT gateway", "NAT dnat_and_snat; none for a NAT gateway", "EgressIP", "A public address mapped to a workload."},
	{"VPC NAT gateway", "VpcNatGateway (pod)", "— (iptables in the pod)", "— (local gateway mode: host stack)", "NAT outside OVN: an opaque hop."},
	{"natOutgoing", "Subnet natOutgoing", "policy reroute + node iptables", "SNAT on GR_<node>", "Egress NAT for a subnet."},
	{"Zone", "—", "one NB/SB", "one node (interconnect mode)", "ovn-kubernetes interconnect runs one OVN database pair per node."},
}

// conceptKey picks the concept entry for n.
func (m model) conceptKey(n *xnode) string {
	if n == nil {
		return ""
	}
	switch n.kind {
	case kLRP:
		if n.conn == nil {
			return "lrp/subnet"
		}
		return "lrp/" + map[string]string{"external gateway": "gateway", "interconnect": "interconnect",
			"node transit (join)": "join", "subnet gateway": "subnet"}[portRole(n.conn)]
	case kLS:
		switch {
		case n.name == "join":
			return "ls/join"
		case hasLocalnet(n):
			return "ls/underlay"
		}
		return "ls/subnet"
	case kLSP:
		typ := pstr(props(n.gn), "type")
		switch {
		case m.natGatewayOf(n) != nil:
			return "lsp/natgw"
		case typ == "router":
			return "lsp/router"
		case typ == "localnet":
			return "lsp/localnet"
		case strings.HasPrefix(n.name, "node-"):
			return "lsp/node"
		}
		return "lsp/workload"
	}
	return n.kind
}
