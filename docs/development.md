# Development Guide

This guide covers building, testing, and running `ovn-topology` for local development.

## Prerequisites

To build and run the project, you need:
- Go 1.27+
- `golangci-lint`
- `make`

Alternatively, you can use [mise](https://mise.jdx.dev/) to auto-install and pin the correct Go version and linter:
```bash
mise install
```

## Build Commands

These common `make` tasks cover the development lifecycle:

```bash
make generate    # Regenerate OVSDB models and OpenAPI Go types
make lint        # Run golangci-lint (enforces forbidigo read-only rules)
make test        # Run unit tests and golden-file comparisons
make build       # Build bin/ovn-topologyd and bin/ovn-topo
make image       # Build the container image
```

Run `make generate lint test` before submitting changes. If you modify `api/openapi.yaml`, regenerate and update Go consumers in the same change.

## Local Daemon Development

To develop the daemon locally on your laptop, you need access to the target cluster's OVN databases (Northbound port `6641` and Southbound port `6642`). 

Because Kube-OVN `ovn-central` pods use `hostNetwork`, database instances listen on node IPs. A standard `kubectl port-forward svc/ovn-nb` fails because port forwarding only connects to `localhost` inside the pod.

### 1. Set Up the Proxy Relay
The `pf` tasks in `mise` set up a temporary Python TCP relay pod (`ovn-topology-proxy`) in the `kube-system` namespace. This relay forwards localhost requests to the `ovn-nb` and `ovn-sb` Services, and then tunnels them to your laptop.

```bash
# Deploy the proxy relay and forward OVN database ports to localhost
mise run pf
```

*Note: Set `PROXY_IMAGE` in your environment to match your cluster's Kube-OVN version or mirror.*

### 2. Run the Daemon Locally
In your first terminal, run the local daemon against the forwarded ports:
```bash
mise run daemon
```

### 3. Test and Run Clients
In a second terminal, you can interact with your local daemon or run the TUI against it:
```bash
mise run test-local  # Query local daemon endpoints
mise run tui-local   # Run the TUI against your local daemon
```

### 4. Cleanup
When finished, tear down the port-forwards and remove the proxy relay pod:
```bash
mise run pf-proxy-remove
```

## Testing & Fixtures

The codebase relies on OVSDB snapshots and in-memory databases to run unit tests without requiring a live OVN cluster.

### Fixtures
OVSDB snapshots reside in `testdata/` and are captured from live dev clusters using `ovsdb-client backup`. 
During test runs, `pkg/graph` unit tests load these snapshots into an in-process libovsdb test server (`github.com/ovn-kubernetes/libovsdb/server`). This ensures tests run through the actual monitor update path.

### Golden-File Tests
The REST API endpoints (`/graph`) use golden-file comparisons to catch regressions in response structure.
To update golden files after a legitimate API change:
```bash
go test ./... -update
```

### Scenario Scopes
Test scenarios inside `testdata/` cover complex configurations:
- Centralized gateway with HA chassis groups
- Custom VPCs with distributed Floating IPs (FIP)
- Underlay and localnet attachments
- Logical Switches attached to multiple Logical Routers
- Unbound ports and disconnected/offline chassis

## Repository Layout & Style

See `AGENTS.md` and `ROADMAP.md` for our coding conventions, strict read-only design checks, dependency limits (`pkg/diag` constraints), and milestone scopes.

## Release Artifacts & Air-Gap Packaging

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
