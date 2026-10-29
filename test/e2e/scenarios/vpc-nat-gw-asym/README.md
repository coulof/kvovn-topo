# vpc-nat-gw-asym

Custom VPC `vpc-c` with two iptables VPC NAT gateways (macvlan WAN legs on `egress0`): `gw-out` (lanIp 10.30.0.254, on `ovn-topo-agent1`) takes the VPC's `0.0.0.0/0` route and SNATs `vpc-c-net`; `gw-in` (lanIp 10.30.0.253, on `ovn-topo-agent2`) DNATs 8080 to pod `web` (10.30.0.10). No `src-ip` route or reroute leads to `gw-in`.

This is the ADR-0004 asymmetric return: replies to flows `gw-in` DNATs follow `0.0.0.0/0` to `gw-out` and bypass `gw-in`. `kvovn-topo` flags `vpc-c` in Problems for `gw-in` and not for `gw-out`.

Expected NB: LR `vpc-c` with `0.0.0.0/0 → 10.30.0.254` and **no** NAT rows; LS `vpc-c-net` with the `web` LSP and the two gateways' LAN LSPs at .254 and .253.

```
test/e2e/scenarios/apply.sh vpc-nat-gw-asym
kubectl get vpc-nat-gateways,iptables-eips,iptables-dnat-rules,iptables-snat-rules
kubectl ko nbctl lr-route-list vpc-c
kubectl ko nbctl lr-nat-list vpc-c       # empty
```
