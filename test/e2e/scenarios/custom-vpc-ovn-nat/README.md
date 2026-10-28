# custom-vpc-ovn-nat

Custom VPC `vpc-a` with OVN NAT: a distributed FIP on pod `web-1` and an SNAT for subnet `vpc-a-net`, both on EIPs from the `external` underlay subnet (ProviderNetwork on `egress0`, a dummy NIC in each VM). The centralized gateway is `ovn-topo-agent1`.

Expected NB: LR `vpc-a` with a gateway LRP to LS `external` (localnet), a `dnat_and_snat` NAT row with `logical_port` = `web-1.vpc-a`, a `snat` row for `10.10.0.0/24`, and the `0.0.0.0/0` static route.

```
test/e2e/scenarios/apply.sh custom-vpc-ovn-nat
kubectl get ovn-eip,ovn-fip,ovn-snat     # all Ready
kubectl ko nbctl lr-nat-list vpc-a
```

Enabling the external gateway also connects the default VPC: `ovn-cluster` gets its own gateway LRP (`ovn-cluster-external`) and EIP on `external`, so LS `external` is attached to two LRs.

The ProviderNetwork takes `egress0` into OVS. Delete this scenario before applying `vpc-nat-gw`, which puts a macvlan on the same NIC.
