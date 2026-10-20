package main

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/coulof/ovn-topology/pkg/api"
	"github.com/coulof/ovn-topology/pkg/diag"
	"github.com/coulof/ovn-topology/pkg/graph"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestTUIModelView(t *testing.T) {
	m := initialModel("http://127.0.0.1:8080/api/v1", "dev-fixture", "default", false, "", "")

	// Trigger initial command to load fixture graph
	cmd := m.Init()
	if cmd == nil {
		t.Fatalf("expected non-nil Init cmd")
	}

	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	var loaded graphLoadedMsg
	if ok {
		for _, bcmd := range batch {
			if bcmd != nil {
				if l, isLoaded := bcmd().(graphLoadedMsg); isLoaded {
					loaded = l
					break
				}
			}
		}
	} else if l, isLoaded := msg.(graphLoadedMsg); isLoaded {
		loaded = l
	}

	if loaded.resp == nil {
		t.Fatalf("expected graphLoadedMsg with non-nil resp, err: %v", loaded.err)
	}

	// Process loaded message (collapsed by default)
	m.applyGraph(loaded.resp)

	expectedOrder := []string{
		"ovn_cluster",     // Router in VPC default
		"default-subnet",  // Switch in VPC default
		"custom-vpc",      // Router in VPC custom-vpc
		"custom-subnet",   // Switch in VPC custom-vpc
		"external-subnet", // Standalone switch
		"join",            // Shared Interconnect switch
		"provider-ext",    // External underlay
	}

	if len(m.nodeList) != len(expectedOrder) {
		t.Fatalf("expected %d nodes in navigation list, got %d", len(expectedOrder), len(m.nodeList))
	}

	for i, expectedName := range expectedOrder {
		if m.nodeList[i].Name != expectedName {
			t.Errorf("at index %d: expected %s, got %s", i, expectedName, m.nodeList[i].Name)
		}
	}

	// 1. Verify Tree View rendering
	treeView := m.View()
	if !strings.Contains(treeView, "OVN Network Topology") {
		t.Errorf("expected view header, got: %s", treeView)
	}
	if !strings.Contains(treeView, "1. L3 Tree") {
		t.Errorf("expected tab 1, got: %s", treeView)
	}
	if !strings.Contains(treeView, "press space to expand") {
		t.Errorf("expected press space to expand hint, got: %s", treeView)
	}

	// Expand switch default-subnet with space key
	m.expandedSwitches["ls:ls-default-uuid"] = true
	expandedTree := m.View()
	if !strings.Contains(expandedTree, "press space to collapse") {
		t.Errorf("expected press space to collapse in expanded tree, got: %s", expandedTree)
	}
	if !strings.Contains(treeView, "VPC: default") {
		t.Errorf("expected VPC: default in view, got: %s", treeView)
	}
	if !strings.Contains(treeView, "VPC: custom-vpc") {
		t.Errorf("expected VPC: custom-vpc in view, got: %s", treeView)
	}
	if !strings.Contains(treeView, "► ") {
		t.Errorf("expected active cursor glyph ► in view, got: %s", treeView)
	}
	if !strings.Contains(treeView, "lrp-default") {
		t.Errorf("expected port branch lrp-default in view, got: %s", treeView)
	}
	if !strings.Contains(treeView, "10.244.0.1/24") {
		t.Errorf("expected CIDR 10.244.0.1/24 in view, got: %s", treeView)
	}
	if !strings.Contains(treeView, "Interconnects (Multi-LR Subnets)") {
		t.Errorf("expected Interconnects in view, got: %s", treeView)
	}

	// 2. Switch to L2 Subnet View
	m.mode = viewL2
	l2Cmd := m.fetchL2Cmd(m.focusedLS)
	if l2Cmd != nil {
		l2Msg := l2Cmd().(l2LoadedMsg)
		m.l2Resp = l2Msg.resp
		var lsps []api.GraphNode
		for _, n := range l2Msg.resp.Nodes {
			if n.Type == api.GraphNodeTypeLSP {
				lsps = append(lsps, n)
			}
		}
		m.l2Nodes = lsps
	}
	l2View := m.View()
	if !strings.Contains(l2View, "Subnets (Broadcast Domains)") {
		t.Errorf("expected Subnets header in L2 view, got: %s", l2View)
	}
	if !strings.Contains(l2View, "Workloads in default-subnet") {
		t.Errorf("expected Workloads in default-subnet in L2 view, got: %s", l2View)
	}

	// 3. Switch to Chassis View
	m.mode = viewChassis
	chassisCmd := m.fetchChassisCmd()
	if chassisCmd != nil {
		chassisMsg := chassisCmd().(chassisLoadedMsg)
		m.chassisResp = chassisMsg.resp
		m.chassisNodes = chassisMsg.resp.Nodes
	}
	chassisView := m.View()
	if !strings.Contains(chassisView, "Cluster Nodes") {
		t.Errorf("expected Cluster Nodes in Chassis view, got: %s", chassisView)
	}
	if !strings.Contains(chassisView, "Node Details: node-1") {
		t.Errorf("expected Node Details: node-1 in Chassis view, got: %s", chassisView)
	}

	// 4. Switch to Matrix View and verify table rendering
	m.mode = viewMatrix
	matrixView := m.View()
	if !strings.Contains(matrixView, "PORT (LRP)") {
		t.Errorf("expected matrix table header, got: %s", matrixView)
	}
	if !strings.Contains(matrixView, "TARGET SWITCH") {
		t.Errorf("expected TARGET SWITCH in table, got: %s", matrixView)
	}
	if !strings.Contains(matrixView, "lrp-default") {
		t.Errorf("expected lrp-default in matrix, got: %s", matrixView)
	}
	if !strings.Contains(matrixView, "default-subnet") {
		t.Errorf("expected default-subnet in matrix, got: %s", matrixView)
	}
	if !strings.Contains(matrixView, "Port Interconnection Inspector") {
		t.Errorf("expected Port Interconnection Inspector in matrix view, got: %s", matrixView)
	}
	if !strings.Contains(matrixView, "Topology Role:") {
		t.Errorf("expected Topology Role in inspector, got: %s", matrixView)
	}

	// Select the join interconnect port and verify cross-router listing
	for idx, conn := range m.portList {
		if conn.PortName == "lrp-join-def" {
			m.selectedIdx = idx
			break
		}
	}
	joinMatrixView := m.View()
	if !strings.Contains(joinMatrixView, "Interconnected VPC Routers on 'join'") {
		t.Errorf("expected Interconnected VPC Routers on join, got: %s", joinMatrixView)
	}
	if !strings.Contains(joinMatrixView, "custom-vpc") {
		t.Errorf("expected custom-vpc to be listed as peer on join, got: %s", joinMatrixView)
	}
}

