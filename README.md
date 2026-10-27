# ovn-topology

A read-only network topology visualizer for OVN as deployed by Kube-OVN on Rancher-managed RKE2 and Harvester clusters.

## Components

- **`ovn-topologyd`**: Go daemon that connects to OVN Northbound (NB) and Southbound (SB) databases over OVSDB, monitors updates, maintains a joined in-memory network graph, and serves it over HTTP JSON and SSE.
- **`ovn-topo`**: Terminal UI (Bubble Tea) that renders an L3 topology tree and an interconnection matrix of router ports directly in your terminal.
- **`ovn-topo mcp`**: Model Context Protocol (MCP) stdio server that lets AI agents (OpenCode, Claude Code, agy) query the same graph, read-only.
- **Rancher UI Extension**: paused; archived at tag `archive/ui-extension` (see below).
- **`charts/ovn-topology/`**: Helm chart deployed once per downstream cluster.

## Design and Security Rules

- **Strictly read-only**: OVSDB does not have a read-only user role. Read-only access is guaranteed in code and enforced during build by `golangci-lint` using `forbidigo`. The codebase contains no write operations (`Create`, `Update`, `Mutate`, `Delete`, `Transact`) against OVSDB or the Kubernetes API.
- **OVSDB is the source of truth**: The daemon does not read Kube-OVN CRDs (such as Vpc, Subnet, or IP). Kubernetes metadata comes from OVN NB `external_ids`. Kubernetes reads are get/list/watch on `Node`, `Pod`, `Service`, `EndpointSlice`, KubeVirt `VirtualMachine`/`VirtualMachineInstance` and `NetworkAttachmentDefinition`, used only to enrich graph nodes (node names, workload identity), never to create them.
- **Access control**: The daemon has no authentication of its own and is never exposed (no NodePort, LoadBalancer or Ingress). API access goes through the Kubernetes service proxy (`/api/v1/namespaces/<namespace>/services/http:ovn-topologyd:http/proxy/api/v1/...`), which authenticates the caller; authorization is standard `services/proxy` RBAC. The TUI and `ovn-topo mcp` use this path by default.
- **Air-gap friendly**: No runtime calls to the internet, no CDN dependencies, and non-root distroless container images.

## Architecture

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
  │  │   • HTTP REST + SSE API (ClusterIP :8080, no authn,  │  │
  │  │     never exposed: no NodePort / LB / Ingress)       │  │
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

All clients use the same path (`pkg/kubeproxy`): they find the daemon Service (`kube-system/ovn-topologyd`, else by the chart label) and call it through the API server's service proxy with the kubeconfig's credentials. No port-forward is needed.

## Quick Start

