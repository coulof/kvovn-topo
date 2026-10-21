# OVN Topology TUI Guide (`ovn-topo`)

`ovn-topo` is a k9s-style terminal explorer for the topology served by `ovn-topologyd`. You navigate **xray trees** (objects and what they contain) and a **`:ports` table** (the router-port matrix), follow **relations** (`⇢`) between objects, and run Tier 1 diagnostics on whatever is selected.

```bash
ovn-topo --daemon-url http://127.0.0.1:8080/api/v1   # live daemon (mise run tui)
ovn-topo --fixture                                   # bundled dataset, offline (run from the repo root)
ovn-topo --diag                                      # + Tier 1 exec diagnostics (ADR-0001)
```

---

## 1. Screen layout

```text
 Daemon:   http://127.0.0.1:8080/api/v1        <:>     command         <e/E>   expand/fold     <r>  refresh
 Revision: 1842  ● live                        </>     filter          <i>     details pane    <L>  live on/off
 Topology: 2 VPC · 4 LS · 412 LSP · 3 chassis  <1-4>   vpc/ch/wl/ports <d>     describe        <t>  theme
 Diag:     DIAG ready                          <h/l>   fold/unfold     <y>     raw             <?>  help
 Theme:    SUSE                                <enter> toggle/goto     <b>     back            <q>  quit
┌──────────────────────── Xray(vpc)[18] ─────────────────────────┐┌───────── Details lr:lr-default-uuid ─────────┐
│ ▾      VPCS (2)                                                ││ ● LR   ovn_cluster                           │
│ ├─ ▾ ● VPC  default  1 router · 1 switch [default]             ││   ports 3 · routes 1 · nat 0 · pol 1 · lb 1  │
│ │  ├─ ▸ ● LR   ovn_cluster  ports 3 · routes 1 · nat 0 · pol 1 ││                                              │
│ │  └─ ▸ ! LS   default-subnet  10.244.0.1/24 · 412 ports · 1 u…││ Ports & interconnections (3)                 │
│ └─ ▾ ● VPC  custom-vpc  1 router · 1 switch                    ││ • lrp-default  10.244.0.1/24                 │
│    ├─ ▸ ● LR   custom-vpc  ports 2 · routes 0 · nat 1 · pol 0  ││     → LS   default-subnet                    │
│    └─ ▸ ● LS   custom-subnet  10.100.0.1/24 · 2 ports · 1 bound││ • lrp-gw                                     │
│ ▾      SHARED & STANDALONE SWITCHES (2)                        ││     Active GW on node-1                      │
│ └─ ▾ ● LS   join  100.64.0.2/16, 100.64.0.1/16 [shared by 2 r…]││ • lrp-join-def  100.64.0.1/16                │
│    ├╌ router ⇢ LR   custom-vpc                                 ││     → LS   join  (shared interconnect)       │
│    └╌ router ⇢ LR   ovn_cluster                                ││ L3  Routes 1 · Policies 1 · NAT 0 · LBs 1    │
└────────────────────────────────────────────────────────────────┘└──────────────────────────────────────────────┘
 xray  vpc  lr:lr-default-uuid
```

- **Header (5 rows):** daemon, revision and live state (`● live` over SSE, `◌ polling` when the stream is unavailable, `‖ paused`, `○ offline fixture`), topology totals, diagnostics state, theme, and the hotkeys for the current screen. Terminals shorter than 18 rows get a one-line header.
- **Main box:** title shows the screen (`Xray(vpc)`, `Ports(lrp)`, `Describe(lsp)`…), the row count and the active filter (`</pod>`).
- **Details pane (right, 100+ columns):** explains the selected object and how it interconnects (see §2). `i` hides/shows it.
- **Crumbs (bottom row):** `xray`/`table` › current root › selected object ID, and on the right any message (jump targets, errors, refresh failures).

Every frame is laid out to exactly the terminal size: nothing scrolls off the top, long lines are truncated with `…`, and lists scroll to keep the cursor visible.

---

## 2. Xray trees — `1` `2` `3`

| Key | Root | Tree |
|---|---|---|
| `1` / `:xray vpc` | VPCs, shared & standalone switches, external networks | VPC → LR → LRP · VPC → LS → LSP |
| `2` / `:xray chassis` | chassis | chassis → LSPs bound there (+ active gateway roles) |
| `3` / `:xray workload` | Kubernetes namespaces | namespace → VM / Pod / Node → its NICs (LSPs) |

- **Rows:** fold marker (`▾`/`▸`), health glyph, kind tag, name, a dim summary (CIDR, IP, `@chassis`, counts) and a badge (`[unbound]`, `[GW active hv-01]`, `[shared by 2 routers]`).
- **Relations (`├╌ label ⇢ …`)** appear when you unfold an object. They are graph edges, not ownership: `bound on` (chassis), `workload`, `router port`, `peer switch`, `peer port`, `gateway (active)`, `localnet`, `router` (for shared switches). Relations already visible as the parent or a sibling are not repeated.
- **`Enter` on a relation jumps** to that object in the xray where it lives (a chassis opens the chassis xray, a workload the workload xray), expanding the path to it. **`b`** returns to the exact row you left.
- **Two levels start open:** in the VPC xray you see each VPC's routers and switches with their summaries, folded; router ports, switch ports and relations appear when you unfold (`l`/`Space`). Large switches/chassis/namespaces (more than 16 children) start folded even at the top. `e`/`E` unfolds/folds everything visible (repeat to go deeper).
- A multi-NIC KubeVirt VM is one workload row whose children are its NICs on different switches.

