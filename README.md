# ovn-topology

A read-only network topology visualizer for OVN as deployed by Kube-OVN on Rancher-managed RKE2 and Harvester clusters. On OpenShift (ovn-kubernetes), the TUI shows one node at a time.

It consists of:
- **`ovn-topologyd`**: a Go daemon that monitors the OVN NB and SB databases, joins them into an in-memory graph, and serves it over HTTP JSON and SSE.
- **`ovn-topo`**: a k9s-style terminal UI over the same graph.
- **`ovn-topo mcp`**: an MCP server, so AI agents (OpenCode, Claude Code, agy) can query the graph read-only.

---

## Connection Architecture

The TUI and `ovn-topo mcp` reach the daemon through the Kubernetes API server's service proxy. No port-forward, public IP or Ingress is needed:

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

The API server authenticates the kubeconfig user and checks `services/proxy` RBAC on `kube-system/ovn-topologyd`. The security design is in the [API & Security Reference](docs/api.md).

---

## Installation

### 1. In-cluster daemon (`ovn-topologyd`), RKE2 and Harvester

```bash
helm repo add coulof https://coulof.github.io/ovn-topology
helm install ovn-topology coulof/ovn-topology --namespace kube-system
```

TLS, endpoints and viewer RBAC are in the [chart README](charts/ovn-topology/README.md#installation).

### 2. Client (`ovn-topo`)

Download the binary for your platform from [GitHub Releases](https://github.com/coulof/ovn-topology/releases) (Linux amd64, macOS arm64, Windows amd64) and put it on your `PATH`.

---

## Terminal UI (`ovn-topo`)

```bash
export KUBECONFIG=~/.kube/hv-01.yaml
ovn-topo            # passive (Tier 0)
ovn-topo --diag     # plus exec-based diagnostics (Tier 1, ADR-0001)
```

`1`–`4` switch between the VPC, chassis and workload xray trees and the router-port table. `Enter` follows a relation, `i` toggles the details pane, `d` describes the selection, `?` lists every key. The [TUI guide](docs/tui-guide.md) walks through use cases with screenshots, and its [§8](docs/tui-guide.md#8-keys) has the full key table.

With `--diag`, `x` opens diagnostics on the selection: `ovn-trace`, tcpdump, conntrack, OVS ports and bridges, flows and `ovn-controller` logs ([§6](docs/tui-guide.md#6-tier-1-diagnostics--x-requires---diag)). `!` runs a details-pane Inspect command such as `kubectl ko nbctl lr-route-list` with the TUI suspended. `ovn-topo` finds the diagnostic pods itself: `ovn-central`, `kube-ovn-cni` and `ovs-ovn` for Kube-OVN, else `ovnkube-node` for ovn-kubernetes. `--k8s-namespace` pins the namespace.

### OpenShift: one node at a time

OpenShift runs ovn-kubernetes in interconnect mode: one NB/SB per node and no central database, so there is no daemon yet ([ADR-0005](docs/adr/0005-ovnk-interconnect.md)). The TUI works per node, called a zone:

```bash
ovn-topo --diag --zone list       # the zones (nodes)
ovn-topo --diag --zone <node>     # live, through a pods/exec relay to the node's sockets
KUBECTL=oc hack/capture-fixtures.sh <name> --flavor ovnk
ovn-topo --fixture-dir testdata/scenarios/<name>/zone-<node>   # offline
```

`x` diagnostics run in the `ovnkube-node` pod of the selected object's node, whatever zone is shown. A trace stops at `transit_switch`: other zones are not traced.

---

## Model Context Protocol (MCP)

`ovn-topo mcp` serves Tier 0 tools to AI agents over stdio.

### Available Tools
- `find`: Search routers, switches, ports, and chassis by name, IP, MAC, namespace, pod, or VM.
- `get_graph`: Retrieve the L3, L2, or chassis topology graph.
- `get_node`: Show full detail of one node, including OVSDB columns and Kubernetes identity.
- `list_chassis`: List OVN chassis with binding counts and gateway roles.

### Tool Setup

`ovn-topo` must be on the agent's `PATH`, or use an absolute path.

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
```bash
claude mcp add ovn-topology -- ovn-topo mcp
```

#### 3. agy
```bash
agy mcp add ovn-topology -- ovn-topo mcp
```

Backends, result caps and offline use are in the [MCP guide](docs/mcp.md); ready-made prompts are in [docs/mcp-prompts.md](docs/mcp-prompts.md).

---

## Documentation

- [TUI guide](docs/tui-guide.md): use cases, screens and keys.
- [MCP guide](docs/mcp.md) and [MCP prompts](docs/mcp-prompts.md).
- [Development](docs/development.md): build, test, local daemon, releases.
- [API and security](docs/api.md): endpoints, service-proxy access, read-only guarantees.
- [ADRs](docs/adr/): privilege tiers, history, MCP server, CNI flavors, ovn-kubernetes interconnect.
