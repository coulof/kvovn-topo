package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/coulof/ovn-topology/pkg/api"
)

func TestProblemsListsAndJumps(t *testing.T) {
	m := press(loaded(t, 140, 40, false), "4")
	if m.mode != modeProblems {
		t.Fatalf("4 must open the Problems view, got %s", m.mode)
	}
	view := ansi.Strip(m.View())
	for _, want := range []string{"Xray(problems)", "pending-pod.default", "unbound: no chassis has claimed this port", "problem (4)"} {
		if !strings.Contains(view, want) {
			t.Errorf("expected %q in:\n%s", want, view)
		}
	}
	for i, r := range m.rows {
		if r.n.name == "pending-pod.default" {
			m.cursor = i
		}
	}
	m = press(m, "enter")
	if n := m.selectedXNode(); m.mode != modeVPC || n == nil || n.name != "pending-pod.default" {
		t.Fatalf("enter must jump to the port in the VPC xray, got %s %v", m.mode, n)
	}
	if m = press(m, "b"); m.mode != modeProblems {
		t.Errorf("b must come back to Problems")
	}
}

// TestNATReturnPathProblem replays the vpc-onewater incident: a default route
// to the upstream router and nothing pinning replies to the NAT gateway.
func TestNATReturnPathProblem(t *testing.T) {
	m := press(withNATGateway(t, labModel(t)), "4")
	lr := m.tree.byID[selectRow(t, press(m, "1"), kLR, "vpc-onewater").selectedXNode().id]
	has := func(m model, s string) bool {
		for _, p := range m.problems() {
			if p.n.id == lr.id && strings.Contains(p.reason, s) {
				return true
			}
		}
		return false
	}
	if !has(m, "checking the NAT gateway return path") || !m.nodeLoading[lr.id] {
		t.Fatalf("opening Problems must fetch the router's routes and say it is checking")
	}

	src, dst := api.SrcIp, api.DstIp
	before := &api.NodeDetail{Id: lr.id, StaticRoutes: &[]api.StaticRoute{{IpPrefix: "0.0.0.0/0", Nexthop: "10.144.124.30", Policy: &dst}}}
	m = send(m, nodeDetailMsg{id: lr.id, detail: before})
	if !has(m, "replies may bypass NAT gateway one-water-nat: 0.0.0.0/0 goes to 10.144.124.30") {
		t.Errorf("expected the return-path warning, got %v", m.problems())
	}

	after := &api.NodeDetail{Id: lr.id, StaticRoutes: &[]api.StaticRoute{
		{IpPrefix: "192.168.54.35/32", Nexthop: "192.168.54.46", Policy: &src},
		{IpPrefix: "0.0.0.0/0", Nexthop: "10.144.124.30", Policy: &dst}}}
	m.nodeLoading[lr.id] = true
	m = send(m, nodeDetailMsg{id: lr.id, detail: after})
	if has(m, "replies may bypass") {
		t.Errorf("a src-ip route to the gateway must clear the warning")
	}
}
