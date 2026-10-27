package diag

// Inspect commands: the read-only kubectl argv behind the TUI details pane's
// Inspect lines. The TUI runs them locally with the user's kubeconfig while it
// is suspended (ADR-0001, Consequences). Built from graph objects only; every
// subcommand comes from a closed set, so no name can turn into another command.

import (
	"fmt"
	"regexp"
	"strings"
)

// nbQueries and sbQueries are the read-only ovn-nbctl / ovn-sbctl queries the
// pane offers, each followed by one object name.
var (
	nbQueries = map[string]bool{
		"show":                     true,
		"lr-route-list":            true,
		"lr-policy-list":           true,
		"lr-nat-list":              true,
		"list Logical_Router_Port": true,
		"list Logical_Switch_Port": true,
	}
	sbQueries = map[string]bool{
		"list Chassis":                   true,
		"find Port_Binding logical_port": true,
	}
)

// k8sNameRe is a Kubernetes namespace or pod name (DNS-1123 subdomain).
var k8sNameRe = regexp.MustCompile(`^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`)

// BuildKoNbctlCommand is `kubectl ko nbctl <query> <obj>` (Kube-OVN's kubectl
// plugin, run in the ovn-central leader). A "find … <column>" query takes the
// object as that column's value.
func BuildKoNbctlCommand(query, obj string) ([]string, error) {
	return koCommand("nbctl", nbQueries, query, obj)
}

// BuildKoSbctlCommand is `kubectl ko sbctl <query> <obj>`.
func BuildKoSbctlCommand(query, obj string) ([]string, error) {
	return koCommand("sbctl", sbQueries, query, obj)
}

func koCommand(tool string, allowed map[string]bool, query, obj string) ([]string, error) {
	if !allowed[query] {
		return nil, fmt.Errorf("%s query not allowed: %q", tool, query)
	}
	if !portNameRe.MatchString(obj) || strings.HasPrefix(obj, "-") { // a leading dash would read as an option
		return nil, fmt.Errorf("invalid OVN object name: %q", obj)
	}
	words := strings.Fields(query)
	if words[0] == "find" { // find <table> <column>=<obj>
		col := words[len(words)-1]
		words = append(words[:len(words)-1], col+"="+obj)
	} else {
		words = append(words, obj)
	}
	return append([]string{"kubectl", "ko", tool}, words...), nil
}

// BuildNATGatewayIptablesCommand prints the NAT table of a Kube-OVN VPC NAT
// gateway pod: its DNAT/SNAT rules and EIPs, which OVN does not see.
func BuildNATGatewayIptablesCommand(namespace, pod string) ([]string, error) {
	if !k8sNameRe.MatchString(namespace) || !k8sNameRe.MatchString(pod) {
		return nil, fmt.Errorf("invalid pod reference: %q/%q", namespace, pod)
	}
	return []string{"kubectl", "-n", namespace, "exec", pod, "--", "iptables-save", "-t", "nat"}, nil
}

// WithKubeContext pins a plain kubectl argv to a kubeconfig context. Plugin
// commands (`kubectl ko …`) cannot take it and are returned with ok=false.
func WithKubeContext(argv []string, context string) (out []string, ok bool) {
	if context == "" {
		return argv, true
	}
	if len(argv) < 2 || argv[0] != "kubectl" || argv[1] == "ko" {
		return argv, false
	}
	return append([]string{"kubectl", "--context", context}, argv[1:]...), true
}
