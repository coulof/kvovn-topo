package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// scenarioModel loads a B1 lab capture (test/e2e/scenarios) fully expanded.
func scenarioModel(t *testing.T, name string) model {
	t.Helper()
	m := initialModel(testDaemon, "../../testdata/scenarios/"+name, "default", false, nil, "")
	m = send(m, tea.WindowSizeMsg{Width: 160, Height: 70})
	m = send(m, m.fetchGraphCmd()())
	m = send(m, m.fetchChassisCmd()())
	if m.tree == nil {
		t.Fatalf("%s did not load: %v", name, m.err)
	}
	return expandAll(m)
}

// natReturnWarnings returns the Problems reasons for router lr's NAT gateways,
// after fetching its routes as opening Problems does.
func natReturnWarnings(t *testing.T, m model, lr string) []string {
	t.Helper()
	m = fetchSelectedDetail(t, selectRow(t, m, kLR, lr))
	var out []string
	for _, p := range m.problems() {
		if p.n.kind == kLR && p.n.name == lr && strings.Contains(p.reason, "NAT gateway") {
			out = append(out, p.reason)
		}
	}
	return out
}

// TestScenarioNATReturnPath: vpc-nat-gw-asym has two gateways and a default
// route to gw-out, so only gw-in's DNAT replies bypass their gateway.
func TestScenarioNATReturnPath(t *testing.T) {
	got := natReturnWarnings(t, scenarioModel(t, "vpc-nat-gw-asym"), "vpc-c")
	if len(got) != 1 || !strings.Contains(got[0], "replies may bypass NAT gateway gw-in: 0.0.0.0/0 goes to 10.30.0.254") {
		t.Errorf("expected one warning, for gw-in: %q", got)
	}
	if got := natReturnWarnings(t, scenarioModel(t, "vpc-nat-gw"), "vpc-b"); len(got) != 0 {
		t.Errorf("the documented setup routes 0.0.0.0/0 to the gateway: no warning expected, got %q", got)
	}
}

// TestScenarioOVNNATRelations: custom-vpc-ovn-nat's distributed FIP leads from
// the router to web-1's port.
func TestScenarioOVNNATRelations(t *testing.T) {
	m := fetchSelectedDetail(t, selectRow(t, scenarioModel(t, "custom-vpc-ovn-nat"), kLR, "vpc-a"))
	port := m.tree.lspByName["web-1.vpc-a"]
	if port == nil || !refTo(m.selectedXNode(), "nat 192.168.107.100", port.id) {
		t.Errorf("the FIP must lead to web-1.vpc-a: %v", m.selectedXNode().relations())
	}
}
