package diag

import (
	"fmt"
	"strconv"
)

// BuildOvsVsctlListInterfaceCommand returns the typed command for querying OVS interfaces.
func BuildOvsVsctlListInterfaceCommand() []string {
	return []string{"ovs-vsctl", "--format=json", "list", "Interface"}
}

// BuildOvsVsctlShowCommand returns the typed command for ovs-vsctl show.
func BuildOvsVsctlShowCommand() []string {
	return []string{"ovs-vsctl", "show"}
}

// BuildCTZoneListCommand returns the typed command for inspecting conntrack zones.
func BuildCTZoneListCommand() []string {
	return []string{"ovs-appctl", "ct-zone-list"}
}

// BuildConntrackDumpCommand returns the typed command for dumping conntrack entries.
func BuildConntrackDumpCommand(zone int) []string {
	if zone > 0 {
		return []string{"ovs-appctl", "dpctl/dump-conntrack", fmt.Sprintf("zone=%d", zone)}
	}
	return []string{"ovs-appctl", "dpctl/dump-conntrack"}
}

// BuildOvsOfctlDumpFlows returns the typed command for inspecting OpenFlow flows.
func BuildOvsOfctlDumpFlows(bridge string) ([]string, error) {
	if bridge == "" {
		bridge = "br-int"
	}
	if invalidChars.MatchString(bridge) {
		return nil, fmt.Errorf("invalid characters in bridge name: %q", bridge)
	}
	return []string{"ovs-ofctl", "-O", "OpenFlow13", "dump-flows", bridge}, nil
}

// BuildOvnControllerLogTail returns the typed command for inspecting local ovn-controller logs.
func BuildOvnControllerLogTail(lines int) []string {
	if lines <= 0 {
		lines = 50
	}
	if lines > 500 {
		lines = 500
	}
	return []string{"tail", "-n", strconv.Itoa(lines), "/var/log/ovn/ovn-controller.log"}
}
