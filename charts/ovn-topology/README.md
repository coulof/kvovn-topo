# ovn-topology Helm Chart

`ovn-topology` is a read-only network topology daemon for OVN as deployed by Kube-OVN on Rancher-managed RKE2 and Harvester clusters.

## Security Architecture & Read-Only Guarantee

1. **Strictly Read-Only**: OVSDB (OVN Northbound and Southbound) does not natively offer a role-based read-only user mode. In `ovn-topology`, read-only access is guaranteed at the code and compilation level:
   - No write methods (`Create`, `Update`, `Mutate`, `Delete`, `Transact` with mutations) are called on any OVSDB client.
   - Enforced by continuous `golangci-lint` with the `forbidigo` rule on all builds.
2. **Access Control**:
   - The daemon has no authentication of its own. The API is reached only through the Kubernetes service proxy, which authenticates the caller and enforces RBAC:
     `/api/v1/namespaces/<namespace>/services/http:ovn-topologyd:http/proxy/api/v1/...`
   - Viewer authorization requires standard Kubernetes `services/proxy` RBAC permissions (see [Viewer access](#viewer-access)).
   - The Service stays `ClusterIP`. Do not expose it with a NodePort, a LoadBalancer or an Ingress without an authenticating proxy: that would publish the whole topology unauthenticated.
3. **Daemon RBAC**:
   - The daemon ServiceAccount is granted strictly `get`, `list`, and `watch` on the kinds it enriches the graph with: `nodes`, `pods`, `services`, `endpointslices`, KubeVirt `virtualmachines`/`virtualmachineinstances` and `network-attachment-definitions`.
   - If TLS is enabled, read-only (`get`) access is granted to the designated OVN client secret.
   - No permissions exist to read or mutate Kube-OVN CRDs or any other cluster resources.
4. **Runtime Security**:
   - Runs as non-root user `65534` (nobody).
   - Read-only root filesystem (`readOnlyRootFilesystem: true`).
   - Drops all Linux capabilities (`capabilities.drop: [ALL]`).

## Installation

Install on downstream clusters running Kube-OVN:

```bash
helm repo add coulof https://coulof.github.io/ovn-topology
helm install ovn-topology coulof/ovn-topology \
  --namespace kube-system \
  --set ovn.nbEndpoints="tcp:ovn-nb.kube-system.svc.cluster.local:6641" \
  --set ovn.sbEndpoints="tcp:ovn-sb.kube-system.svc.cluster.local:6642"
```

## Accessing the API (Harvester and RKE2)

`ovn-topo` (TUI) and `ovn-topo mcp` (AI agents) go through the service proxy of your kubeconfig by default; no port-forward, IP or Ingress is needed. With no flags they use `kube-system/ovn-topologyd`, or find the Service by the label `app.kubernetes.io/name=ovn-topology` in any namespace. `--namespace` and `--service` pin it.

| Cluster | API base URL |
|---|---|
| Rancher-managed (Harvester or RKE2) | `https://<rancher>/k8s/clusters/<cluster-id>/api/v1/namespaces/kube-system/services/http:ovn-topologyd:http/proxy/api/v1` |
| Harvester, direct | `https://<harvester-vip>:6443/api/v1/namespaces/kube-system/services/http:ovn-topologyd:http/proxy/api/v1` |

Use the kubeconfig that Rancher (or Harvester) gives you for that cluster:

```bash
ovn-topo --kubeconfig ~/.kube/harvester.yaml          # TUI
ovn-topo mcp --kubeconfig ~/.kube/harvester.yaml      # MCP server
curl -sH "Authorization: Bearer $TOKEN" \
  "https://<rancher>/k8s/clusters/<cluster-id>/api/v1/namespaces/kube-system/services/http:ovn-topologyd:http/proxy/api/v1/version"
```

On Harvester, kube-vip and its IP pools can give a `LoadBalancer` Service an address, and rke2-ingress-nginx serves the VIP. Neither is used here on purpose: both would bypass Kubernetes authentication and RBAC. A browser-facing hostname would need an authenticating proxy in front of the daemon, such as kube-rbac-proxy; that needs its own ADR.

### Viewer access

Set `rbac.viewer.create=true` to create:
- a Role `<fullname>-viewer` in the release namespace: `get` on `services/proxy` and `services` for the daemon Service;
- unless `rbac.viewer.discovery=false`, a ClusterRole `<fullname>-discovery`: `list` on `services`, so the clients can find the Service outside the default location.

No bindings are created. Bind users or groups yourself, or add the rules to a Rancher cluster or project role:

```bash
kubectl -n kube-system create rolebinding ovn-topology-viewers \
  --role ovn-topologyd-viewer --group network-viewers
kubectl create clusterrolebinding ovn-topology-discovery \
  --clusterrole ovn-topologyd-discovery --group network-viewers
```

## Configuration

| Parameter | Description | Default |
|---|---|---|
| `image.repository` | Daemon container image | `ghcr.io/coulof/ovn-topologyd` |
| `image.tag` | Daemon container tag | `v0.1.0` |
| `ovn.nbEndpoints` | OVN Northbound OVSDB connection endpoints | `tcp:ovn-nb.kube-system.svc.cluster.local:6641` |
| `ovn.sbEndpoints` | OVN Southbound OVSDB connection endpoints | `tcp:ovn-sb.kube-system.svc.cluster.local:6642` |
| `ovn.tls.enabled` | Enable TLS for OVSDB connections | `false` |
| `ovn.tls.secretName` | Kubernetes secret containing TLS cert/key/CA | `""` |
| `service.port` | HTTP service port | `8080` |
| `rbac.viewer.create` | Create the viewer Role (and discovery ClusterRole); no bindings | `false` |
| `rbac.viewer.discovery` | With `rbac.viewer.create`, also create the `list services` ClusterRole | `true` |
| `resources.limits.cpu` | CPU limit | `500m` |
| `resources.limits.memory` | Memory limit | `512Mi` |
