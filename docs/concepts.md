# Concepts: OVN, Kube-OVN and ovn-kubernetes

`kvovn-topo` shows OVN's own objects, named the way Kube-OVN names them where it can. This page is the long form of the TUI's `?` help: what each object is, how to read it, and what usually goes wrong. The [TUI guide](tui-guide.md) covers the screens and keys.

OVN keeps two databases. The **Northbound (NB)** database holds the intended topology: routers, switches, ports, routes, NAT. The **Southbound (SB)** database holds what is running: which host (chassis) claimed which port. `kvovn-topo` reads both and joins them; it never writes to either.

## Logical router

An OVN logical router (LR) routes between the switches attached to its ports. Static routes, routing policies, NAT rules and load balancers are configured on it.

In Kube-OVN, each VPC has one router. `ovn-cluster` is the default VPC's router.

**How OVN picks a route.** Routing happens first. Among the static routes whose prefix matches the packet, the longest prefix wins, across `dst-ip` routes (matched on destination) and `src-ip` routes (matched on source); on a tie, `dst-ip` wins. Routing policies apply next, by priority, and can allow, drop or reroute what routing chose.

**What goes wrong:**
- **Replies bypass a VPC NAT gateway.** A `src-ip` route pins traffic *from* a prefix to a next hop. When a VPC NAT gateway DNATs inbound traffic to a VM, the VM's replies must go back through the gateway. Without a `src-ip` route (or a reroute policy) to the gateway's LAN IP, they follow `0.0.0.0/0` out of the VPC's own gateway port, and the client drops them. The Problems view (`4`) flags routers where this can happen.
- **`nat 0` is not "no NAT".** A VPC NAT gateway does its NAT in iptables inside a pod, and centralized `natOutgoing` uses a policy reroute plus node iptables. Neither appears in NB `NAT`.

## Gateway router port

A router port with gateway chassis, or an HA chassis group, is centralized: its traffic leaves through the one chassis holding its `cr-<port>` redirect port. When that chassis fails, the redirect port moves to the next one by priority (automatically with BFD).

In Kube-OVN this is a VPC's external gateway port, on the external (underlay) subnet. EIPs, SNAT and floating IPs leave through it.

In the pane, `GW active <node>` is the chassis that holds `cr-<port>`, and *Routes out this port* lists the routes whose next hop sits on the port's network, usually `0.0.0.0/0`. A gateway port with no active chassis means the VPC has no egress.

## Subnet gateway port

A router port without gateway chassis is distributed: every chassis routes it locally, so traffic between subnets of the same VPC never leaves the host. In Kube-OVN its address is the subnet's gateway, the default gateway of its pods and VMs.

## Join

Kube-OVN's join network (`100.64.0.0/16`) connects the default VPC router to every node. Each node has a `node-<name>` port on the `join` switch, which is the node's `ovn0` interface. It carries node ↔ pod traffic and is the target of the per-node reroute policies (shown as `node <name>` next hops). A node without its port cannot reach pods.

## Interconnect port

A router port on a switch that several routers share. Traffic between those routers crosses the switch. In Kube-OVN this is usually the external switch shared by several VPC gateways. Shared is not peered: each VPC still routes by its own table.

## Logical switch

An OVN logical switch (LS) is a layer-2 broadcast domain. Its ports are workloads, router links and possibly a localnet uplink. In Kube-OVN it is a Subnet: the CIDR comes from its router port, and its ports are pods and VM NICs. The pane shows which router serves it and how many of its ports are unbound.

## Underlay

A switch with a `localnet` port reaches a physical network: on each chassis, `ovn-bridge-mappings` maps the network to an OVS bridge. In Kube-OVN these are underlay and external subnets on a provider network. VPC gateway ports and VPC NAT gateways attach there. OVN cannot see past the localnet port: the upstream router and firewall decide what happens next. A chassis without the bridge mapping drops traffic for that network.

## Workload port

