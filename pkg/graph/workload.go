package graph

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// WorkloadInfo represents enriched Kubernetes workload identity for an LSP.
type WorkloadInfo struct {
	Kind              string // "VirtualMachine", "Pod", "Deployment", "DaemonSet", "StatefulSet"
	WorkloadName      string
	PodName           string
	Namespace         string
	Owner             string // e.g. "VirtualMachine/vm-onewater", "Deployment/coredns"
	VMI               string // VirtualMachineInstance name for KubeVirt
	NetworkAttachment string // NetworkAttachmentDefinition for multi-NIC interfaces
	Node              string
}

// WorkloadResolver resolves Pod and KubeVirt VM metadata.
type WorkloadResolver interface {
	Resolve(namespace, podOrVM string) *WorkloadInfo
	Sync(ctx context.Context) error
}

// StaticWorkloadResolver is a mock implementation for tests and offline fixtures.
type StaticWorkloadResolver struct {
	mu        sync.RWMutex
	workloads map[string]*WorkloadInfo // Keyed by "namespace/name" or "name"
}

// NewStaticWorkloadResolver creates an empty StaticWorkloadResolver.
func NewStaticWorkloadResolver() *StaticWorkloadResolver {
	return &StaticWorkloadResolver{
		workloads: make(map[string]*WorkloadInfo),
	}
}

// Add registers a static workload entry.
func (r *StaticWorkloadResolver) Add(info WorkloadInfo) {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := fmt.Sprintf("%s/%s", info.Namespace, info.PodName)
	r.workloads[key] = &info
	r.workloads[info.PodName] = &info
	if info.WorkloadName != "" {
		vmKey := fmt.Sprintf("%s/%s", info.Namespace, info.WorkloadName)
		r.workloads[vmKey] = &info
		r.workloads[info.WorkloadName] = &info
	}
}

// Resolve looks up workload info.
func (r *StaticWorkloadResolver) Resolve(namespace, podOrVM string) *WorkloadInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	key := fmt.Sprintf("%s/%s", namespace, podOrVM)
	if info, ok := r.workloads[key]; ok {
		return info
	}
	if info, ok := r.workloads[podOrVM]; ok {
		return info
	}
	return nil
}

// Sync is a no-op for StaticWorkloadResolver.
func (r *StaticWorkloadResolver) Sync(ctx context.Context) error {
	return nil
}

// KubeWorkloadResolver reads live Pods from the Kubernetes API to resolve workload identities.
type KubeWorkloadResolver struct {
	client kubernetes.Interface
	logger *slog.Logger

	mu        sync.RWMutex
	workloads map[string]*WorkloadInfo
}

// NewKubeWorkloadResolver creates a new KubeWorkloadResolver.
func NewKubeWorkloadResolver(client kubernetes.Interface, logger *slog.Logger) *KubeWorkloadResolver {
	if logger == nil {
		logger = slog.Default()
	}
	return &KubeWorkloadResolver{
		client:    client,
		logger:    logger,
		workloads: make(map[string]*WorkloadInfo),
	}
}

// Resolve looks up a pod or VM by namespace and name.
func (r *KubeWorkloadResolver) Resolve(namespace, podOrVM string) *WorkloadInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	key := fmt.Sprintf("%s/%s", namespace, podOrVM)
	if info, ok := r.workloads[key]; ok {
		return info
	}
	if info, ok := r.workloads[podOrVM]; ok {
		return info
	}
	return nil
}

// Sync scans all Kubernetes pods and builds the workload identity index.
func (r *KubeWorkloadResolver) Sync(ctx context.Context) error {
	if r.client == nil {
		return nil
	}

	pods, err := r.client.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return fmt.Errorf("failed to list pods for workload resolution: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	r.workloads = make(map[string]*WorkloadInfo, len(pods.Items)*2)

	for i := range pods.Items {
		pod := &pods.Items[i]
		info := extractWorkloadFromPod(pod)

		key := fmt.Sprintf("%s/%s", info.Namespace, info.PodName)
		r.workloads[key] = info
		r.workloads[info.PodName] = info
		if info.WorkloadName != "" {
			vmKey := fmt.Sprintf("%s/%s", info.Namespace, info.WorkloadName)
			r.workloads[vmKey] = info
			r.workloads[info.WorkloadName] = info
		}
	}

	return nil
}

func extractWorkloadFromPod(pod *corev1.Pod) *WorkloadInfo {
	info := &WorkloadInfo{
		PodName:   pod.Name,
		Namespace: pod.Namespace,
		Node:      pod.Spec.NodeName,
	}

	// 1. Check for KubeVirt VM
	if pod.Labels["kubevirt.io"] == "virt-launcher" || pod.Labels["vm.kubevirt.io/name"] != "" {
		info.Kind = "VirtualMachine"
		vmName := pod.Labels["vm.kubevirt.io/name"]
		if vmName == "" {
			vmName = pod.Labels["harvesterhci.io/vmName"]
		}
		if vmName == "" {
			vmName = strings.TrimPrefix(pod.Name, "virt-launcher-")
			if idx := strings.LastIndex(vmName, "-"); idx > 0 {
				vmName = vmName[:idx] // strip random suffix
			}
		}
		info.WorkloadName = vmName
		info.VMI = pod.Labels["vmi.kubevirt.io/id"]
		if info.VMI == "" {
			info.VMI = vmName
		}
		info.Owner = fmt.Sprintf("VirtualMachine/%s", vmName)
		return info
	}

	// 2. Check OwnerReferences
	for _, owner := range pod.OwnerReferences {
		switch owner.Kind {
		case "VirtualMachineInstance":
			info.Kind = "VirtualMachine"
			info.WorkloadName = owner.Name
			info.VMI = owner.Name
			info.Owner = fmt.Sprintf("VirtualMachine/%s", owner.Name)
			return info
		case "ReplicaSet":
			// Strip template hash to get Deployment name: e.g. coredns-5596665f84 -> coredns
			depName := owner.Name
			if idx := strings.LastIndex(owner.Name, "-"); idx > 0 {
				depName = owner.Name[:idx]
			}
			info.Kind = "Deployment"
			info.WorkloadName = depName
			info.Owner = fmt.Sprintf("Deployment/%s", depName)
			return info
		case "DaemonSet", "StatefulSet", "Job":
			info.Kind = owner.Kind
			info.WorkloadName = owner.Name
			info.Owner = fmt.Sprintf("%s/%s", owner.Kind, owner.Name)
			return info
		}
	}

	// 3. Fallback: standalone Pod
	info.Kind = "Pod"
	info.WorkloadName = pod.Name
	info.Owner = fmt.Sprintf("Pod/%s", pod.Name)

	// Check multi-NIC NetworkAttachmentDefinition
	if netStatus, ok := pod.Annotations["k8s.v1.cni.cncf.io/network-status"]; ok && netStatus != "" {
		info.NetworkAttachment = netStatus
	}

	return info
}
