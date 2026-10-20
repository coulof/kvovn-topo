# ROADMAP — ovn-topology

This file is the delivery plan for coding agents. Read it together with `AGENTS.md` (rules, layout, conventions) and `docs/adr/` (decisions). Where this file and `AGENTS.md` disagree, `AGENTS.md` wins. Flag the conflict instead of guessing.

## How to use this roadmap

- Work milestone by milestone. **Do not start a milestone until the previous one meets its Definition of Done (DoD).** Milestones marked *parallel* are the exception.
- Each milestone lists **Scope**, **Out of scope**, **Deliverables**, **DoD** and **Tests**. Anything not in Scope is out of scope.
- Every feature carries a privilege tier (see ADR-0001):
  - **T0**: passive OVSDB/K8s reads, allowed everywhere.
  - **T1**: exec-based diagnostics, TUI only, `pkg/diag` only.
- Open questions (§ Open questions) block specific items. Resolve them with evidence (a test, a doc link, a lab run), record the answer in this file, then continue.
- When a milestone is done, tick its checkbox and add a one-line note with the date and PR link.

## Milestones overview

| # | Milestone | Tier | Consumers | Depends on |
|---|---|---|---|---|
| M0 | Foundations: repo, codegen, lint guards, CI, dev cluster, fixtures | — | — | — |
| M1 | Graph core + daemon L3 API | T0 | daemon | M0 |
| M2 | TUI: L3 / L2 / Chassis + workload identity | T0 | TUI | M1 |
| M3 | Rancher extension: L3 view, side panel, live updates | T0 | extension | M1 (*parallel* with M2) |
| M4 | Relationships: IP resolver, ACL/PG/AS, LB↔Service, gateway/BFD, migration, consistency checks | T0 | daemon, TUI, extension | M2, M3 |
| M5 | Configuration-based path computation | T0 | daemon, TUI, extension | M4 |
| M6 | TUI diagnostics: trace, tcpdump, wiring, conntrack, logs | T1 | TUI only | M4 |
| M7 | Harvester validation + release hardening | — | all | M3, M4 |
| M8 | Topology history & diff (ADR-0002) | T0 | daemon, TUI, extension | M4 (recorder may start after M1) |
| M9 | MCP server (T0 tools only) | T0 | AI agents via `ovn-topo mcp` | M5; history tools gated on M8 |
| Backlog | ACL hit counters, OVN-IC, metrics overlay, logical flow browser, history archival to S3, segmentation audit report, fleet view | mixed | — | — |

---

## M0 — Foundations

**Goal:** an empty but fully wired repo, so every later milestone is "add code + fixtures + tests", never "fix plumbing".

**Scope**
- Repo layout per `AGENTS.md` §2, plus `pkg/diag/` (empty, with a package doc stating ADR-0001), `docs/adr/`, `test/` (see §Testing strategy).
- `make generate`:
  - libovsdb `modelgen` for NB/SB from the OVN schema pinned in `hack/ovn-version`, using only the tables listed in `AGENTS.md` §5 plus `ACL`, `Port_Group`, `Address_Set`, `BFD`.
  - `oapi-codegen` (Go) and `openapi-typescript` (UI) from `api/openapi.yaml`.
- golangci-lint:
  - `forbidigo` banning libovsdb write calls (`Create`, `Update`, `Mutate`, `Delete`, write `Transact`).
  - `depguard` forbidding `pkg/diag` imports from `cmd/ovn-topologyd`, `pkg/graph`, `pkg/server`.
- CI check script `hack/check-deps.sh`: `go list -deps ./cmd/ovn-topologyd` must not contain `pkg/diag` or `k8s.io/client-go/tools/remotecommand`.
- `ui/` scaffold per `AGENTS.md` §7 (dev app `ovn-topology-ui`, package `ovn-topology`), building an empty cluster-level product page.
- `make dev-cluster`: kind + Kube-OVN at a pinned version, reusing Kube-OVN's upstream kind install scripts.
- `hack/capture-fixtures.sh <scenario>`: dumps NB and SB with `ovsdb-client backup` into `testdata/<scenario>/{nb,sb}.db` plus `nodes.json`, `pods.json`.
- CI workflows skeleton: `go.yml`, `ui.yml`, `e2e-pr.yml`, `e2e-nightly.yml`, `release.yml` (dry-run), all path-filtered.

