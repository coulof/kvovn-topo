package history

import (
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/coulof/kvovn-topo/pkg/graph"
	"github.com/coulof/kvovn-topo/pkg/ovsdb/nbmodel"
	"github.com/coulof/kvovn-topo/pkg/ovsdb/sbmodel"
)

// summarize maps row changes to events (the ADR-0002 summarizer). Every row
// change yields at least one event: unmapped ones (nil) become generic row
// events. An empty, non-nil result means the change is told by other rows of
// the same revision (a router's port list grows with the new port's row).
// prev and next are the snapshots around the change, for names and owners.
func summarize(prev, next *graph.Snapshot, rows []RowChange) []Event {
	var out []Event
	for _, rc := range rows {
		evs := summarizeRow(prev, next, rc)
		if evs == nil {
			evs = []Event{genericEvent(rc)}
		}
		out = append(out, evs...)
	}
	return out
}

func ev(kind, object, name, format string, args ...any) Event {
	return Event{Kind: kind, Object: object, Name: name, Summary: fmt.Sprintf(format, args...)}
}

func genericEvent(rc RowChange) Event {
	e := Event{Kind: "row." + string(rc.Op)}
	switch rc.Op {
	case OpModify:
		e.Summary = fmt.Sprintf("%s %s modified: %s", rc.Table, short(rc.Key), strings.Join(rc.Columns, ", "))
	case OpInsert:
		e.Summary = fmt.Sprintf("%s %s added", rc.Table, short(rc.Key))
	default:
		e.Summary = fmt.Sprintf("%s %s removed", rc.Table, short(rc.Key))
	}
	return e
}

func short(uuid string) string {
	if len(uuid) > 8 {
		return uuid[:8]
	}
	return uuid
}

// added/removed/modified verb for an op, with the matching kind suffix.
func verb(op Op) string {
	switch op {
	case OpInsert:
		return "added"
	case OpRemove:
		return "removed"
	}
	return "changed"
}

func summarizeRow(prev, next *graph.Snapshot, rc RowChange) []Event {
	switch v := firstNonNil(rc.New, rc.Old).(type) {
	case *nbmodel.LogicalRouter:
		if rc.Op == OpModify && !hasAny(rc.Columns, "name", "enabled", "options") {
			return []Event{} // its routes/NATs/ports moved: their own rows report it
		}
		return []Event{ev("lr."+verb(rc.Op), graph.PrefixLR+v.UUID, v.Name, "router %s %s%s", v.Name, verb(rc.Op), cols(rc))}
	case *nbmodel.LogicalSwitch:
		if rc.Op == OpModify && !hasAny(rc.Columns, "name", "other_config") {
			return []Event{} // port membership: the port rows report it
		}
		return []Event{ev("ls."+verb(rc.Op), graph.PrefixLS+v.UUID, v.Name, "switch %s %s%s", v.Name, verb(rc.Op), cols(rc))}
	case *nbmodel.LogicalRouterPort:
		return []Event{ev("lrp."+verb(rc.Op), graph.PrefixLRP+v.UUID, v.Name, "router port %s %s%s", v.Name, verb(rc.Op), lrpDetail(rc))}
	case *nbmodel.LogicalSwitchPort:
		return []Event{ev("lsp."+verb(rc.Op), graph.PrefixLSP+v.UUID, v.Name, "switch port %s %s%s", v.Name, verb(rc.Op), lspDetail(rc))}
	case *nbmodel.NAT:
		return natEvents(prev, next, rc)
	case *nbmodel.LogicalRouterStaticRoute:
		lr := ownerLR(prev, next, func(r *nbmodel.LogicalRouter) []string { return r.StaticRoutes }, v.UUID)
		return []Event{ev("route."+verb(rc.Op), lrID(lr), lrName(lr), "route %s %s on %s%s", routeText(v), verb(rc.Op), lrName(lr), routeDetail(rc))}
	case *nbmodel.LogicalRouterPolicy:
		lr := ownerLR(prev, next, func(r *nbmodel.LogicalRouter) []string { return r.Policies }, v.UUID)
		return []Event{ev("policy."+verb(rc.Op), lrID(lr), lrName(lr), "policy %d %q %s %s on %s%s", v.Priority, v.Match, v.Action, verb(rc.Op), lrName(lr), cols(rc))}
	case *nbmodel.LoadBalancer:
		if rc.Op == OpModify && slices.Contains(rc.Columns, "vips") {
			return []Event{ev("lb.backends", graph.PrefixLB+v.UUID, v.Name, "load balancer %s backends changed: %s", v.Name, vipsDiff(rc))}
		}
		return []Event{ev("lb."+verb(rc.Op), graph.PrefixLB+v.UUID, v.Name, "load balancer %s %s%s", v.Name, verb(rc.Op), cols(rc))}
	case *sbmodel.Chassis:
		return []Event{ev("chassis."+verb(rc.Op), graph.PrefixChassis+v.Name, v.Name, "chassis %s (%s) %s%s", v.Hostname, short(v.Name), verb(rc.Op), cols(rc))}
	case *sbmodel.PortBinding:
		return bindingEvents(prev, next, rc)
	}
	return nil
}

