package main

// Router-port facts shared by the details pane and the CNI layer: what a port
// is for (role) and how its traffic is routed.

import (
	"fmt"
	"strings"
)

func portRole(c *RouterPortConnection) string {
	switch {
	case c.ActiveGateway != "":
		return "external gateway"
	case c.IsShared:
		return "interconnect"
	case c.SwitchName == "join":
		return "node transit (join)"
	default:
		return "subnet gateway"
	}
}

func (m model) portFacts(c *RouterPortConnection) (routers []string, cidr, mode, explain string) {
	st := m.styles
	for _, p := range m.switchPorts[c.SwitchID] {
		routers = append(routers, p.RouterName)
	}
	if m.tree != nil {
		if ls := m.tree.byID[c.SwitchID]; ls != nil {
			cidr = st.MutedText.Render(ls.summary)
		}
	}
	ip := ""
	if len(c.Networks) > 0 {
		ip = extractCleanIP(c.Networks[0])
	}
	mode = "Distributed — every chassis routes this subnet locally (no redirect port)"
	explain = fmt.Sprintf("Workloads on '%s' use %s as their default gateway.", c.SwitchName, ip)
	switch {
	case c.ActiveGateway != "":
		mode = "Centralized — traffic egresses via the active gateway chassis " + m.resolveNodeName(c.ActiveGateway)
		explain = "External gateway port (EIP / SNAT); the cr-" + c.PortName + " redirect port is bound on the active chassis."
	case c.IsShared:
		explain = fmt.Sprintf("Interconnect: '%s' is attached to %d routers (%s).", c.SwitchName, len(routers), strings.Join(routers, ", "))
	case c.SwitchName == "join":
		explain = "Node transit: connects the VPC router to every node's ovn0 interface over the join switch."
	}
	return routers, cidr, mode, explain
}
