# ovn-topology

A read-only network topology visualizer for OVN as deployed by Kube-OVN on Rancher-managed RKE2 and Harvester clusters. On OpenShift (ovn-kubernetes, interconnect mode), the TUI shows one node at a time, live (`--diag --zone`) or from a capture. An in-cluster daemon there is proposed in [ADR-0005](docs/adr/0005-ovnk-interconnect.md).

It consists of:
- **`ovn-topologyd`**: Go daemon that connects to OVN NB and SB databases, maintains an in-memory network graph, and serves it over HTTP JSON and SSE.
- **`ovn-topo`**: Terminal UI (Bubble Tea) that renders an L3 topology tree, interconnect details, and port matrices directly in your terminal.
- **`ovn-topo mcp`**: Model Context Protocol (MCP) server that lets AI agents (OpenCode, Claude Code, agy) query the network graph.

---

## Connection Architecture

All clients (`ovn-topo` TUI and `ovn-topo mcp`) connect through the standard Kubernetes service proxy. No port-forwards, public IPs, or Ingresses are needed:

```text
  Downstream Cluster (RKE2 / Harvester)
  ┌────────────────────────────────────────────────────────────┐
  │  OVN Northbound (6641)        OVN Southbound (6642)        │
  │           ▲                              ▲                 │
  │           │ OVSDB push monitor           │ OVSDB push      │
  │           │ (leader_only=false)          │ monitor         │
  │  ┌────────┴──────────────────────────────┴──────────────┐  │
  │  │ ovn-topologyd                                        │  │
  │  │   • In-memory joined graph (atomic snapshots)        │  │
  │  │   • K8s NodeMapper (chassis <-> node)                │  │
  │  │   • HTTP REST + SSE API (ClusterIP :8080)            │  │
  │  └──────────────────────────▲───────────────────────────┘  │
  │                             │ service proxy                │
  │  ┌──────────────────────────┴───────────────────────────┐  │
  │  │ Kubernetes API server: authn + services/proxy RBAC   │  │
  │  └──────────────────────────▲───────────────────────────┘  │
  └─────────────────────────────┼──────────────────────────────┘
                                │ kubeconfig (Rancher /k8s/clusters/<id>
                                │ or the Harvester / RKE2 API, :6443)
          ┌─────────────────────┼─────────────────────┐
          │                     │                     │
   ovn-topo (TUI)        ovn-topo mcp         Rancher UI Extension
   (Bubble Tea)          (AI agents, stdio)   (paused)
```

Clients query the Service `kube-system/ovn-topologyd` using standard `kubeconfig` credentials. The API server handles authentication and checks `services/proxy` RBAC.

For more details on security design, see the [API & Security Reference](docs/api.md).

---

## Installation

### 1. In-Cluster Daemon (`ovn-topologyd`)
Deploy the daemon to downstream clusters running Kube-OVN using the Helm chart:

```bash
helm repo add coulof https://coulof.github.io/ovn-topology
helm install ovn-topology coulof/ovn-topology \
  --namespace kube-system \
  --set ovn.nbEndpoints="tcp:ovn-nb.kube-system.svc.cluster.local:6641" \
  --set ovn.sbEndpoints="tcp:ovn-sb.kube-system.svc.cluster.local:6642"
```