### Details pane

On terminals at least 100 columns wide, the right pane describes the selection (on a `⇢` row: the target, i.e. where `Enter` goes). With the pane shown, port rows drop their inline summary — the pane carries it.

| Selection | Pane |
|---|---|
| Router | **Ports & interconnections**: each LRP with networks, peer switch (`shared interconnect`), MAC, active gateway · **L3** counts (routes, policies, NAT, LBs) + the NAT trap note. The route, policy and NAT rows themselves are planned (ROADMAP M2, follow-up "LR routes, policies & NAT"); see §7 meanwhile |
| Router port | Router/VPC, networks, MAC, peer switch + summary, peer LSP, routers on that switch, gateway, role, **routing** (distributed vs centralized) and what the port is for |
| Switch | **Connected routers** (router, VPC, via which LRP and network) · port counts · localnet underlay |
| Switch port | Type, IP, MAC, workload, bound chassis (or **unbound**) · **Path out**: `LSP → LS → LRP gw → LR (VPC)` |
| Chassis | Hostname, node, encaps, bound ports, **active gateway for** |
| Workload | Kind, namespace, node, each NIC with IP, switch and chassis |
| VPC | Contents and **egress** gateways |

Every pane ends with the Kubernetes identity and the object's relations (`label ⇢ object`).

### Health glyphs

| Glyph | Meaning |
|---|---|
| `●` (green) | OK: port bound, switch fully bound, chassis present |
| `!` (yellow) | Degraded: switch with unbound ports, disabled port |
| `✗` (red) | Unbound port (no `Port_Binding.chassis`) or a workload with such a NIC |
| `·` (dim) | Infrastructure port (`router`, `localnet`, …) — never reported as unbound |

Shapes and colours both carry the state, so it stays readable without colour.

---

## 3. Router-port table — `4` / `:ports`

The Port Matrix as a resource table: one row per logical router port, including gateway ports known only from the router's properties.

- **Columns:** VPC, router port, networks, peer switch (`*` = shared by more than one router), MAC, gateway (`distributed` or `active <chassis>`), role (`subnet gateway`, `node transit (join)`, `interconnect`, `external gateway`). On narrow terminals MAC, then ROLE, then NETWORKS are hidden instead of wrapping.
- **`s` / `S`** cycles the sort column / reverses it (the header shows `↑`/`↓`). **`/`** filters across all columns.
- **Inspector (bottom):** router and VPC, peer switch and its summary, peer LSP, routers on that switch, gateway, the routing mode (distributed vs centralized) and a one-line explanation of the port's role. It docks when the terminal is tall enough; **`i`** toggles it.
- **`Enter`** opens the port in the VPC xray, **`b`** comes back to the same table row. **`d`** / **`y`** describe it.

---

## 4. Describe & raw — `d` / `y`

`d` opens everything known about the selected object: graph properties, Kubernetes identity (namespace, owner, VMI, node, NAD), its relations and children, plus the daemon's `/node/{id}` detail (external_ids, related objects). `y` toggles the raw OVSDB columns. `j`/`k` scroll, `Esc` returns.

---

## 5. Filter, commands, live updates

- **`/text`**: substring filter on name, summary and badge. Matching rows are shown with all their ancestors, so you keep the context (`/pod-2` shows `VPCs › default › default-subnet › app-pod-2.default`). `Esc` clears it.
- **`:`**: command prompt with `Tab` completion — `xray vpc|chassis|workload`, `ports` (`lrp`, `matrix`), `theme <name>`, `quit`.
- **Live updates:** the TUI subscribes to `/events` (SSE). Each event refetches the L3 graph and only the switches whose ports changed (falling back to all switches for unknown IDs). If the stream breaks it polls `/graph` every 10 s with `If-None-Match` and keeps retrying SSE. Rows whose content changed flash for 3 s. `L` pauses/resumes; `r` forces a full reload.
- **Ports are fetched completely:** L2 pages (200 ports each) are followed until the last one.

---

## 6. Tier 1 Diagnostics — `x` (requires `--diag`)

`x` opens the diagnostics modal for the selection: an LSP (or a workload, via its first NIC), a switch, a router, a router port or a chassis. Groups, namespaces, VPCs and external networks are not diagnosable and `x` says so. Without `--diag`, or when RBAC denies `pods/exec`, `x` explains why instead of failing.

