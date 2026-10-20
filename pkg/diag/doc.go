// Package diag implements Tier 1 exec-based diagnostics (ADR-0001).
//
// Tier 1 features execute read-only diagnostic commands (such as ovn-trace,
// tcpdump, and conntrack queries) by executing into privileged Kube-OVN
// pods (ovn-central, ovs-ovn, kube-ovn-cni) using the operator's kubeconfig.
//
// Architectural Rules (ADR-0001):
//   - Tier 1 code is strictly for the TUI (cmd/ovn-topo) behind an explicit --diag flag.
//   - The daemon (cmd/ovn-topologyd), graph engine (pkg/graph), and HTTP server (pkg/server)
//     must NEVER import or depend on pkg/diag or k8s.io/client-go/tools/remotecommand.
//   - All executed commands must come from a typed allowlist with sanitized arguments;
//     free-form command strings are strictly forbidden.
package diag
