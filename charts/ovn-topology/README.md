# ovn-topology Helm Chart

`ovn-topology` is a read-only network topology daemon for OVN as deployed by Kube-OVN on Rancher-managed RKE2 and Harvester clusters.

## Security Architecture & Read-Only Guarantee

1. **Strictly Read-Only**: OVSDB (OVN Northbound and Southbound) does not natively offer a role-based read-only user mode. In `ovn-topology`, read-only access is guaranteed at the code and compilation level:
   - No write methods (`Create`, `Update`, `Mutate`, `Delete`, `Transact` with mutations) are called on any OVSDB client.
   - Enforced by continuous `golangci-lint` with the `forbidigo` rule on all builds.
2. **Access Control**:
   - Access to the daemon API is secured via the Kubernetes service proxy:
     `/api/v1/namespaces/<namespace>/services/http:ovn-topologyd:8080/proxy/api/v1/...`
   - Viewer authorization requires standard Kubernetes `services/proxy` RBAC permissions.
3. **Daemon RBAC**:
   - The daemon ServiceAccount is granted strictly `get`, `list`, and `watch` permissions on `Node` objects (solely to map Chassis hostname ↔ Kubernetes Node).
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
| `resources.limits.cpu` | CPU limit | `500m` |
| `resources.limits.memory` | Memory limit | `512Mi` |
