# MCP server: `ovn-topo mcp`

`ovn-topo mcp` serves the OVN topology to AI agents over the [Model Context Protocol](https://modelcontextprotocol.io) on stdio. The agent can look up objects and their neighbours ("which switch is 10.244.0.10 on, and which chassis is it bound to?") from the same graph the daemon and the TUI use. Design: [ADR-0003](adr/0003-mcp-server.md).

## Guarantees

- **Read-only, Tier 0 only.** Every tool reads the graph and nothing else. Nothing writes to OVSDB or Kubernetes. No tool runs `exec`, `tcpdump` or `ovn-trace`, with or without `--diag`. `pkg/mcp` cannot import `pkg/diag`; `depguard` and `hack/check-deps.sh` enforce this in CI (ADR-0001).
- Every tool is annotated `readOnlyHint: true`, `destructiveHint: false`, `idempotentHint: true`, `openWorldHint: false`.
- **Cluster text stays data.** Object names, `external_ids` and other strings come from cluster users. They appear only inside a result's `data`. The `summary` line is built from counts only, and a test (`TestInjectionStaysInData`) proves that hostile names never reach it.
- No network calls outside the cluster (air-gap safe).

## Tools

| Tool | Purpose |
|---|---|
| `find` | Search LR, LS, LRP, LSP and chassis by name, IP, MAC, namespace, pod, VM or chassis hostname / encap IP. Returns IDs. |
| `get_graph` | `view` = `l3` (default), `l2` (needs `focus=ls:<id>`, paginated) or `chassis`; optional `focus` and `depth` (1–5). |
| `get_node` | Full detail of one object: OVSDB columns, `external_ids`, Kubernetes identity, related objects. |
| `list_chassis` | Chassis with encaps, node name, binding counts, gateway roles and HA priorities. |

The roadmap's other tools are added with the milestone that provides their data:
- `resolve_ip`, `effective_acls`, `run_checks` (M4)
- `compute_path` (M5)
- `list_changes`, `diff`, `history_coverage` and the `at` argument (M8)

Every result has the same envelope:

```json
{
  "revision": 42,
  "timestamp": "2026-10-23T12:00:00Z",
  "historical": false,
  "backend": "daemon",
  "summary": "3 of 3 matches (3 exact): 3 LSP",
  "truncated": false,
  "next": "",
  "data": { }
}
```

Results are capped:
- `find`: 50 hits.
- `get_graph`: 150 nodes. A larger graph returns counts and the 10 most-connected nodes instead.
- `l2`: 100 ports per page.
- `get_node`: 100 related objects and 50 entries per raw list column.
- `list_chassis`: 100 chassis.

When `truncated` is true, pass `next` back as `cursor` to get the next page. Errors are tool errors whose text starts with a stable code: `bad_request`, `not_found`, `not_ready`, `unavailable_on_backend` or `backend_error`.

## Backends

| Mode | Flags | Needs |
|---|---|---|
| Daemon via service proxy (default) | `--namespace kube-system --service ovn-topologyd --port http`, optional `--kubeconfig`, `--context` | `get` on `services/proxy` for the daemon Service. This is the same RBAC as the UI. |
| Daemon at a URL | `--daemon-url http://127.0.0.1:8080/api/v1` | Network reach, e.g. `kubectl -n kube-system port-forward svc/ovn-topologyd 8080` |
| Embedded graph | `--nb tcp:127.0.0.1:6641 --sb tcp:127.0.0.1:6642` (plus `--ovn-cert/--ovn-key/--ovn-ca` for TLS) | Reach to OVSDB, e.g. `kubectl -n kube-system port-forward svc/ovn-nb 6641` and `svc/ovn-sb 6642`. Uses the kubeconfig for node and workload names when it loads. |
| Fixture | `--fixture` (dev fixture, run from the repo) or `--fixture-dir testdata/scenarios/<name>` | Nothing. |

Logs go to stderr (`--log-level`, default `warn`); stdout carries only the protocol.

## Client configuration

The examples use the default backend (service proxy through the current kubeconfig context). Add flags to `args` for the other modes.

**Claude Code**

```sh
claude mcp add ovn-topology -- ovn-topo mcp --context my-cluster
```

or in `.mcp.json`:

```json
{
  "mcpServers": {
    "ovn-topology": {
      "command": "ovn-topo",
      "args": ["mcp", "--context", "my-cluster"]
    }
  }
}
```

**OpenCode** (`opencode.json`):

```json
{
  "$schema": "https://opencode.ai/config.json",
  "mcp": {
    "ovn-topology": {
      "type": "local",
      "command": ["ovn-topo", "mcp", "--context", "my-cluster"],
      "enabled": true
    }
  }
}
```

**Gemini CLI** (`~/.gemini/settings.json`):

```json
{
  "mcpServers": {
    "ovn-topology": {
      "command": "ovn-topo",
      "args": ["mcp", "--context", "my-cluster"]
    }
  }
}
```

To try it without a cluster, use `"args": ["mcp", "--fixture-dir", "/path/to/ovn-topology/testdata/scenarios/hv-01-lab"]`.
