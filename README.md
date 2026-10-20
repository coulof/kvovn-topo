# ovn-topology

A read-only network topology visualizer for OVN as deployed by Kube-OVN on Rancher-managed RKE2 and Harvester clusters.

## Components

- **`ovn-topologyd`**: Go daemon that connects to OVN Northbound (NB) and Southbound (SB) databases over OVSDB, monitors updates, maintains a joined in-memory network graph, and serves it over HTTP JSON and SSE.
- **`ovn-topo`**: Terminal UI (Bubble Tea) that renders an L3 topology tree and an interconnection matrix of router ports directly in your terminal.
- **`ui/`**: Rancher UI Extension (Vue 3, `@rancher/shell`, D3.js + ELK) that embeds topology views into Rancher's cluster explorer.
- **`charts/ovn-topology/`**: Helm chart deployed once per downstream cluster.

## Design and Security Rules

- **Strictly read-only**: OVSDB does not have a read-only user role. Read-only access is guaranteed in code and enforced during build by `golangci-lint` using `forbidigo`. The codebase contains no write operations (`Create`, `Update`, `Mutate`, `Delete`, `Transact`) against OVSDB or the Kubernetes API.
- **OVSDB is the source of truth**: The daemon does not read Kube-OVN CRDs (such as Vpc, Subnet, or IP). Kubernetes metadata comes from OVN NB `external_ids`. The only Kubernetes resource read by the daemon is `Node`, used solely to map chassis hostnames to node names.
- **Access control**: API access goes through the Kubernetes service proxy (`/api/v1/namespaces/<namespace>/services/http:ovn-topologyd:8080/proxy/api/v1/...`). Authorization is controlled by standard `services/proxy` RBAC rules.
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
  │  │   • HTTP REST + SSE API (:8080)                      │  │
  │  └───────────────────────┬──────────────────────────────┘  │
  └──────────────────────────┼─────────────────────────────────┘
                             │
     ┌───────────────────────┴───────────────────────┐
     │                                               │
     ▼                                               ▼
Rancher UI Extension                         ovn-topo (TUI)
(via k8s service proxy)                      (Bubble Tea)
```

## Quick Start: Local Testing

To test against an existing cluster (for example `export KUBECONFIG=~/.kube/hv-01.yaml`):

```bash
# 1. Forward OVN database ports to localhost
mise run pf

# 2. Run the daemon locally (terminal 1)
mise run daemon

# 3. Test the API or launch the TUI (terminal 2)
mise run test-live
mise run tui
```

When finished, stop the port-forwarding:

```bash
mise run pf-stop
```

## TUI Controls (`ovn-topo`)

- **`Tab`**: Switch between **Topology Tree View** and **Port Interconnection Matrix**.
- **`j` / `k`** or **`↓` / `↑`**: Move selection line-by-line across routers, switches, and ports.
- **`r`**: Refresh topology from the daemon.
- **`q`**: Quit.

## Development

Prerequisites: Go 1.24+, `golangci-lint`, `make`.

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
| `/events` | GET | SSE stream emitting graph revision changes |
| `/readyz` | GET | Readiness probe (returns 200 after initial DB sync) |
| `/healthz` | GET | Liveness probe |
| `/version` | GET | Daemon version and OVN schema versions |
