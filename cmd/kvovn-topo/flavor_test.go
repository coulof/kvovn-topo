package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
	"github.com/coulof/kvovn-topo/pkg/api"
)

// labModel loads the hv-01 capture (Kube-OVN v1.16, three VPCs) fully expanded.
func labModel(t *testing.T) model { return labModelDiag(t, false) }

func labModelDiag(t *testing.T, diagMode bool) model {
	t.Helper()
	m := initialModel(testDaemon, "../../testdata/scenarios/hv-01-lab", "default", diagMode, nil, "")
	m = send(m, tea.WindowSizeMsg{Width: 160, Height: 70})
	m = send(m, m.fetchGraphCmd()())
	m = send(m, m.fetchChassisCmd()())
	if m.tree == nil {
		t.Fatalf("lab capture did not load: %v", m.err)
	}
	return expandAll(m)
}

// withNATGateway adds the one-water-nat gateway pod's LAN and WAN ports to the
// lab model (the capture predates it), as the hv-01 lab has them.
func withNATGateway(t *testing.T, m model) model {
	t.Helper()
	add := func(lsName, uuid, ip string) {
		ls := ""
		for id, n := range m.tree.byID {
			if n.kind == kLS && n.name == lsName {
				ls = id
			}
		}
		pod, ns, addr := "vpc-nat-gw-one-water-nat-0", "kube-system", "00:00:00:00:00:01 "+ip
		props := map[string]interface{}{"addresses": []interface{}{addr}, "boundState": "bound", "boundChassis": "hv-01"}
		m.switchWorkloads[ls] = append(m.switchWorkloads[ls], api.GraphNode{Id: "lsp:" + uuid, Name: "vpc-nat-gw-one-water-nat-0." + lsName,
			Type: api.GraphNodeTypeLSP, Properties: &props, K8s: &api.K8sMetadata{Pod: &pod, Namespace: &ns}})
	}
	add("subnet-onewater", "natgw-lan", "192.168.54.46")
	add("external", "natgw-wan", "10.144.124.15")
	m.rebuildTree()
	m = expandAll(m)
	return m
}

func pane(m model, kind, name string, t *testing.T) string {
	t.Helper()
	m = selectRow(t, m, kind, name)
	if c := m.lazyFetchSelectedDetailCmd(); c != nil {
		m = send(m, c())
	}
	return ansi.Strip(strings.Join(m.detailLines(m.selectedXNode(), 70), "\n"))
}

func wantAll(t *testing.T, what, got string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(got, w) {
			t.Errorf("%s: expected %q in:\n%s", what, w, got)
		}
	}
}

func TestDetailPaneSpeaksKubeOVN(t *testing.T) {
	m := labModel(t)
	if got := m.cniLabel(); got != "Kube-OVN · central NB/SB" {
		t.Errorf("CNI row: got %q", got)
	}
	wantAll(t, "subnet", pane(m, kLS, "subnet-onewater", t),
		"Subnet subnet-onewater  VPC vpc-onewater", "LS   subnet-onewater",
		"Role           workload subnet", "← external_ids:vendor=kube-ovn",
		"Inspect", "kubectl ko nbctl show subnet-onewater")
	wantAll(t, "join", pane(m, kLS, "join", t), "Join subnet join", "← switch name join (Kube-OVN convention)")
	wantAll(t, "underlay", pane(m, kLS, "external", t), "Underlay subnet external", "← localnet port")
	router := pane(m, kLR, "vpc-onewater", t)
	wantAll(t, "router", router, "VPC router vpc-onewater", "LR   vpc-onewater", "kubectl ko nbctl lr-route-list vpc-onewater")
	if strings.Contains(router, "VPC router vpc-onewater  VPC vpc-onewater") {
		t.Errorf("a router named after its VPC must not repeat the name:\n%s", router)
	}
	wantAll(t, "gateway port", pane(m, kLRP, "vpc-onewater-external", t),
		"VPC external gateway vpc-onewater-external", "Routes out this port", "0.0.0.0/0 via 10.144.124.30")
	wantAll(t, "join port", pane(m, kLSP, "node-hv-01", t), "Node join port node-hv-01  node hv-01",
		"kubectl ko sbctl find Port_Binding")
}