For more configuration details, see the [Chart Documentation](charts/ovn-topology/README.md#installation).

**OpenShift (ovn-kubernetes, interconnect mode):** there is no daemon yet ([ADR-0005](docs/adr/0005-ovnk-interconnect.md)); the TUI works per node:
- live: `ovn-topo --diag --zone <node>` (Tier 1, `pods/exec`);
- offline: capture with `KUBECTL=oc hack/capture-fixtures.sh <name> --flavor ovnk`, then `ovn-topo --fixture-dir testdata/scenarios/<name>/zone-<node>`.

### 2. Client CLI (`ovn-topo`)
Download the precompiled static binary for your architecture from [GitHub Releases](https://github.com/coulof/ovn-topology/releases) and place it on your `PATH`.

---

## Terminal UI (`ovn-topo`)

To run the TUI, point it to your cluster's kubeconfig:

```bash
export KUBECONFIG=~/.kube/hv-01.yaml
ovn-topo
```

### Controls & Navigation
- **`1` – `4`**: Switch views:
  - **`1`** (or `:xray vpc`): L3 View (VPCs, Routers, Subnets)
  - **`2`** (or `:xray chassis`): OVN Chassis bindings and encap IPs
  - **`3`** (or `:xray workload`): Kubernetes workloads (Pods, VMs, Namespaces)
  - **`4`** (or `:ports`): Router port matrix
- **`j`/`k`**, **`h`/`l`**, **`Space`**: Navigate, fold/unfold, and toggle tree items.
- **`e`/`E`**: Expand or fold all nodes.
- **`Enter`**: Follow a relation (e.g. hop from a port to its connected switch).
- **`b`**: Go back in navigation history.
- **`i`**: Toggle the right-side **Details Pane** (shows IP/MAC, active gateways, peers, and subnets).
- **`/`**: Filter the tree. Keeps ancestors of matched items.
- **`d`**: Describe selected object.
- **`y`**: View raw OVSDB columns.
- **`r`**: Force refresh.
- **`L`**: Toggle live SSE updates.
- **`t`**: Cycle UI themes (e.g., `--theme=suse`, `dracula`, `nord`, `catppuccin`).
- **`?`**: Help overlay.
- **`q`**: Quit.

### Tier 1 Diagnostics (`x` key)
Launch the TUI with `--diag` to enable interactive diagnostic executions directly on cluster pods. `ovn-topo` detects the CNI from the pods it finds: `ovn-central`, `kube-ovn-cni` and `ovs-ovn` in `kube-system` for Kube-OVN, else `ovnkube-node` for ovn-kubernetes. `--k8s-namespace` pins the namespace.

```bash
ovn-topo --diag
```

From any supported object in the tree, press **`x`** to run:
1. **`ovn-trace`**: Run point-and-click packet simulations to trace routing, ACLs, and NAT.
2. **Conntrack dump**: View connection tracking tables on the active chassis node.
3. **OVS interfaces**: View OVS port bindings and statuses via `ovs-vsctl`.
4. **Controller logs**: Tail logs from `ovn-controller` on the host node.

See the complete [TUI Guide](docs/tui-guide.md) for screenshots and walkthroughs.

### OpenShift: one node at a time (`--zone`)
On ovn-kubernetes in interconnect mode (OpenShift 4.14+), `--zone <node>` shows one node's OVN live without a daemon. `ovn-topo` reads that node's NB/SB unix sockets through a `pods/exec` stream to `socat` in its `ovnkube-node` pod. It runs libovsdb monitors over that stream and serves the graph to itself on loopback. `--zone list` prints the nodes:

```bash
ovn-topo --diag --zone list
ovn-topo --diag --zone <node>
```

The `x` diagnostics work there too. Each action runs in the `ovnkube-node` pod of the selected object's own node, whatever `--zone` shows:
- `ovn-trace` runs in `sbdb`;
- OVS, conntrack and tcpdump run in `ovn-controller`;
- the logs come from `ovn-controller`'s container log.

A trace covers only its node's zone: hops past `transit_switch` are not traced.

---

## Model Context Protocol (MCP)

`ovn-topo` contains an MCP server (`ovn-topo mcp`) that communicates with AI agents over stdio. 

### Available Tools
- `find`: Search routers, switches, ports, and chassis by name, IP, MAC, namespace, pod, or VM.
- `get_graph`: Retrieve the L3, L2, or chassis topology graph.
- `get_node`: Show full detail of one node, including OVSDB columns and Kubernetes identity.
- `list_chassis`: List OVN chassis with binding counts and gateway roles.

### Tool Setup

To configure your agent, use the corresponding snippet below. Ensure `ovn-topo` is on your `PATH`, or use an absolute path.

#### 1. OpenCode
Add this to your `opencode.json` (or `~/.config/opencode/opencode.json`):

```json
{
  "mcp": {
    "ovn-topology": {
      "type": "local",
      "command": ["ovn-topo", "mcp"],
      "enabled": false
    }
  }
}
```

#### 2. Claude Code
Run the CLI command:
```bash
claude mcp add ovn-topology -- ovn-topo mcp
```

#### 3. agy
Run the CLI command:
```bash
agy mcp add ovn-topology -- ovn-topo mcp
```

For backends, result limits, and offline/fixture mode setups, see [MCP Guide](docs/mcp.md) and [MCP Prompt Guide](docs/mcp-prompts.md).

---

## Documentation Directory

The `docs/` directory contains full technical and operational details:

- [TUI Guide](docs/tui-guide.md) – Step-by-step UI instructions and screenshots.
- [MCP Server Guide](docs/mcp.md) – Detailed tool descriptions, caps, and backend modes.
- [MCP Prompt Guide](docs/mcp-prompts.md) – Pre-written troubleshooting prompts for AI sessions.
- [Development Guide](docs/development.md) – Compiling, testing, local daemon proxy, and release packaging.
- [API & Security Reference](docs/api.md) – REST and SSE endpoint details, service proxy schemas, and safety guarantees.
- [Architecture Decision Records (ADRs)](docs/adr/) – ADRs covering privilege tiers, history stores, the MCP server, CNI flavors and ovn-kubernetes interconnect.
