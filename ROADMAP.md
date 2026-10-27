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
| M3 | Rancher extension: L3 view, side panel, live updates — **paused 2026-10-23** | T0 | extension | M1 (*parallel* with M2) |
| M4 | Relationships: IP resolver, ACL/PG/AS, LB↔Service, gateway/BFD, migration, consistency checks | T0 | daemon, TUI, extension | M2, M3 |
| M5 | Configuration-based path computation | T0 | daemon, TUI, extension | M4 |
| M6 | TUI diagnostics: trace, tcpdump, wiring, conntrack, logs | T1 | TUI only | M4 |
| M7 | Harvester validation + release hardening | — | all | M3, M4 |
| M8 | Topology history & diff (ADR-0002) | T0 | daemon, TUI, extension | M4 (recorder may start after M1) |
| M9 | MCP server (T0 tools only) | T0 | AI agents via `ovn-topo mcp` | M5; history tools gated on M8 |
| Backlog | ACL hit counters, OVN-IC, metrics overlay, logical flow browser, history archival to S3, segmentation audit report, fleet view | mixed | — | — |

## Status and tech-debt register (refreshed 2026-10-27)

**The order rule keeps being broken.** M2, M3 and M6 were built while the M0 and M1 DoD were open, and M6 ahead of M4. The 2026-10-25 review froze feature work behind the foundation pass below. Features landed anyway, each on an explicit request:
- M9 MCP server (ADR-0003), service-proxy access (`pkg/kubeproxy`), OpenFlow inspection in diag (`92d27f9`);
- the ovn-kubernetes zone view, `--zone` and `--fixture-dir` (ADR-0005);
- the M2 router rows (`ee985f3`), the CNI-aware details pane and the `!` Inspect runner (ADR-0004 interim, ADR-0001 amendment).

Only small debt items closed. The foundation pass is still the next work.

| M | DoD | Reality |
|---|---|---|
| M0 | 0/4 | Lint and deps guards work; `hack/capture-fixtures.sh` exists. No `test/` tree, no recorded `make dev-cluster` run. |
| M1 | 0/5 | API and goldens on one hand-written fixture plus two lab captures. Live data path fixed 2026-10-21. Layer 2/3 tests, scale test and API freeze missing. |
| M2 | 6/6 | Done. Scenario navigation checked on the lab, not on scenario fixtures. |
| M3 | 2/3 | Paused. No live updates and no API-version banner in the extension. |
| M4, M5 | 0 | Not started. An interim NAT-gateway "Outside OVN" view lives in the TUI (`cmd/ovn-topo/flavor.go`). |
| M6 | 5/5 | Done out of order; verified by hand on `hv-01`. Detrace follow-up open. |
| M7, M8 | 0 | Not started. Two pre-releases (v0.1.0-dev, v0.1.1-dev) ran `release.yml` end to end. |
| M9 | 2/5 | Started out of order (ADR-0003). `find`, `get_graph`, `get_node`, `list_chassis` on both backends; the rest waits for M4/M5/M8 data. |

**Tech-debt register** (open items; closed ones are in git history):

| # | Item | Where it closes |
|---|---|---|
| B1 | Scenario fixtures. Two lab captures are committed: `hv-01-lab` (the whole lab, not a scenario) and `kubevirt-multinic` (its `meta.json` does not say what was set up). Still to do: one clean capture per named scenario (`overlay-default`, `custom-vpc-ovn-nat`, `vpc-nat-gw` first), each with a distinct NB and a `meta.json` describing the setup. The lab runs OVN 25.03.4 while `hack/ovn-version` pins v24.03.2 (Q4): align it before relying on newer columns, such as the policy `jump` action. | M0 / M1 |
| B2 | No `test/` tree: layers 2–5 absent. | M0 / M1 / M7 |
| B3 | No scale test. `/graph` rebuilds the whole view per request, with no per-revision cache. | M1 |
| B5 | Extension has one unit test (`layout.test.ts`). | M3 (paused) |
| E1 | `cmd/ovn-topo/diag_ui.go`: one 1.4k-line modal with flag-driven state. | before M5 |
| E3 | `ovn-trace` and OVS output parsed with regexes (`pkg/diag/ovs.go` grew with `92d27f9`); `ofproto/trace` and `ovn-detrace` would add two more formats. | M5 / M6 detrace follow-up |
| F1 | Versions are `0.x-dev` pre-releases; no stable release yet. | M7 |
| N1 | The `services/proxy` resourceName form in `charts/ovn-topology/templates/viewer-rbac.yaml` is unverified (both the bare Service name and `http:<svc>:http` are listed). | Foundation pass step 2 |
| N2 | The service-proxy path (TUI and `ovn-topo mcp` default, `mise run test-live`) is tested only against a fake API server (`cmd/ovn-topo/proxy_test.go`), never through Rancher or the Harvester VIP. | Foundation pass step 2 |
| N3 | ADR-0003 (MCP server) is still `Proposed`. | Foundation pass step 2 |

