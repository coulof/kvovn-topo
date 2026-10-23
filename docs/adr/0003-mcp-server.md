# ADR-0003 — MCP server: SDK, backends and tool gating

- Status: Proposed
- Date: 2026-10-23
- Deciders: coulof

## Context

M9 (`ROADMAP.md`) adds an MCP server so AI agents can answer network questions from real OVN data. The roadmap already binds the transport (stdio, `ovn-topo mcp`), the two backends (A: daemon through the Kubernetes service proxy; B: embedded `pkg/graph` against OVSDB), the T0-only rule (ADR-0001) and the output discipline. It leaves four things open:

1. **Q9: which MCP Go library.** The roadmap says to use the official Go SDK unless kvtop uses a different one. kvtop (`internal/mcp`) uses **no SDK**: about 550 lines of hand-written JSON-RPC that pin protocol `2024-11-05`.
2. **How `find` works on backend A.** The daemon API has no search endpoint. The only way to enumerate LSPs is `view=l2` page by page, per LS.
3. **What to do with tools whose data does not exist yet.** M9 depends on M5 (path) and on M8 for history. M4 (IP resolver, ACLs, checks) and M5 are not started (status register, 2026-10-23). Six of the ten planned tools have no data source.
4. **How the prompt-injection rule is enforced** rather than just stated.

Forces: air-gapped targets (no runtime network calls beyond the cluster), the read-only hard rule, a small dependency budget (AGENTS.md §10), and LLM consumers that need compact, stable JSON.

## Decision

1. **Library: official SDK `github.com/modelcontextprotocol/go-sdk` (v1.8.0).** kvtop is not aligned with it; it should migrate when it next touches MCP. That is not in this repo's scope.
2. **One `Backend` interface in `pkg/mcp`** with `Name`, `Graph`, `Node`, `Find` and `Chassis`:
   - **A, `HTTPBackend`:** talks to the daemon's `/api/v1`, either at an explicit `--daemon-url` or through the API server's service proxy built from the user's kubeconfig (`/api/v1/namespaces/<ns>/services/http:<svc>:http/proxy`). The viewer needs `services/proxy` on that Service, which is the same RBAC as the UI.
   - **B, `SnapshotBackend`:** reads a `graph.Store`. `ovn-topo mcp --nb … --sb …` fills it from the OVSDB syncer: the user runs `kubectl port-forward` or has direct reach. `--fixture` fills it from a committed snapshot, which tests use too.

   Both backends run the same `pkg/graph` code. Their outputs differ only in `backend`.
3. **`find` is server-side: `GET /api/v1/find`** is added to `api/openapi.yaml` and implemented as `graph.Snapshot.Find`. Backend B calls the same function in process. The search covers names, IP, MAC, namespace, pod, VM and chassis hostname or encap IP, with an optional type filter and offset paging.
4. **Tools are registered only when their milestone has shipped.** This change registers `find`, `get_graph`, `get_node` and `list_chassis`. `list_chassis` is not in the roadmap's table: it exposes `/chassis` (gateway roles and HA priorities), which `get_graph view=chassis` does not carry. `resolve_ip`, `effective_acls` and `run_checks` arrive with M4, `compute_path` with M5, and the history tools with M8, in the PR that ships their data. The `at` argument is not offered until M8. A tool that exists but cannot run on a given backend (history on B) returns a tool error with `code: unavailable_on_backend`. It never returns an empty success.
5. **Every result is an envelope:** `{revision, timestamp, historical, backend, summary, truncated, next, data}`, sent as `structuredContent` with an output schema, and mirrored as one JSON text block for clients without structured content. Caps:
   - `find`: at most 50 items.
   - `get_graph`: at most 150 nodes. Over the cap, it returns counts per type and the IDs of the largest containers instead of the graph.
   - `l2`: uses the daemon's pagination, at most 100 per page.

   Cursors are opaque strings.
6. **Injection hygiene is enforced by types.** `summary` is built by a `summarize` helper whose arguments are only integers, closed enums (`view`, node type) and IDs (`<type>:<uuid>`). It has no string parameter that could carry a name or an `external_ids` value. Names and descriptions appear only inside `data`. A fixture with instruction-like LSP names and `external_ids` proves this (see DoD).
7. **No T1 path.** `pkg/mcp` imports `pkg/graph`, `pkg/api` and the SDK only, and `depguard` plus `hack/check-deps.sh` already cover it. `ovn-topo mcp` never builds a diag executor, even with `--diag`.
8. **stdout belongs to the protocol.** All logs go to stderr through `slog`.

