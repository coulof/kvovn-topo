# ADR-0002 — Topology history: what to record, where to keep it

- Status: Proposed
- Date: 2026-10-20
- Deciders: coulof
- Related: ADR-0001 (privilege tiers), ROADMAP M8

## Context

Users want to answer "what changed since it last worked?" and to see the topology at a past point in time. OVN keeps no usable history:
- Clustered `ovsdb-server` logs are compacted.
- The NB/SB databases only hold current state.

The daemon already receives every change through OVSDB monitors and assigns a monotonic `revision`. History must be recorded by us, from the moment the daemon runs.

Constraints:
- **Read-only (hard rule 1)** forbids writing to OVSDB and to the Kubernetes API. It does not forbid the daemon writing to its own local storage. That distinction must be explicit.
- **Air-gap:** no mandatory external service.
- **Stateless-by-default:** installing the chart must not require a StorageClass.
- **Graph logic evolves:** history should benefit from later improvements to `pkg/graph` (new joins, better checks).

## Decision drivers

1. Past graphs must be reconstructible with the *current* graph code.
2. Bounded resource use (memory, disk), with predictable retention.
3. Works with zero configuration; persistence is opt-in.
4. Honest about gaps (daemon down, monitor disconnected).

## Options considered

### What to record
| Option | Pros | Cons |
|---|---|---|
| **A. Graph-level deltas** (node/edge added/removed/changed) | Directly user-meaningful | Coupled to the graph code version; graph bugs are frozen into history; new relationships can't be computed for the past |
| **B. OVSDB row-level changes** (db, table, uuid, old/new columns) + periodic checkpoints | Canonical, schema-versioned source; replay through current `pkg/graph`; new features work on old data | Must replay to show a past graph; SB churn is noisy |

### Where to keep it
| Option | Verdict |
|---|---|
| In-memory ring buffer | **Default.** Zero config, lost on restart |
| Embedded store on a PVC (bbolt) | **Opt-in persistence.** Pure Go, single file, air-gap friendly |
| Object storage (S3-compatible) | Deferred (backlog: export/archival) |
| ConfigMaps / CRs | **Rejected**: writes to the Kubernetes API, violates hard rule 1 |
| Re-reading ovsdb-server raft logs | **Rejected**: compaction makes it unreliable; requires access to DB files |
| External TSDB / SQL server | **Rejected**: mandatory dependency, air-gap friction |

## Decision

1. **Record OVSDB row-level changes (option B)** for the tables the project consumes (AGENTS.md §5 only), stamped with:
   - wall-clock time
   - `epoch` (a random ID per daemon start, or per store creation when persistent)
   - `revision`
   - OVSDB schema version
2. **Checkpoints:** a full snapshot of the consumed tables every N changes or T minutes (defaults: 5 000 changes / 15 min), and at every (re)connect. Reconstruction of time T = nearest checkpoint ≤ T + replay of changes up to T through the current `pkg/graph`.
3. **Noise control at write time:**
   - Drop column changes that never affect the graph (configurable denylist, e.g. SB `Port_Binding.up` flaps only when unchanged within a 2 s window, `nb_cfg`-style counters).
   - Coalesce bursts per row within a 1 s window.
   - Never drop structural changes (row insert/delete, references).
4. **Gaps are first-class:** a disconnect or daemon restart writes a `gap` record. On reconnect, the fresh snapshot is diffed against the last known state and stored as one `coalesced` change set, flagged as such. The UI never interpolates across a gap.
5. **Storage:**
   - Default `history.mode=memory`, bounded by count, bytes and age (defaults 200k changes / 256 MiB / 24 h, first limit wins).
   - `history.mode=persistent` uses bbolt on a PVC, with retention by age and size (defaults 7 days / 2 GiB), compacting oldest checkpoints first.
   - Single replica only (no shared writer).
6. **Read-only clarification:** hard rule 1 is restated as *"never write to OVSDB or the Kubernetes API"*. Writing to the daemon's own history store is allowed and is the only permitted write.
7. **Kubernetes enrichment is not historicized.** Past graphs show identity from NB `external_ids` (recorded). Live enrichment (owner, VM, NAD) is shown only when the object still exists, and is labelled "current, may differ from then".
8. **Derived views:**
   - **diff(T1, T2)** compares reconstructed graphs by node ID and attributes.
   - **The change feed** is produced by a summarizer that maps row changes to human-readable events (e.g. "FIP 10.0.0.5 moved from vm-a to vm-b", "gateway for tenant-1 failed over node2 → node3"). Unmapped changes appear as generic row events, never hidden.

## Consequences

- New package `pkg/history` (recorder, store interface with `memory` and `bbolt` implementations, reconstructor, summarizer). T0. It may be used by the daemon and by the TUI in embedded mode (memory only).
- History only covers the time the daemon has been running. This is documented in UI copy and in the chart README.
- History contains IPs, MACs, pod and VM names. It is served through the same API and RBAC as live data. There is no separate export without an explicit backlog task.
- Reconstruction cost grows with distance from a checkpoint. Checkpoint cadence is the tuning knob; M8 sets latency targets.
- The schema version stamp means a Kube-OVN/OVN upgrade inside the retention window must still reconstruct. Fields missing in older schemas are treated as absent, never as errors. This is a required test case.
- The chart gains optional `persistence` values (StorageClass, size). Default installs stay stateless.

## Open points (resolve in M8)

- Exact noise denylist, decided from real churn measured on the `overlay-default` and `kubevirt-multinic` scenarios.
- Whether `revision` remains monotonic across restarts in persistent mode (the proposal is `(epoch, revision)` ordering by time, with `epoch` changes marked as gaps).