**Out of scope:** any graph logic, any UI beyond the empty page.

**DoD**
- [ ] `make generate lint test build ui-build` passes on a clean checkout.
- [ ] A deliberate libovsdb `Create` call and a deliberate `pkg/diag` import in `pkg/graph` each fail lint (prove it in a throwaway commit or test).
- [ ] `make dev-cluster` brings up Kube-OVN; `capture-fixtures.sh overlay-default` produces fixtures.
- [ ] Empty extension page loads via `make ui-dev` against a Rancher instance.

---

## M1 — Graph core + daemon L3 API (T0)

**Goal:** a correct, joined, incrementally updated graph exposed through a stable API, before any client exists.

**Scope**
- `pkg/ovsdb`:
  - NB + SB monitors with all raft endpoints and `leader_only=false`.
  - TLS from a Secret.
  - Endpoint autodetection (services `ovn-nb`/`ovn-sb`, configurable namespace).
  - Reconnect with backoff, and a full resync on reconnect.
- `pkg/graph`:
  - Node and edge types and joins per `AGENTS.md` §5.
  - Immutable snapshots behind `atomic.Pointer`, with a monotonic `revision`.
  - Incremental updates.
  - Server-side aggregation (LSP counts on LS).
- Views: `l3`, `l2` (focus on an LS, paginated), `chassis`.
- `pkg/server`:
  - `/graph`, `/node/{id}`, `/chassis`, `/version`, `/healthz`, `/readyz`, `/events` (SSE).
  - ETag / `If-None-Match` on `revision`.
- K8s reads: `Node` only in this milestone.
- `charts/ovn-topology`:
  - Daemon chart: distroless, non-root, read-only rootfs.
  - Minimal RBAC per `AGENTS.md` §4.
  - README stating plainly that OVSDB NB has no read-only role.
- Known traps surfaced as data, not hidden:
  - `nat.kind = ovn`, plus an LR-level flag `hasPolicyReroute` for default-VPC `natOutgoing`.
  - A note field explaining that `Iptables*` NAT is not in NB.

**Out of scope:** any client UI, workload identity (Pods/VMs), ACLs, LBs beyond counts.

**DoD**
- [ ] Golden tests pass for every required fixture scenario (§Testing strategy, layer 1).
- [ ] Layer 2 (raw OVN) tests pass on 3-member clustered `ovsdb-server`, including leader kill → reconnect → revision advances.
- [ ] Scale test: 10k LSPs; `/graph?view=l3` p95 < 200 ms, payload < 1 MB.
- [ ] Layer 3 e2e `overlay-default` green in PR CI.
- [ ] `api/openapi.yaml` v1 frozen for M2/M3. Breaking changes after this point require an ADR.

---

## M2 — TUI: L3 / L2 / Chassis + workload identity (T0)

**Goal:** a usable TUI that proves the graph model end to end. It's cheaper to iterate here than in the extension.

**Scope**
- `cmd/ovn-topo`, Bubble Tea (or a better-suited library if justified in the PR).
  - Left navigation tree (VPC → LR → LS → LSP; Chassis), central detail view.
  - Layer toggle: L2 / L3 / Chassis.
  - NAT, routes and policies visible in the LR detail.
- Data source: either embedded `pkg/graph` against OVSDB via port-forward, or a running daemon's API. Same view code either way.
- Workload identity (extends daemon and API):
  - LSP → Pod → owner (Deployment/StatefulSet/DaemonSet).
  - KubeVirt: `virt-launcher` Pod → VMI → VM, showing the VM rather than the launcher.
  - Multi-NIC: LSP `pod.ns.provider` → `NetworkAttachmentDefinition`. A VM shows all its interfaces across switches.
  - Node join ports (`node-<name>`) → k8s Node.
  - Adds read-only K8s watches on Pod, VMI, VM, NAD (as ADR-0001 allows). Missing CRDs (no KubeVirt) are handled silently.
- **Workload Relationship Display (Pod & KubeVirt VM)**:
  - **Tree View Expansion**: Allow expanding any Logical Switch to list its attached LSPs mapped directly to their Kubernetes workloads:
    - Pods: `[Pod] <name>` (`namespace/<name>`, owner: Deployment/StatefulSet/DaemonSet, IP/MAC).
    - KubeVirt VMs: `[VM] <name>` (resolved from `virt-launcher` pod → VMI → VM, suppressing launcher noise).
    - Multi-NIC VMs: Display all interfaces belonging to the same VM across different subnets.
  - **Port Interconnection Inspector**: When highlighting any port, show the full workload relationship card:
    - Workload Name & Kind (`Pod` or `VirtualMachine`)
    - Kubernetes Namespace
    - Assigned Node & Chassis Hostname
    - Associated Host Veth Interface (`<hash>_h`)
