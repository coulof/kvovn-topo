package history

import (
	"reflect"
	"sort"

	"github.com/coulof/kvovn-topo/pkg/graph"
	"github.com/coulof/kvovn-topo/pkg/ovsdb/nbmodel"
	"github.com/coulof/kvovn-topo/pkg/ovsdb/sbmodel"
)

// table gives generic access to one snapshot table: the consumed NB/SB tables
// (AGENTS.md §5), keyed as graph.Snapshot keys them.
type table struct {
	db, name string
	// diff calls emit for every key whose row differs between prev and next.
	diff func(prev, next *graph.Snapshot, emit func(key string, old, new any))
	// put sets (v != nil) or removes (v == nil) a row of s.
	put func(s *graph.Snapshot, key string, v any)
	// size is the row count of s.
	size func(s *graph.Snapshot) int
}

func tbl[T any](db, name string, get func(*graph.Snapshot) map[string]*T) table {
	return table{
		db: db, name: name,
		diff: func(prev, next *graph.Snapshot, emit func(string, any, any)) {
			pm, nm := get(prev), get(next)
			for k, n := range nm {
				p, ok := pm[k]
				switch {
				case !ok:
					emit(k, nil, n)
				case p == n: // copy-on-write snapshots share unchanged rows
				case !reflect.DeepEqual(p, n): // a resync re-reads every row
					emit(k, p, n)
				}
			}
			for k, p := range pm {
				if _, ok := nm[k]; !ok {
					emit(k, p, nil)
				}
			}
		},
		put: func(s *graph.Snapshot, key string, v any) {
			m := get(s)
			if v == nil {
				delete(m, key)
				return
			}
			m[key] = v.(*T)
		},
		size: func(s *graph.Snapshot) int { return len(get(s)) },
	}
}

const (
	dbNB = "OVN_Northbound"
	dbSB = "OVN_Southbound"
)

var tables = []table{
	tbl(dbNB, nbmodel.LogicalRouterTable, func(s *graph.Snapshot) map[string]*nbmodel.LogicalRouter { return s.LogicalRouters }),
	tbl(dbNB, nbmodel.LogicalRouterPortTable, func(s *graph.Snapshot) map[string]*nbmodel.LogicalRouterPort { return s.LogicalRouterPorts }),
	tbl(dbNB, nbmodel.LogicalRouterStaticRouteTable, func(s *graph.Snapshot) map[string]*nbmodel.LogicalRouterStaticRoute { return s.StaticRoutes }),
	tbl(dbNB, nbmodel.LogicalRouterPolicyTable, func(s *graph.Snapshot) map[string]*nbmodel.LogicalRouterPolicy { return s.Policies }),
	tbl(dbNB, nbmodel.NATTable, func(s *graph.Snapshot) map[string]*nbmodel.NAT { return s.NATs }),
	tbl(dbNB, nbmodel.GatewayChassisTable, func(s *graph.Snapshot) map[string]*nbmodel.GatewayChassis { return s.GatewayChassis }),
	tbl(dbNB, nbmodel.HAChassisGroupTable, func(s *graph.Snapshot) map[string]*nbmodel.HAChassisGroup { return s.HAChassisGroups }),
	tbl(dbNB, nbmodel.HAChassisTable, func(s *graph.Snapshot) map[string]*nbmodel.HAChassis { return s.HAChassis }),
	tbl(dbNB, nbmodel.LogicalSwitchTable, func(s *graph.Snapshot) map[string]*nbmodel.LogicalSwitch { return s.LogicalSwitches }),
	tbl(dbNB, nbmodel.LogicalSwitchPortTable, func(s *graph.Snapshot) map[string]*nbmodel.LogicalSwitchPort { return s.LogicalSwitchPorts }),
	tbl(dbNB, nbmodel.LoadBalancerTable, func(s *graph.Snapshot) map[string]*nbmodel.LoadBalancer { return s.LoadBalancers }),
	tbl(dbSB, sbmodel.ChassisTable, func(s *graph.Snapshot) map[string]*sbmodel.Chassis { return s.Chassis }),
	tbl(dbSB, sbmodel.EncapTable, func(s *graph.Snapshot) map[string]*sbmodel.Encap { return s.Encaps }),
	tbl(dbSB, sbmodel.PortBindingTable, func(s *graph.Snapshot) map[string]*sbmodel.PortBinding { return s.PortBindings }),
	tbl(dbSB, sbmodel.DatapathBindingTable, func(s *graph.Snapshot) map[string]*sbmodel.DatapathBinding { return s.DatapathBindings }),
	tbl(dbSB, sbmodel.BFDTable, func(s *graph.Snapshot) map[string]*sbmodel.BFD { return s.BFDs }),
}

var tableByName = func() map[string]table {
	m := make(map[string]table, len(tables))
	for _, t := range tables {
		m[t.name] = t
	}
	return m
}()

// rowCount is the number of rows of all consumed tables in s.
func rowCount(s *graph.Snapshot) int {
	n := 0
	for _, t := range tables {
		n += t.size(s)
	}
	return n
}

// changedColumns lists the OVSDB columns that differ between two rows of the
// same model type, sorted. _uuid is never listed.
func changedColumns(old, new any) []string {
	ov, nv := reflect.Indirect(reflect.ValueOf(old)), reflect.Indirect(reflect.ValueOf(new))
	if !ov.IsValid() || !nv.IsValid() || ov.Type() != nv.Type() || ov.Kind() != reflect.Struct {
		return nil
	}
	var cols []string
	for i := 0; i < ov.NumField(); i++ {
		col := ov.Type().Field(i).Tag.Get("ovsdb")
		if col == "" || col == "_uuid" {
			continue
		}
		if !reflect.DeepEqual(ov.Field(i).Interface(), nv.Field(i).Interface()) {
			cols = append(cols, col)
		}
	}
	sort.Strings(cols)
	return cols
}
