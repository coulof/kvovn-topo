package graph

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/coulof/ovn-topology/pkg/ovsdb/nbmodel"
	"github.com/coulof/ovn-topology/pkg/ovsdb/sbmodel"
)

// LoadTestSnapshot loads test fixtures from testdata/ovn-nb.json and testdata/ovn-sb.json.
func LoadTestSnapshot(t testing.TB) *Snapshot {
	if t != nil {
		t.Helper()
	}

	fail := func(format string, args ...interface{}) {
		if t != nil {
			t.Fatalf(format, args...)
		} else {
			panic(fmt.Sprintf(format, args...))
		}
	}

	// Locate testdata directory
	candidates := []string{
		"../../testdata",
		"../testdata",
		"testdata",
		"../../../../testdata",
	}
	var testdataDir string
	for _, dir := range candidates {
		if _, err := os.Stat(filepath.Join(dir, "ovn-nb.json")); err == nil {
			testdataDir = dir
			break
		}
	}
	if testdataDir == "" {
		fail("testdata directory not found from candidates: %v", candidates)
		return nil
	}

	nbBytes, err := os.ReadFile(filepath.Join(testdataDir, "ovn-nb.json"))
	if err != nil {
		fail("failed to read ovn-nb.json: %v", err)
		return nil
	}
	sbBytes, err := os.ReadFile(filepath.Join(testdataDir, "ovn-sb.json"))
	if err != nil {
		fail("failed to read ovn-sb.json: %v", err)
		return nil
	}

	var nbRaw struct {
		NB struct {
			LogicalRouters      map[string]map[string]interface{} `json:"Logical_Router"`
			LogicalRouterPorts map[string]map[string]interface{} `json:"Logical_Router_Port"`
			LogicalSwitches     map[string]map[string]interface{} `json:"Logical_Switch"`
			LogicalSwitchPorts  map[string]map[string]interface{} `json:"Logical_Switch_Port"`
			NATs                map[string]map[string]interface{} `json:"NAT"`
			GatewayChassis      map[string]map[string]interface{} `json:"Gateway_Chassis"`
			HAChassisGroups     map[string]map[string]interface{} `json:"HA_Chassis_Group"`
			HAChassis           map[string]map[string]interface{} `json:"HA_Chassis"`
			StaticRoutes        map[string]map[string]interface{} `json:"Logical_Router_Static_Route"`
			Policies            map[string]map[string]interface{} `json:"Logical_Router_Policy"`
			LoadBalancers       map[string]map[string]interface{} `json:"Load_Balancer"`
		} `json:"OVN_Northbound"`
	}
	if err := json.Unmarshal(nbBytes, &nbRaw); err != nil {
		fail("failed to unmarshal ovn-nb.json: %v", err)
		return nil
	}

	var sbRaw struct {
		SB struct {
			Chassis          map[string]map[string]interface{} `json:"Chassis"`
			Encaps           map[string]map[string]interface{} `json:"Encap"`
			PortBindings     map[string]map[string]interface{} `json:"Port_Binding"`
			DatapathBindings map[string]map[string]interface{} `json:"Datapath_Binding"`
		} `json:"OVN_Southbound"`
	}
	if err := json.Unmarshal(sbBytes, &sbRaw); err != nil {
		fail("failed to unmarshal ovn-sb.json: %v", err)
		return nil
	}

	mockWorkloads := NewStaticWorkloadResolver()
	mockWorkloads.Add(WorkloadInfo{
		Kind:         "VirtualMachine",
		WorkloadName: "vm-custom",
		PodName:      "lsp-custom-workload",
		Namespace:    "custom-ns",
		Owner:        "VirtualMachine/vm-custom",
		VMI:          "vm-custom",
		Node:         "node-2",
	})
	mockWorkloads.Add(WorkloadInfo{
		Kind:         "Deployment",
		WorkloadName: "coredns",
		PodName:      "lsp-default-workload",
		Namespace:    "default",
		Owner:        "Deployment/coredns",
		Node:         "node-1",
	})

	snap := NewEmptySnapshot(1, &StaticNodeMapper{
		Overrides: map[string]string{
			"chassis-node-1": "node-1",
			"chassis-node-2": "node-2",
			"chassis-node-3": "node-3",
		},
	})
	snap.WorkloadResolver = mockWorkloads

	// Helper to extract ovsdb map [["k", "v"], ...]
	parseMap := func(v interface{}) map[string]string {
		res := make(map[string]string)
		if arr, ok := v.([]interface{}); ok && len(arr) == 2 && arr[0] == "map" {
			if items, ok := arr[1].([]interface{}); ok {
				for _, it := range items {
					if kv, ok := it.([]interface{}); ok && len(kv) == 2 {
						k, _ := kv[0].(string)
						val, _ := kv[1].(string)
						if k != "" {
							res[k] = val
						}
					}
				}
			}
		}
		return res
	}

	// Helper to extract ovsdb set of strings or UUIDs
	parseSet := func(v interface{}) []string {
		var res []string
		if arr, ok := v.([]interface{}); ok && len(arr) == 2 && arr[0] == "set" {
			if items, ok := arr[1].([]interface{}); ok {
				for _, it := range items {
					if str, ok := it.(string); ok {
						res = append(res, str)
					} else if uArr, ok := it.([]interface{}); ok && len(uArr) == 2 && uArr[0] == "uuid" {
						if uStr, ok := uArr[1].(string); ok {
							res = append(res, uStr)
						}
					}
				}
			}
		}
		return res
	}

	// Parse LRs
	for uuid, row := range nbRaw.NB.LogicalRouters {
		name, _ := row["name"].(string)
		enabled := true
		if en, ok := row["enabled"].(bool); ok {
			enabled = en
		}
		snap.LogicalRouters[uuid] = &nbmodel.LogicalRouter{
			UUID:         uuid,
			Name:         name,
			Enabled:      &enabled,
			Ports:        parseSet(row["ports"]),
			StaticRoutes: parseSet(row["static_routes"]),
			Policies:     parseSet(row["policies"]),
			Nat:          parseSet(row["nat"]),
			LoadBalancer: parseSet(row["load_balancer"]),
			ExternalIDs:  parseMap(row["external_ids"]),
		}
	}

	// Parse LRPs
	for uuid, row := range nbRaw.NB.LogicalRouterPorts {
		name, _ := row["name"].(string)
		mac, _ := row["mac"].(string)
		snap.LogicalRouterPorts[uuid] = &nbmodel.LogicalRouterPort{
			UUID:           uuid,
			Name:           name,
			MAC:            mac,
			Networks:       parseSet(row["networks"]),
			GatewayChassis: parseSet(row["gateway_chassis"]),
			ExternalIDs:    parseMap(row["external_ids"]),
		}
	}

	// Parse LSs
	for uuid, row := range nbRaw.NB.LogicalSwitches {
		name, _ := row["name"].(string)
		snap.LogicalSwitches[uuid] = &nbmodel.LogicalSwitch{
			UUID:        uuid,
			Name:        name,
			Ports:       parseSet(row["ports"]),
			ExternalIDs: parseMap(row["external_ids"]),
		}
	}

	// Parse LSPs
	for uuid, row := range nbRaw.NB.LogicalSwitchPorts {
		name, _ := row["name"].(string)
		portType, _ := row["type"].(string)
		snap.LogicalSwitchPorts[uuid] = &nbmodel.LogicalSwitchPort{
			UUID:        uuid,
			Name:        name,
			Type:        portType,
			Options:     parseMap(row["options"]),
			Addresses:   parseSet(row["addresses"]),
			ExternalIDs: parseMap(row["external_ids"]),
		}
	}

	// Parse NATs
	for uuid, row := range nbRaw.NB.NATs {
		natType, _ := row["type"].(string)
		extIP, _ := row["external_ip"].(string)
		logIP, _ := row["logical_ip"].(string)
		var logPort *string
		if lp, ok := row["logical_port"].(string); ok && lp != "" {
			logPort = &lp
		}
		snap.NATs[uuid] = &nbmodel.NAT{
			UUID:        uuid,
			Type:        natType,
			ExternalIP:  extIP,
			LogicalIP:   logIP,
			LogicalPort: logPort,
			ExternalIDs: parseMap(row["external_ids"]),
		}
	}

	// Parse Gateway Chassis
	for uuid, row := range nbRaw.NB.GatewayChassis {
		name, _ := row["name"].(string)
		chassisName, _ := row["chassis_name"].(string)
		priority := int(row["priority"].(float64))
		snap.GatewayChassis[uuid] = &nbmodel.GatewayChassis{
			UUID:        uuid,
			Name:        name,
			ChassisName: chassisName,
			Priority:    priority,
			ExternalIDs: parseMap(row["external_ids"]),
		}
	}

	// Parse HA Chassis Groups
	for uuid, row := range nbRaw.NB.HAChassisGroups {
		name, _ := row["name"].(string)
		snap.HAChassisGroups[uuid] = &nbmodel.HAChassisGroup{
			UUID:        uuid,
			Name:        name,
			HaChassis:   parseSet(row["ha_chassis"]),
			ExternalIDs: parseMap(row["external_ids"]),
		}
	}

	// Parse HA Chassis
	for uuid, row := range nbRaw.NB.HAChassis {
		chassisName, _ := row["chassis_name"].(string)
		priority := int(row["priority"].(float64))
		snap.HAChassis[uuid] = &nbmodel.HAChassis{
			UUID:        uuid,
			ChassisName: chassisName,
			Priority:    priority,
			ExternalIDs: parseMap(row["external_ids"]),
		}
	}

	// Parse Static Routes
	for uuid, row := range nbRaw.NB.StaticRoutes {
		prefix, _ := row["ip_prefix"].(string)
		nexthop, _ := row["nexthop"].(string)
		snap.StaticRoutes[uuid] = &nbmodel.LogicalRouterStaticRoute{
			UUID:        uuid,
			IPPrefix:    prefix,
			Nexthop:     nexthop,
			ExternalIDs: parseMap(row["external_ids"]),
		}
	}

	// Parse Policies
	for uuid, row := range nbRaw.NB.Policies {
		priority := int(row["priority"].(float64))
		match, _ := row["match"].(string)
		action, _ := row["action"].(string)
		nexthop, _ := row["nexthop"].(string)
		snap.Policies[uuid] = &nbmodel.LogicalRouterPolicy{
			UUID:        uuid,
			Priority:    priority,
			Match:       match,
			Action:      action,
			Nexthop:     &nexthop,
			ExternalIDs: parseMap(row["external_ids"]),
		}
	}

	// Parse Load Balancers
	for uuid, row := range nbRaw.NB.LoadBalancers {
		name, _ := row["name"].(string)
		protocol, _ := row["protocol"].(string)
		snap.LoadBalancers[uuid] = &nbmodel.LoadBalancer{
			UUID:        uuid,
			Name:        name,
			Protocol:    &protocol,
			Vips:        parseMap(row["vips"]),
			ExternalIDs: parseMap(row["external_ids"]),
		}
	}

	// Parse Chassis (SB)
	for uuid, row := range sbRaw.SB.Chassis {
		name, _ := row["name"].(string)
		hostname, _ := row["hostname"].(string)
		snap.Chassis[name] = &sbmodel.Chassis{
			UUID:        uuid,
			Name:        name,
			Hostname:    hostname,
			Encaps:      parseSet(row["encaps"]),
			OtherConfig: parseMap(row["other_config"]),
			ExternalIDs: parseMap(row["external_ids"]),
		}
	}

	// Parse Encaps
	for uuid, row := range sbRaw.SB.Encaps {
		chassisName, _ := row["chassis_name"].(string)
		ip, _ := row["ip"].(string)
		encapType, _ := row["type"].(string)
		snap.Encaps[uuid] = &sbmodel.Encap{
			UUID:        uuid,
			ChassisName: chassisName,
			IP:          ip,
			Type:        encapType,
			Options:     parseMap(row["options"]),
		}
	}

	// Parse Port Bindings
	for uuid, row := range sbRaw.SB.PortBindings {
		lport, _ := row["logical_port"].(string)
		pbType, _ := row["type"].(string)
		var chassisRef *string
		if chArr, ok := row["chassis"].([]interface{}); ok && len(chArr) == 2 && chArr[0] == "uuid" {
			chUUID := chArr[1].(string) // the Chassis row UUID, as OVSDB stores the reference
			chassisRef = &chUUID
		}
		snap.PortBindings[uuid] = &sbmodel.PortBinding{
			UUID:        uuid,
			LogicalPort: lport,
			Type:        pbType,
			Chassis:     chassisRef,
			Options:     parseMap(row["options"]),
			ExternalIDs: parseMap(row["external_ids"]),
		}
	}

	snap.Reindex()
	return snap
}
