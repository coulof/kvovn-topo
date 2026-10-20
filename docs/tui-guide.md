# OVN Topology TUI Guide (`ovn-topo`)

This guide explains how to navigate and interpret the `ovn-topo` terminal visualizer, read network topology relationships, and capture screenshots covering all core OVN and Kube-OVN architectural concepts.

---

## 1. Views & Navigation

The TUI provides four operational views, switchable via number keys **`1` - `4`** or the bottom navigation bar:

### View 1: Topology Tree (`1. L3 Tree`)
Hierarchical layout grouped by VPC boundaries:
- **Compound VPC Box**: Groups a Logical Router (`LR`) and its attached Logical Switches (`LS`).
- **Branch Port Indicators**:
  - `├──` / `└──`: Logical Router Port (LRP) connecting the router to an internal subnet or transit network.
  - `[GW IP: 10.54.0.1/16]`: Distributed default gateway interface for pods and VMs in that subnet.
  - `[Transit IP: 100.64.0.1/16]`: Multi-VPC interconnection interface.
  - `[EIP: 10.144.124.13/27] [GW: hv-01]`: Centralized external gateway interface (External IP) active on the designated chassis node (highlighted in Persimmon `#fe7c3f`).
- **Workload Expansion (`Space` / `Enter`)**: Subnets are collapsed by default. Press **`Space`** on any switch to toggle its workload pods and KubeVirt VMs.
- **Interconnects Box**: Displays shared switches (such as `join`) that attach to multiple Logical Routers across distinct VPCs.
- **External Provider Underlays Box**: Displays provider networks attached via `localnet` switch ports to physical bridges.
- **Side Panel (Right)**: Shows full metadata of the currently highlighted entity (IDs, route counts, NAT rules, policy counts, bound/unbound port tallies, and Kubernetes annotations).

### View 2: L2 Subnets Explorer (`2. L2 Subnets`)
Interactive dual-pane broadcast domain explorer:
- **Left Pane (Subnets)**: Scroll through all subnets in the cluster with bound/unbound port counts.
- **Right Pane (Workload Cards)**: Press **`Tab`**, **`→`**, or **`Enter`** to move focus into the workload cards for that subnet.
  - Scroll through specific VMs and Pods with **`j` / `k`** or **`↑` / `↓`**.
  - Each card shows port name, IP/MAC, hosting cluster node, and KubeVirt VMI identity.
  - Press **`x`** on any card to run diagnostics directly against that specific workload!
  - Press **`←`** or **`Tab`** to return focus to the Subnets list.

### View 3: Chassis Explorer (`3. Chassis`)
Split-pane physical node and tunnel inspector:
- **Left Pane (Nodes)**: List of physical cluster nodes (`hv-01` .. `hv-04`) with total OVN port bindings.
- **Right Pane (Details)**: Encap tunnel IP, tunnel type (`geneve`/`vxlan`), active gateway roles, and chassis UUID.

### View 4: Port Matrix (`4. Port Matrix`)
Tabular inventory of all L3 interconnects in the cluster:
- **Columns**: `VPC/ROUTER`, `PORT (LRP)`, `INTERFACE IP (CIDR)`, `TARGET SWITCH`, `MAC`, `GATEWAY STATUS`.
- **Shared Indicator (`*`)**: Marks switches attached to more than one Logical Router.
- **Port Inspector Box (Bottom)**: Detailed inspection card for the highlighted router port.

---

## 2. k9s-Style Bottom Navigation Dock

`ovn-topo` features an uncluttered top header and a dedicated 2-row bottom status and hotkey bar:

```text
───────────────────────────────────────────────────────────────────────────────────────────────────────
 ● Connected to http://127.0.0.1:8080 │ Rev 8 │ 12 nodes, 7 edges │ Theme: [suse]
 <1-4> Views  <Space> Expand  <Tab/←/→> Pane  <x> Diag  <t> Theme  <j/k/↑/↓> Nav  <r> Refresh  <q> Quit
```
- **Row 1**: Real-time connection indicator, daemon URL, topology revision, graph totals, and current active color theme.
- **Row 2**: Context-sensitive hotkey badges formatted with theme accents.

---

## 3. Visual Palette & Semantic Colors

| Element | Color | Role |
|---|---|---|
| **Gateways, EIPs & Alerts** | **Persimmon** (`#fe7c3f`) | Centralized HA gateways, EIP interfaces, active chassis roles |
| **CIDRs & Network IPs** | **Mint** / Cyan (`#90ebcd`) | Subnet IP prefixes, interface IPs, and transit ranges |
| **Router Ports (LRPs)** | **Waterhole** / Blue (`#2453ff`) | LRP and LSP port identifier badges |
| **Bound / Healthy Ports** | **Jungle** / Green (`#30ba78`) | Bound ports, delivered packet trace verdicts, active statuses |
| **Unbound Ports & Errors** | **Red** (`#ff5555`) | Unbound logical ports, dropped packets, connection errors |
| **Compound VPC Boxes** | **Primary Theme Accent** | VPC container boundary and header banners |

---

## 4. Tier 1 Diagnostics (`x` key)

When started with `--diag`, pressing **`x`** opens the diagnostics menu for the currently highlighted entity:

