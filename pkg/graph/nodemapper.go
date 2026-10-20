package graph

import (
	"context"
	"log/slog"
	"sync"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const OvnChassisAnnotation = "ovn.kubernetes.io/chassis"

// KubeNodeMapper dynamically maps OVN chassis names to Kubernetes Node names.
type KubeNodeMapper struct {
	client kubernetes.Interface
	logger *slog.Logger

	mu              sync.RWMutex
	chassisToNode   map[string]string
	hostnameToNode  map[string]string
}

// NewKubeNodeMapper creates a new KubeNodeMapper.
func NewKubeNodeMapper(client kubernetes.Interface, logger *slog.Logger) *KubeNodeMapper {
	if logger == nil {
		logger = slog.Default()
	}
	return &KubeNodeMapper{
		client:         client,
		logger:         logger,
		chassisToNode:  make(map[string]string),
		hostnameToNode: make(map[string]string),
	}
}

// NodeForChassis resolves a chassis name and hostname to a Kubernetes node name.
func (m *KubeNodeMapper) NodeForChassis(hostname string, chassisID string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if node, ok := m.chassisToNode[chassisID]; ok && node != "" {
		return node
	}
	if node, ok := m.hostnameToNode[hostname]; ok && node != "" {
		return node
	}
	if hostname != "" {
		return hostname
	}
	return chassisID
}

// Sync reads all Kubernetes nodes and updates the internal mapping table.
func (m *KubeNodeMapper) Sync(ctx context.Context) error {
	if m.client == nil {
		return nil
	}

	nodes, err := m.client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.chassisToNode = make(map[string]string, len(nodes.Items))
	m.hostnameToNode = make(map[string]string, len(nodes.Items))

	for i := range nodes.Items {
		node := &nodes.Items[i]
		m.hostnameToNode[node.Name] = node.Name

		if chassisID, ok := node.Annotations[OvnChassisAnnotation]; ok && chassisID != "" {
			m.chassisToNode[chassisID] = node.Name
		}
		// Also check addresses for hostnames
		for _, addr := range node.Status.Addresses {
			if addr.Type == corev1.NodeHostName && addr.Address != "" {
				m.hostnameToNode[addr.Address] = node.Name
			}
		}
	}

	m.logger.Debug("synchronized kubernetes node chassis mappings", "node_count", len(nodes.Items))
	return nil
}