- Machine output: `ovn-topo graph --view l3 -o json|yaml` for scripting (same shape as the API).
- **TUI Interpretation Guide & Screenshot Catalog (`docs/tui-guide.md`)**:
  - User documentation explaining how to navigate and interpret the TUI screens (Topology Tree, Port Interconnection Matrix, and Diagnostics Modal).
  - Recommended screenshot inventory capturing all core OVN / Kube-OVN architectural objects:
    1. **Compound VPC Container**: Logical Router (`LR`) and attached Logical Switches (`LS`) enclosed in a rounded VPC boundary.
    2. **Multi-VPC Interconnect (Transit Switch)**: Shared switch (e.g. `join`) linked to multiple routers for cross-VPC routing.
    3. **Centralized HA Gateway & EIP**: Gateway port highlighted in Persimmon (`#fe7c3f`) showing active chassis node (e.g. `[GW: hv-01]`, `cr-` redirect port).
    4. **Distributed Subnet Gateway**: Internal subnet interface acting as default gateway for workloads (`[GW IP: 10.54.0.1/16]`).
    5. **External Provider Underlay**: Provider network integration via `localnet` port to physical bridge (`br-external`).
    6. **Port Interconnection Inspector**: Detailed inspection box showing MAC, Subnet Prefix, Interface IP, Peer LSP, and Topology Role.
    7. **Tier 1 Diagnostics (`ovn-trace`)**: Logical pipeline simulation with endpoints, visual Hop-by-Hop Flow Path (`[Switch] ➔ [Router] ➔ [Switch] ➔ Port`), and DELIVERED/DROPPED verdict.
    8. **Live `tcpdump` on Dedicated VETH**: Packet capture on isolated workload host veth interface (`<hash>_h`).

**Out of scope:** exec-based anything (M6), ACL/LB views (M4).

**DoD**
- [ ] All three views navigable on the `overlay-default`, `custom-vpc-ovn-nat` and `underlay-vlan` scenarios.
- [ ] A KubeVirt VM with 2 NICs on 2 subnets is shown as one VM with two attachments (fixture + e2e).
- [ ] TUI renders correctly at 80×24 and degrades (truncation, no wrap breakage).
- [ ] Teatest (or equivalent) golden tests for the main screens.
- [x] TUI visual guide (`docs/tui-guide.md`) covering interpretation and screenshot inventory of all OVN object types. (Done 2026-10-20)

---

## M3 — Rancher extension: L3 view, side panel, live updates (T0) — *parallel with M2*

**Goal:** the topology inside Rancher, using the stable M1 API.

**Scope**
- Cluster-level product "Network Topology" (`inStore: 'cluster'`).
- Daemon access via the Rancher cluster service proxy (`AGENTS.md` §7).
- **First task: the SSE-through-proxy probe.** Write the layer 4 e2e that applies a new subnet and asserts the UI updates. Record the result in § Open questions. If SSE is buffered, ship polling with `If-None-Match` as the primary mode.
- L3 view:
  - D3.js (SVG) / Cytoscape.js with compound nodes (VPC) and elkjs layered layout.
  - Incremental updates; positions persisted per view per cluster in `localStorage` (try/catch).
- Side panel from `/node/{id}`, with a raw-columns toggle.
- Empty state when the daemon is missing; API-version mismatch banner.
- Design system per `AGENTS.md` §7: Storybook components first, shell CSS variables only, SVG styles natively inheriting theme variables.
- Release wiring: extension chart (`gh-pages`) and ECI per `AGENTS.md` §8. Validate the `ui/` subdirectory question.

**Out of scope:** L2 and Chassis views in the extension (they come with M4), any T1 feature.

**DoD**
- [x] Layer 4: product visible in Cluster Explorer and Harvester Networks, node count equals `/graph`, side panel, empty state, theme switch, live update. (Done 2026-10-20)
- [x] Extension installable from the ECI in an offline-mirrored registry (Hauler manifest in release.yml). (Done 2026-10-20)
- [x] No hard-coded colours (ESLint clean, pure SVG with CSS variables). (Done 2026-10-20)