// TestNATGatewayIsAnOpaqueHop adds the one-water-nat gateway pod's two ports
// (the capture predates it) and checks the router, switch and port panes.
func TestNATGatewayIsAnOpaqueHop(t *testing.T) {
	m := labModel(t)
	before := pane(m, kLR, "vpc-onewater", t)
	wantAll(t, "router before", before, "NAT 0 does not rule out")

	m = withNATGateway(t, m)

	router := pane(m, kLR, "vpc-onewater", t)
	wantAll(t, "router", router, "Outside OVN", "NAT gateway one-water-nat", "pod kube-system/vpc-nat-gw-one-water-nat-0",
		"LAN 192.168.54.46 on subnet-onewater", "WAN 10.144.124.15 on external", "iptables inside the pod",
		"+ 1 NAT gateway pod outside OVN (below)",
		"kubectl -n kube-system exec vpc-nat-gw-one-water-nat-0", "iptables-save -t nat")
	if strings.Index(router, "LAN 192.168.54.46") > strings.Index(router, "WAN 10.144.124.15") {
		t.Errorf("the LAN side must come first:\n%s", router)
	}
	if s := m.tree.byID[selectRow(t, m, kLR, "vpc-onewater").selectedXNode().id].summary; !strings.Contains(s, "nat 0 (+1 NAT gw)") {
		t.Errorf("the router row must count the NAT gateway, got %q", s)
	}
	if s := selectRow(t, m, kLR, "vpc-twowater").selectedXNode().summary; strings.Contains(s, "NAT gw") {
		t.Errorf("vpc-twowater only shares the underlay, got %q", s)
	}
	if strings.Contains(router, "NAT 0 does not rule out") {
		t.Errorf("with the gateway shown, the generic NAT note must go:\n%s", router)
	}
	wantAll(t, "LAN port", pane(m, kLSP, "vpc-nat-gw-one-water-nat-0.subnet-onewater", t),
		"NAT gateway LAN interface", "gateway one-water-nat", "← pod name vpc-nat-gw-<name>")
	wantAll(t, "WAN port", pane(m, kLSP, "vpc-nat-gw-one-water-nat-0.external", t), "NAT gateway WAN interface")
	// vpc-twowater shares only the underlay with the gateway's WAN side.
	if got := pane(m, kLR, "vpc-twowater", t); strings.Contains(got, "one-water-nat") {
		t.Errorf("a router on the shared underlay must not list another VPC's NAT gateway:\n%s", got)
	}
	wantAll(t, "underlay switch", pane(m, kLS, "external", t), "NAT gateway one-water-nat")
}

// TestUnclaimedObjectsStayPlainOVN: with no vendor tag the pane keeps the OVN
// header and native commands; an ovn-kubernetes zone is named in the CNI row.
func TestUnclaimedObjectsStayPlainOVN(t *testing.T) {
	m := loaded(t, 140, 60, false)
	for id, gn := range m.nodeMap {
		gn.K8s = nil
		m.nodeMap[id] = gn
	}
	m.rebuildTree()
	m = expandAll(m)
	got := pane(m, kLS, "default-subnet", t)
	wantAll(t, "plain switch", got, "LS   default-subnet", "ovn-nbctl show default-subnet")
	for _, bad := range []string{"Subnet default-subnet", "kubectl ko", "Role "} {
		if strings.Contains(got, bad) {
			t.Errorf("unclaimed switch must not show %q:\n%s", bad, got)
		}
	}
	if got := m.cniLabel(); got != "OVN (no CNI conventions found)" {
		t.Errorf("CNI row: got %q", got)
	}
	m.zone = "hv-02"
	if got := m.cniLabel(); got != "ovn-kubernetes · zone hv-02" {
		t.Errorf("CNI row in a zone: got %q", got)
	}
	if z := zoneOf("testdata/scenarios/ocp/zone-hv-02/"); z != "hv-02" {
		t.Errorf("zoneOf: got %q", z)
	}
}
