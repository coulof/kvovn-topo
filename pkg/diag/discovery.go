package diag

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// PodLocator discovers diagnostic target pods (ovn-central, kube-ovn-cni, ovs-ovn) in the cluster.
type PodLocator struct {
	client    kubernetes.Interface
	namespace string
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

// FindOvnCentralPod finds an active/running ovn-central pod.
func (l *PodLocator) FindOvnCentralPod(ctx context.Context) (PodRef, error) {
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

// FindNodeOVSPod finds the ovs-ovn pod running on the specified node (or falls back to kube-ovn-cni).
func (l *PodLocator) FindNodeOVSPod(ctx context.Context, nodeName string) (PodRef, error) {
	pods, err := l.client.CoreV1().Pods(l.namespace).List(ctx, metav1.ListOptions{
		LabelSelector: "app=ovs-ovn",
		FieldSelector: fmt.Sprintf("spec.nodeName=%s", nodeName),
	})
	if err == nil && len(pods.Items) > 0 {
		for _, p := range pods.Items {
			if p.Status.Phase == corev1.PodRunning {
				container := "openvswitch"
				for _, c := range p.Spec.Containers {
					if c.Name == "ovs-daemons" || c.Name == "openvswitch" {
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

	// Fallback to kube-ovn-cni on that node if ovs-ovn is absent
	return l.FindNodeCNIPod(ctx, nodeName)
}
