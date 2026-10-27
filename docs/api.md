# API & Security Reference

The contract is `api/openapi.yaml`. How to reach the API (service-proxy URLs for Rancher and Harvester, viewer RBAC) is in the [chart README](../charts/ovn-topology/README.md#accessing-the-api-harvester-and-rke2).

## Security rules

- **Read-only.** OVSDB has no read-only role, so read-only is enforced in code: `golangci-lint` (`forbidigo`) rejects any OVSDB `Create`, `Update`, `Mutate`, `Delete` or write `Transact`, and any write call on the Kubernetes client.
- **OVSDB is the source of truth.** No Kube-OVN CRDs are read. Kubernetes identity comes from NB `external_ids`; the daemon only gets, lists and watches `Node`, `Pod`, `Service`, `EndpointSlice`, KubeVirt `VirtualMachine`/`VirtualMachineInstance` and `NetworkAttachmentDefinition`, to enrich graph nodes, never to create them.
- **No exposure.** The daemon has no authentication of its own and is never published (no NodePort, LoadBalancer or Ingress). Clients go through the API server's service proxy, which authenticates them and checks `services/proxy` RBAC.
- **Air-gap.** No runtime internet calls; distroless, non-root image.

## Endpoints

Base path `/api/v1`.

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

`/graph` never lists every port: switches carry port counts, and ports appear only in `view=l2&focus=ls:<id>`, paginated. `/node/{id}` on a router adds `staticRoutes`, `policies` and `nats`. If the SSE stream breaks (a proxy may buffer it), clients poll `/graph` with `If-None-Match: <revision>`.
