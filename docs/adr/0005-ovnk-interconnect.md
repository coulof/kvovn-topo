# ADR-0005 — ovn-kubernetes interconnect: zone collectors and merge

- Status: Proposed
- Date: 2026-10-27
- Deciders: coulof
- Answers: ROADMAP Q14. Builds on ADR-0004 (flavor adapter) and touches ADR-0001 (privilege tiers) and ADR-0002 (history keys).

## Context

OpenShift is in scope (Q13). On OpenShift 4.14+ ovn-kubernetes runs in **interconnect (IC) mode**. A read-only spike on `hol-powerflex-lab` (OCP 4.19.19, OVN 25.03.1, 3 nodes) gave these facts.

### No central database
- Each node is its own zone (`k8s.ovn.org/zone-name` = node name).
- The `ovnkube-node` pod runs that zone's `nbdb`, `sbdb` and `northd`.
- The databases listen only on unix sockets, `/var/run/ovn/ovn{nb,sb}_db.sock`. On the host that is `/var/run/ovn-ic`, mode `root:root 0750`.
- There is no NB/SB Service. Today's daemon fails with `lookup ovn-nb.kube-system.svc.cluster.local ... no such host`.

### Zones are not partitions
Each zone holds a near-complete copy of the cluster's logical objects:
- `ovn_cluster_router`, `join`, `transit_switch`;
- all 88 Port_Groups and 83 Address_Sets;
- most Load_Balancers.

The copies share **no** row UUIDs across zones. Per zone only these are local:
- the node switch `<node>`;
- `GR_<node>` and `ext_<node>`;
- the zone's pod LSPs, NAT rows and Port_Bindings.

### The stitching keys are exact
- **Transit ports.** `transit_switch` has one `tstor-<node>` per node:
  - it is a `type=router` port in the owning zone;
  - it is a `type=remote` port elsewhere, with `external_ids:node`, `options:requested-chassis` and the same MAC/transit IP.

  All 6 remote ports resolve to exactly one owner. Every zone routes every other zone's pod subnet to the owner's transit IP.
- **Chassis.** SB `Chassis` rows appear in every zone. `other_config:is-remote=false` marks the zone's own chassis, and `Chassis.name` equals `k8s.ovn.org/node-chassis-id`.

### The core already reads one zone
The pinned 24.03 models accept the live NB 7.11.0 / SB 20.41.0 schemas with no errors. `pkg/ovsdb.LoadFixture` loads each zone, and the L3 view renders 2 LRs, 5 LSs and 5 `router-peer` edges. Two Kube-OVN assumptions misfire:
- **Pod identity.** ovn-k pod LSPs carry `external_ids:pod="true"` and `namespace=<ns>`, and are named `<ns>_<pod>`. The core reads `"true"` as the pod name, so no workload resolves.
- **Chassis → node.** The rule uses `ovn.kubernetes.io/chassis`. It finds nothing, and the match only works through the hostname fallback.

Both belong to the ADR-0004 adapter.

## Decision

1. **Option (a): zone collectors plus a merger in the daemon.**
   - A DaemonSet `ovn-topology-zone` runs one collector per node. It reads the local NB/SB sockets with libovsdb monitors and serves the zone's raw rows to the daemon on the pod network, protected by mTLS between the daemon and its collectors.
   - The daemon stays a single Deployment and owns the merged graph, the API, SSE and history.
   - Collectors hold no graph logic.
2. **Merge rules** (flavor adapter, ovn-kubernetes only):
   - **Replicated objects** have one node keyed by name, with per-zone provenance in properties: `ovn_cluster_router`, `join`, `transit_switch`, Port_Group, Address_Set, Load_Balancer, Chassis, secondary layer2/`interconn-ts` switches.
     - The chassis attributes come from the zone where `is-remote=false`.
   - **Zone-local objects** keep their UUID-based ID, prefixed with the zone: `<type>:<zone>/<uuid>`.
   - **Transit ports.** A `type=remote` `tstor-<node>` is not a node of its own. It resolves to the owning zone's `tstor-<node>` → `rtots-<node>`. When a zone is missing, the port is kept and flagged `unresolved`, as with unbound ports.