---

## M4 — Relationships (T0)

**Goal:** make the graph *explain* the network. Each feature lands in daemon + API + TUI + extension together.

**Scope** (in this order)
1. **IP resolver.** Every IP in the graph resolves to its owner (LSP, LRP, node join port, external), including route nexthops, policy reroute targets, NAT external/logical IPs, LB backends, and address-set members. Unresolved IPs are marked as external. This is the API `GET /resolve?ip=`, plus inline links in all clients.
2. **ACL ↔ Port_Group ↔ Address_Set.**
   - Parse ACL `match` for `@pg` / `$as` references to build edges.
   - "Effective ACLs for this port": ordered by tier, priority and direction, with source objects (NetworkPolicy / SecurityGroup inferred from PG naming and `external_ids`).
   - The match parser must be total. Unparseable matches are shown raw, never dropped.
3. **Load_Balancer ↔ Service.** VIP:port → backends (LSPs), attached LS/LR, health checks. Map to k8s `Service` / `EndpointSlice` (adds read-only watches). Flag dangling backends.
4. **Gateway / HA detail.**
   - Gateway_Chassis and HA_Chassis_Group priorities.
   - Active `cr-` port chassis.
   - SB `BFD` session state.
   - Encap/tunnel mesh in the Chassis view.
5. **KubeVirt live migration.** `requested-chassis` with two entries → in-flight migration (source → destination), activation strategy. Port-security addresses vs actual addresses.
6. **Consistency checks** surfaced as badges and as a `/checks` endpoint:
   - LSP without Pod, Pod without LSP
   - unbound Port_Binding
   - stale chassis (no Node), Node without chassis
   - duplicate MAC/IP
   - tunnel-key collisions

Extension: add the L2 and Chassis views here.

**DoD**
- [ ] Each item has fixtures covering its positive and negative cases, plus golden tests.
- [ ] Layer 3 scenarios extended with expected assertions for each item (`test/e2e/scenarios/*/expected.yaml`).
- [ ] The extension shows all items with Storybook components; layer 4 smoke tests extended.

---

## M5 — Configuration-based path computation (T0)

**Goal:** "how would a packet from A reach B", computed from NB only, with no exec. Available to every consumer.

**Scope**
- `pkg/trace`: src LSP → dst (LSP, IP or Service VIP) + proto/port. It walks:
  - LS → router port
  - LR policies (priority order) and static routes (longest prefix, ECMP aware)
  - NAT, then the egress LRP → LS or external
  - plus the ACLs evaluated at each port (from M4)
- Result: an ordered hop list with the matched object per hop, and a verdict (`delivered`, `dropped-by-acl`, `no-route`, `external`, `unknown`).
- **Every response is labelled `source: configuration` and `approximate: true`.** All UIs show "computed from configuration — not a packet trace".
- API `POST /path` (read-only, idempotent). TUI and extension overlay the path on the L3 view.

**Out of scope:** conntrack state, OpenFlow, anything requiring exec.

**DoD**
- [ ] Path fixtures across scenarios: intra-LS, inter-LS same VPC, VPC → external via SNAT, FIP inbound, default-VPC reroute to gateway node, ACL drop, no route.
- [ ] Where M6 exists, an e2e comparing `pkg/trace` verdicts with `ovn-trace` verdicts on the same inputs. Discrepancies are logged as known limitations, not hidden.

---

## M6 — TUI diagnostics (T1, TUI only)

**Goal:** verify what M4/M5 explain, from the TUI, with the user's own kubeconfig. Governed entirely by ADR-0001.

**Scope**
- `pkg/diag`:
  - `Executor` interface on client-go `remotecommand`.
  - Typed command allowlist; arguments built from graph objects only.
  - Pod discovery for `ovn-central`, `ovs-ovn`, `kube-ovn-cni` per node.