A switch port (LSP) carries a workload's MAC and IP. When a host's `ovn-controller` claims it, the SB `Port_Binding` records that chassis. Kube-OVN names it `<pod>.<namespace>`, or `<vm>.<namespace>.<nad>.<subnet>.ovn` for a NIC attached through a NetworkAttachmentDefinition.

**Unbound** means no chassis has claimed the port. Usually the pod is not running yet, `ovs-ovn` is down on its node, or the port is pinned to another chassis (`requested-chassis`).

## Router link

A switch port of type `router` is the switch side of a router port (`options:router-port`). It is never bound to a chassis, by design.

## VPC NAT gateway

A Kube-OVN VPC NAT gateway is a pod, `vpc-nat-gw-<name>`, with two interfaces: a LAN interface on a VPC subnet and a WAN interface on the external network. Its EIPs, DNAT and SNAT rules are iptables inside the pod (the `Iptables*` resources). OVN sees only the two switch ports, so `kvovn-topo` shows the gateway under *Outside OVN* and, with `--diag`, `!` runs `iptables-save -t nat` in the pod.

Replies to traffic the gateway DNATs must return through it: the router needs a `src-ip` route, or its default route, pointing at the gateway's LAN IP. See [Logical router](#logical-router).

## Chassis

A chassis is one `ovn-controller`, identified by its host's OVS system-id. It claims ports and builds Geneve tunnels to the other chassis. It is normally one per node: Kube-OVN names it in the node annotation `ovn.kubernetes.io/chassis`, ovn-kubernetes in `k8s.ovn.org/node-chassis-id`. `Chassis.hostname` is informational only. The pane shows which rule matched a node. When two rows share a hostname, the one the node's annotation names is the live `ovn-controller`; the other is likely a stale row left by a reinstall, and that is the one Problems lists. An interconnect chassis is not a host of this cluster.

## VPC

Not an OVN object: `kvovn-topo` groups a router with its switches. In Kube-OVN, a Vpc is one router, its subnets, and optionally a NAT gateway and an external gateway. Its pane lists its egress gateway ports.

## Workload

Not an OVN object: `kvovn-topo` groups switch ports by their pod, and a VM through its virt-launcher pod and VMI, with one NIC per network.

## Names across Kube-OVN, OVN and ovn-kubernetes

| Concept | Kube-OVN | OVN | ovn-kubernetes |
|---|---|---|---|
| VPC | Vpc | Logical_Router and its switches | network / UDN |
| Subnet | Subnet | Logical_Switch | node switch / UDN switch |
| Router port | subnet gateway, VPC gateway | Logical_Router_Port | `rtos-<node>`, GR ports |
| Switch port | pod / VM NIC | Logical_Switch_Port | `<ns>_<pod>` |
| Chassis | node | Chassis (`ovn-controller`) | node (one zone each) |
| Port binding | — | SB Port_Binding | same |
| `cr-` port | VPC gateway | chassisredirect Port_Binding | same |
| Join network | `join`, 100.64.0.0/16 | a switch | `join`; `transit_switch` across zones |
| Underlay | provider network, external subnet | localnet port | localnet (secondary networks) |
| `src-ip` route | policySrc static route | Static_Route `policy=src-ip` | same |
| Policy | policy route | Logical_Router_Policy | same (EgressIP reroutes) |
| FIP / EIP | OvnFip / OvnEip; IptablesEIP in a NAT gateway | NAT `dnat_and_snat`; none for a NAT gateway | EgressIP |
| VPC NAT gateway | VpcNatGateway (pod) | none (iptables in the pod) | none (local gateway mode: host stack) |
| natOutgoing | Subnet natOutgoing | policy reroute + node iptables | SNAT on `GR_<node>` |
| Zone | — | one NB/SB | one node (interconnect mode) |

ovn-kubernetes names come from the interconnect spike (ADR-0005) and upstream conventions; ovn-kubernetes has no adapter in `kvovn-topo` yet.
