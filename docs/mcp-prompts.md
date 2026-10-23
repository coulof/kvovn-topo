# MCP Prompt Guide

This guide lists prompts for AI coding agents (OpenCode, Claude Code, agy) connected to the `ovn-topo mcp` server ([setup](mcp.md)).

Each section shows practical troubleshooting questions, which MCP tool the agent invokes, and what data it inspects. All answers come from the OVN Northbound/Southbound configuration and bindings; nothing is captured or traced on the wire.

---

## 1. Locate a Workload or Address

Turn a pod, VM, IP or MAC into OVN objects.

### Prompts

- **"Which logical switch port has IP `10.244.0.10`, and which pod owns it?"**
  - *Tool*: `find` (`query="10.244.0.10"`)
  - *Data*: LSP ID, parent switch (`parent`), Kubernetes identity (`k8s.namespace`, `k8s.pod`, `k8s.workloadKind`).

- **"Find the OVN ports of VM `vm-a` in namespace `tenant-1`."**
  - *Tool*: `find` (`query="vm-a"`, `type="LSP"`), then filter on `k8s.namespace`
  - *Data*: One LSP per NIC. KubeVirt secondary NICs are named `<vm>.<namespace>.<nad>.<provider>.ovn`.

- **"Who has MAC `00:00:00:aa:bb:01`?"**
  - *Tool*: `find` (`query="00:00:00:aa:bb:01"`)
  - *Data*: Exact match on LSP addresses and LRP MACs.

- **"List every port in namespace `tenant-a`."**
  - *Tool*: `find` (`query="tenant-a"`, `type="LSP"`), following `next` while `truncated` is true
  - *Data*: Namespace matches are exact; names containing the text also match.

---

## 2. Port State and Placement

Check whether a port is bound and where it runs.

### Prompts

- **"Is the port of pod `app-pod-1` bound, and on which node?"**
  - *Tool*: `find` (`query="app-pod-1"`), then `get_graph` (`view="l2"`, `focus=<parent ls id>`)
  - *Data*: `boundState` (`bound`/`unbound`), `boundChassis`, `up`, `enabled`, and the chassis → Kubernetes node name.

- **"Show me the full OVSDB row for this port."**
  - *Tool*: `get_node` (`id="lsp:<uuid>"`)
  - *Data*: `raw` columns (`addresses`, `options`, `port_security`, `type`…), `externalIds`, related objects.

- **"Which ports are unbound on switch `default-subnet`?"**
  - *Tool*: `find` (`query="default-subnet"`, `type="LS"`), then `get_graph` (`view="l2"`, `focus=<ls id>`) page by page
  - *Data*: LSP nodes with `boundState="unbound"`.

---

## 3. Routing Topology

Understand VPCs, routers and how switches attach.

### Prompts

- **"Draw the L3 topology: VPCs, routers and their subnets."**
  - *Tool*: `get_graph` (`view="l3"`)
  - *Data*: VPC compound nodes, LR and LS nodes, `router-peer` edges labelled with LRP networks, localnet links to External. On a large cluster the result is an overview (counts and most-connected nodes); the agent then focuses on one.

- **"Which routers is the `join` switch attached to?"**
  - *Tool*: `find` (`query="join"`, `type="LS"`), then `get_graph` (`focus=<ls id>`, `depth=1`)
  - *Data*: Every LR peered with the switch (an LS can attach to several routers).

- **"What does router `ovn-cluster` look like: ports, NAT rules, routes?"**
  - *Tool*: `get_node` (`id="lr:<uuid>"`)
  - *Data*: Related ports, attached switches and NAT rules; static routes and policies as UUID lists in `raw` (`static_routes`, `policies`). Route and policy contents arrive with M4.

---

## 4. Chassis and Gateways

Inspect hypervisors, encapsulation and gateway placement.

### Prompts

- **"List the chassis, their tunnel IPs and how many ports each one hosts."**
  - *Tool*: `list_chassis`
  - *Data*: Encap type and IP, Kubernetes node name, `bindingCount`.

- **"Which node is the active gateway for the external router port, and what are the standby priorities?"**
  - *Tool*: `list_chassis`
  - *Data*: `gatewayRoles` (router port, priority, `active`) and `haGroups` priorities per chassis.

- **"What runs on chassis `node-1`?"**
  - *Tool*: `find` (`query="node-1"`, `type="Chassis"`), then `get_node` (`id="chassis:<name>"`)
  - *Data*: Bound LSPs and the router ports it is the active gateway for, as `related` IDs.

- **"Which tunnel endpoint is `192.168.1.12`?"**
  - *Tool*: `find` (`query="192.168.1.12"`)
  - *Data*: The chassis whose encap IP matches (`matchedField="encap-ip"`).

---

## Not Available Yet

These questions need tools planned in later milestones. Agents will not see the tools until they ship.

- **"Why can't `vm-a` reach `vm-b`?"**: `compute_path` and `effective_acls` (M4–M5).
- **"What changed in `tenant-1` overnight?"**: `list_changes` and `diff` (M8).
- Packet captures and real `ovn-trace` runs stay out of MCP for good: they are Tier 1, TUI only (ADR-0001).