- **`[1] ovn-trace`** — destination picker with fzf-style search, custom IP (`10.144.97.201` or `10.144.97.201:22`), category tabs (`Tab`: All / VM / Pod / Gateway / Node / Public DNS) and protocol presets (`Ctrl+P`, `F1`–`F5`: ICMP, TCP 22/80/443, UDP 53). The microflow starts at the **selected** port (inport, MAC, IP); traffic leaving the subnet gets the router's gateway MAC as `eth.dst`. Output: verdict (`DELIVERED` / `DROPPED`), hop-by-hop path, evaluated pipeline stages; `o` toggles raw output.
- **`[2]` live tcpdump** on the port's dedicated host veth (`<hash>_h`; `ovn0` for node ports, the external bridge for gateways), with presets or a custom BPF filter. `Space`/`Esc` stops the capture.
- **`[3]` filtered conntrack** for the port IP · **`[4]` OVS port interface** (link state, ofport, MTU, counters) · **`[5]` `ovs-vsctl show`** on the hosting node · **`[6]` `ovn-controller` logs** (chassis UUID → node name resolved).

Commands come from the typed allowlist in `pkg/diag`; nothing is built from free-form strings.

### What `x` acts on

The target is captured when you press `x` and shown at the top of the modal: the object (kind, name, ID), its datapath, IP/MAC, and the node with **where that node comes from**. Every action runs on exactly that target. Nothing is borrowed from another object; what cannot be derived from the selection is listed as *unavailable* with the reason.

| Selection | Trace from | Node actions run on | Unavailable |
|---|---|---|---|
| Workload port (LSP) | the port itself (its switch, IP, MAC) | its **Port_Binding chassis** | node actions if the port is unbound |
| Infrastructure port (router / localnet) | the port, if it has an IP | — | tcpdump, conntrack, OVS port, bridges, logs |
| Router port (LRP) | the router datapath, from this port | the **active gateway chassis** (centralized); tcpdump on `br-external` | OVS port; all node actions if distributed |
| Chassis | — | that chassis | trace, tcpdump, OVS port |
| Workload (VM / Pod) | its NIC; **with several NICs you pick one first** | that NIC's chassis | as for its NIC |
| Switch, router, VPC, namespace | — | — | everything (select a port or a chassis) |

- **Kubernetes vs OVN node:** if Kubernetes reports a different node than OVN's binding (e.g. during live migration), the modal warns and uses the OVN binding.
- **Live updates while the modal is open:** they re-check the target by ID. If it disappears the modal says so and disables the actions. If it moves to another chassis you are told, and the node is updated.
- **tcpdump and OVS port** look up the interface whose `external_ids:iface-id` is the port, on the port's node. If none exists, the action fails with that message: it never captures on `ovn0`, `br-int` or another node instead.

---

## 7. Known traps (by design, also shown in `?` help)

- **VPC NAT Gateway (`Iptables*`) NAT is iptables inside a pod**, not OVN NAT — it never appears in the NB `NAT` table or in the router's `nat` count.
- **Default-VPC `natOutgoing` in centralized mode** is an LR policy reroute plus node iptables, not NB `NAT`.
- **Routes, policies and NAT rows are not listed yet**: the pane and `d` show counts only, and `y` on a router shows just the row UUIDs. To see the rows:
  ```bash
  kubectl ko nbctl lr-route-list <router>     # static routes (prefix, nexthop, output port, policy)
  kubectl ko nbctl lr-policy-list <router>    # policies by priority
  kubectl ko nbctl lr-nat-list <router>       # SNAT / DNAT / FIP
  ```
  On the default VPC, most policies are Kube-OVN's subnet `allow` rules plus per-node `reroute` rules to node join IPs (`100.64.x.x`).
- **A switch can attach to several routers** (e.g. `join`, or an external switch shared by VPCs): it is listed under *Shared & standalone switches* with `[shared by N routers]` and `router ⇢` relations to each.

---

## 8. Screenshot catalog

Capture these to document every core OVN / Kube-OVN concept:

1. **Compound VPC** — `1`, a VPC unfolded: LR with its LRPs and the VPC's switches.
2. **Multi-VPC shared switch** — `1`, *Shared & standalone switches*, `join` with `[shared by 2 routers]` and its `router ⇢` relations.
3. **Centralized HA gateway & EIP** — `4`, a row with `active <chassis>`; inspector showing *Centralized* routing and the `cr-` redirect port.
4. **Distributed subnet gateway** — `4`, a subnet-gateway row; inspector *Distributed* routing and the default-gateway sentence.
5. **External underlay (localnet)** — `1`, *External networks*, the provider network with its `localnet from ⇢` switch.
6. **Port relations & jump** — `1`, an LSP unfolded showing `bound on ⇢ CH` and `workload ⇢`, then `Enter` on the chassis relation (chassis xray, crumbs message).
7. **Chassis view** — `2`, chassis with bound ports and `active gateway for ⇢ LRP`.
8. **Multi-NIC VM** — `3`, a VirtualMachine with its NICs on two switches.
9. **Describe & raw** — `d` then `y` on an LSP: properties, Kubernetes identity, external_ids, raw columns.
10. **Tier 1 `ovn-trace` delivered / dropped** — `x` → `1` from a workload port: verdict, hop-by-hop path, stages.
11. **Live tcpdump on the dedicated veth** — `x` → `2`: header `Listening on interface '<hash>_h'` and packets.
12. **`ovs-vsctl show`** — `x` → `5`: bridges, tunnels and BFD, with the line counter.
