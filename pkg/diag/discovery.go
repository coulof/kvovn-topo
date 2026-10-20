package diag

import (
	"context"
	"fmt"
	"strings"
	"sync"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// PodLocator discovers diagnostic target pods (ovn-central, kube-ovn-cni, ovs-ovn) in the cluster.
type PodLocator struct {
	client    kubernetes.Interface
	namespace string
	uuidMap   map[string]string
	mu        sync.Mutex
}

// NewPodLocator creates a locator targeting the specified namespace (typically "kube-system").
func NewPodLocator(client kubernetes.Interface, namespace string) *PodLocator {
	if namespace == "" {
		namespace = "kube-system"
	}
	return &PodLocator{
		client:    client,
		namespace: namespace,
	}
}

// FindOvnCentralPod finds an active/running ovn-central pod, prioritizing the active Southbound
// Raft leader (labeled ovn-sb-leader=true by Kube-OVN) to prevent clustered Raft follower connection rejections.
func (l *PodLocator) FindOvnCentralPod(ctx context.Context) (PodRef, error) {
	// 1. Try to find the active Southbound Raft leader pod
	leaderPods, err := l.client.CoreV1().Pods(l.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=ovn-central,ovn-sb-leader=true",
	})
	if err == nil && len(leaderPods.Items) > 0 {
		for _, p := range leaderPods.Items {
			if p.Status.Phase == corev1.PodRunning {
				return PodRef{
					Namespace: l.namespace,
					Name:      p.Name,
					Container: "ovn-central",
				}, nil
			}
		}
	}

	// 2. Fall back to any running ovn-central pod (for single-node or non-HA setups)
	pods, err := l.client.CoreV1().Pods(l.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=ovn-central",
	})
	if err != nil {
		return PodRef{}, fmt.Errorf("failed to list ovn-central pods: %w", err)
	}

	if len(pods.Items) == 0 {
		// Fallback: search by prefix
		allPods, err := l.client.CoreV1().Pods(l.namespace).List(ctx, metav1.ListOptions{})
		if err != nil {
			return PodRef{}, fmt.Errorf("failed to list pods in namespace %s: %w", l.namespace, err)
		}
		for _, p := range allPods.Items {
			if strings.HasPrefix(p.Name, "ovn-central") && p.Status.Phase == corev1.PodRunning {
				return PodRef{Namespace: l.namespace, Name: p.Name, Container: "ovn-central"}, nil
			}
		}
		return PodRef{}, fmt.Errorf("no running ovn-central pod found in namespace %s", l.namespace)
	}

	for _, p := range pods.Items {
		if p.Status.Phase == corev1.PodRunning {
			return PodRef{
				Namespace: l.namespace,
				Name:      p.Name,
				Container: "ovn-central",
			}, nil
		}
	}

	return PodRef{}, fmt.Errorf("no running ovn-central pod found (found %d non-running)", len(pods.Items))
}

// FindNodeCNIPod finds the kube-ovn-cni pod running on the specified node.
func (l *PodLocator) FindNodeCNIPod(ctx context.Context, nodeName string) (PodRef, error) {
	nodeName = l.resolveKubeNodeName(ctx, nodeName)
	pods, err := l.client.CoreV1().Pods(l.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=kube-ovn-cni",
		FieldSelector: fmt.Sprintf("spec.nodeName=%s", nodeName),
	})
	if err != nil {
		return PodRef{}, fmt.Errorf("failed to list kube-ovn-cni pod on node %s: %w", nodeName, err)
	}

	if len(pods.Items) == 0 {
		// Try listing by label without field selector (in case fieldSelector isn't supported)
		allCNIs, err := l.client.CoreV1().Pods(l.namespace).List(ctx, metav1.ListOptions{
			LabelSelector: "app=kube-ovn-cni",
		})
		if err == nil {
			for _, p := range allCNIs.Items {
				if p.Spec.NodeName == nodeName && p.Status.Phase == corev1.PodRunning {
					return PodRef{Namespace: l.namespace, Name: p.Name, Container: "cni-server"}, nil
				}
			}
		}
		return PodRef{}, fmt.Errorf("no kube-ovn-cni pod found running on node %s", nodeName)
	}

	for _, p := range pods.Items {
		if p.Status.Phase == corev1.PodRunning {
			return PodRef{
				Namespace: l.namespace,
				Name:      p.Name,
				Container: "cni-server",
			}, nil
		}
	}

	return PodRef{}, fmt.Errorf("kube-ovn-cni pod on node %s is not running", nodeName)
}