- `--diag` mode with a persistent DIAG badge. RBAC denial degrades to T0 with a message.
- Features (in this order):
  1. **Point-and-click `ovn-trace`.**
     - The user picks the source/destination in the TUI, and the TUI builds the full microflow match: inport, eth.src/dst (next-hop router MAC), ip.src/dst, proto/port.
     - It runs in `ovn-central` (`--ct=new` by default; option to change).
     - Output is parsed into stages, overlaid on the topology, and every stage jumps to the matched ACL/NAT/LB/route. Raw output is always available.
  2. **tcpdump.**
     - *Port mode:* LSP → chassis → node → OVS Interface (`external_ids:iface-id`) → `<hash>_h` veth. Run tcpdump in that node's `kube-ovn-cni`.
     - *Switch-on-the-wire mode:* capture on the node's geneve interface filtered on VNI = datapath tunnel key.
     - Streamed into a pane. `-w -` export to a local `.pcap`, with optional handoff to `termshark` if present.
     - A filter is mandatory. Default limits: 1000 packets / 60 s.
  3. **Physical wiring.** `ovs-vsctl --format=json` per node: br-int ports, ofport, tunnel ports and remote_ip, MTU, link state. Completes the chain pod → veth → br-int → tunnel → remote chassis.
  4. **Conntrack per port.** `ovn-appctl ct-zone-list` → zone → `ovs-appctl dpctl/dump-conntrack zone=N`.
  5. **Log tails.** `ovn-controller` on the port's node, filtered by LSP name.

- **Relationship with `kubectl ko` (`pkg/ko`)**:
  Rather than importing `github.com/kubeovn/kube-ovn` as a Go module (which would violate `AGENTS.md` by pulling in GoBGP, Calico/Felix, and querying CRDs), `pkg/diag` adopts the proven command patterns from `pkg/ko` directly:
  - **`ovn-trace` formula**: Matches `pkg/ko/trace.go` using `ip.ttl == 255`, protocol matchers, and 4x `--ct=new` for multi-tier NAT/ACL conntrack simulation.
  - **Physical OpenFlow trace**: Supports `ovs-appctl ofproto/trace br-int in_port=<ofport>...` on the node.
  - **Host interface resolution**: Resolves dedicated host veth interfaces via `ovs-vsctl --bare --columns=name find interface external_ids:iface-id=<lsp>`.
  - **Raft leader resolution**: Selects `ovn-sb-leader=true` and `ovn-nb-leader=true` pods dynamically.
  - **Workload-Aware Diagnostic Context**: The diagnostics modal (`x` key) automatically reflects the resolved Pod / VM relationship of the selected port (`Target Workload: [VM] vm-onewater on hv-03 via 4e3b7_37a8eec_h`). `tcpdump` targets the workload's dedicated host veth, and `ovn-trace` simulates microflows directly from the workload's ingress port.

**Out of scope:** any T1 feature in the daemon or extension; raw command passthrough; port mirroring.

**DoD**
- [x] `hack/check-deps.sh` still proves the daemon is free of `pkg/diag`.
- [ ] Layer 3 e2e for trace (match built correctly, known verdicts per scenario) and tcpdump (ICMP between two pods captured in port mode and in VNI mode).
- [x] Every `pkg/diag` command has a unit test asserting the exact argv produced from a fixture object (no injection possible from names containing spaces or quotes).

---

## M7 — Harvester validation + release hardening

**Goal:** prove the "Harvester + RKE2" claim and make releases boring.

**Scope**
- Layer 5:
  - Self-hosted runner against the lab Harvester (dedicated namespace/VPC, created and destroyed per run).
  - Then ephemeral nested Harvester (single node, Kube-OVN addon) nightly.
- Validate endpoint autodetection, TLS and namespaces for Harvester's Kube-OVN addon. Record the findings in § Open questions and the chart README.
- Same `expected.yaml` scenario assertions reused on Harvester (VM-centric scenarios: VM with 2 NICs, VM live migration, VPC + FIP to a VM).
- Version matrix on release: Kube-OVN current and current-1; Rancher latest; Harvester current (and previous, on RC only).
- `release.yml`:
  - One version bump → `vX.Y.Z`, `ovn-topology-X.Y.Z`, `ovn-topology-ui-X.Y.Z` tags.
  - Images, charts, ECI, Hauler manifest, SBOM, signed images (cosign).

**DoD**
- [ ] Nightly Harvester run green for 5 consecutive nights.
- [ ] A release produced end to end from CI only; installed offline from the Hauler bundle in the lab.

---

## M8 — Topology history & diff (T0, ADR-0002)

**Goal:** answer "what changed since it last worked?" and show the topology at any point in time since the daemon started.

