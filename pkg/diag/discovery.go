package diag

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// Flavor is the OVN deployment the diagnostic pods belong to.
type Flavor int

const (
	// FlavorKubeOVN: central ovn-central, kube-ovn-cni and ovs-ovn pods.
	FlavorKubeOVN Flavor = iota
	// FlavorOVNK: ovn-kubernetes interconnect, one ovnkube-node pod (and zone) per node.
	FlavorOVNK
)

func (f Flavor) String() string {
	if f == FlavorOVNK {
		return "ovn-kubernetes"
	}
	return "Kube-OVN"
}

// PodLocator discovers diagnostic target pods in the cluster: ovn-central,
// kube-ovn-cni and ovs-ovn for Kube-OVN, ovnkube-node for ovn-kubernetes. The
// flavor (and, unless given, the namespace) is detected on first use.
type PodLocator struct {
	client    kubernetes.Interface
	namespace string
	auto      bool   // namespace not given: search kube-system, then OVNKNamespaces
	home      string // ovn-kubernetes zone (node) the graph comes from, if known

	mu       sync.Mutex
	detected bool
	flavor   Flavor
}

// NewPodLocator creates a locator for the namespace of the OVN pods. An empty
// namespace means detect: kube-system for Kube-OVN, else the ovn-kubernetes one.
func NewPodLocator(client kubernetes.Interface, namespace string) *PodLocator {
	l := &PodLocator{client: client, namespace: namespace}
	if namespace == "" {
		l.namespace, l.auto = "kube-system", true
	}
	return l
}

// ns is the namespace of the OVN pods, as detected so far.
func (l *PodLocator) ns() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.namespace
}

// SetHomeZone sets the ovn-kubernetes zone the graph was read from (--zone). It
// is the trace zone for targets that have no node, such as distributed router ports.
func (l *PodLocator) SetHomeZone(node string) { l.home = node }

// Detect returns the flavor and namespace of the OVN pods. A namespace with
// ovn-central pods is Kube-OVN, one with ovnkube-node pods is ovn-kubernetes.
// The answer is cached once found; with nothing found it stays Kube-OVN in the
// given (or default) namespace and is retried on the next call.
func (l *PodLocator) Detect(ctx context.Context) (Flavor, string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.detected {
		return l.flavor, l.namespace
	}
	candidates := []string{l.namespace}
	if l.auto {
		candidates = append(candidates, OVNKNamespaces...)
	}
	for _, ns := range candidates {
		for _, c := range []struct {
			selector string
			flavor   Flavor
		}{{"app=ovn-central", FlavorKubeOVN}, {"app=ovnkube-node", FlavorOVNK}} {
			pods, err := l.client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: c.selector, Limit: 1})
			if err == nil && len(pods.Items) > 0 {
				l.detected, l.flavor, l.namespace = true, c.flavor, ns
				return l.flavor, l.namespace
			}
		}
	}
	return FlavorKubeOVN, l.namespace
}

// FindProbePod returns a pod to check pods/exec on: ovn-central for Kube-OVN, any
// running ovnkube-node (sbdb) for ovn-kubernetes.
func (l *PodLocator) FindProbePod(ctx context.Context) (PodRef, Flavor, error) {
	flavor, ns := l.Detect(ctx)
	if flavor != FlavorOVNK {
		pod, err := l.FindOvnCentralPod(ctx)
		return pod, flavor, err
	}
	pods, err := l.client.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: "app=ovnkube-node"})
	if err != nil {
		return PodRef{}, flavor, fmt.Errorf("failed to list ovnkube-node pods in %s: %w", ns, err)
	}
	for _, p := range pods.Items {
		if p.Status.Phase == corev1.PodRunning {
			return PodRef{Namespace: ns, Name: p.Name, Container: SB.container()}, flavor, nil
		}
	}
	return PodRef{}, flavor, fmt.Errorf("no running ovnkube-node pod in %s", ns)
}