Closed since 2026-10-21: B4 (deterministic codegen: libovsdb's `MaxLength()` overflows on amd64 only; `hack/normalize-models.py` and a CI diff check), C1 (`forbidigo` on client-go `Patch`/`Apply*`/`Evict*`), C2 (`depguard` for `pkg/mcp`), C3 (schemas were vendored all along), A6 (SSE keepalive), E2 (one chassis → node resolver), F2 (CORS removed).

### Foundation pass

One PR per step, in this order. Steps 1 and 3 are done.

1. ~~Register refresh.~~
2. **Lab checks on hv-01 (N1–N3).** Deploy the latest image; run `mise run test-live`, `mcp-live` and `tui` through Rancher and through the Harvester VIP. Bind a test user to `ovn-topologyd-viewer` only and record which resourceName form works. Accept or amend ADR-0003.
3. ~~B4: deterministic codegen.~~
4. **B1: scenario fixtures.** Align `hack/ovn-version` with OVN 25.03 (re-answer Q4) and regenerate. Capture `overlay-default`, `custom-vpc-ovn-nat` and `vpc-nat-gw`, with scenario and MCP goldens.
5. **B2: layer 2 tests.** A 3-member raft `ovsdb-server` in CI with leader kill → reconnect → revision advances (M1 DoD 2), then the layer 3 `overlay-default` e2e on kind (M1 DoD 4, M0 DoD 3).
6. **B3: scale.** A 10k-LSP synthetic fixture and a `/graph?view=l3` benchmark (p95 < 200 ms, payload < 1 MB); a per-revision view cache if needed. Closes M1 DoD 3.
7. **M1 API freeze.** `api/openapi.yaml` v1, including `/find`; breaking changes after this need an ADR.
8. **E1: split `diag_ui.go`** into per-action models, guarded by `TestDiagTargetIsAlwaysTheSelection` and the executor tests.

Then M4 prep, then M4, which unlocks the MCP tools `resolve_ip`, `effective_acls` and `run_checks`.

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

**Follow-up (2026-10-21): live data path audit.** *Done.* The snapshot could silently diverge from OVSDB while `/readyz` stayed green: libovsdb's reconnect repopulates its cache without Delete events, a full event queue dropped events, rebuilds ignored `List` errors, several monitored tables were never applied incrementally, and Nodes/Pods were listed once. Now an `Echo` probe drives readiness and resyncs on reconnect, overflow triggers a resync, a failed rebuild publishes nothing, all monitored tables apply incrementally, and Node/Pod informers bump the revision. Covered by `pkg/ovsdb/syncer_test.go` and `pkg/graph/kubewatch_test.go`. The layer 2 DoD against a real clustered `ovsdb-server` is still open.

---

## M2 — TUI: L3 / L2 / Chassis + workload identity (T0)

**Goal:** a usable TUI that proves the graph model end to end. It's cheaper to iterate here than in the extension.

**Scope**
- `cmd/ovn-topo`, Bubble Tea (or a better-suited library if justified in the PR).
  - Navigation tree (VPC → LR → LS → LSP; chassis) and a detail view, with L2, L3 and chassis layers (since replaced by xray trees, see the follow-up).
  - NAT, routes and policies visible in the LR detail.
- Data source: either embedded `pkg/graph` against OVSDB via port-forward, or a running daemon's API. Same view code either way.
- Workload identity (extends daemon and API):
  - LSP → Pod → owner (Deployment/StatefulSet/DaemonSet).
  - KubeVirt: `virt-launcher` Pod → VMI → VM, showing the VM rather than the launcher.
  - Multi-NIC: LSP `pod.ns.provider` → `NetworkAttachmentDefinition`. A VM shows all its interfaces across switches.
  - Node join ports (`node-<name>`) → k8s Node.
  - Adds read-only K8s watches on Pod, VMI, VM, NAD (as ADR-0001 allows). Missing CRDs (no KubeVirt) are handled silently.
- On a port: workload name and kind, namespace, node, chassis and host veth (`<hash>_h`).
- Machine output: `ovn-topo graph --view l3 -o json|yaml` (same shape as the API).
- `docs/tui-guide.md`: how to read each screen, organised by use case, with TUI/Harvester screenshot pairs.

**Out of scope:** exec-based anything (M6), ACL/LB views (M4).

**DoD**
- [x] All three views navigable on the `overlay-default`, `custom-vpc-ovn-nat` and `underlay-vlan` scenarios. (Done 2026-10-20, on the `hv-01` lab. These scenarios are not committed as fixtures yet; see tech-debt B1.)
- [x] A KubeVirt VM with 2 NICs on 2 subnets is shown as one VM with two attachments (fixture + e2e). (Done 2026-10-20)
- [x] TUI renders correctly at 80×24 and degrades (truncation, no wrap breakage). (Done 2026-10-20)
- [x] Teatest (or equivalent) golden tests for the main screens. (Done 2026-10-20)
- [x] TUI visual guide (`docs/tui-guide.md`) covering interpretation and screenshot inventory of all OVN object types. (Done 2026-10-20)
- [x] LR detail lists static routes, policies and NAT rules, not just their counts (scope item "NAT, routes and policies visible in the LR detail"). (Done 2026-10-27)

**Follow-up (2026-10-21): navigation redesign.** *Done.* The four fixed views became k9s-style xray trees (`:xray vpc|chassis|workload`) with relation jumps, plus the `:ports` table. Every frame is sized to the terminal (tested at 60×14 to 200×60), L2 fetches follow all pages, and the TUI consumes `/events` with `If-None-Match` polling fallback. Still to do: re-run the three scenarios on a live cluster with the new navigation.

**Follow-up (2026-10-21): LR routes, policies & NAT.** *Done 2026-10-27* (`ecb89b7` API, `ee985f3` TUI, `90fc635` fixes). `NodeDetail` carries `staticRoutes[]` (longest prefix first, `ecmp` flagged), `policies[]` (priority descending) and `nats[]` (type, then external IP). The details pane shows 8 rows of each, `d` all of them, and a router port lists the routes that leave through it. Next hops are annotated client-side until M4's IP resolver. The ordering contract is checked on every captured scenario, with per-router goldens and MCP `get_node` goldens. **Open:** OVN 25.03's policy `jump` action (`chain`/`jump_chain`) is not in the 24.03-pinned models or in `RouterPolicy.action`; it closes with B1's version alignment.

---

## M3 — Rancher extension: L3 view, side panel, live updates (T0) — *parallel with M2*

> **Paused (2026-10-23).** The extension code is removed from the tree and archived at tag `archive/ui-extension` (restore: `git checkout archive/ui-extension -- ui .github/workflows/ui.yml`). Work continues on the daemon and the TUI. Where M4 and M7 depend on M3, only their extension parts wait; their daemon and TUI parts do not. The open DoD items below stay open.

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
- [x] Layer 4: product visible in Cluster Explorer and Harvester Networks, node count equals `/graph`, side panel, empty state, theme switch. (Done 2026-10-20)
- [ ] Live update: the extension consumes `/events` with the `If-None-Match` polling fallback, and shows the API-version mismatch banner. (Corrected 2026-10-21: the page loads `/graph` once and never refreshes. The earlier tick covered both.)
- [x] Extension installable from the ECI in an offline-mirrored registry (Hauler manifest in release.yml). (Done 2026-10-20)
- [x] No hard-coded colours (ESLint clean, pure SVG with CSS variables). (Done 2026-10-20)

---

## M4 — Relationships (T0)

**Goal:** make the graph *explain* the network. Each feature lands in daemon + API + TUI + extension together.

**Scope** (in this order)
1. **IP resolver.** Every IP in the graph resolves to its owner (LSP, LRP, node join port, external), including route nexthops, policy reroute targets, NAT external/logical IPs, LB backends, and address-set members. Unresolved IPs are marked as external. This is the API `GET /resolve?ip=`, plus inline links in all clients. It replaces the TUI's interim client-side next-hop annotation from the M2 "LR routes, policies & NAT" follow-up.
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
   - **asymmetric return around an opaque hop** (ADR-0004 decision 5): an LR with an opaque hop on one of its switches, a different egress, and no `src-ip` route or reroute policy sending that switch to the hop's LAN IP. A warning, labelled heuristic. Origin: the 2026-10 `vpc-onewater` / `one-water-nat` incident on `hv-01`.
7. **Opaque hops (ADR-0004).** The `OpaqueHop` node type, for points where OVN hands traffic to something it cannot see. First case: the Kube-OVN VPC NAT gateway pod, joined to its LAN/WAN LSPs, with `inspect: iptables-nat`. It replaces the "not OVN NAT" footnote in the TUI (`cmd/ovn-topo/views.go`) with an object. *Interim (2026-10-27):* the TUI details pane already shows the NAT gateway as an "Outside OVN" section. It is detected client-side by pod name in `cmd/ovn-topo/flavor.go`, together with the Kube-OVN vocabulary, `← via` provenance and Inspect commands. This moves behind the M4-prep adapter; Q12 (the gateway pod's labels) is still open.

**M4 prep (2026-10-27, ADR-0004): CNI adapter boundary.** Before item 1, move the Kube-OVN conventions in `pkg/graph` behind a flavor adapter interface, with Kube-OVN as the only implementation:
- identity from `external_ids`;
- `cr-` gateway ports and `join`;
- the chassis annotation;
- UI notes.

It is a pure refactor: all goldens stay unchanged. With no adapter matched, the graph must still be valid raw OVN. This was the backlog item "Identity adapter boundary".

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
- [ ] Path fixtures across scenarios: intra-LS, inter-LS same VPC, VPC → external via SNAT, FIP inbound, default-VPC reroute to gateway node, ACL drop, no route, and DNAT return bypassing an opaque hop (the `vpc-nat-gw` scenario before its `src-ip` route: verdict `external` via the VPC's external LRP, noting that it skips the NAT gateway).
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
     - Fuzzy search, category filtering (`[VM]`, `[Pod]`, `[Gateway]`, `[Node]`, `[Public DNS]`), and direct Custom Target IP entry (`Search / Target IP: <ip>[:port]`).
     - Supported protocols: ICMP (Ping), TCP 22 (SSH), TCP 80 (HTTP), TCP 443 (HTTPS), UDP 53 (DNS).
     - It runs in `ovn-central` leader (`ovn-sb-leader=true`) using 4x `--ct=new` conntrack evaluation.
     - Output is parsed into stages with a hop-by-hop path and a verdict (`DELIVERED`, `DROPPED`, `NO-ROUTE`); `o` toggles the raw output.
  2. **tcpdump.**
     - *Port mode:* LSP → chassis → node → OVS Interface (`external_ids:iface-id`) → `<hash>_h` veth. Runs line-buffered (`-l`) tcpdump in that node's `kube-ovn-cni` container.
     - Filter presets available: ICMP, TCP, filtered Port IP.
  3. **Physical wiring.** `ovs-vsctl show` in the node's OVS pod: bridges, ports, interfaces and tunnel endpoints.
  4. **Conntrack.** `ovs-appctl dpctl/dump-conntrack` on the port's node, filtered by the port IP.
  5. **Log tails.** `ovn-controller` logs on the port's node.

- **`kubectl ko` patterns without the dependency.** Importing `github.com/kubeovn/kube-ovn` would pull in GoBGP and Calico and read CRDs (Q7), so `pkg/diag` reuses `pkg/ko`'s command patterns instead: the `ovn-trace` formula (`ip.ttl == 255`, 4× `--ct=new`), host veth lookup by `external_ids:iface-id`, and raft leader selection (`ovn-nb-leader`/`ovn-sb-leader`). The diag modal targets the selected port's workload, e.g. `[VM] vm-onewater on hv-03 via 4e3b7_37a8eec_h`.

**Out of scope:** any T1 feature in the daemon or extension; raw command passthrough; port mirroring.

**DoD**
- [x] `hack/check-deps.sh` still proves the daemon is free of `pkg/diag`.
- [x] Point-and-click `ovn-trace`, live `tcpdump`, conntrack, `ovs-vsctl show`, and controller log tailing implemented in TUI.
- [x] Layer 3 e2e for trace and tcpdump command builders verified against real Kube-OVN/OVS outputs on Harvester `hv-01`. (Manual lab run; no automated layer 3 suite exists yet, see tech-debt B2.)
- [x] Automated regression tests prevent view and pane index mismatches (now `TestDiagTargetIsAlwaysTheSelection` and the executor-level tests, see the follow-up below).
- [x] Every `pkg/diag` command has a unit test asserting the exact argv produced from a fixture object (no injection possible from names containing spaces or quotes).

**Follow-up (2026-10-21): diagnostic target audit.** *Done.* Diagnostics often acted on something other than the selection: the target was re-derived from the cursor on every redraw, through helpers that fell back to the first pod, the first node, `ovn0` or `br-int`. `cmd/ovn-topo/diagtarget.go` now captures one immutable target when `x` is pressed (port, datapath, IP/MAC, node and how it was found). Actions that can't be derived from the selection are disabled with the reason, the interface lookup returns `ErrIfaceNotFound` instead of guessing, and the target is revalidated on live updates. `TestDiagTargetIsAlwaysTheSelection` checks every xray row, and the suite is mutation-checked.

**Follow-up (2026-10-27, ADR-0004): opaque-hop inspection.** *Partly done.* The details pane's `!` runs `kubectl exec <vpc-nat-gw pod> -- iptables-save -t nat` locally, suspended (ADR-0001 amendment, `27dac63`). Still open, once M4 item 7 exists: the same inspection inside the diag modal through `pkg/diag` (`iptables-save -t nat -c`, legacy or nft variant, and `ip -4 -o addr` for the EIPs). The ovn-kubernetes `nft-ruleset` kind waits for an ovn-kubernetes adapter.

**Planned follow-up (2026-10-23): OpenFlow trace + `ovn-detrace`.** Not started; needs Q11 answered first.

`ovn-trace` simulates the logical pipeline from NB/SB: it says what the *configuration* does, not whether `ovn-controller` installed those flows on the node. Add two steps to the trace action:
- `ovs-appctl ofproto/trace br-int` in the source node's `ovs-ovn`, with the same microflow as the `ovn-trace` match (`in_port=<host veth>` from the captured target, `dl_src/dst`, `nw_src/dst`, `nw_ttl`, ports). Typed builder, no shell.
- `ovn-detrace`, fed the `ofproto/trace` output on stdin (`Executor.Stream`). It maps each OpenFlow `cookie` to the SB `Logical_Flow` and the NB object behind it (ACL, LB, route, NAT), so a drop names its ACL.

Each tool gets its own verdict, derived from the expected chassis/port in the graph. A logical-vs-physical disagreement (stale `ovn-controller`, missing flows) is shown as a banner, not averaged. If `ovn-detrace` is absent from the image it is disabled with the reason; nothing is installed at runtime (air-gap, read-only).

*DoD (when scheduled)*
- [ ] Q11 answered on `hv-01` with evidence.
- [ ] `BuildOfprotoTraceCommand` and `BuildDetraceCommand` are typed, with exact-argv tests including hostile names.
- [ ] The trace result shows logical (`ovn-trace`), physical (`ofproto/trace`) and annotated (`ovn-detrace`) outputs, one verdict per tool, and the disagreement banner.
- [ ] Parser tests on outputs captured from `hv-01` (see E3).
- [ ] Missing `ovn-detrace` disables only that step, with the reason.
- [ ] `hack/check-deps.sh` still clean; nothing added to the daemon.

---

## M7 — Harvester validation + release hardening

**Goal:** prove the "Harvester + RKE2" claim and make releases boring.

**Scope**
- Layer 5:
  - Self-hosted runner against the lab Harvester (dedicated namespace/VPC, created and destroyed per run).
  - Then ephemeral nested Harvester (single node, Kube-OVN addon) nightly.
- Validate endpoint autodetection, TLS and namespaces for Harvester's Kube-OVN addon. Record the findings in § Open questions and the chart README.
- **Access on Harvester (decision 2026-10-25):** the daemon is never exposed through kube-vip, an IP pool or ingress-nginx. The TUI and `ovn-topo mcp` reach it through the API-server service proxy (Rancher `/k8s/clusters/<id>` or the Harvester VIP `:6443`), via `pkg/kubeproxy`. The chart's optional `rbac.viewer` roles grant access. Lab item: check which `services/proxy` resourceName form the authorizer matches (bare name or `http:<svc>:http`), then keep only that one in `templates/viewer-rbac.yaml`. A browser hostname (Ingress plus kube-rbac-proxy) would need its own ADR.
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
- SDK: the official Go MCP SDK (`github.com/modelcontextprotocol/go-sdk`); kvtop uses none (Q9).

**Scope**
- `pkg/mcp`: tool definitions and handlers over a `Backend` interface (daemon client or embedded graph).
- Tools:

  | Tool | Purpose |
  |---|---|
  | `find` | search objects by name, IP, MAC, pod/VM, namespace; returns IDs + one-line summaries (backed by `GET /find`, ADR-0003) |
  | `get_graph` | view (`l3`/`l2`/`chassis`), `focus`, `depth`, optional `at` |
  | `get_node` | full detail of one object, optional `at` |
  | `list_chassis` | chassis with encaps, binding counts, gateway roles and HA priorities (`/chassis`; added by ADR-0003) |
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
- Docs: a `docs/mcp.md` section with client configuration examples (Claude Code, OpenCode, agy — aligned with kvtop's README, 2026-10-23) and the T0-only guarantee.

**Out of scope:** exec/diagnostic tools, write tools, HTTP transport in the daemon, auth beyond the user's kubeconfig.

**DoD**
- [x] `depguard` proves `pkg/mcp` has no path to `pkg/diag`. *2026-10-23: `hack/check-deps.sh` passes on `pkg/mcp`.*
- [ ] Golden tests of every tool's output on fixtures (shape, caps, cursors, `summary` built only from typed fields). *2026-10-23: done for the four shipped tools (`testdata/golden/mcp/`, `TestCapsAndCursors`); each later tool adds its own.*
- [x] Injection test: a fixture with LSP names / `external_ids` containing instruction-like text. The text never appears outside its data field. *2026-10-23: `TestInjectionStaysInData`; extend it with each new tool.*
- [ ] Scenario eval (`test/mcp/`): a set of questions per layer 3 scenario, each with expected facts (e.g. "path vm-a → vm-b verdict = dropped-by-acl, ACL = …"). These are run as deterministic tool-call scripts in CI (no LLM in CI). An optional manual run with a real agent is recorded in the PR.
- [ ] Works with backend A (daemon via service proxy) and backend B (embedded); history tools return a clear "not available with this backend" error on B. *2026-10-23: both backends work, and `TestBackendParity` shows identical output over HTTP and in process. With no flags the daemon Service is discovered (default name, else chart label; `TestDiscoverService`). The service proxy has not been run against a live cluster yet. The history part waits for M8.*

---

## Backlog (not scheduled — do not start without an explicit task)

- **ACL hit counters (T1):** OpenFlow cookie → logical flow → ACL, aggregated across nodes (`ovn-detrace`-style). Builds on the M6 `ovn-detrace` follow-up.
- **OVN-IC:** two-cluster interconnect view (`icnbctl`/`icsbctl` databases as an additional monitor).
- **Prometheus overlay (T0):** chassis up, raft health, `subnet_used_ip_count`, pinger loss/latency, OVS interface rx/tx on LSP edges. Optional; requires Rancher Monitoring.
- **History archival/export:** periodic checkpoint + change upload to S3-compatible storage for long retention (ADR-0002 follow-up).
- **MCP over streamable HTTP in the daemon:** needs an auth story beyond kubeconfig; own ADR.
- **Segmentation audit report:** reachability matrix (namespace/VPC) from ACL/PG/AS + path engine, exported as signed evidence. Configuration-based, labelled as such.
- **Fleet view:** cross-cluster inventory and CIDR-overlap detection in a global Rancher product page.
- ~~**Identity adapter boundary**~~: moved to M4 prep (ADR-0004, 2026-10-27).
- **ovn-kubernetes adapter (ADR-0004, ADR-0005):** identity from `k8s.ovn.org/*` `external_ids`, network/UDN grouping, local gateway mode as an opaque hop, interconnect zone merge. Q13 resolved; waits for ADR-0005 acceptance and Q15.
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
| Q1 | Does the Rancher service proxy pass SSE without buffering? | M3 live updates | **Resolved (2026-10-20)**: Reverse proxies may buffer chunked HTTP. Clients must degrade: attempt SSE on `/events` and fall back to polling `/graph` with `If-None-Match`. The TUI does this. **The extension does not consume either yet** (2026-10-21, see the M3 DoD). Whether the Rancher proxy actually buffers has not been measured. |
| Q2 | Do the Rancher reusable extension workflows run from `ui/`? | M3 release | **Resolved (2026-10-20)**: No. Reusable workflows hardcode `working-directory: ./`. Solved via custom workflow steps in `.github/workflows/ui.yml` and `release.yml`. |
| Q3 | Harvester Kube-OVN addon: namespace, service names, TLS secret, OVN version | M1 autodetect defaults, M7 | **Resolved (2026-10-20, lab hv-01)**: Namespace is `kube-system`, services are `ovn-nb:6641` and `ovn-sb:6642`. TLS is disabled (`ENABLE_SSL: false`). Pods use `hostNetwork: true` and bind to node IP. As a result, `kubectl port-forward` to them cannot reach NB/SB (it dials the pod's localhost), so local dev uses the `ovn-topology-proxy` relay pod (`mise run pf`). |
| Q4 | Which OVN version to pin in `hack/ovn-version` (match Harvester's addon and upstream Kube-OVN stable)? | M0 codegen | **Resolved (2026-10-20, lab hv-01)**: OVN `24.03`, pinned to `v24.03.2`. **To re-answer:** the lab now runs OVN 25.03.4 (foundation pass step 4). |
| Q5 | KubeVirt LSP naming for secondary NICs across Kube-OVN versions | M2 identity | **Resolved (2026-10-20, lab hv-01)**: KubeVirt VMs attached to Kube-OVN NADs follow the pattern `<vm-name>.<namespace>.<nad-name>.<vlan|subnet>.ovn`. Default management interfaces (`multus: default/default`) bind to the physical bridge `mgmt-br` outside OVN and do not create OVN LSPs. |
| Q6 | Does standalone (non-Rancher) Harvester UI need support? | scope | **No** for now: Rancher-managed Harvester only |
| Q7 | Should `pkg/diag` import `github.com/kubeovn/kube-ovn/pkg/ko` directly? | M6 | **Resolved (2026-10-20)**: **No.** Importing `github.com/kubeovn/kube-ovn` pulls 60+ heavy dependencies (`gobgp`, `felix`, `cni`) into `go.mod` and violates `AGENTS.md` rule 2 by querying Kube-OVN CRDs (`Subnet`, `Vpc`). Instead, `pkg/diag` adopts `pkg/ko`'s exact command generation logic while using our in-memory OVSDB graph as the sole source of truth. |
| Q8 | Measured monitor churn per table (to set the ADR-0002 noise denylist and checkpoint cadence) | M8 | open; measure on `overlay-default` + `kubevirt-multinic` |
| Q9 | Which MCP Go SDK does kvtop use? Align or justify | M9 | **Resolved (2026-10-23, ADR-0003)**: kvtop uses **no SDK**. `internal/mcp` is hand-written JSON-RPC pinned to protocol `2024-11-05`, which predates tool annotations. We use the official `github.com/modelcontextprotocol/go-sdk` v1.8.0 for annotations, `structuredContent`/`outputSchema`, version negotiation and the in-memory test transport. kvtop should adopt it when it next touches MCP. |
| Q10 | How does OVN handle inter-VPC traffic in Kube-OVN without VPC peering? | M6 trace | **Resolved (2026-10-20, lab hv-01)**: In Kube-OVN, separate VPCs (separate Logical Routers) route non-local traffic to `0.0.0.0/0` via the VPC's external gateway (`cr-vpc-<name>-external`), egressing through `localnet.external` onto the physical underlay. `ovn-trace` verifies packet delivery to `localnet.external`; the physical upstream firewall then enforces inter-subnet access. |
| Q11 | Is `ovn-detrace` (and its python `ovs` bindings) shipped in the Kube-OVN images on Harvester, and in which pod can it reach NB, SB and the node's OVS db? Candidates: `ovs-ovn` (local OVS db; NB/SB over the `ovn-nb`/`ovn-sb` services, TLS?) or `ovn-central` (NB/SB sockets; `--ovsdb` only names ports?). | M6 detrace follow-up | open; check `which ovn-detrace` and a dry run in both pods on `hv-01` |
| Q12 | Which Pod labels, annotations and owner identify a Kube-OVN VPC NAT gateway pod, and do its two LSPs carry `external_ids:pod`? | M4 item 7 (ADR-0004) | **Partly answered (2026-10-27, lab hv-01):** both ports carry `external_ids:pod=kube-system/vpc-nat-gw-one-water-nat-0` and are attached through NADs, named `<pod>.<ns>.<nad>.<subnet>.ovn` (LAN on `subnet-onewater`, WAN on `external`). The TUI detects the gateway by pod name. Still unverified: the expected label `ovn.kubernetes.io/vpc-nat-gw=true`, annotation `ovn.kubernetes.io/vpc_nat_gw=<name>` and owner `StatefulSet/vpc-nat-gw-<name>` (`kubectl -n kube-system get pod vpc-nat-gw-one-water-nat-0 -o yaml`). |
| Q13 | Is ovn-kubernetes in scope? The targets (Rancher RKE2, Harvester) do not ship it, so supporting it means OpenShift or vanilla clusters. | ovn-kubernetes adapter | **Resolved (2026-10-27)**: yes, OpenShift is in scope (user request; lab `hol-powerflex-lab`, OCP 4.19.19). The chart does not work there today: the daemon stays unready with `lookup ovn-nb.kube-system.svc.cluster.local ... no such host`, because there is no central NB/SB. Moving the chart to another namespace does not help. Support is the ADR-0005 work, not a chart setting. |
| Q14 | ovn-kubernetes interconnect mode runs one NB/SB per node on a unix socket in `ovnkube-node`, with no central database. Which model: (a) DaemonSet zone collectors and a merger, (b) per-zone view, (c) central-DB deployments only? | ovn-kubernetes adapter, ADR-0002 | **Spike done (2026-10-27, `hol-powerflex-lab`, OCP 4.19.19, OVN 25.03.1)**; the model is proposed in ADR-0005, pending acceptance. Evidence: (1) each node is one zone: `nbdb`/`sbdb` containers in `ovnkube-node`, sockets `/var/run/ovn/ovn{nb,sb}_db.sock` (host `/var/run/ovn-ic`), `root:root 0750`, no Service; (2) zone snapshots share **no** row UUIDs; `ovn_cluster_router`, `transit_switch`, all Port_Groups/Address_Sets and most Load_Balancers are replicated by name; (3) every `type=remote` `tstor-<node>` port resolves to exactly one zone's local `tstor-<node>` (same MAC/IP), and every zone routes each other zone's pod subnet to that transit IP (6/6); (4) `Chassis.name` == node annotation `k8s.ovn.org/node-chassis-id`, with `other_config:is-remote` marking copies; (5) the pinned 24.03 models load the live NB 7.11.0 / SB 20.41.0 schemas with no errors. Captured with `hack/capture-fixtures.sh --flavor ovnk`. The fixtures hold lab data and are not committed. |
| Q15 | Do upstream ovn-kubernetes (kind, `contrib/kind.sh` interconnect) and OpenShift agree on pod labels, container names, socket paths and `external_ids`? Does local gateway mode change the per-zone NB as ADR-0004 decision 4 expects? | ADR-0005 | open; rerun the capture on a kind lab and commit an anonymous `ovnk-ic-kind` scenario |
