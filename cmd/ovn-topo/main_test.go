package main

import (
	"strings"
	"testing"
)

func TestTUIModelView(t *testing.T) {
	m := initialModel("http://127.0.0.1:8080/api/v1", "dev-fixture")

	// Trigger initial command to load fixture graph
	cmd := m.Init()
	if cmd == nil {
		t.Fatalf("expected non-nil Init cmd")
	}

	msg := cmd()
	loaded, ok := msg.(graphLoadedMsg)
	if !ok {
		t.Fatalf("expected graphLoadedMsg")
	}
	if loaded.err != nil {
		t.Fatalf("unexpected error loading fixture: %v", loaded.err)
	}

	// Process loaded message
	m.applyGraph(loaded.resp)

	// 1. Verify Tree View rendering
	treeView := m.View()
	if !strings.Contains(treeView, "OVN Network Topology") {
		t.Errorf("expected view header, got: %s", treeView)
	}
	if !strings.Contains(treeView, "1. Topology Tree") {
		t.Errorf("expected tab 1, got: %s", treeView)
	}
	if !strings.Contains(treeView, "VPC: default") {
		t.Errorf("expected VPC: default in view, got: %s", treeView)
	}
	if !strings.Contains(treeView, "VPC: custom-vpc") {
		t.Errorf("expected VPC: custom-vpc in view, got: %s", treeView)
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

	// 2. Switch to Matrix View and verify table rendering
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
}