// FindNodeOVSPod finds the ovs-ovn or ovs pod running on the specified node (or falls back to kube-ovn-cni).
func (l *PodLocator) FindNodeOVSPod(ctx context.Context, nodeName string) (PodRef, error) {
	nodeName = l.resolveKubeNodeName(ctx, nodeName)
	selectors := []string{"app=ovs", "app=ovs-ovn"}
	for _, sel := range selectors {
		pods, err := l.client.CoreV1().Pods(l.namespace).List(ctx, metav1.ListOptions{
			LabelSelector: sel,
			FieldSelector: fmt.Sprintf("spec.nodeName=%s", nodeName),
		})
		if err == nil && len(pods.Items) > 0 {
			for _, p := range pods.Items {
				if p.Status.Phase == corev1.PodRunning {
					container := "openvswitch"
					for _, c := range p.Spec.Containers {
						if c.Name == "openvswitch" || c.Name == "ovs-daemons" {
							container = c.Name
							break
						}
					}
					return PodRef{
						Namespace: l.namespace,
						Name:      p.Name,
						Container: container,
					}, nil
				}
			}
		}

		// Also check by label without field selector
		allPods, err := l.client.CoreV1().Pods(l.namespace).List(ctx, metav1.ListOptions{
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
						Namespace: l.namespace,
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

// FindOVSInterface locates which cluster node hosts the dedicated veth interface for an OVN port,
// and returns the node name and interface name (e.g. "4e3b7_37a8eec_h" or "ovn0").
// If the port has no dedicated veth (e.g. a pure logical router port), it falls back to an active UP
// interface: "br-external" for external gateways, or "ovn0" for internal/node traffic.
func (l *PodLocator) FindOVSInterface(ctx context.Context, exec Executor, hintNode string, portName string) (string, string, error) {
	if portName == "" {
		if hintNode == "" {
			hintNode = l.getFirstNodeName(ctx)
		}
		return hintNode, "ovn0", nil
	}

	// 1. If hintNode is provided, check it first
	if hintNode != "" {
		ovsPod, err := l.FindNodeOVSPod(ctx, hintNode)
		if err == nil {
			iface := l.queryInterfaceOnPod(ctx, exec, ovsPod, portName)
			if iface != "" {
				return hintNode, iface, nil
			}
		}
	}

	// 2. Query other cluster nodes to find where this port is attached
	nodes, err := l.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, n := range nodes.Items {
			if n.Name == hintNode {
				continue
			}
			ovsPod, err := l.FindNodeOVSPod(ctx, n.Name)
			if err != nil {
				continue
			}
			iface := l.queryInterfaceOnPod(ctx, exec, ovsPod, portName)
			if iface != "" {
				return n.Name, iface, nil
			}
		}
	}

	// Fallback to active UP interface on hintNode (never br-int which is administratively DOWN)
	if hintNode == "" {
		hintNode = l.getFirstNodeName(ctx)
	}

	lowerPort := strings.ToLower(portName)
	if strings.Contains(lowerPort, "ext") || strings.Contains(lowerPort, "external") || strings.Contains(lowerPort, "gw") {
		return hintNode, "br-external", nil
	}

	return hintNode, "ovn0", nil
}

func (l *PodLocator) queryInterfaceOnPod(ctx context.Context, exec Executor, ovsPod PodRef, portName string) string {
	cmd := []string{"ovs-vsctl", "--bare", "--columns=name", "find", "interface", fmt.Sprintf("external_ids:iface-id=%s", portName)}
	stdout, _, err := exec.Exec(ctx, ovsPod, cmd)
	if err == nil {
		lines := strings.Split(strings.TrimSpace(string(stdout)), "\n")
		for _, line := range lines {
			iface := strings.TrimSpace(line)
			if iface != "" {
				return iface
			}
		}
	}
	return ""
}

func (l *PodLocator) getFirstNodeName(ctx context.Context) string {
	nodes, err := l.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{Limit: 1})
	if err == nil && len(nodes.Items) > 0 {
		return nodes.Items[0].Name
	}
	return "cluster-node"
}

func (l *PodLocator) PopulateChassisUUIDs(ctx context.Context, exec Executor) {
	if exec == nil {
		return
	}
	pod, err := l.FindOvnCentralPod(ctx)
	if err != nil {
		return
	}
	stdout, _, err := exec.Exec(ctx, pod, []string{"ovn-sbctl", "--bare", "--columns=_uuid,hostname", "list", "Chassis"})
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.uuidMap == nil {
		l.uuidMap = make(map[string]string)
	}
	lines := strings.Split(string(stdout), "\n")
	for i := 0; i+1 < len(lines); i += 2 {
		uuid := strings.TrimSpace(lines[i])
		host := strings.TrimSpace(lines[i+1])
		if uuid != "" && host != "" {
			l.uuidMap[uuid] = host
		}
	}
}

func (l *PodLocator) ResolveChassisUUID(uuid string) string {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.uuidMap != nil {
		if host, ok := l.uuidMap[uuid]; ok && host != "" {
			return host
		}
	}
	return ""
}

func (l *PodLocator) resolveKubeNodeName(ctx context.Context, candidate string) string {
	if candidate == "" {
		return candidate
	}
	if host := l.ResolveChassisUUID(candidate); host != "" {
		return host
	}
	// Check if candidate is already a valid node name
	if n, err := l.client.CoreV1().Nodes().Get(ctx, candidate, metav1.GetOptions{}); err == nil && n != nil {
		return n.Name
	}

	// Try resolving via annotations or hostname labels
	nodes, err := l.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err == nil {
		for _, n := range nodes.Items {
			if n.Name == candidate || n.Labels["kubernetes.io/hostname"] == candidate {
				return n.Name
			}
			if n.Annotations["ovn.kubernetes.io/chassis"] == candidate ||
				n.Annotations["k8s.ovn.org/chassis"] == candidate {
				return n.Name
			}
		}
	}
	return candidate
}
