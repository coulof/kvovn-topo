# vpc-nat-gw

Custom VPC `vpc-b` behind the iptables VPC NAT gateway `gw-b` (a pod on `ovn-topo-agent2` with a macvlan leg on `egress0`). Pod `web` (10.20.0.10) has a DNAT 8080→80 and the subnet an SNAT, both as `Iptables*` rules inside the gateway pod.

This is the documented, symmetric setup: the VPC routes `0.0.0.0/0` to the gateway's `lanIp` 10.20.0.254, so replies to DNATed flows go back through the gateway and `kvovn-topo` reports no NAT-return problem. The ADR-0004 asymmetric case needs the default route to leave elsewhere while the gateway DNATs.

Expected NB: LR `vpc-b` with `0.0.0.0/0 → 10.20.0.254` (plus Kube-OVN's own `10.20.0.0/24` src-ip route to the subnet gateway) and **no** NAT rows; LS `vpc-b-net` with the `web` LSP and the gateway's LAN LSP at 10.20.0.254. The gateway's WAN leg is a macvlan on `egress0`, outside OVN: it has no LSP.

```
test/e2e/scenarios/apply.sh vpc-nat-gw
kubectl get vpc-nat-gw,iptables-eip,iptables-dnat-rules,iptables-snat-rules
kubectl ko nbctl lr-route-list vpc-b
kubectl ko nbctl lr-nat-list vpc-b       # empty
```