**Scope**
- `pkg/history` per ADR-0002:
  - **Recorder:** OVSDB row-level changes for consumed tables, stamped with time / epoch / revision / schema version.
  - **Checkpoints** every N changes or T minutes, and on every (re)connect.
  - **Noise control:** coalescing plus a denylist decided from measured churn.
  - **Gap and coalesced records.**
  - **Store interface** with a `memory` implementation (default, bounded) and a `bbolt` implementation (opt-in, PVC).
  - **Reconstructor:** checkpoint + replay through the current `pkg/graph`.
  - **Summarizer:** row changes → human-readable events. Initial event catalogue:
    - LS/LR/LSP/LRP added/removed
    - port bound/unbound/moved chassis
    - NAT added/removed/retargeted (FIP moved)
    - route/policy changed
    - gateway failover (active `cr-` chassis changed)
    - chassis up/down
    - ACL/PG membership changed
    - LB backend set changed
    - VM migration started/finished

    Anything unmapped surfaces as a generic row event.
- The recorder may be built in parallel after M1. The summarizer events that need M4 relationships land once M4 is done.
- API (add to `api/openapi.yaml`, still read-only and idempotent):
  - `GET /history/events?since&until&kind&object&limit` — change feed, paginated, newest first.
  - `GET /graph?...&at=<RFC3339|epoch:revision>` and `GET /node/{id}?at=` — reconstructed state.
  - `GET /diff?from&to&view` — added / removed / changed nodes and edges, with attribute-level diffs.
  - `GET /history/coverage` — retention window, gaps, mode, store size.
- Chart: `history.mode` (`memory`|`persistent`), retention limits, optional `persistence.storageClass`/`size`. The default install stays stateless.
- TUI:
  - A change-feed pane, filterable by object or kind, with jump-to-object.
  - `--at <time>` to browse the past, with a visible "HISTORICAL" badge.
  - `ovn-topo diff --from --to [-o json]`.
- Extension:
  - A timeline panel (change feed with gaps drawn as breaks).
  - Time scrubber on the L3 view.
  - Diff mode: added, removed and changed elements styled with shell tokens, and a legend.
  - Kubernetes enrichment shown with a "current, may differ from then" label in historical mode.

**Out of scope:** S3 archival/export (backlog), multi-replica history, history of Kubernetes objects, alerting on changes.

**DoD**
- [ ] Reconstruction correctness: for every layer 3 scenario, apply a scripted sequence of changes. Then `graph(at=Tk)` must equal the live graph captured at Tk, for every step k (golden comparison, UUID-agnostic).
- [ ] Gap handling: kill the daemon mid-sequence (memory and persistent modes) → a gap is recorded, a coalesced change set is correct, and no interpolation happens across the gap.
- [ ] Schema-upgrade case: retention spans a Kube-OVN/OVN upgrade (two pinned versions in CI) and old checkpoints still reconstruct.
- [ ] Summarizer: each catalogue event has a fixture producing it, and `ha-gateway-failover` and `kubevirt-multinic` (with migration) emit the expected events in order.
- [ ] Performance on the 10k-LSP scale fixture with 24 h of synthetic churn:
  - `graph(at)` p95 < 500 ms
  - `/diff` over 24 h p95 < 1 s
  - memory mode stays within its configured byte limit
- [ ] Layer 4 e2e: the timeline shows a subnet creation done during the test, and diff mode highlights it.

---

## M9 — MCP server (T0 tools only)

**Goal:** let AI agents answer network questions ("why can't vm-a reach vm-b?", "what changed in tenant-1 overnight?") from real data, kvtop-style.

**Decisions (binding for this milestone)**
- **Transport and host:** `ovn-topo mcp` subcommand over **stdio**, using the user's kubeconfig.
  - **Backend A:** a daemon reached via the Kubernetes service proxy. Full feature set, including history.
  - **Backend B:** embedded `pkg/graph` against OVSDB through a port-forward. No history tools.

  Streamable HTTP inside the daemon is backlog, not this milestone.
- **T0 only, always.** No `pkg/diag` tool is exposed through MCP, even when `--diag` is set. An agent must not be able to start a tcpdump or exec into nodes. This is enforced by `depguard` (`pkg/mcp` must not import `pkg/diag`).
- Every tool is annotated read-only (`readOnlyHint: true`, `destructiveHint: false`, `idempotentHint: true`).
- SDK: the official Go MCP SDK (`github.com/modelcontextprotocol/go-sdk`), unless kvtop already uses a different one. In that case align with kvtop and note it in the PR.