### `[1] ovn-trace` (Interactive Simulation)
Opens the Destination Picker Dialog:
- **Search & Custom IP Bar**: Type any text to filter candidates in real-time, or type an arbitrary IP address (e.g. `10.144.97.201` or `10.144.97.201:22`) to trace to custom endpoints outside OVN!
- **Category Tabs**: Press **`Tab`** to filter by `[All]`, `[VM]`, `[Pod]`, `[Gateway]`, `[Node]`, or `[Public DNS]`.
- **Protocol Selection**: Press **`Ctrl+P`** or **`F1` - `F5`** to toggle:
  - `[1]` ICMP (Ping)
  - `[2]` TCP 22 (SSH)
  - `[3]` TCP 80 (HTTP)
  - `[4]` TCP 443 (HTTPS)
  - `[5]` UDP 53 (DNS)
- **Automatic Gateway MAC**: When tracing from a VM to an external destination, `ovn-topo` automatically injects the subnet router's Gateway MAC (`eth.dst`), ensuring seamless traversal through logical routers to `localnet.external`.
- **Trace Output**: Visual **Hop-by-Hop Flow Path**, `DELIVERED` / `DROPPED` verdict, and pipeline stage inspector.

### `[2] - [4] Live tcpdump`
Real-time packet capture on the selected port's **dedicated host veth interface** (`<hash>_h` for VMs/Pods, `ovn0` for node ports, `br-external` for gateways). Includes safe 5-second execution timeout and `-l` line-buffering.

### `[5] Filtered Conntrack`
Connection tracking table filtered specifically for the active port IP, removing loopback and control-plane noise.

### `[6] OVS Physical Bridges & Ports`
Runs `ovs-vsctl show` on the hosting node, displaying bridges, port types (`vxlan`, `internal`, `patch`), tunnel endpoints, and BFD statuses.

### `[7] Tail Controller Logs`
Tails local `ovn-controller` logs on the target node with automatic chassis UUID $\rightarrow$ node name translation.

---

## 4. Recommended Screenshot Catalog

To build complete documentation, capture screenshots of the following 10 architectural scenarios:

### Screenshot 1: Compound VPC Container
- **View**: Topology Tree View (`Tab`).
- **Focus**: The `VPC: ovn-cluster` or custom VPC box.
- **Shows**: The logical router at the top (`▼ [Router] ovn_cluster`), its attached logical switches (`[Switch] default-subnet`), and its branch port tree (`├── (lrp-default) [GW IP: 10.54.0.1/16] ──► [Switch] default-subnet`).

### Screenshot 2: Multi-VPC Transit Switch (Interconnect)
- **View**: Topology Tree View (`Tab`).
- **Focus**: The `Interconnects (Multi-LR Subnets)` box.
- **Shows**: Switch `join` with multiple branch lines connecting into different VPC routers (e.g., `ovn-cluster` and `custom-vpc`).

### Screenshot 3: Centralized HA Gateway & EIPs
- **View**: Port Interconnection Matrix (`Tab`).
- **Focus**: A row with gateway role (e.g. `vpc-onewater-external`).
- **Shows**: Persimmon highlight `Active: hv-01`, gateway role `External EIP Gateway`, and the Port Inspector showing redirect port `cr-vpc-onewater-external`.

### Screenshot 4: Distributed Subnet Gateway (East-West)
- **View**: Port Interconnection Matrix (`Tab`).
- **Focus**: A standard workload subnet row (e.g. `subnet-onewater`).
- **Shows**: Distributed status, gateway IP, and Port Inspector role `Default Gateway for subnet-onewater`.

### Screenshot 5: External Provider Underlay (Localnet / VLAN)
- **View**: Topology Tree View (`Tab`).
- **Focus**: `External Provider Underlays` box.
- **Shows**: `[EXT] provider-ext` or `external` switch mapped to physical NIC bridge `br-external`.

### Screenshot 6: Port Interconnection Inspector
- **View**: Port Interconnection Matrix (`Tab`).
- **Focus**: Bottom inspection box for an interconnect or gateway port.
- **Shows**: MAC address, subnet prefix, interface IP, peer LSP name, routing mode, and interconnected peer routers list.

### Screenshot 7: Tier 1 `ovn-trace` Simulation (Delivered Flow)
- **View**: Diagnostics Modal (`x` ➔ `1`).
- **Focus**: Trace result for an inter-VPC or pod-to-gateway flow.
- **Shows**: Simulation endpoints (Source, Destination, Protocol), green `DELIVERED` badge, visual Hop-by-Hop Flow Path (`ovn-default ──► ovn-cluster ──► join ──► Port 'node-hv-01'`), and evaluated pipeline stages.

### Screenshot 8: Tier 1 `ovn-trace` (Dropped by ACL)
- **View**: Diagnostics Modal (`x` ➔ `1`).
- **Focus**: Trace hitting a deny ACL rule or unroutable destination.
- **Shows**: Red `DROPPED` badge, summary `Packet dropped by rule or ACL`, and the specific ACL table entry evaluated.

### Screenshot 9: Live `tcpdump` on Dedicated Host VETH
- **View**: Diagnostics Modal (`x` ➔ `2` or `4`).
- **Focus**: Live capture on workload port (e.g., `vm-onewater`).
- **Shows**: Header `Captured on dedicated interface '4e3b7_37a8eec_h' (port: vm-onewater on node hv-03)` and packet summaries.

### Screenshot 10: OVS Physical Bridges & Tunnels (`ovs-vsctl show`)
- **View**: Diagnostics Modal (`x` ➔ `6`).
- **Focus**: Physical interface inspection.
- **Shows**: Indented bridge tree showing `br-int`, `br-external`, tunnel endpoints (`vxlan/geneve`, `remote_ip`), and BFD session states, with the line counter `[Lines 1-18 of 45 | ↑/↓ or j/k: scroll]`.
