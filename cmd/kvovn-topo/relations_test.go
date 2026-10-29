package main

import (
	"strings"
	"testing"

	"github.com/coulof/kvovn-topo/pkg/api"
)

func refTo(n *xnode, labelPrefix, to string) bool {
	for _, r := range n.relations() {
		if strings.HasPrefix(r.label, labelPrefix) && r.to == to {
			return true
		}
	}
	return false
}

// TestTroubleshootingRelations: the one-water layout, with the post-mortem's
// src-ip route. The route must lead to the NAT gateway's LAN port, the router
// and the gateway must link both ways, and the link must survive a rebuild.
func TestTroubleshootingRelations(t *testing.T) {
	m := withNATGateway(t, labModel(t))
	lr := selectRow(t, m, kLR, "vpc-onewater").selectedXNode()
	lan := m.tree.lspByName["vpc-nat-gw-one-water-nat-0.subnet-onewater"]
	if lan == nil {
		t.Fatal("injected gateway LAN port missing")
	}
	if !refTo(lr, "nat gateway", lan.id) || !refTo(lan, "serves router", lr.id) {
		t.Errorf("router and NAT gateway must link both ways: %v / %v", lr.relations(), lan.relations())
	}
	if o := m.ipOwner("192.168.54.46"); o == nil || o.id != lan.id {
		t.Fatalf("192.168.54.46 must be owned by the gateway's LAN port, got %v", o)
	}
	if got := m.formatNexthop("192.168.54.46"); got != "192.168.54.46 (NAT gateway one-water-nat)" {
		t.Errorf("next hop label: %q", got)
	}

	src, dst := api.SrcIp, api.DstIp
	detail := &api.NodeDetail{Id: lr.id, StaticRoutes: &[]api.StaticRoute{
		{IpPrefix: "192.168.54.35/32", Nexthop: "192.168.54.46", Policy: &src},
		{IpPrefix: "0.0.0.0/0", Nexthop: "10.144.124.30", Policy: &dst},
	}}
	m.nodeLoading[lr.id] = true
	m = send(m, nodeDetailMsg{id: lr.id, detail: detail})
	lr = m.tree.byID[lr.id]
	if !refTo(lr, "route 192.168.54.35/32 src", lan.id) {
		t.Errorf("the src-ip route must lead to the NAT gateway: %v", lr.relations())
	}
	for _, r := range lr.relations() {
		if strings.HasPrefix(r.label, "route 0.0.0.0/0") {
			t.Errorf("10.144.124.30 is outside the tree; no relation expected, got %v", r)
		}
	}

	// Jump along it from the tree.
	m = expandAll(m)
	i := -1
	for k, r := range m.rows {
		if r.ref && strings.HasPrefix(r.relLabel, "route 192.168.54.35/32") {
			i = k
		}
	}
	if i < 0 {
		t.Fatalf("no route relation row under the router")
	}
	m.cursor = i
	if m = press(m, "enter"); m.selectedXNode() == nil || m.selectedXNode().id != lan.id {
		t.Errorf("enter on the route must jump to the gateway's LAN port")
	}

	m.rebuildTree()
	if !refTo(m.tree.byID[lr.id], "route 192.168.54.35/32 src", lan.id) {
		t.Errorf("detail relations must survive a rebuild within the revision")
	}
}

// TestNATRuleLeadsToItsPort: the fixture's floating IP 172.16.1.100 serves
// custom-worker, and its router's detail links the rule to that port.
func TestNATRuleLeadsToItsPort(t *testing.T) {
	m := selectRow(t, loaded(t, 140, 50, false), kLR, "custom-vpc")
	m = fetchSelectedDetail(t, m)
	port := m.tree.lspByName["custom-worker.tenant-a"]
	if port == nil || !refTo(m.selectedXNode(), "nat 172.16.1.100", port.id) {
		t.Errorf("the NAT rule must lead to custom-worker.tenant-a: %v", m.selectedXNode().relations())
	}
}
