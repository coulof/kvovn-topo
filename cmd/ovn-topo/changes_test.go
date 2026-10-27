package main

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/coulof/ovn-topology/pkg/api"
)

// TestChangesLog: a new revision binds the pending pod, adds a port and
// removes another; the log lists all three, newest batch first, and Enter
// jumps to a live object but not to a removed one.
func TestChangesLog(t *testing.T) {
	m := loaded(t, 140, 40, false)
	if !strings.Contains(ansi.Strip(press(m, "5").View()), "no live updates: this is an offline fixture") {
		t.Errorf("an offline fixture must explain why the log is empty")
	}
	m.fixturePath = ""
	m.graphResp.Revision++
	ls := "ls:ls-default-uuid"
	var kept []api.GraphNode
	for _, w := range m.switchWorkloads[ls] {
		p := map[string]interface{}{}
		for k, v := range *w.Properties {
			p[k] = v
		}
		switch w.Name {
		case "pending-pod.default":
			p["boundState"], p["boundChassis"] = "bound", "chassis-node-2"
		case "app-pod-1.default":
			continue // removed
		}
		w.Properties = &p
		kept = append(kept, w)
	}
	np := map[string]interface{}{"addresses": []interface{}{"00:00:00:00:00:99 10.244.0.99"}, "boundState": "bound", "boundChassis": "chassis-node-1"}
	m.switchWorkloads[ls] = append(kept, api.GraphNode{Id: "lsp:new-uuid", Name: "new-pod.default", Type: api.GraphNodeTypeLSP, Properties: &np})
	m.rebuildTree()

	m = press(m, "5")
	view := ansi.Strip(m.View())
	for _, want := range []string{"pending-pod.default  broken → ok, [unbound] → [—]", "new-pod.default  added", "app-pod-1.default  removed"} {
		if !strings.Contains(view, want) {
			t.Errorf("expected %q in the Changes view:\n%s", want, view)
		}
	}
	for i, r := range m.rows {
		if r.n.name == "app-pod-1.default" {
			m.cursor = i
		}
	}
	if got := press(m, "enter"); got.mode != modeChanges || !got.flashErr {
		t.Errorf("enter on a removed object must stay and say so, got mode %s flash %q", got.mode, got.flash)
	}
	for i, r := range m.rows {
		if r.n.name == "new-pod.default" {
			m.cursor = i
		}
	}
	if got := press(m, "enter"); got.mode != modeVPC || got.selectedXNode() == nil || got.selectedXNode().name != "new-pod.default" {
		t.Errorf("enter must jump to the new port")
	}
}
