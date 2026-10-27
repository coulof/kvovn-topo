# API & Security Reference

This reference details the daemon's HTTP/SSE API endpoints, access patterns via the Kubernetes service proxy, and security design.

The daemon OpenAPI contract is located in the root repository under `api/openapi.yaml`.

---

## Design and Security Rules

- **Strictly read-only**: OVSDB does not support a native read-only user role. Read-only access is guaranteed in code and enforced at build time by `golangci-lint` using `forbidigo`. The codebase contains no write operations (`Create`, `Update`, `Mutate`, `Delete`, `Transact`) against OVSDB or the Kubernetes API.
- **OVSDB is the source of truth**: The daemon does not read Kube-OVN CRDs (such as `Vpc`, `Subnet`, or `IP`). Kubernetes identity metadata comes from OVN NB `external_ids`. Supported Kubernetes reads are get/list/watch on `Node`, `Pod`, `Service`, `EndpointSlice`, KubeVirt `VirtualMachine`/`VirtualMachineInstance`, and `NetworkAttachmentDefinition` to enrich graph nodes (node names, workload identity), never to create them.
- **Access control**: The daemon has no authentication mechanism of its own and is never exposed directly (no NodePort, LoadBalancer, or Ingress). API access goes through the Kubernetes service proxy (`/api/v1/namespaces/<namespace>/services/http:ovn-topologyd:http/proxy/api/v1/...`), which authenticates the caller and checks RBAC. The TUI and `ovn-topo mcp` use this path by default.
- **Air-gap friendly**: No runtime calls to the internet, no CDN dependencies, and non-root distroless container images.

---

## API Summary

The base path for the API is `/api/v1`.

| Endpoint | Method | Description |
|---|---|---|
| `/graph?view=l3` | GET | Aggregated L3 graph (VPCs, LRs, LSs, external networks) |
| `/graph?view=l2&focus=ls:<id>` | GET | Paginated L2 switch ports and chassis bindings |
| `/graph?view=chassis` | GET | Chassis list with encaps and binding counts |
| `/node/{id}` | GET | Full details and raw OVSDB columns for a node |
| `/chassis` | GET | Chassis details, HA groups, and gateway roles |
| `/find?q=<text>&type=&limit=&offset=` | GET | Search objects by name, IP, MAC, namespace, pod, VM |
| `/events` | GET | SSE stream emitting graph revision changes |
| `/readyz` | GET | Readiness probe (returns 200 after initial DB sync) |
| `/healthz` | GET | Liveness probe |
| `/version` | GET | Daemon version and OVN schema versions |

- **Server-side aggregation**: To avoid unbounded LSP lists in `/graph`, LS nodes carry port counts. Individual LSPs only appear in `view=l2&focus=ls:<id>`, paginated.
- **SSE degradation**: If the SSE stream breaks (due to intermediate proxy buffering), the TUI and other clients automatically fall back to polling `/graph` with `If-None-Match: <revision>`.

---

## Accessing the API (Harvester and RKE2)

Clients find the daemon Service (`kube-system/ovn-topologyd` or via the chart label) and request through the API server's service proxy.

| Cluster type | API Base URL |
|---|---|
| **Rancher-managed** (Harvester or RKE2) | `https://<rancher>/k8s/clusters/<cluster-id>/api/v1/namespaces/kube-system/services/http:ovn-topologyd:http/proxy/api/v1` |
| **Harvester direct** | `https://<harvester-vip>:6443/api/v1/namespaces/kube-system/services/http:ovn-topologyd:http/proxy/api/v1` |

Use the kubeconfig issued by Rancher or Harvester for that cluster to query:

```bash
# Query the version endpoint through the service proxy
curl -sH "Authorization: Bearer $TOKEN" \
  "https://<rancher>/k8s/clusters/<cluster-id>/api/v1/namespaces/kube-system/services/http:ovn-topologyd:http/proxy/api/v1/version"
```
