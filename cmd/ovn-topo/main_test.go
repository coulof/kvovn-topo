package main

import (
	"strings"
	"testing"

	"github.com/coulof/ovn-topology/pkg/api"
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

	m.graphResp = loaded.resp
	m.status = "Revision 1"
	for _, n := range loaded.resp.Nodes {
		if n.Type != api.GraphNodeTypeVPC {
			m.nodeList = append(m.nodeList, n)
		}
	}

	viewStr := m.View()
	if !strings.Contains(viewStr, "OVN Network Topology (L3)") {
		t.Errorf("expected view header, got: %s", viewStr)
	}
	if !strings.Contains(viewStr, "VPC: default") {
		t.Errorf("expected VPC: default in view, got: %s", viewStr)
	}
	if !strings.Contains(viewStr, "VPC: custom-vpc") {
		t.Errorf("expected VPC: custom-vpc in view, got: %s", viewStr)
	}
	if !strings.Contains(viewStr, "Interconnect (Multi-LR)") {
		t.Errorf("expected Interconnect in view, got: %s", viewStr)
	}
}
