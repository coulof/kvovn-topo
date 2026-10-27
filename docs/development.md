# Development Guide

## Prerequisites

Go 1.27+, `golangci-lint` 2.15 and `make`. [mise](https://mise.jdx.dev/) installs the pinned Go and linter:
```bash
mise install
```

## Build Commands

```bash
make generate    # Regenerate OVSDB models and OpenAPI Go types
make lint        # Run golangci-lint (enforces forbidigo read-only rules)
make test        # Run unit tests and golden-file comparisons
make build       # Build bin/ovn-topologyd and bin/ovn-topo
make image       # Build the container image
```

Run `make generate lint test` before submitting. A change to `api/openapi.yaml` regenerates and updates its Go consumers in the same commit.

## Local Daemon Development

A local daemon needs the cluster's NB (`6641`) and SB (`6642`). Kube-OVN's `ovn-central` pods use `hostNetwork` and listen on node IPs, so `kubectl port-forward svc/ovn-nb` fails: a port-forward always connects to `localhost` inside the pod.

### 1. Set Up the Proxy Relay
`mise run pf` creates a small relay pod, `kube-system/ovn-topology-proxy`, that forwards 6641/6642 to the `ovn-nb`/`ovn-sb` Services, and port-forwards to it. It is the only thing any task creates in the cluster.

```bash
# Deploy the proxy relay and forward OVN database ports to localhost
mise run pf
```

`PROXY_IMAGE` sets the relay image (any image with `python3`; match your Kube-OVN version or mirror).

### 2. Run the Daemon Locally
Terminal 1:
```bash
mise run daemon
```

### 3. Test and Run Clients
Terminal 2:
```bash
mise run test-local  # Query local daemon endpoints
mise run tui-local   # Run the TUI against your local daemon
```

### 4. Cleanup
Stop the port-forwards and delete the relay pod:
```bash
mise run pf-proxy-remove
```

## Tests and fixtures

Tests need no cluster. A fixture is an NB/SB snapshot plus the Kubernetes identity data (`nodes.json`, `pods.json`, KubeVirt objects). `pkg/ovsdb.LoadFixture` decodes its rows through libovsdb's table cache, the same path monitor updates take, and builds the snapshot with the syncer's code.

- `testdata/ovn-nb.json` and `ovn-sb.json`: the hand-written base fixture used by most unit tests.
- `testdata/scenarios/<name>/`: lab captures made with `hack/capture-fixtures.sh <name>` (read-only: `ovsdb-client query` selects and `kubectl get`; `--flavor ovnk` captures one directory per ovn-kubernetes zone). Today: `hv-01-lab` (the whole lab) and `kubevirt-multinic`. Captures hold real names and IPs; review them before committing. The named scenarios the roadmap needs are still to capture (ROADMAP, B1).

Golden files cover `/graph` views, each router's routes, policies and NAT, and the MCP tools. After an intended output change:

```bash
go test ./... -update
```

## Layout and conventions

`AGENTS.md` has the rules (read-only, privilege tiers, layout, conventions) and `ROADMAP.md` the milestones.

## Release Artifacts & Air-Gap Packaging

A tag `vX.Y.Z` (or a `-dev` pre-release, which does not move `:latest`) runs `release.yml` and publishes:

| Artifact | Registry Path | Description |
|---|---|---|
| **Daemon Image** | `ghcr.io/coulof/ovn-topologyd:X.Y.Z` | `linux/amd64` distroless daemon |
| **Daemon Chart** | `oci://ghcr.io/coulof/charts/ovn-topology:X.Y.Z` | OCI Helm chart for downstream clusters |
| **Binaries** | GitHub Releases | Static `ovn-topologyd` + `ovn-topo` archives for Linux (amd64) and macOS (arm64); `ovn-topo` zip for Windows (amd64) |

### Air-Gapped Environments (Hauler)

Each release has a `hauler.yaml` listing the daemon image and chart:

```bash
# Sync release artifacts into a local Hauler store
hauler store sync -f hauler.yaml

# Copy artifacts to an internal air-gapped registry
hauler store copy registry://registry.internal.example.com
```
