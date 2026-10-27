# ADR-0004 — CNI flavors and opaque hops

- Status: Proposed
- Date: 2026-10-27
- Deciders: coulof

## Context

A lab incident on `hv-01` (VPC `vpc-onewater`, VPC NAT gateway `one-water-nat`) was an asymmetric return path:
- Inbound traffic was DNAT'd inside the gateway pod `vpc-nat-gw-one-water-nat-0`. Its LAN IP is 192.168.54.46 on `subnet-onewater` and its WAN IP 10.144.124.15 on `external`.
- Replies from the VM followed the LR's `0.0.0.0/0 dst-ip → 10.144.124.30` out of `vpc-onewater-external`, which bypassed the pod.
- The fix was a `src-ip` static route `192.168.54.35/32 → 192.168.54.46`.

The post-mortem asked for three things:
1. route and policy rows;
2. the gateway's EIPs and NAT rules (Kube-OVN `VpcNatGateway`/`IptablesEIP`/`Iptables{Dnat,Snat}Rule` CRDs);
3. an automated check for this pattern.

Two forces make this an architecture question rather than a feature:

1. **The NAT rules are outside OVN.** NB `NAT` is empty for this VPC. The rules live in iptables inside the pod, and the secondary EIP 10.144.124.11 is an address added inside the pod by exec. Only the CRDs carry them, and hard rule 2 forbids reading Kube-OVN CRDs.
2. **ovn-kubernetes support has been requested.** ovn-kubernetes has the same "traffic leaves OVN's view" problem in local gateway mode, where traffic goes through the host stack via `ovn-k8s-mp0`. Its own CRDs (EgressIP, EgressFirewall, UserDefinedNetwork, AdminNetworkPolicy) would face the same hard rule 2 question. And `pkg/graph` currently mixes pure OVN joins with Kube-OVN conventions: `vendor=kube-ovn`, `cr-` ports, the `join` switch, the `ovn.kubernetes.io/chassis` annotation, and the UI notes about `Iptables*`.

The roadmap backlog already holds "Identity adapter boundary: isolate Kube-OVN conventions … No other adapter without a request." This ADR is that request.

## Decision

1. **Three layers.**
   - **OVN core** (`pkg/graph`): CNI-agnostic. NB/SB tables, joins, views and native names only. It must produce a useful graph against any OVN with no adapter matched.
   - **Flavor adapter**: one Go interface per CNI, detected from NB `external_ids` (`vendor=kube-ovn` → Kube-OVN; `k8s.ovn.org/*` keys → ovn-kubernetes) and never configured by hand. It supplies:
     - workload identity: LSP → pod/VM;
     - grouping: Kube-OVN VPC, ovn-k network/UDN;
     - the chassis ↔ node rule;
     - gateway semantics;
     - UI notes;
     - opaque-hop detection;
     - for T1 only, the pod/container discovery that `pkg/diag` uses.
   - **Opaque hop**: a new node type, `OpaqueHop`, ID `hop:<adapter>:<stable-key>`. It marks a point where OVN hands traffic to something it cannot see into. It carries what T0 knows (its LSPs or LRP, LAN/WAN IPs, owning pod or node) and a closed-enum `inspect` kind naming the T1 action that would show its inside. Clients state that its inside is unknown in T0.
2. **Hard rule 2 is generalized, not relaxed.** "No CNI CRDs" covers Kube-OVN (`kubeovn.io`) and ovn-kubernetes (`k8s.ovn.org`) alike. Identity comes from NB `external_ids`, and the core Kubernetes kinds already allowed may enrich graph nodes. An adapter may read only those kinds. The AGENTS.md wording changes when this ADR is accepted.
3. **Kube-OVN VPC NAT gateway.**
   - **T0:** the adapter finds the gateway pod (Pod labels and its owning StatefulSet) and joins it to its two LSPs through NB `external_ids:pod`. That gives an `OpaqueHop` bridging the LAN LS and the external LS, with `inspect: iptables-nat`.
   - **T1** (`pkg/diag`, TUI only): typed allowlist entries `iptables-save -t nat -c` (legacy or nft variant, detected from the image) and `ip -4 -o addr` in that pod. They show the live EIPs and DNAT/SNAT rules with hit counters.
4. **ovn-kubernetes local gateway mode** is an `OpaqueHop` per node, with `inspect: nft-ruleset` (T1: `nft list ruleset` on the node). Shared gateway mode needs no opaque hop: its NAT is NB `NAT` on `GR_<node>`.
5. **The asymmetric-return check is core, not adapter code.**

   > LR *R* has an opaque hop *H* whose LAN LSP is on an LS *S* attached to *R*. *R* also has another egress: a gateway LRP, or a default route whose nexthop is not *H*'s LAN IP. If no `src-ip` static route or reroute policy sends traffic from *S* (or part of it) to *H*'s LAN IP, return traffic for flows that *H* DNATs leaves through the other egress.

   - It is a **warning**, labelled heuristic: T0 cannot see whether *H* DNATs anything.
   - It ships with the M4 consistency checks (`/checks`, MCP `run_checks`, an LR badge in the TUI).
   - M5 adds the same scenario as a `pkg/trace` fixture.