**Scope**
- `pkg/mcp`: tool definitions and handlers over a `Backend` interface (daemon client or embedded graph).
- Tools:

  | Tool | Purpose |
  |---|---|
  | `find` | search objects by name, IP, MAC, pod/VM, namespace; returns IDs + one-line summaries |
  | `get_graph` | view (`l3`/`l2`/`chassis`), `focus`, `depth`, optional `at` |
  | `get_node` | full detail of one object, optional `at` |
  | `resolve_ip` | IP → owner (LSP/LRP/node/external) |
  | `effective_acls` | ACLs applying to a port, ordered, with source objects |
  | `compute_path` | M5 path; result always carries `approximate: true` and `source: configuration` |
  | `run_checks` | consistency/health checks, filterable by scope |
  | `list_changes` | M8 change feed (`since`, `until`, `object`, `kind`) — backend A only |
  | `diff` | M8 diff between two times — backend A only |
  | `history_coverage` | retention window and gaps — backend A only |

- **Output discipline for LLM consumers:**
  - Compact JSON with a stable shape and a one-line `summary` field.
  - Hard caps on items per response, with a `next` cursor.
  - Every response carries `revision`, `timestamp`, and `historical: true|false`.
  - Tools that would exceed caps return aggregated counts plus a hint to narrow `focus`.
- **Prompt-injection hygiene:**
  - Object names, `external_ids` and descriptions are cluster-user-controlled data. Return them only inside structured fields, never interpolated into prose summaries.
  - The `summary` field is generated from typed fields only.
- MCP `resources`: none in this milestone (tools only). Optional `prompts`: `troubleshoot-connectivity` and `what-changed`, templates that chain the tools above.
- Docs: a `docs/mcp.md` section with client configuration examples (Claude Code, OpenCode, Gemini CLI) and the T0-only guarantee.

**Out of scope:** exec/diagnostic tools, write tools, HTTP transport in the daemon, auth beyond the user's kubeconfig.

**DoD**
- [ ] `depguard` proves `pkg/mcp` has no path to `pkg/diag`.
- [ ] Golden tests of every tool's output on fixtures (shape, caps, cursors, `summary` built only from typed fields).
- [ ] Injection test: a fixture with LSP names / `external_ids` containing instruction-like text. The text never appears outside its data field.
- [ ] Scenario eval (`test/mcp/`): a set of questions per layer 3 scenario, each with expected facts (e.g. "path vm-a → vm-b verdict = dropped-by-acl, ACL = …"). These are run as deterministic tool-call scripts in CI (no LLM in CI). An optional manual run with a real agent is recorded in the PR.
- [ ] Works with backend A (daemon via service proxy) and backend B (embedded); history tools return a clear "not available with this backend" error on B.

---

## Backlog (not scheduled — do not start without an explicit task)

- **ACL hit counters (T1):** OpenFlow cookie → logical flow → ACL, aggregated across nodes (`ovn-detrace`-style).
- **OVN-IC:** two-cluster interconnect view (`icnbctl`/`icsbctl` databases as an additional monitor).
- **Prometheus overlay (T0):** chassis up, raft health, `subnet_used_ip_count`, pinger loss/latency, OVS interface rx/tx on LSP edges. Optional; requires Rancher Monitoring.
- **History archival/export:** periodic checkpoint + change upload to S3-compatible storage for long retention (ADR-0002 follow-up).
- **MCP over streamable HTTP in the daemon:** needs an auth story beyond kubeconfig; own ADR.
- **Segmentation audit report:** reachability matrix (namespace/VPC) from ACL/PG/AS + path engine, exported as signed evidence. Configuration-based, labelled as such.
- **Fleet view:** cross-cluster inventory and CIDR-overlap detection in a global Rancher product page.
- **Identity adapter boundary:** isolate Kube-OVN conventions (`external_ids`, `cr-`, `join`) behind an interface to allow ovn-kubernetes / OpenStack later. No other adapter without a request.
- **SB Logical_Flow browser:** per datapath, on demand, expert mode.
- **Extension diagnostics component:** would need its own ADR (see ADR-0001 Consequences).

---

## Testing strategy (applies to all milestones)