3. **ID stability.**
   - Name-keyed IDs (`lr:name/ovn_cluster_router`) survive zone restarts and database rebuilds, which UUIDs do not.
   - The `<type>:<uuid>` rule in AGENTS.md §4 gains this flavor-specific form. The OpenAPI ID pattern stays a typed string.
   - ADR-0002 history keys by node ID, so it inherits the same stability.
4. **Privilege.** In OpenShift terms, the collector is the only part that needs:
   - `runAsUser: 0`;
   - a read-only hostPath mount of `/var/run/ovn-ic`;
   - an SCC that allows both (`hostmount-anyuid` or a dedicated SCC).

   The socket grants full OVSDB **write** access. Read-only is guaranteed only by the code (`forbidigo`) and by the collector being a separate, minimal binary. The README must say so. This is still T0 under ADR-0001: it is passive monitoring with no exec. But it is a larger attack surface than the Kube-OVN Service connection, and it is the main cost of this decision.
5. **Kube-OVN is unchanged.** The collector and the merge run only when the adapter detects ovn-kubernetes in IC mode. On a central-DB ovn-k deployment the daemon connects directly, as today.

## Options considered

### (a) Zone collectors plus a merger in the daemon *(chosen)*

**Pros:**
- one cluster-wide graph, the same API and MCP as Kube-OVN;
- push updates per zone;
- the spike shows the merge keys are exact.

**Cons:**
- a privileged DaemonSet with a hostPath and an SCC;
- a new binary and new transport;
- the daemon must tolerate partial views while a zone is missing.

### (b) Per-zone view

**Pros:** no merge. The core already reads one zone.

**Cons:**
- reaching a zone from outside needs exec or port-forward into `ovnkube-node`, which is T1 and TUI-only (ADR-0001), so MCP and the daemon lose it;
- users must know which node a pod is on before they can see it;
- cross-node paths are invisible.

Rejected as the product model. Kept as a dev path through captured fixtures.

### (c) Central-DB deployments only

**Pros:** no new component.

**Cons:** excludes OpenShift 4.14+, the reason Q13 was opened. Rejected.

### Collector variants rejected inside (a)

- **Sidecar in `ovnkube-node`:** the pod is owned by the Cluster Network Operator and cannot be changed.
- **The daemon itself as a DaemonSet:** duplicates the graph, API and history per node, and the API would need leader election.
- **Relay sockets to TCP:** exposes a writable OVSDB on the network.

## Consequences

- **Easier:** OpenShift gets the same views, `/find` and MCP tools as Kube-OVN. Cross-node pod paths are explicit through `transit_switch`.
- **Harder:**
  - a second binary and chart template (DaemonSet, SCC, mTLS);
  - merge tests across N zone fixtures;
  - a readiness rule (`/readyz` after all zones' first snapshot, or a degraded state that names missing zones).
- **Chart on OpenShift:** the pinned `runAsUser: 65534` (`values.yaml`) is refused by `restricted-v2`. Leave the UID unset under OpenShift.
- **Hard rule 2 is unchanged.** Collectors read OVSDB only; no `k8s.ovn.org` CRDs are read.

## Action items

1. [ ] Q15: rerun `hack/capture-fixtures.sh --flavor ovnk` on a kind interconnect lab and commit an anonymous `ovnk-ic-kind` scenario (3 zones).
2. [ ] M4 prep (ADR-0004 item 2): the adapter interface. The ovn-kubernetes identity rules come from this spike (`<ns>_<pod>` LSP name, `pod="true"`, `k8s.ovn.org/node-chassis-id`).
3. [ ] Merge in `pkg/graph`, behind the adapter, tested on the multi-zone fixture (the spike's `TestSpikeOVNKStitch` checks become assertions).
4. [ ] Collector binary and chart: a DaemonSet, an OpenShift SCC, mTLS, and README wording on socket write access.
5. [ ] On acceptance: amend AGENTS.md §4 (ID form), §2 (layout) and §4 Security (collector RBAC/SCC).
