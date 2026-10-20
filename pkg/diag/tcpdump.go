package diag

import (
	"fmt"
	"strconv"
	"strings"
)

const (
	DefaultPacketCount = 100
	MaxPacketCount     = 1000
)

// TcpdumpRequest defines the parameters for running a non-mutating packet capture
// inside a node's kube-ovn-cni daemon pod.
type TcpdumpRequest struct {
	Interface string // Interface name (e.g., veth interface, geneve0, or eth0)
	Filter    string // BPF filter (e.g., "icmp", "tcp port 80", "host 10.244.0.5")
	Count     int    // Max packet count limit (default: 100, capped at 1000)
}

// BuildTcpdumpCommand constructs the argv slice for running tcpdump with strict limits.
func BuildTcpdumpCommand(req TcpdumpRequest) ([]string, error) {
	if req.Interface == "" {
		return nil, fmt.Errorf("interface cannot be empty")
	}

	// Validate inputs against injection
	if invalidChars.MatchString(req.Interface) {
		return nil, fmt.Errorf("invalid characters in interface: %q", req.Interface)
	}

	filter := strings.TrimSpace(req.Filter)
	if filter == "" {
		filter = "ip"
	}
	if invalidChars.MatchString(filter) {
		return nil, fmt.Errorf("invalid characters in filter: %q", filter)
	}

	count := req.Count
	if count <= 0 {
		count = DefaultPacketCount
	}
	if count > MaxPacketCount {
		count = MaxPacketCount
	}

	return []string{
		"tcpdump",
		"-i", req.Interface,
		"-nn",
		"-c", strconv.Itoa(count),
		filter,
	}, nil
}