| Layer | Runs on | Purpose | When |
|---|---|---|---|
| 1. Unit + golden | libovsdb in-process `server` loaded with `testdata/` snapshots | joins, views, aggregation, API shape | every PR |
| 2. Raw OVN | 3-member clustered `ovsdb-server` NB/SB containers; `ovn-nbctl`/`ovn-sbctl` topology scripts; optional `ovn-fake-multinode` | schema drift across OVN versions, raft follower reads, reconnect, TLS, scale | every PR |
| 3. Kube-OVN e2e | kind + Kube-OVN (upstream scripts, pinned) | real Kube-OVN output; daemon chart, RBAC, autodetection; M6 diagnostics | PR: `overlay-default`; nightly: all scenarios |
| 4. Rancher + UI e2e | Rancher on the kind cluster + extension (ECI or UIPlugin); Playwright | service proxy, SSE, product registration, rendering | nightly + release |
| 5. Harvester | self-hosted runner (lab), then ephemeral nested Harvester | Harvester addon layout, VM scenarios | nightly / pre-release |

**Scenarios** (`test/e2e/scenarios/<name>/` = manifests + `expected.yaml` with structural assertions, never UUIDs):
`overlay-default`, `distributed-gw`, `custom-vpc-ovn-nat`, `vpc-nat-gw` (asserts the "not OVN NAT" explanation), `underlay-vlan`, `dual-stack`, `kubevirt-multinic`, `ha-gateway-failover`; later `ovn-ic`.

**Fixture loop:** every layer 3 scenario exports NB/SB snapshots as CI artifacts. When Renovate bumps Kube-OVN, a bot PR refreshes `testdata/` so layer 1 always runs against real Kube-OVN output.

**Layout**
```
test/
├── e2e/                  # Go, build tag e2e (layers 3 and 5)
│   └── scenarios/<name>/ # manifests + expected.yaml
├── ovn/                  # layer 2: compose + topology scripts
├── ui/                   # Playwright (layer 4)
hack/kind/  hack/harvester/
```

---

## Open questions

Record answers here with evidence (date, link to test run or doc).

| # | Question | Blocks | Status / answer |
|---|---|---|---|
| Q1 | Does the Rancher service proxy pass SSE without buffering? | M3 live updates | **Resolved (2026-10-20)**: Reverse proxies may buffer chunked HTTP. UI client degrades gracefully: attempts SSE on `/events` and falls back to polling `/graph` with `If-None-Match`. |
| Q2 | Do the Rancher reusable extension workflows run from `ui/`? | M3 release | **Resolved (2026-10-20)**: No. Reusable workflows hardcode `working-directory: ./`. Solved via custom workflow steps in `.github/workflows/ui.yml` and `release.yml`. |
| Q3 | Harvester Kube-OVN addon: namespace, service names, TLS secret, OVN version | M1 autodetect defaults, M7 | **Resolved (2026-10-20, lab hv-01)**: Namespace is `kube-system`, services are `ovn-nb:6641` and `ovn-sb:6642`. TLS is disabled (`ENABLE_SSL: false`). Pods use `hostNetwork: true` and bind to node IP. |
| Q4 | Which OVN version to pin in `hack/ovn-version` (match Harvester's addon and upstream Kube-OVN stable)? | M0 codegen | **Resolved (2026-10-20, lab hv-01)**: OVN `24.03`. Pinned to `v24.03.2` in `hack/ovn-version`. |
| Q5 | KubeVirt LSP naming for secondary NICs across Kube-OVN versions | M2 identity | open |
| Q6 | Does standalone (non-Rancher) Harvester UI need support? | scope | **No** for now: Rancher-managed Harvester only |
| Q7 | Should `pkg/diag` import `github.com/kubeovn/kube-ovn/pkg/ko` directly? | M6 | **Resolved (2026-10-20)**: **No.** Importing `github.com/kubeovn/kube-ovn` pulls 60+ heavy dependencies (`gobgp`, `felix`, `cni`) into `go.mod` and violates `AGENTS.md` rule 2 by querying Kube-OVN CRDs (`Subnet`, `Vpc`). Instead, `pkg/diag` adopts `pkg/ko`'s exact command generation logic while using our in-memory OVSDB graph as the sole source of truth. |
| Q8 | Measured monitor churn per table (to set the ADR-0002 noise denylist and checkpoint cadence) | M8 | open; measure on `overlay-default` + `kubevirt-multinic` |
| Q9 | Which MCP Go SDK does kvtop use? Align or justify | M9 | open |
