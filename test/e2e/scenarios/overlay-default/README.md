# overlay-default

The default VPC only: k3s v1.35 with Kube-OVN v1.16.2 (geneve overlay) on three Lima VMs, and a 2-replica nginx Deployment in `demo` spread over both agents behind a ClusterIP Service.

Expected NB: `ovn-cluster` with `ovn-default` and `join`; two `demo` pod LSPs, one bound on each agent's chassis; a `Load_Balancer` for `demo/web`.

```
test/e2e/scenarios/apply.sh overlay-default
kubectl -n demo rollout status deploy/web
kubectl ko nbctl show ovn-cluster
```
