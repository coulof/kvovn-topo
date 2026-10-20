# ADR-0001 — Privilege tiers: passive topology vs. exec-based diagnostics

- Status: Accepted
- Date: 2026-10-20
- Deciders: coulof

## Context

The project visualizes OVN topology as deployed by Kube-OVN, through a Go daemon (consumed by a Rancher UI extension) and a Go TUI. The daemon and extension are committed to being **read-only**.

Many valuable troubleshooting features (`kubectl ko trace`, `tcpdump`, `vsctl`, `appctl`, conntrack dumps) don't mutate OVN or Kubernetes state, but they all require `pods/exec` into privileged pods (`ovn-central`, `ovs-ovn`, `kube-ovn-cni`). In practice that is root on cluster nodes. "Doesn't mutate state" and "requires low privilege" are different properties. Conflating them would break the security story of the daemon.

Some `kubectl ko` commands do mutate state or create workloads (`nb|sb kick/restore`, `reload`, `perf`, `diagnose subnet`).

## Decision

Features are classified into tiers, and the tier decides which binaries may contain them.

| Tier | Mechanism | Allowed in | Examples |
|---|---|---|---|
| **0 – Passive** | OVSDB monitors (NB/SB) + Kubernetes `get/list/watch` on non-Kube-OVN resources | daemon, TUI, extension | topology views, pod/VM attachment, IP resolver, ACL/PG/AS edges, LB backends, consistency checks |
| **1 – Diagnostic** | `pods/exec` into Kube-OVN/OVS pods, running read-only commands only | **TUI only**, behind an explicit `--diag` mode | `ovn-trace`, `tcpdump`, `ovs-vsctl show/list`, `ovs-appctl ct-zone-list` / `dpctl/dump-conntrack`, `ovs-ofctl dump-flows`, log tails |
| **Excluded** | Anything that mutates state or creates workloads | nowhere | `kick`, `restore`, `reload`, `perf`, `diagnose subnet`, raw `nbctl`/`ofctl` passthrough, OVN port mirroring setup |

Enforcement:

1. Tier 1 code lives in `pkg/diag` only, behind an `Executor` interface implemented with client-go `remotecommand`.
2. `cmd/ovn-topologyd` and `pkg/graph`, `pkg/server` must not import `pkg/diag`. This is enforced by `depguard` in golangci-lint plus a CI check (`go list -deps ./cmd/ovn-topologyd | grep pkg/diag` must be empty).
3. Every command run by `pkg/diag` comes from a typed allowlist (`ovn-trace`, `tcpdump`, `ovs-vsctl --format=json list|show`, …). No free-form command strings from user input. Arguments are built from graph objects, never interpolated raw.
4. The TUI shows a persistent "DIAG" badge when Tier 1 is enabled. When RBAC denies exec, it degrades to Tier 0 with a clear message, not an error.
5. `tcpdump` sessions require a filter and have default packet-count and duration limits.

## Consequences

- The daemon's ServiceAccount never needs `pods/exec`; the extension keeps a clean read-only security review.
- Point-and-click trace and tcpdump are TUI features at first. Bringing them to the extension would require a separate, opt-in component with its own ADR.
- A limited, NB-only path computation (`pkg/trace`, no exec) is Tier 0 and may be used by the extension. Its results are approximate. The UI must label them "computed from configuration", distinct from `ovn-trace` results.
- Kubernetes read access expands beyond `Node` to: `Pod`, `Service`, `EndpointSlice`, KubeVirt `VirtualMachineInstance`/`VirtualMachine`, and `NetworkAttachmentDefinition`. All are get/list/watch. Kube-OVN CRDs remain out of scope.