## Options considered

### Q9. Option A: official Go SDK *(chosen)*

| Dimension | Assessment |
|---|---|
| Complexity | Low: tool handlers are typed functions, and the SDK infers schemas from structs |
| Cost | One direct dependency (+ `google/jsonschema-go`, `segmentio/encoding`, `yosida95/uritemplate` in `go.mod`; nothing at runtime beyond stdio) |
| Protocol coverage | Current spec: annotations, `structuredContent`/`outputSchema`, version negotiation, cancellation |
| Team familiarity | Medium; same author as kvtop, new library |

**Pros:** M9 requires read-only annotations (`readOnlyHint`, etc.). Those arrived in protocol `2025-03-26`, after kvtop's pinned `2024-11-05`. The SDK also gives typed input validation, an in-memory transport for golden tests, and protocol upgrades for free.
**Cons:** It adds a dependency and departs from kvtop's style.

### Q9. Option B: hand-rolled JSON-RPC, as in kvtop

| Dimension | Assessment |
|---|---|
| Complexity | Medium: about 500 lines to own, plus hand-written schemas |
| Cost | No dependency |
| Protocol coverage | Stuck at whatever is implemented. Annotations and structured output would be new code |
| Team familiarity | High (kvtop) |

**Pros:** no dependency, and the same code shape as kvtop.
**Cons:** we would maintain protocol code ourselves and hand-write JSON schemas that can drift from the Go types. Without version negotiation, newer clients either downgrade or break.

### Find. Option A: `/find` endpoint *(chosen)* vs Option B: client-side crawl

Crawling means one `/graph?view=l2` request per LS page for every query. On a 10k-LSP cluster that is hundreds of requests per question, and it breaks the "server-side aggregation" rule. One endpoint, shared with backend B, is cheaper and testable with goldens. A future TUI search could also use it.

### Unbuilt tools. Option A: register only shipped tools *(chosen)* vs Option B: register all ten with "not implemented" errors

Agents plan from `tools/list`. A tool that always fails wastes turns, and it advertises answers we cannot give, such as "approximate path". Option A keeps `tools/list` honest. Each milestone then adds its tools with its data.

## Trade-off analysis

The SDK costs one dependency but buys protocol currency and typed schemas. The read-only annotations M9 requires are exactly the feature kvtop's hand-rolled code does not have. Gating tools by milestone makes M9 ship in slices. That breaks the roadmap's order rule again (M9 before M4/M5), so the status register records it. The DoD stays per tool: each slice closes its own golden, injection and scenario tests.

## Consequences

- Easier: agents get a working `find → get_node → get_graph` loop today, on both backends and on fixtures.
- Easier: `/find` is a new T0 API that the TUI can use later.
- Harder: `api/openapi.yaml` grows (`/find`, `FindResponse`). It stays additive.
- Revisit: when M4, M5 and M8 land, add their tools and fill the scenario eval (`test/mcp/`). Also revisit if MCP over HTTP in the daemon is scheduled; it needs its own ADR for auth.
- kvtop drifts from this repo until it adopts the SDK.

## Action items

1. [x] `GET /find` in `api/openapi.yaml`, `graph.Snapshot.Find`, and the server handler.
2. [x] `pkg/mcp`: `Backend`, `HTTPBackend`, `SnapshotBackend`, the four tools, envelope, caps, `summarize`.
3. [x] `ovn-topo mcp` subcommand (kubeconfig service proxy / `--daemon-url` / `--nb --sb` / `--fixture`).
4. [x] Golden tests per tool on the fixture, an injection test, and a caps/cursor test.
5. [x] `docs/mcp.md` with client configs. Answer Q9 in `ROADMAP.md`.
6. [ ] M4: `resolve_ip`, `effective_acls`, `run_checks`. M5: `compute_path`. M8: history tools, `at`.
7. [ ] `test/mcp/` scenario eval once named scenario captures exist (B1).