func firstNonNil(a, b any) any {
	if a != nil {
		return a
	}
	return b
}

func hasAny(cols []string, want ...string) bool {
	for _, w := range want {
		if slices.Contains(cols, w) {
			return true
		}
	}
	return false
}

func cols(rc RowChange) string {
	if rc.Op != OpModify || len(rc.Columns) == 0 {
		return ""
	}
	return " (" + strings.Join(rc.Columns, ", ") + ")"
}

func lrpDetail(rc RowChange) string {
	if rc.Op == OpModify {
		o, n := rc.Old.(*nbmodel.LogicalRouterPort), rc.New.(*nbmodel.LogicalRouterPort)
		if slices.Contains(rc.Columns, "networks") {
			return fmt.Sprintf(": networks %s → %s", list(o.Networks), list(n.Networks))
		}
		return cols(rc)
	}
	p := firstNonNil(rc.New, rc.Old).(*nbmodel.LogicalRouterPort)
	return " " + list(p.Networks)
}

func lspDetail(rc RowChange) string {
	if rc.Op == OpModify {
		o, n := rc.Old.(*nbmodel.LogicalSwitchPort), rc.New.(*nbmodel.LogicalSwitchPort)
		if slices.Contains(rc.Columns, "addresses") {
			return fmt.Sprintf(": addresses %s → %s", list(o.Addresses), list(n.Addresses))
		}
		return cols(rc)
	}
	p := firstNonNil(rc.New, rc.Old).(*nbmodel.LogicalSwitchPort)
	if len(p.Addresses) > 0 {
		return " " + list(p.Addresses)
	}
	return ""
}

func list(v []string) string {
	if len(v) == 0 {
		return "—"
	}
	return strings.Join(v, " ")
}

func natEvents(prev, next *graph.Snapshot, rc RowChange) []Event {
	v := firstNonNil(rc.New, rc.Old).(*nbmodel.NAT)
	lr := ownerLR(prev, next, func(r *nbmodel.LogicalRouter) []string { return r.Nat }, v.UUID)
	id := graph.PrefixNAT + v.UUID
	name := natName(v)
	if rc.Op == OpModify && hasAny(rc.Columns, "logical_ip", "logical_port") {
		o := rc.Old.(*nbmodel.NAT)
		n := rc.New.(*nbmodel.NAT)
		return []Event{ev("nat.retargeted", id, name, "%s %s moved %s → %s on %s",
			natLabel(n), n.ExternalIP, natTarget(o), natTarget(n), lrName(lr))}
	}
	return []Event{ev("nat."+verb(rc.Op), id, name, "%s %s → %s %s on %s%s",
		natLabel(v), v.ExternalIP, natTarget(v), verb(rc.Op), lrName(lr), cols(rc))}
}

func natName(n *nbmodel.NAT) string { return string(n.Type) + " " + n.ExternalIP }

func natLabel(n *nbmodel.NAT) string {
	if n.Type == nbmodel.NATTypeDNATAndSNAT {
		return "FIP"
	}
	return strings.ToUpper(string(n.Type))
}

func natTarget(n *nbmodel.NAT) string {
	if n.LogicalPort != nil && *n.LogicalPort != "" {
		return n.LogicalIP + " (" + *n.LogicalPort + ")"
	}
	return n.LogicalIP
}

func routeText(r *nbmodel.LogicalRouterStaticRoute) string {
	s := r.IPPrefix + " via " + r.Nexthop
	if r.Policy != nil && *r.Policy == nbmodel.LogicalRouterStaticRoutePolicySrcIP {
		s += " (src-ip)"
	}
	return s
}

func routeDetail(rc RowChange) string {
	if rc.Op != OpModify {
		return ""
	}
	o, n := rc.Old.(*nbmodel.LogicalRouterStaticRoute), rc.New.(*nbmodel.LogicalRouterStaticRoute)
	if o.Nexthop != n.Nexthop || o.IPPrefix != n.IPPrefix {
		return fmt.Sprintf(": was %s", routeText(o))
	}
	return cols(rc)
}