6. **ovn-kubernetes support itself is not scheduled by this ADR.** The adapter boundary is built in M4 prep with Kube-OVN as its only implementation. Adding ovn-kubernetes waits for the open questions below (target fit, interconnect mode).

## Options considered

### NAT gateway rules. Option A: T0 opaque hop + T1 iptables *(chosen)*

**Pros:**
- no new RBAC for the daemon;
- live state with counters, not desired state;
- the same slot serves ovn-k local gateway mode;
- respects ADR-0001: exec stays in `pkg/diag`, from a typed allowlist.

**Cons:** in T0, including MCP, EIPs and NAT rules are not visible. The check in decision 5 does not need them.

### NAT gateway rules. Option B: read `Iptables*`/`VpcNatGateway` CRDs in T0

**Pros:** EIPs and rules visible to every client, MCP included.

**Cons:**
- CRDs are desired state; a failed reconcile looks correct;
- couples the daemon to Kube-OVN CRD versions and adds RBAC;
- ovn-kubernetes would need the same exception for its CRDs, so hard rule 2 would erode one CNI at a time.

Rejected.

### NAT gateway rules. Option C: identify the pod only, no rule visibility

**Pros:** smallest change.

**Cons:** leaves operators exactly where the incident left them: they know the gateway exists, but get no way to check what it does. Rejected, since option A's T1 part is cheap.

### ovn-kubernetes. Option A: adapter boundary *(chosen)* vs Option B: a fork or a separate view

A separate "ovn-k view" would duplicate the joins and views, and a fork would double maintenance. Most of the graph is plain OVN: what differs is identity, grouping and the opaque hops, which is exactly what the adapter covers.

## Trade-off analysis

The adapter boundary costs a refactor of `pkg/graph` before M4: Kube-OVN conventions move behind an interface, and the fixture goldens must not change. It buys three things:
- M4's IP resolver and checks are written once against the core;
- the post-mortem's check becomes CNI-agnostic;
- a later ovn-kubernetes adapter is additive.

Opaque hops make the tool's blind spots explicit instead of hiding them in footnotes, which follows the AGENTS.md rule that known traps are shown, not hidden.

## Consequences

- Easier: the VPC NAT gateway and its asymmetry risk become visible in T0, and its rules in T1, with no CRD reads.
- Easier: an ovn-kubernetes adapter, if scheduled, plugs into existing slots.
- Harder: `api/openapi.yaml` gains the `OpaqueHop` node type and an `inspect` enum. Both are additive.
- Harder: the M6 allowlist gains `iptables-save`/`ip addr` in arbitrary gateway pods. Those pods are found by the adapter from graph objects, never from user strings.
- Open, and blocks any ovn-kubernetes work:
  - **Target fit.** The targets are Rancher RKE2/Harvester, and neither ships ovn-kubernetes. Supporting it means OpenShift or vanilla clusters, which is a scope decision.
  - **Interconnect mode.** ovn-kubernetes in interconnect mode runs one NB/SB per node (zone), on a unix socket inside `ovnkube-node`, with no central database. The options are:
    - (a) a DaemonSet of zone collectors plus a merger, stitching `type=remote` transit-switch ports, with zone-prefixed IDs. This touches ADR-0002 and the snapshot model.
    - (b) a per-zone view.
    - (c) support only central-DB deployments.

    This needs a spike.

    Spike done on OpenShift 4.19 (2026-10-27): Q13 is resolved (OpenShift is in scope), and option (a) is proposed in ADR-0005.
  - **ovn-kubernetes NB conventions.** The `k8s.ovn.org/owner-type`/`owner-controller`/`name`/`id` `external_ids`, LSP naming and the node chassis annotation (`k8s.ovn.org/node-chassis-id`) must be verified on a real cluster, per version.

## Action items

1. [ ] Verify the NAT-gw pod labels and annotations on `hv-01` (ROADMAP Q12). *Partly: its ports carry `external_ids:pod` (2026-10-27).*
2. [ ] M4 prep: the adapter interface in `pkg/graph`, with Kube-OVN as its only implementation; goldens unchanged.
3. [ ] M4: the `OpaqueHop` node type (API + TUI + MCP); the asymmetric-return check in `/checks`.
4. [ ] B1: capture a `vpc-nat-gw` scenario with the one-water layout, before and after the `src-ip` route.
5. [ ] M5: path fixture "DNAT return bypasses opaque hop".
6. [ ] M6: `iptables-save`/`ip addr` allowlist entries for `inspect: iptables-nat`. *Partly: the details pane's `!` runs `iptables-save -t nat` locally (ADR-0001 amendment).*
7. [x] Answer Q13 (ovn-kubernetes target fit) and Q14 (interconnect mode): ADR-0005.
8. [ ] On acceptance: amend AGENTS.md hard rule 2 and the §2 layout.