// FindTracePod returns the pod ovn-trace runs in. Kube-OVN: ovn-central (the SB
// is central, node is ignored). ovn-kubernetes: the sbdb of node's zone, where
// the port's logical flows are; with no node, the home zone. Never another zone.
func (l *PodLocator) FindTracePod(ctx context.Context, node string) (PodRef, error) {
	if flavor, _ := l.Detect(ctx); flavor != FlavorOVNK {
		return l.FindOvnCentralPod(ctx)
	}
	if node == "" {
		node = l.home
	}
	if node == "" {
		return PodRef{}, errors.New("source port is not bound to a node: its ovn-kubernetes zone is unknown (use --zone)")
	}
	return l.FindOVNKZonePod(ctx, node, SB)
}

// FindCapturePod returns the host-network pod tcpdump runs in on node:
// kube-ovn-cni for Kube-OVN, ovnkube-node (ovn-controller) for ovn-kubernetes.
func (l *PodLocator) FindCapturePod(ctx context.Context, node string) (PodRef, error) {
	if flavor, _ := l.Detect(ctx); flavor == FlavorOVNK {
		return l.findOVNKNodePod(ctx, node, ovnkControllerContainer)
	}
	return l.FindNodeCNIPod(ctx, node)
}

// ControllerLog returns the last lines of ovn-controller's log on pod (from
// FindNodeOVSPod). Kube-OVN writes it to a file, read with tail over exec;
// ovn-kubernetes logs to stdout, read from the Pod's log subresource.
func (l *PodLocator) ControllerLog(ctx context.Context, exec Executor, pod PodRef, lines int) ([]byte, error) {
	if flavor, _ := l.Detect(ctx); flavor == FlavorOVNK {
		n := int64(lines)
		out, err := l.client.CoreV1().Pods(pod.Namespace).GetLogs(pod.Name,
			&corev1.PodLogOptions{Container: pod.Container, TailLines: &n}).DoRaw(ctx)
		if err != nil {
			return nil, fmt.Errorf("logs of %s: %w", pod, err)
		}
		return out, nil
	}
	stdout, stderr, err := exec.Exec(ctx, pod, BuildOvnControllerLogTail(lines))
	if err != nil {
		return nil, fmt.Errorf("%s", firstNonEmpty(strings.TrimSpace(string(stderr)), err.Error()))
	}
	return stdout, nil
}

// FindOvnCentralPod finds an active/running ovn-central pod, prioritizing the active Southbound
// Raft leader (labeled ovn-sb-leader=true by Kube-OVN) to prevent clustered Raft follower connection rejections.
func (l *PodLocator) FindOvnCentralPod(ctx context.Context) (PodRef, error) {
	// 1. Try to find the active Southbound Raft leader pod
	leaderPods, err := l.client.CoreV1().Pods(l.ns()).List(ctx, metav1.ListOptions{
		LabelSelector: "app=ovn-central,ovn-sb-leader=true",
	})
	if err == nil && len(leaderPods.Items) > 0 {
		for _, p := range leaderPods.Items {
			if p.Status.Phase == corev1.PodRunning {
				return PodRef{
					Namespace: l.ns(),
					Name:      p.Name,
					Container: "ovn-central",
				}, nil
			}
		}
	}

	// 2. Fall back to any running ovn-central pod (for single-node or non-HA setups)
	pods, err := l.client.CoreV1().Pods(l.ns()).List(ctx, metav1.ListOptions{
		LabelSelector: "app=ovn-central",
	})
	if err != nil {
		return PodRef{}, fmt.Errorf("failed to list ovn-central pods: %w", err)
	}

	if len(pods.Items) == 0 {
		// Fallback: search by prefix
		allPods, err := l.client.CoreV1().Pods(l.ns()).List(ctx, metav1.ListOptions{})
		if err != nil {
			return PodRef{}, fmt.Errorf("failed to list pods in namespace %s: %w", l.ns(), err)
		}
		for _, p := range allPods.Items {
			if strings.HasPrefix(p.Name, "ovn-central") && p.Status.Phase == corev1.PodRunning {
				return PodRef{Namespace: l.ns(), Name: p.Name, Container: "ovn-central"}, nil
			}
		}
		return PodRef{}, fmt.Errorf("no running ovn-central pod found in namespace %s", l.ns())
	}

	for _, p := range pods.Items {
		if p.Status.Phase == corev1.PodRunning {
			return PodRef{
				Namespace: l.ns(),
				Name:      p.Name,
				Container: "ovn-central",
			}, nil
		}
	}

	return PodRef{}, fmt.Errorf("no running ovn-central pod found (found %d non-running)", len(pods.Items))
}