func vipsDiff(rc RowChange) string {
	o, n := rc.Old.(*nbmodel.LoadBalancer).Vips, rc.New.(*nbmodel.LoadBalancer).Vips
	keys := map[string]bool{}
	for k := range o {
		keys[k] = true
	}
	for k := range n {
		keys[k] = true
	}
	var parts []string
	for _, k := range sortedKeys(keys) {
		if o[k] != n[k] {
			parts = append(parts, fmt.Sprintf("%s [%s] → [%s]", k, o[k], n[k]))
		}
	}
	return strings.Join(parts, "; ")
}

func sortedKeys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ownerLR finds the router whose refs column holds uuid, in next then prev
// (a removed row is only referenced before the change).
func ownerLR(prev, next *graph.Snapshot, refs func(*nbmodel.LogicalRouter) []string, uuid string) *nbmodel.LogicalRouter {
	for _, s := range []*graph.Snapshot{next, prev} {
		for _, lr := range s.LogicalRouters {
			if slices.Contains(refs(lr), uuid) {
				return lr
			}
		}
	}
	return nil
}

func lrID(lr *nbmodel.LogicalRouter) string {
	if lr == nil {
		return ""
	}
	return graph.PrefixLR + lr.UUID
}

func lrName(lr *nbmodel.LogicalRouter) string {
	if lr == nil {
		return "?"
	}
	return lr.Name
}

// bindingEvents reports where a logical port runs: bound, unbound, moved, and
// for a chassisredirect port (cr-<lrp>) the active gateway failing over.
func bindingEvents(prev, next *graph.Snapshot, rc RowChange) []Event {
	var oldCh, newCh string
	var pb *sbmodel.PortBinding
	if o, ok := rc.Old.(*sbmodel.PortBinding); ok {
		oldCh, pb = chassisName(prev, o.Chassis), o
	}
	if n, ok := rc.New.(*sbmodel.PortBinding); ok {
		newCh, pb = chassisName(next, n.Chassis), n
	}
	if oldCh == newCh {
		if rc.Op == OpModify && !slices.Contains(rc.Columns, "up") {
			return nil // generic event
		}
		if rc.Op == OpModify {
			up := rc.New.(*sbmodel.PortBinding).Up
			id, name := portObject(prev, next, pb.LogicalPort)
			return []Event{ev("port.up", id, name, "port %s up=%s", pb.LogicalPort, boolText(up))}
		}
		if rc.Op == OpRemove || newCh == "" {
			return nil
		}
	}

	if lrp, ok := strings.CutPrefix(pb.LogicalPort, "cr-"); ok {
		id, _ := portObject(prev, next, lrp)
		switch {
		case oldCh == "":
			return []Event{ev("gateway.active", id, lrp, "gateway %s active on %s", lrp, newCh)}
		case newCh == "":
			return []Event{ev("gateway.lost", id, lrp, "gateway %s has no active chassis (was %s)", lrp, oldCh)}
		}
		return []Event{ev("gateway.failover", id, lrp, "gateway %s failed over %s → %s", lrp, oldCh, newCh)}
	}

	id, name := portObject(prev, next, pb.LogicalPort)
	switch {
	case oldCh == "":
		return []Event{ev("port.bound", id, name, "port %s bound on %s", pb.LogicalPort, newCh)}
	case newCh == "":
		return []Event{ev("port.unbound", id, name, "port %s unbound (was on %s)", pb.LogicalPort, oldCh)}
	}
	return []Event{ev("port.moved", id, name, "port %s moved %s → %s", pb.LogicalPort, oldCh, newCh)}
}

func boolText(b *bool) string {
	if b == nil {
		return "unset"
	}
	return fmt.Sprint(*b)
}

// chassisName resolves a Port_Binding.chassis reference (a row UUID) to the
// chassis hostname (Kube-OVN chassis names are system-id UUIDs), else its
// name; an unknown reference is returned as is.
func chassisName(s *graph.Snapshot, ref *string) string {
	if ref == nil || *ref == "" {
		return ""
	}
	ch, ok := s.ChassisByUUID[*ref]
	if !ok {
		ch, ok = s.Chassis[*ref]
	}
	switch {
	case !ok:
		return *ref
	case ch.Hostname != "":
		return ch.Hostname
	}
	return ch.Name
}

// portObject is the graph node of a logical port by name: an LSP or an LRP.
func portObject(prev, next *graph.Snapshot, name string) (id, display string) {
	for _, s := range []*graph.Snapshot{next, prev} {
		if lsp, ok := s.LSPByName[name]; ok {
			return graph.PrefixLSP + lsp.UUID, lsp.Name
		}
		if lrp, ok := s.LRPByName[name]; ok {
			return graph.PrefixLRP + lrp.UUID, lrp.Name
		}
	}
	return "", name
}
