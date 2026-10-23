package graph

import (
	"testing"

	"github.com/coulof/ovn-topology/pkg/api"
)

func TestFind(t *testing.T) {
	snap := LoadTestSnapshot(t)

	tests := []struct {
		name      string
		query     string
		typ       api.FindHitType
		wantFirst string
		wantField api.FindHitMatchedField
		wantTotal int // -1: don't check
	}{
		{"pod IP exact", "10.244.0.11", "", "lsp:lsp-pod2-uuid", api.Ip, 1},
		{"LRP network IP", "100.64.0.1", "", "lrp:lrp-join-def-uuid", api.Ip, 1},
		{"MAC, case-insensitive", "00:00:00:AA:BB:01", "", "lsp:lsp-pod1-uuid", api.Mac, 1},
		{"encap IP", "192.168.1.12", "", "chassis:chassis-node-2", api.EncapIp, 1},
		{"namespace exact", "tenant-a", "", "lsp:lsp-cust-pod-uuid", api.Namespace, 1},
		{"exact name first", "join", "", "ls:ls-join-uuid", api.Name, -1},
		{"type filter", "join", api.FindHitTypeLRP, "lrp:lrp-join-cust-uuid", api.Name, 2},
		{"chassis hostname", "node-3", "", "chassis:chassis-node-3", api.Hostname, 1},
		{"no hit", "does-not-exist", "", "", "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := snap.Find(tt.query, tt.typ, 0, 0)
			if tt.wantTotal >= 0 && res.Total != tt.wantTotal {
				t.Fatalf("total = %d, want %d (%+v)", res.Total, tt.wantTotal, res.Items)
			}
			if tt.wantFirst == "" {
				return
			}
			if len(res.Items) == 0 {
				t.Fatalf("no hits for %q", tt.query)
			}
			got := res.Items[0]
			if got.Id != tt.wantFirst || got.MatchedField != tt.wantField {
				t.Fatalf("first hit = %s (%s), want %s (%s)", got.Id, got.MatchedField, tt.wantFirst, tt.wantField)
			}
		})
	}
}

func TestFindParentAndPaging(t *testing.T) {
	snap := LoadTestSnapshot(t)

	res := snap.Find("10.244.0.10", "", 0, 0)
	if len(res.Items) != 1 || res.Items[0].Parent == nil || *res.Items[0].Parent != "ls:ls-default-uuid" {
		t.Fatalf("want LSP with parent ls:ls-default-uuid, got %+v", res.Items)
	}

	all := snap.Find("pod", "", 0, 0)
	if all.Total < 3 {
		t.Fatalf("want at least 3 pod hits, got %d", all.Total)
	}
	page := snap.Find("pod", "", 1, 1)
	if page.Total != all.Total || len(page.Items) != 1 || page.Items[0].Id != all.Items[1].Id {
		t.Fatalf("offset paging mismatch: %+v vs %+v", page.Items, all.Items)
	}
	if past := snap.Find("pod", "", 10, all.Total+5); len(past.Items) != 0 {
		t.Fatalf("offset past end returned %d items", len(past.Items))
	}
}