// FindNodeCNIPod finds the kube-ovn-cni pod running on the specified node.
func (l *PodLocator) FindNodeCNIPod(ctx context.Context, nodeName string) (PodRef, error) {
	pods, err := l.client.CoreV1().Pods(l.ns()).List(ctx, metav1.ListOptions{
		LabelSelector: "app=kube-ovn-cni",
		FieldSelector: fmt.Sprintf("spec.nodeName=%s", nodeName),
	})
	if err != nil {
		return PodRef{}, fmt.Errorf("failed to list kube-ovn-cni pod on node %s: %w", nodeName, err)
	}

	if len(pods.Items) == 0 {
		// Try listing by label without field selector (in case fieldSelector isn't supported)
		allCNIs, err := l.client.CoreV1().Pods(l.ns()).List(ctx, metav1.ListOptions{
			LabelSelector: "app=kube-ovn-cni",
		})
		if err == nil {
			for _, p := range allCNIs.Items {
				if p.Spec.NodeName == nodeName && p.Status.Phase == corev1.PodRunning {
					return PodRef{Namespace: l.ns(), Name: p.Name, Container: "cni-server"}, nil
				}
			}
		}
		return PodRef{}, fmt.Errorf("no kube-ovn-cni pod found running on node %s", nodeName)
	}

	for _, p := range pods.Items {
		// Re-check the node: never exec on a pod of another node, even if the
		// field selector was not honoured.
		if p.Spec.NodeName == nodeName && p.Status.Phase == corev1.PodRunning {
			return PodRef{
				Namespace: l.ns(),
				Name:      p.Name,
				Container: "cni-server",
			}, nil
		}
	}

	return PodRef{}, fmt.Errorf("kube-ovn-cni pod on node %s is not running", nodeName)
}

// FindNodeOVSPod finds the ovs-ovn or ovs pod running on the specified node (or falls back to kube-ovn-cni).
func (l *PodLocator) FindNodeOVSPod(ctx context.Context, nodeName string) (PodRef, error) {
	if flavor, _ := l.Detect(ctx); flavor == FlavorOVNK {
		// ovn-controller runs as root with the host's OVS db.sock and all OVS tools.
		return l.findOVNKNodePod(ctx, nodeName, ovnkControllerContainer)
	}
	selectors := []string{"app=ovs", "app=ovs-ovn"}
	for _, sel := range selectors {
		pods, err := l.client.CoreV1().Pods(l.ns()).List(ctx, metav1.ListOptions{
			LabelSelector: sel,
			FieldSelector: fmt.Sprintf("spec.nodeName=%s", nodeName),
		})
		if err == nil && len(pods.Items) > 0 {
			for _, p := range pods.Items {
				if p.Spec.NodeName == nodeName && p.Status.Phase == corev1.PodRunning {
					container := "openvswitch"
					for _, c := range p.Spec.Containers {
						if c.Name == "openvswitch" || c.Name == "ovs-daemons" {
							container = c.Name
							break
						}
					}
					return PodRef{
						Namespace: l.ns(),
						Name:      p.Name,
						Container: container,
					}, nil
				}
			}
		}

		// Also check by label without field selector
		allPods, err := l.client.CoreV1().Pods(l.ns()).List(ctx, metav1.ListOptions{
			LabelSelector: sel,
		})
		if err == nil {
			for _, p := range allPods.Items {
				if p.Spec.NodeName == nodeName && p.Status.Phase == corev1.PodRunning {
					container := "openvswitch"
					for _, c := range p.Spec.Containers {
						if c.Name == "openvswitch" || c.Name == "ovs-daemons" {
							container = c.Name
							break
						}
					}
					return PodRef{
						Namespace: l.ns(),
						Name:      p.Name,
						Container: container,
					}, nil
				}
			}
		}
	}

	// Fallback to kube-ovn-cni on that node if dedicated ovs pod is absent
	return l.FindNodeCNIPod(ctx, nodeName)
}

