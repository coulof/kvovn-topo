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
- **`t`**: Cycle color themes live (**SUSE**, **Dracula**, **Nord**, **Tokyo Night**, **Catppuccin Mocha**, **Default**).
- **`j` / `k`** or **`↓` / `↑`**: Move selection line-by-line across routers, switches, and ports.
- **`r`**: Refresh topology from the daemon.
- **`q`**: Quit.

Launch with a preferred theme:
```bash
./bin/ovn-topo --theme=suse
./bin/ovn-topo --theme=dracula
./bin/ovn-topo --theme=nord
./bin/ovn-topo --theme=tokyonight
./bin/ovn-topo --theme=catppuccin
```

## Rancher and Harvester UI Extension

The extension integrates into two places:
- **Cluster Explorer**: Navigate to your cluster ➡️ in the left navigation sidebar, select **OVN Topology** ➡️ **Topology** (`/c/:cluster/ovn-topology/topology`).
- **Harvester Virtualization Management**: Navigate to your Harvester cluster ➡️ in the left sidebar under **Networks**, select **OVN Topology** (`/c/:cluster/harvester/ovn-topology`).

To test the extension with a running Rancher Manager:

```bash
# Serve the built package locally for Developer Load (port 4500)
mise run ui-serve

# Or run the development server with live reload
mise run ui-dev
```

## Release Artifacts

Every version tag (`vX.Y.Z`) publishes:

| Artifact | Registry Path | Description |
|---|---|---|
| **Daemon Image** | `ghcr.io/coulof/ovn-topologyd:vX.Y.Z` | Multi-arch (`linux/amd64`, `linux/arm64`) distroless daemon |
| **Daemon Chart** | `oci://ghcr.io/coulof/charts/ovn-topology:X.Y.Z` | OCI Helm chart for downstream clusters |
| **Extension Catalog Image (ECI)** | `ghcr.io/coulof/ovn-topology-ui:vX.Y.Z` | Rancher Extension Catalog container |
| **Binaries** | GitHub Releases | Static `ovn-topologyd` and `ovn-topo` archives for Linux/macOS |

### Air-Gapped Environments (Hauler)

Each release includes a `hauler.yaml` manifest bundling the daemon image, Helm chart, and Extension Catalog Image:

```bash
# Sync release artifacts into a local Hauler store
hauler store sync -f hauler.yaml

# Copy artifacts to an internal air-gapped registry
hauler store copy registry://registry.internal.example.com
```

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