func TestThemes(t *testing.T) {
	themeNames := []string{"default", "suse", "dracula", "nord", "tokyonight", "catppuccin"}

	for _, name := range themeNames {
		theme, idx := GetThemeByName(name)
		if idx < 0 || idx >= len(AvailableThemes) {
			t.Errorf("invalid theme index for %s: %d", name, idx)
		}
		styles := CompileStyles(theme)
		rendered := styles.Title.Render("Test")
		if rendered == "" {
			t.Errorf("failed to render title with theme %s", name)
		}
	}

	// Verify cycle
	m := initialModel("http://127.0.0.1:8080/api/v1", "dev-fixture", "dracula", false, "", "")
	if m.theme.Name != "Dracula" {
		t.Errorf("expected Dracula, got %s", m.theme.Name)
	}
}

func TestDiagnosticsMode(t *testing.T) {
	fakeClient := fake.NewSimpleClientset(
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "ovn-central-0",
				Namespace: "kube-system",
				Labels:    map[string]string{"app": "ovn-central"},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "ovs-ovn-node1",
				Namespace: "kube-system",
				Labels:    map[string]string{"app": "ovs-ovn"},
			},
			Spec:   corev1.PodSpec{NodeName: "node1"},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
	)

	mockExec := diag.NewMockExecutor()
	mockExec.StdoutMap["true"] = []byte("")
	mockExec.StdoutMap["ovn-trace"] = []byte("output(\"target-port\");\n")
	mockExec.StdoutMap["ovs-appctl"] = []byte("tcp,orig=(src=10.244.0.5,dst=10.244.0.1)\n")

	m := initialModel("http://127.0.0.1:8080/api/v1", "dev-fixture", "default", true, "", "kube-system")
	m.k8sClient = fakeClient
	m.diagExecutor = mockExec
	m.diagLocator = diag.NewPodLocator(fakeClient, "kube-system")

	// Trigger probe
	probeCmd := m.probeDiagCmd()
	if probeCmd == nil {
		t.Fatalf("expected non-nil probeDiagCmd")
	}
	probeMsg := probeCmd()
	diagMsg, ok := probeMsg.(diagProbeMsg)
	if !ok {
		t.Fatalf("expected diagProbeMsg, got %T", probeMsg)
	}
	if !diagMsg.available {
		t.Fatalf("expected diag to be available: %s", diagMsg.reason)
	}

	// Update model with probe result
	m.diagAvailable = diagMsg.available
	m.diagReason = diagMsg.reason
	m.diagCentralPod = diagMsg.centralPod

	// Apply graph fixture
	snap := graph.LoadTestSnapshot(nil)
	resp, _ := snap.BuildGraph("l3", "", 1, 1, 100)
	m.applyGraph(resp)

	// Verify DIAG ACTIVE badge in view
	view := m.View()
	if !strings.Contains(view, "DIAG ACTIVE") {
		t.Errorf("expected DIAG ACTIVE badge in view, got: %s", view)
	}
	if !strings.Contains(view, "| x: diag") {
		t.Errorf("expected | x: diag shortcut in view, got: %s", view)
	}

	// Open diagnostics modal
	m.diagModal = true

	modalView := m.View()
	if !strings.Contains(modalView, "TIER 1 DIAGNOSTICS") {
		t.Errorf("expected TIER 1 DIAGNOSTICS header in modal, got: %s", modalView)
	}
	if !strings.Contains(modalView, "[1] ovn-trace") {
		t.Errorf("expected option [1] ovn-trace in modal, got: %s", modalView)
	}

	// Open destination picker
	m.diagStep = diagStepPickDest
	m.traceDestinations = m.collectTraceDestinations(nil, "10.244.0.1")
	if len(m.traceDestinations) == 0 {
		t.Fatalf("expected candidate destinations to be collected")
	}
	pickerView := m.View()
	if !strings.Contains(pickerView, "Choose Destination Target:") {
		t.Errorf("expected Choose Destination Target in picker, got: %s", pickerView)
	}

	traceCmd := m.executeTrace(m.traceDestinations[0].IP, "icmp", 0)
	if traceCmd == nil {
		t.Fatalf("expected non-nil traceCmd")
	}
	traceResMsg := traceCmd()
	execMsg, ok := traceResMsg.(diagExecResultMsg)
	if !ok {
		t.Fatalf("expected diagExecResultMsg, got %T", traceResMsg)
	}
	if execMsg.err != nil {
		t.Fatalf("unexpected trace error: %v", execMsg.err)
	}

	// Apply result
	m.diagRunning = false
	m.diagStep = diagStepResult
	m.diagAction = execMsg.action
	m.diagTitle = execMsg.title
	m.diagTraceRes = execMsg.traceRes
	m.diagRawOutput = execMsg.rawOutput

	resultView := m.View()
	if !strings.Contains(resultView, "DELIVERED") {
		t.Errorf("expected DELIVERED verdict in trace result view, got: %s", resultView)
	}

	// Close modal
	m.diagModal = false
	if m.diagModal {
		t.Errorf("expected diagModal to be closed")
	}
}

func TestMachineOutput(t *testing.T) {
	// Test JSON machine output
	if err := runMachineOutput("", "dev-fixture", "l3", "", "json"); err != nil {
		t.Errorf("expected machine output json to succeed, got: %v", err)
	}

	// Test YAML machine output
	if err := runMachineOutput("", "dev-fixture", "chassis", "", "yaml"); err != nil {
		t.Errorf("expected machine output yaml to succeed, got: %v", err)
	}
}
