# ovn-topology Helm Chart

`ovn-topology` is a read-only network topology daemon for OVN as deployed by Kube-OVN on Rancher-managed RKE2 and Harvester clusters. On OpenShift (ovn-kubernetes), `ovn-topo` reads one node at a time, live or from a capture; see [OpenShift](#openshift-ovn-kubernetes).

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
   - Runs as non-root user `65534` (nobody). On OpenShift the SCC assigns the UID instead.
   - Read-only root filesystem (`readOnlyRootFilesystem: true`).
   - Drops all Linux capabilities (`capabilities.drop: [ALL]`).

## Installation

| Platform | CNI | Install | Status |
|---|---|---|---|
| RKE2 (Rancher-managed) | Kube-OVN | Helm chart, `kube-system` | supported |
| Harvester (Rancher-managed) | Kube-OVN addon | Helm chart, `kube-system` | supported |
| OpenShift 4.14+ | ovn-kubernetes, interconnect mode | no chart; `ovn-topo --diag --zone` (live) or a capture (offline) | per node; no daemon yet (ADR-0005) |

### RKE2 and Harvester (Kube-OVN)

Install the chart once on each downstream cluster that runs Kube-OVN, in the namespace of the OVN databases:

```bash
helm repo add coulof https://coulof.github.io/ovn-topology
helm install ovn-topology coulof/ovn-topology --namespace kube-system
```

The defaults point at the Kube-OVN Services `ovn-nb:6641` / `ovn-sb:6642` in `kube-system`. That is the layout of both upstream Kube-OVN on RKE2 and the Harvester Kube-OVN addon (ROADMAP Q3). If your OVN databases live elsewhere or use TLS, override the defaults:

```bash
helm install ovn-topology coulof/ovn-topology --namespace kube-system \
  --set ovn.nbEndpoints="ssl:ovn-nb.kube-system.svc.cluster.local:6641" \
  --set ovn.sbEndpoints="ssl:ovn-sb.kube-system.svc.cluster.local:6642" \
  --set ovn.tls.enabled=true --set ovn.tls.secretName=<secret with tls.crt, tls.key, ca.crt>
```

`ovn.*Endpoints` takes a comma-separated list. List every raft member so reads can use followers.

Check that the daemon is ready, then connect with the kubeconfig Rancher gives you for that cluster (see [Accessing the API](#accessing-the-api-harvester-and-rke2)):

```bash
kubectl -n kube-system rollout status deploy/ovn-topologyd
ovn-topo --kubeconfig ~/.kube/<cluster>.yaml
```

### OpenShift (ovn-kubernetes)

The daemon cannot run on OpenShift yet:
- Since 4.14, ovn-kubernetes runs in interconnect mode. Each node has its own NB/SB inside its `ovnkube-node` pod (`openshift-ovn-kubernetes`).
- Those databases listen only on root-owned unix sockets, and there is no `ovn-nb`/`ovn-sb` Service. Changing the namespace does not help.
- Live support needs per-node collectors and a merge, proposed in ADR-0005.

The chart detects OpenShift (`config.openshift.io/v1`). With the default endpoints, it refuses to install rather than deploy a daemon that is never ready. For a central-DB ovn-kubernetes, set `ovn.nbEndpoints`/`ovn.sbEndpoints` explicitly. On OpenShift the chart then drops the fixed `runAsUser`/`runAsGroup`/`fsGroup`, so the `restricted-v2` SCC can assign them.

Without the chart, `ovn-topo` shows one node (zone) at a time, in two modes. Both need a user who can `pods/exec` into `openshift-ovn-kubernetes`, which in practice means cluster-admin.

#### Live (TUI, Tier 1)

```bash
ovn-topo --diag --zone list        # the nodes, one zone each
ovn-topo --diag --zone <node>
```

How it works:
- `ovn-topo` opens a `pods/exec` stream to `socat` in the node's `ovnkube-node` pod (`nbdb` and `sbdb` containers), connected to the database's unix socket.
- It runs libovsdb monitors over that stream, so changes appear live, as with the daemon.
- It serves the graph to itself on `127.0.0.1`.
- Nothing is deployed in the cluster.

**These sockets accept writes.** `ovn-topo` stays read-only only because of its own code: no write calls on the OVSDB client, enforced by `forbidigo` in CI. OVN itself does not stop a write. This mode is exec-based, so ADR-0001 keeps it in the TUI, behind `--diag`. MCP does not offer it.

#### Offline (TUI or MCP)

Use this mode for MCP clients, for users without exec rights at viewing time, or to share or keep a snapshot:

```bash
# From a clone of the repository. Read-only: ovsdb-client select queries and kubectl get.
KUBECTL=oc hack/capture-fixtures.sh my-cluster --flavor ovnk

# One directory per node (zone); open one in the TUI or serve it to an MCP client
ovn-topo --fixture-dir testdata/scenarios/my-cluster/zone-<node>
ovn-topo mcp --fixture-dir testdata/scenarios/my-cluster/zone-<node>
```

The capture holds real names, IPs and MACs. Keep it out of the repository.

#### Limits of both modes

- Each node's view shows its own switch, `GR_<node>` and pods. Traffic to other nodes stops at `transit_switch`. A cluster-wide view needs the ADR-0005 merge.
- Pods show their namespace but not their workload; the ovn-kubernetes identity rules are not implemented yet (ADR-0004).

If an earlier install left a daemon that never becomes ready, remove it:

```bash
helm uninstall ovn-topology --namespace kube-system
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
