# kvovn-topo Helm Chart

`kvovn-topo` is a read-only network topology daemon for OVN as deployed by Kube-OVN on Rancher-managed RKE2 and Harvester clusters. On OpenShift (ovn-kubernetes), `kvovn-topo` reads one node at a time, live or from a capture; see [OpenShift](#openshift-ovn-kubernetes).

## Security

- **Read-only.** OVSDB has no read-only role, so read-only is enforced in code: `golangci-lint` (`forbidigo`) rejects every OVSDB write call ([docs/api.md](../../docs/api.md#security-rules)).
- **Access.** The daemon has no authentication of its own. It is reached only through the Kubernetes service proxy (`/api/v1/namespaces/<namespace>/services/http:kvovn-topod:http/proxy/api/v1/...`), which authenticates the caller; viewers need `services/proxy` RBAC ([Viewer access](#viewer-access)). Keep the Service `ClusterIP`: a NodePort, LoadBalancer or Ingress without an authenticating proxy would publish the whole topology unauthenticated.
- **Daemon RBAC.** `get`, `list` and `watch` on `nodes`, `pods`, `services`, `endpointslices`, KubeVirt `virtualmachines`/`virtualmachineinstances` and `network-attachment-definitions`, plus `get` on the OVN client secret when TLS is on. Nothing on Kube-OVN CRDs.
- **Runtime.** Non-root UID `65534` (assigned by the SCC on OpenShift), read-only root filesystem, all capabilities dropped.

## Installation

| Platform | CNI | Install | Status |
|---|---|---|---|
| RKE2 (Rancher-managed) | Kube-OVN | Helm chart, `kube-system` | supported |
| Harvester (Rancher-managed) | Kube-OVN addon | Helm chart, `kube-system` | supported |
| OpenShift 4.14+ | ovn-kubernetes, interconnect mode | no chart; `kvovn-topo --diag --zone` (live) or a capture (offline) | per node; no daemon yet (ADR-0005) |

### RKE2 and Harvester (Kube-OVN)

Install the chart once on each downstream cluster that runs Kube-OVN, in the namespace of the OVN databases:

```bash
helm repo add coulof https://coulof.github.io/kvovn-topo
helm install kvovn-topo coulof/kvovn-topo --namespace kube-system
```

The defaults point at the Kube-OVN Services `ovn-nb:6641` / `ovn-sb:6642` in `kube-system`. That is the layout of both upstream Kube-OVN on RKE2 and the Harvester Kube-OVN addon (ROADMAP Q3). If your OVN databases live elsewhere or use TLS, override the defaults:

```bash
helm install kvovn-topo coulof/kvovn-topo --namespace kube-system \
  --set ovn.nbEndpoints="ssl:ovn-nb.kube-system.svc.cluster.local:6641" \
  --set ovn.sbEndpoints="ssl:ovn-sb.kube-system.svc.cluster.local:6642" \
  --set ovn.tls.enabled=true --set ovn.tls.secretName=<secret with tls.crt, tls.key, ca.crt>
```

`ovn.*Endpoints` takes a comma-separated list. List every raft member so reads can use followers.

Check that the daemon is ready, then connect with the kubeconfig Rancher gives you for that cluster (see [Accessing the API](#accessing-the-api-harvester-and-rke2)):

```bash
kubectl -n kube-system rollout status deploy/kvovn-topod
kvovn-topo --kubeconfig ~/.kube/<cluster>.yaml
```

### OpenShift (ovn-kubernetes)

The daemon cannot run on OpenShift yet:
- Since 4.14, ovn-kubernetes runs in interconnect mode. Each node has its own NB/SB inside its `ovnkube-node` pod (`openshift-ovn-kubernetes`).
- Those databases listen only on root-owned unix sockets, and there is no `ovn-nb`/`ovn-sb` Service. Changing the namespace does not help.
- Live support needs per-node collectors and a merge, proposed in ADR-0005.

The chart detects OpenShift (`config.openshift.io/v1`). With the default endpoints, it refuses to install rather than deploy a daemon that is never ready. For a central-DB ovn-kubernetes, set `ovn.nbEndpoints`/`ovn.sbEndpoints` explicitly. On OpenShift the chart then drops the fixed `runAsUser`/`runAsGroup`/`fsGroup`, so the `restricted-v2` SCC can assign them.

Without the chart, `kvovn-topo` shows one node (zone) at a time, in two modes. Both need a user who can `pods/exec` into `openshift-ovn-kubernetes`, which in practice means cluster-admin.

#### Live (TUI, Tier 1)

```bash
kvovn-topo --diag --zone list        # the nodes, one zone each
kvovn-topo --diag --zone <node>
```

How it works:
- `kvovn-topo` opens a `pods/exec` stream to `socat` in the node's `ovnkube-node` pod (`nbdb` and `sbdb` containers), connected to the database's unix socket.
- It runs libovsdb monitors over that stream, so changes appear live, as with the daemon.
- It serves the graph to itself on `127.0.0.1`.
- Nothing is deployed in the cluster.
- The `x` diagnostics run in the `ovnkube-node` pod of the selected object's node: `ovn-trace` in `sbdb`; OVS, conntrack and tcpdump in `ovn-controller`; logs from `ovn-controller`'s container log. `kvovn-topo` detects the namespace. A trace stays inside that node's zone.

**These sockets accept writes.** `kvovn-topo` stays read-only only because of its own code: no write calls on the OVSDB client, enforced by `forbidigo` in CI. OVN itself does not stop a write. This mode is exec-based, so ADR-0001 keeps it in the TUI, behind `--diag`. MCP does not offer it.

#### Offline (TUI or MCP)

Use this mode for MCP clients, for users without exec rights at viewing time, or to share or keep a snapshot:

```bash
# From a clone of the repository. Read-only: ovsdb-client select queries and kubectl get.
KUBECTL=oc hack/capture-fixtures.sh my-cluster --flavor ovnk

# One directory per node (zone); open one in the TUI or serve it to an MCP client
kvovn-topo --fixture-dir testdata/scenarios/my-cluster/zone-<node>
kvovn-topo mcp --fixture-dir testdata/scenarios/my-cluster/zone-<node>
```

The capture holds real names, IPs and MACs. Keep it out of the repository.

#### Limits of both modes

- Each node's view shows its own switch, `GR_<node>` and pods. Traffic to other nodes stops at `transit_switch`. A cluster-wide view needs the ADR-0005 merge.
- Pods show their namespace but not their workload; the ovn-kubernetes identity rules are not implemented yet (ADR-0004).

If an earlier install left a daemon that never becomes ready, remove it:

```bash
helm uninstall kvovn-topo --namespace kube-system
```

## Accessing the API (Harvester and RKE2)

`kvovn-topo` (TUI) and `kvovn-topo mcp` (AI agents) go through the service proxy of your kubeconfig by default; no port-forward, IP or Ingress is needed. With no flags they use `kube-system/kvovn-topod`, or find the Service by the label `app.kubernetes.io/name=kvovn-topo` in any namespace. `--namespace` and `--service` pin it.

| Cluster | API base URL |
|---|---|
| Rancher-managed (Harvester or RKE2) | `https://<rancher>/k8s/clusters/<cluster-id>/api/v1/namespaces/kube-system/services/http:kvovn-topod:http/proxy/api/v1` |
| Harvester, direct | `https://<harvester-vip>:6443/api/v1/namespaces/kube-system/services/http:kvovn-topod:http/proxy/api/v1` |

Use the kubeconfig that Rancher (or Harvester) gives you for that cluster:

```bash
kvovn-topo --kubeconfig ~/.kube/harvester.yaml          # TUI
kvovn-topo mcp --kubeconfig ~/.kube/harvester.yaml      # MCP server
curl -sH "Authorization: Bearer $TOKEN" \
  "https://<rancher>/k8s/clusters/<cluster-id>/api/v1/namespaces/kube-system/services/http:kvovn-topod:http/proxy/api/v1/version"
```

On Harvester, kube-vip and its IP pools can give a `LoadBalancer` Service an address, and rke2-ingress-nginx serves the VIP. Neither is used here on purpose: both would bypass Kubernetes authentication and RBAC. A browser-facing hostname would need an authenticating proxy in front of the daemon, such as kube-rbac-proxy; that needs its own ADR.

### Viewer access

Set `rbac.viewer.create=true` to create:
- a Role `<fullname>-viewer` in the release namespace: `get` on `services/proxy` and `services` for the daemon Service;
- unless `rbac.viewer.discovery=false`, a ClusterRole `<fullname>-discovery`: `list` on `services`, so the clients can find the Service outside the default location.

No bindings are created. Bind users or groups yourself, or add the rules to a Rancher cluster or project role:

```bash
kubectl -n kube-system create rolebinding kvovn-topo-viewers \
  --role kvovn-topod-viewer --group network-viewers
kubectl create clusterrolebinding kvovn-topo-discovery \
  --clusterrole kvovn-topod-discovery --group network-viewers
```

## Configuration

| Parameter | Description | Default |
|---|---|---|
| `image.repository` | Daemon container image | `ghcr.io/coulof/kvovn-topod` |
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