With the chart installed on the cluster, the TUI needs only a kubeconfig. It reaches the daemon through the Kubernetes service proxy (Rancher's `/k8s/clusters/<id>` or the API server directly), which enforces authentication and `services/proxy` RBAC. Nothing is exposed, and no port-forward is needed:

```bash
export KUBECONFIG=~/.kube/hv-01.yaml
mise run tui        # or: ./bin/ovn-topo
```

See [Accessing the API](charts/ovn-topology/README.md#accessing-the-api-harvester-and-rke2) for the URLs and the optional viewer role.

Check the in-cluster daemon's API from the shell, again through the service proxy:

```bash
mise run test-live              # readyz, version, graph, chassis, find
mise run api-find 10.244.0.10   # also api-ready, api-version, api-graph, api-chassis, api-events
```

### Local daemon development

Only a daemon you run on your laptop needs port-forwards: it must reach the cluster's OVN databases. The `[dev]` tasks in `mise tasks` cover this flow.

A plain `kubectl port-forward svc/ovn-nb` cannot do it. The ovn-central pods use `hostNetwork` and the databases listen on the node IP, while `port-forward` always connects to `localhost` inside the pod. So `mise run pf` first creates a small relay pod, `ovn-topology-proxy` in `kube-system`. It runs a Python TCP relay in the Kube-OVN image (set `PROXY_IMAGE` to match your version or mirror) that forwards 6641/6642 to the `ovn-nb`/`ovn-sb` Services, and `pf` port-forwards to that pod. It is the only thing any task creates in the cluster; remove it when you are done.

```bash
# 1. Forward OVN database ports to localhost
mise run pf

# 2. Run the daemon locally (terminal 1)
mise run daemon

# 3. Test the API or launch the TUI against it (terminal 2)
mise run test-local
mise run tui-local
```

When finished, stop the port-forwards and delete the relay pod:

```bash
mise run pf-proxy-remove   # runs pf-stop first
```

## TUI Controls (`ovn-topo`)

k9s-style navigation; see [`docs/tui-guide.md`](docs/tui-guide.md) for the full guide.

- **`1` - `4`** (or `:xray vpc`, `:xray chassis`, `:xray workload`, `:ports`): switch between the three xray trees and the router-port table.
- **`j`/`k`**, **`h`/`l`**, **`Space`**: move, fold/unfold, toggle. **`e`/`E`** expands/folds everything.
- **`Enter`** on a `⇢` relation (chassis, workload, peer port…) jumps to that object; **`b`** goes back.
- **`i`**: show/hide the right **details pane** (interconnections of the selection: router ports and peers, connected routers, path out of a subnet, gateways); the tree starts with two levels open.
- **`/`**: filter (keeps the ancestors of every match). **`d`** describe, **`y`** raw columns.
- **`x`**: open **Tier 1 Diagnostics** on the selection (interactive `ovn-trace` with fzf search & custom IP, live `tcpdump`, conntrack, `ovs-vsctl show`, controller logs).
- **`r`** refresh, **`L`** pause/resume live (SSE) updates, **`t`** cycle themes, **`?`** help, **`q`** quit.

Launch with Tier 1 diagnostics enabled:
```bash
mise run tui-diag
```

Launch with a preferred theme:
```bash
./bin/ovn-topo --theme=suse
./bin/ovn-topo --theme=dracula
./bin/ovn-topo --theme=nord
./bin/ovn-topo --theme=tokyonight
./bin/ovn-topo --theme=catppuccin
```

### Tier 1 Diagnostics Mode (ADR-0001)

Launch `ovn-topo` with `--diag` to enable read-only execution diagnostics in cluster pods (`ovn-central`, `kube-ovn-cni`, `ovs-ovn`). The exec uses the same kubeconfig and `--context` as the graph, so it always targets the cluster on screen:

```bash
# Launch with diagnostics enabled
./bin/ovn-topo --diag --kubeconfig=~/.kube/config --context harvester

# Or using mise
mise run tui-diag
```

Available diagnostic actions from the TUI (`x` key):
1. **`ovn-trace`**: point-and-click microflow packet trace against `ovn-central` to inspect routing, ACLs, and NAT stage-by-stage.
2. **Conntrack dump**: dump connection tracking entries on the active chassis node.
3. **OVS interfaces**: inspect physical interfaces and port bindings via `ovs-vsctl`.
4. **Controller logs**: tail `ovn-controller` logs on the target node.

If RBAC denies `pods/exec`, the TUI header shows the `DIAG` badge as degraded with the reason and falls back to passive mode without crashing.

## Model Context Protocol (MCP)

`ovn-topo` includes an [MCP](https://modelcontextprotocol.io/) server (`ovn-topo mcp`) that communicates over stdio using JSON-RPC 2.0. With no flags it finds the in-cluster `ovn-topologyd` Service (`kube-system/ovn-topologyd`, else by the chart label in any namespace) and reads it through the Kubernetes service proxy of your kubeconfig (`services/proxy` RBAC); it can also read a daemon URL, OVSDB directly, or a fixture ([backends](docs/mcp.md#backends)).

Every tool is read-only (Tier 0, ADR-0001): no writes, no `exec`, no packet capture. Object names and `external_ids` are returned as data only, never in the one-line `summary` ([design](docs/adr/0003-mcp-server.md)).

### Available MCP Tools

| Tool | Description | Key Parameters |
|---|---|---|
| `find` | Search routers, switches, ports and chassis by name, IP, MAC, namespace, pod, VM, chassis hostname or encap IP | `query`, `type` (LR, LS, LRP, LSP, Chassis), `limit`, `cursor` |
| `get_graph` | Topology graph for a view, optionally around one object | `view` (l3, l2, chassis), `focus`, `depth`, `cursor` |
| `get_node` | Full detail of one object: OVSDB columns, external_ids, Kubernetes identity, related objects | `id` |
| `list_chassis` | Chassis with encaps, node name, binding counts, gateway roles and HA priorities | `cursor` |

Path computation, ACLs, consistency checks and history tools arrive with milestones M4, M5 and M8 (see `ROADMAP.md`).

### Tool Setup

#### 1. OpenCode

Add this entry to the `mcp` object of `opencode.json` (or `~/.config/opencode/opencode.json`):

```jsonc
"ovn-topology": {
    "type": "local",
    "command": ["ovn-topo", "mcp"],
    "enabled": false
},
```

`"enabled": false` keeps the server off (and its tools out of the context) until you switch it on in OpenCode. With no flags, `ovn-topo mcp` uses the kubeconfig OpenCode inherits (`$KUBECONFIG`, else `~/.kube/config`) and reaches the daemon through the Kubernetes service proxy.

With a custom kubeconfig:

```jsonc
"ovn-topology": {
    "type": "local",
    "command": ["ovn-topo", "mcp", "--kubeconfig", "/path/to/kubeconfig"],
    "enabled": false
},
```

In a new file, the entry goes inside `{"$schema": "https://opencode.ai/config.json", "mcp": { … }}`.

#### 2. Claude Code

Add `ovn-topology` using the CLI:

```bash
claude mcp add ovn-topology -- ovn-topo mcp

# With custom kubeconfig
claude mcp add ovn-topology -- ovn-topo mcp --kubeconfig /path/to/kubeconfig
```

Or configure via `.mcp.json` in your repository root:

```json
{
  "mcpServers": {
    "ovn-topology": {
      "command": "ovn-topo",
      "args": ["mcp"]
    }
  }
}
```

#### 3. agy

Add `ovn-topology` using `agy mcp`:

```bash
agy mcp add ovn-topology ovn-topo mcp

# With custom kubeconfig
agy mcp add ovn-topology -- ovn-topo mcp --kubeconfig /path/to/kubeconfig
```

Check a setup with `mise run mcp-offline` (fixture) or `mise run mcp-live` (cluster). For ready-to-use troubleshooting prompts, see the [MCP Prompt Guide](docs/mcp-prompts.md); for backends, result format and caps, see [docs/mcp.md](docs/mcp.md).

## Rancher UI Extension (paused)

The Rancher/Harvester UI extension is paused. Its code is archived at tag `archive/ui-extension` (`git checkout archive/ui-extension -- ui`). The daemon API stays usable by any client.

## Release Artifacts

Every version tag (`vX.Y.Z`) publishes:

| Artifact | Registry Path | Description |
|---|---|---|
| **Daemon Image** | `ghcr.io/coulof/ovn-topologyd:X.Y.Z` | `linux/amd64` distroless daemon |
| **Daemon Chart** | `oci://ghcr.io/coulof/charts/ovn-topology:X.Y.Z` | OCI Helm chart for downstream clusters |
| **Binaries** | GitHub Releases | Static `ovn-topologyd` + `ovn-topo` archives for Linux (amd64) and macOS (arm64); `ovn-topo` zip for Windows (amd64) |

### Air-Gapped Environments (Hauler)

Each release includes a `hauler.yaml` manifest bundling the daemon image and Helm chart:

```bash
# Sync release artifacts into a local Hauler store
hauler store sync -f hauler.yaml

# Copy artifacts to an internal air-gapped registry
hauler store copy registry://registry.internal.example.com
```

## Development

Prerequisites: Go 1.27+, `golangci-lint`, `make` (or `mise install`, which pins both).

```bash
make generate    # Regenerate OVSDB models and OpenAPI Go types
make lint        # Run golangci-lint (enforcing forbidigo read-only check)
make test        # Run unit tests and golden-file comparisons
make build       # Build bin/ovn-topologyd and bin/ovn-topo
make image       # Build container image
```

## API Summary

Base path: `/api/v1`

| Endpoint | Method | Description |
|---|---|---|
| `/graph?view=l3` | GET | Aggregated L3 graph (VPCs, LRs, LSs, external networks) |
| `/graph?view=l2&focus=ls:<id>` | GET | Paginated L2 switch ports and chassis bindings |
| `/graph?view=chassis` | GET | Chassis list with encaps and binding counts |
| `/node/{id}` | GET | Full details and raw OVSDB columns for a node |
| `/chassis` | GET | Chassis details, HA groups, and gateway roles |
| `/find?q=<text>&type=&limit=&offset=` | GET | Search objects by name, IP, MAC, namespace, pod, VM |
| `/events` | GET | SSE stream emitting graph revision changes |
| `/readyz` | GET | Readiness probe (returns 200 after initial DB sync) |
| `/healthz` | GET | Liveness probe |
| `/version` | GET | Daemon version and OVN schema versions |