// ErrIfaceNotFound reports that no OVS interface on the node carries the port's iface-id.
var ErrIfaceNotFound = errors.New("no OVS interface with this iface-id")

// FindOVSInterface returns the OVS interface bound to an OVN logical port on the given node
// (the node of its Port_Binding chassis). It never guesses: an unknown node, an unreachable
// OVS pod or a port with no interface on that node is an error, so callers cannot end up
// capturing on an unrelated interface.
func (l *PodLocator) FindOVSInterface(ctx context.Context, exec Executor, node string, portName string) (string, error) {
	if node == "" {
		return "", fmt.Errorf("port %s: node unknown (port not bound to a chassis)", portName)
	}
	cmd, err := BuildFindIfaceByIfaceIDCommand(portName)
	if err != nil {
		return "", err
	}
	ovsPod, err := l.FindNodeOVSPod(ctx, node)
	if err != nil {
		return "", err
	}
	stdout, stderr, err := exec.Exec(ctx, ovsPod, cmd)
	if err != nil {
		return "", fmt.Errorf("iface-id lookup for %s on %s failed: %s", portName, node, firstNonEmpty(strings.TrimSpace(string(stderr)), err.Error()))
	}
	for _, line := range strings.Split(strings.TrimSpace(string(stdout)), "\n") {
		if iface := strings.TrimSpace(line); iface != "" {
			return iface, nil
		}
	}
	return "", fmt.Errorf("port %s on node %s: %w", portName, node, ErrIfaceNotFound)
}

// FindBridgeUplink finds the physical/system uplink interface attached to an OVS bridge
// (e.g. enp2s0 on br-external) on the given node. If no physical uplink is found, it falls
// back to the bridge name itself.
func (l *PodLocator) FindBridgeUplink(ctx context.Context, exec Executor, node string, bridge string) (string, error) {
	if node == "" || bridge == "" {
		return bridge, nil
	}
	ovsPod, err := l.FindNodeOVSPod(ctx, node)
	if err != nil {
		return bridge, nil
	}

	cmd, err := BuildListBridgePortsCommand(bridge)
	if err != nil {
		return bridge, nil
	}

	stdout, _, err := exec.Exec(ctx, ovsPod, cmd)
	if err != nil {
		return bridge, nil
	}

	var ports []string
	for _, line := range strings.Split(strings.TrimSpace(string(stdout)), "\n") {
		p := strings.TrimSpace(line)
		if p != "" {
			ports = append(ports, p)
		}
	}
	if len(ports) == 0 {
		return bridge, nil
	}

	typeCmd, err := BuildListInterfacesTypeCommand(ports...)
	if err != nil {
		return bridge, nil
	}

	stdout, _, err = exec.Exec(ctx, ovsPod, typeCmd)
	if err != nil {
		return bridge, nil
	}

	var resp struct {
		Data [][]string `json:"data"`
	}
	if err := json.Unmarshal(stdout, &resp); err == nil {
		for _, row := range resp.Data {
			if len(row) >= 2 {
				name, ifaceType := row[0], row[1]
				// "" or "system" represents the real physical/system interface
				if ifaceType == "" || ifaceType == "system" {
					return name, nil
				}
			}
		}
	}

	return bridge, nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
