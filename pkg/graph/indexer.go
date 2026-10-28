package graph

import (
	"sort"
	"strings"

	"github.com/coulof/ovn-topology/pkg/ovsdb/nbmodel"
	"github.com/coulof/ovn-topology/pkg/ovsdb/sbmodel"
)

// Reindex rebuilds secondary indices on the snapshot.
// Must be called after populating or updating tables before the snapshot is made active.
func (s *Snapshot) Reindex() {
	s.LRPByName = make(map[string]*nbmodel.LogicalRouterPort, len(s.LogicalRouterPorts))
	for _, lrp := range s.LogicalRouterPorts {
		s.LRPByName[lrp.Name] = lrp
	}

	s.LSPByName = make(map[string]*nbmodel.LogicalSwitchPort, len(s.LogicalSwitchPorts))
	for _, lsp := range s.LogicalSwitchPorts {
		s.LSPByName[lsp.Name] = lsp
	}

	s.ChassisByUUID = make(map[string]*sbmodel.Chassis, len(s.Chassis))
	for _, ch := range s.Chassis {
		s.ChassisByUUID[ch.UUID] = ch
	}

	s.PortBindingByPort = make(map[string]*sbmodel.PortBinding, len(s.PortBindings))
	s.CRPortBindingByLRP = make(map[string]*sbmodel.PortBinding)
	for _, pb := range s.PortBindings {
		s.PortBindingByPort[pb.LogicalPort] = pb
		if strings.HasPrefix(pb.LogicalPort, "cr-") {
			lrpName := strings.TrimPrefix(pb.LogicalPort, "cr-")
			s.CRPortBindingByLRP[lrpName] = pb
		}
	}

	s.LSPsBySwitch = make(map[string][]string, len(s.LogicalSwitches))
	for lsUUID, ls := range s.LogicalSwitches {
		s.LSPsBySwitch[lsUUID] = append([]string(nil), ls.Ports...)
	}

	s.LRPsByRouter = make(map[string][]string, len(s.LogicalRouters))
	lrpToRouter := make(map[string]string, len(s.LogicalRouterPorts))
	for lrUUID, lr := range s.LogicalRouters {
		s.LRPsByRouter[lrUUID] = append([]string(nil), lr.Ports...)
		for _, portUUID := range lr.Ports {
			lrpToRouter[portUUID] = lrUUID
		}
	}

	s.AttachedSwitchesByLR = make(map[string][]string)
	s.AttachedRoutersByLS = make(map[string][]string)

	for lsUUID, ls := range s.LogicalSwitches {
		for _, lspUUID := range ls.Ports {
			lsp, ok := s.LogicalSwitchPorts[lspUUID]
			if !ok || lsp.Type != "router" {
				continue
			}
			routerPortName := lsp.Options["router-port"]
			if routerPortName == "" {
				continue
			}
			lrp, ok := s.LRPByName[routerPortName]
			if !ok {
				continue
			}
			lrUUID, ok := lrpToRouter[lrp.UUID]
			if !ok {
				continue
			}

			s.AttachedSwitchesByLR[lrUUID] = appendIfNotPresent(s.AttachedSwitchesByLR[lrUUID], lsUUID)
			s.AttachedRoutersByLS[lsUUID] = appendIfNotPresent(s.AttachedRoutersByLS[lsUUID], lrUUID)
		}
	}
	// Built from map iteration: sort so /node and its goldens are stable.
	for _, ids := range s.AttachedSwitchesByLR {
		sort.Strings(ids)
	}
	for _, ids := range s.AttachedRoutersByLS {
		sort.Strings(ids)
	}
}

func appendIfNotPresent(list []string, item string) []string {
	for _, existing := range list {
		if existing == item {
			return list
		}
	}
	return append(list, item)
}

// chassisFor resolves a chassis reference: a name (the snapshot key and node ID)
// or a row UUID (what Port_Binding.chassis holds in OVSDB). Nil if unknown.
func (s *Snapshot) chassisFor(ref string) *sbmodel.Chassis {
	if ch, ok := s.Chassis[ref]; ok {
		return ch
	}
	return s.ChassisByUUID[ref]
}

// boundChassisName returns the name of the chassis a port binding is bound on, or
// "" if unbound. An unknown chassis reference is returned as is.
func (s *Snapshot) boundChassisName(pb *sbmodel.PortBinding) string {
	if pb == nil || pb.Chassis == nil || *pb.Chassis == "" {
		return ""
	}
	if ch := s.chassisFor(*pb.Chassis); ch != nil {
		return ch.Name
	}
	return *pb.Chassis
}
