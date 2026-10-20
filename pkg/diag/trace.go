package diag

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Dangerous shell metacharacters that must never appear in any argument.
var invalidChars = regexp.MustCompile(`[;&|` + "`" + `$\r\n\t]`)

// TraceRequest defines the parameters for building an ovn-trace microflow command.
type TraceRequest struct {
	Datapath string // Logical switch or router datapath name
	Inport   string // Ingress Logical Switch Port name
	SrcMAC   string // Source MAC address
	DstMAC   string // Destination (or next-hop router) MAC address
	SrcIP    string // Source IPv4 address
	DstIP    string // Destination IPv4 address
	Protocol string // "icmp", "tcp", "udp" (default: "icmp")
	SrcPort  int    // Source port for TCP/UDP
	DstPort  int    // Destination port for TCP/UDP
	CTState  string // "new" or "est" (default: "new")
}

// TraceStage represents a parsed stage from the ovn-trace execution tree.
type TraceStage struct {
	Pipeline string // "ingress" or "egress"
	Table    int
	Name     string
	Action   string
	Match    string
}

// TraceResult summarizes the output of an ovn-trace run.
type TraceResult struct {
	RawOutput string
	Verdict   string // "delivered", "dropped", "no-route", "unknown"
	Summary   string
	FinalPort string
	Stages    []TraceStage
}

// BuildTraceCommand constructs the typed argv slice for running ovn-trace in ovn-central.
func BuildTraceCommand(req TraceRequest) ([]string, error) {
	if req.Datapath == "" {
		return nil, fmt.Errorf("datapath cannot be empty")
	}
	if req.Inport == "" {
		return nil, fmt.Errorf("inport cannot be empty")
	}
	if req.SrcIP == "" || req.DstIP == "" {
		return nil, fmt.Errorf("both src-ip and dst-ip are required")
	}

	// Validate inputs against injection
	for _, field := range []string{req.Datapath, req.Inport, req.SrcMAC, req.DstMAC, req.SrcIP, req.DstIP} {
		if invalidChars.MatchString(field) {
			return nil, fmt.Errorf("invalid characters in parameter: %q", field)
		}
	}

	ct := req.CTState
	if ct == "" {
		ct = "new"
	}
	if ct != "new" && ct != "est" {
		return nil, fmt.Errorf("invalid ct state: must be 'new' or 'est'")
	}

	proto := strings.ToLower(req.Protocol)
	if proto == "" {
		proto = "icmp"
	}

	var matchParts []string
	matchParts = append(matchParts, fmt.Sprintf(`inport == "%s"`, req.Inport))

	if req.SrcMAC != "" {
		matchParts = append(matchParts, fmt.Sprintf("eth.src == %s", req.SrcMAC))
	}
	if req.DstMAC != "" {
		matchParts = append(matchParts, fmt.Sprintf("eth.dst == %s", req.DstMAC))
	}

	matchParts = append(matchParts, fmt.Sprintf("ip4.src == %s", req.SrcIP))
	matchParts = append(matchParts, fmt.Sprintf("ip4.dst == %s", req.DstIP))

	switch proto {
	case "icmp":
		matchParts = append(matchParts, "ip.proto == 1 && icmp4.type == 8 && icmp4.code == 0")
	case "tcp":
		srcP := req.SrcPort
		if srcP <= 0 {
			srcP = 40000
		}
		dstP := req.DstPort
		if dstP <= 0 {
			dstP = 80
		}
		matchParts = append(matchParts, fmt.Sprintf("ip.proto == 6 && tcp.src == %d && tcp.dst == %d", srcP, dstP))
	case "udp":
		srcP := req.SrcPort
		if srcP <= 0 {
			srcP = 40000
		}
		dstP := req.DstPort
		if dstP <= 0 {
			dstP = 53
		}
		matchParts = append(matchParts, fmt.Sprintf("ip.proto == 17 && udp.src == %d && udp.dst == %d", srcP, dstP))
	default:
		return nil, fmt.Errorf("unsupported protocol: %s (supported: icmp, tcp, udp)", proto)
	}

	matchStr := strings.Join(matchParts, " && ")
	return []string{
		"ovn-trace",
		fmt.Sprintf("--ct=%s", ct),
		req.Datapath,
		matchStr,
	}, nil
}

var (
	outputPortRegex = regexp.MustCompile(`output\("([^"]+)"\)`)
	outputRegex     = regexp.MustCompile(`output to "([^"]+)"`)
	tableRegex      = regexp.MustCompile(`(?:table=(\d+)\s*\(([^)]*)\)|^\s*(\d+)\.\s+([a-zA-Z0-9_-]+))`)
)

// ParseTraceOutput parses raw ovn-trace output into a structured TraceResult.
func ParseTraceOutput(raw string) TraceResult {
	res := TraceResult{
		RawOutput: raw,
		Verdict:   "unknown",
		Summary:   "Trace completed with unclassified verdict",
	}

	lines := strings.Split(raw, "\n")
	var stages []TraceStage
	currentPipeline := "ingress"

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}

		if strings.HasPrefix(trimmed, "# egress") {
			currentPipeline = "egress"
		} else if strings.HasPrefix(trimmed, "# ingress") {
			currentPipeline = "ingress"
		}

		// Stage detection
		if m := tableRegex.FindStringSubmatch(trimmed); len(m) > 0 {
			var tableNum int
			var stageName string
			if m[1] != "" {
				tableNum, _ = strconv.Atoi(m[1])
				stageName = m[2]
			} else if len(m) > 4 && m[3] != "" {
				tableNum, _ = strconv.Atoi(m[3])
				stageName = m[4]
			}

			if stageName != "" {
				stages = append(stages, TraceStage{
					Pipeline: currentPipeline,
					Table:    tableNum,
					Name:     stageName,
					Action:   trimmed,
				})
			}
		}

		// Verdict detection
		if m := outputPortRegex.FindStringSubmatch(trimmed); len(m) > 1 {
			res.Verdict = "delivered"
			res.FinalPort = m[1]
			res.Summary = fmt.Sprintf("Packet delivered to port %s", m[1])
		} else if m := outputRegex.FindStringSubmatch(trimmed); len(m) > 1 {
			res.Verdict = "delivered"
			res.FinalPort = m[1]
			res.Summary = fmt.Sprintf("Packet forwarded to %s", m[1])
		} else if strings.Contains(trimmed, "drop;") || strings.Contains(trimmed, "/* drop */") || strings.Contains(trimmed, "action=drop") {
			if res.Verdict != "delivered" {
				res.Verdict = "dropped"
				res.Summary = "Packet dropped by rule or ACL"
			}
		} else if strings.Contains(trimmed, "No route") || strings.Contains(trimmed, "unroutable") {
			res.Verdict = "no-route"
			res.Summary = "Packet dropped: no route found"
		}
	}

	res.Stages = stages
	return res
}
