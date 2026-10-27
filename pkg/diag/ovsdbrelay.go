package diag

// OVSDB relay over exec, for ovn-kubernetes interconnect mode (ADR-0005). Each
// node's NB/SB listens only on a unix socket inside its ovnkube-node pod, so the
// TUI reaches it through a pods/exec stream to socat. Tier 1: TUI only, --diag.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// OVSDB names one of the two OVN databases of a zone.
type OVSDB int

const (
	NB OVSDB = iota
	SB
)

func (d OVSDB) String() string {
	if d == SB {
		return "sb"
	}
	return "nb"
}

// container is the ovnkube-node container serving the database.
func (d OVSDB) container() string {
	if d == SB {
		return "sbdb"
	}
	return "nbdb"
}

// OVNKNamespaces are the namespaces of ovnkube-node, OpenShift first.
var OVNKNamespaces = []string{"openshift-ovn-kubernetes", "ovn-kubernetes"}

// BuildOVSDBRelayCommand returns the allowlisted relay command: socat between the
// exec stream and the database's fixed unix socket. The path is never built from input.
func BuildOVSDBRelayCommand(db OVSDB) []string {
	sock := "/var/run/ovn/ovnnb_db.sock"
	if db == SB {
		sock = "/var/run/ovn/ovnsb_db.sock"
	}
	return []string{"socat", "STDIO", "UNIX-CONNECT:" + sock}
}

// FindOVNKZonePod finds the running ovnkube-node pod of a node and targets the
// container serving db.
func (l *PodLocator) FindOVNKZonePod(ctx context.Context, nodeName string, db OVSDB) (PodRef, error) {
	return l.findOVNKNodePod(ctx, nodeName, db.container())
}

// ovnkControllerContainer is the ovnkube-node container running ovn-controller,
// with the host's OVS socket: OVS commands and tcpdump (host network) run there.
const ovnkControllerContainer = "ovn-controller"

// findOVNKNodePod finds the running ovnkube-node pod of a node and targets container.
func (l *PodLocator) findOVNKNodePod(ctx context.Context, nodeName, container string) (PodRef, error) {
	if nodeName == "" {
		return PodRef{}, errors.New("no node given for the ovnkube-node pod")
	}
	pods, err := l.client.CoreV1().Pods(l.ns()).List(ctx, metav1.ListOptions{
		LabelSelector: "app=ovnkube-node",
		FieldSelector: fmt.Sprintf("spec.nodeName=%s", nodeName),
	})
	if err != nil {
		return PodRef{}, fmt.Errorf("failed to list ovnkube-node pod on node %s: %w", nodeName, err)
	}
	for _, p := range pods.Items {
		// Re-check the node: never exec on a pod of another node, even if the
		// field selector was not honoured.
		if p.Spec.NodeName == nodeName && p.Status.Phase == corev1.PodRunning {
			return PodRef{Namespace: l.ns(), Name: p.Name, Container: container}, nil
		}
	}
	return PodRef{}, fmt.Errorf("no running ovnkube-node pod on node %s in %s", nodeName, l.ns())
}

// ListOVNKZones returns the nodes that run an ovnkube-node pod, one zone each.
func (l *PodLocator) ListOVNKZones(ctx context.Context) ([]string, error) {
	pods, err := l.client.CoreV1().Pods(l.ns()).List(ctx, metav1.ListOptions{LabelSelector: "app=ovnkube-node"})
	if err != nil {
		return nil, fmt.Errorf("failed to list ovnkube-node pods in %s: %w", l.ns(), err)
	}
	var zones []string
	for _, p := range pods.Items {
		if p.Spec.NodeName != "" && p.Status.Phase == corev1.PodRunning {
			zones = append(zones, p.Spec.NodeName)
		}
	}
	return zones, nil
}

// StartOVSDBRelay listens on 127.0.0.1 and relays every accepted connection to
// db through its own exec stream, so an OVSDB client (libovsdb monitors, with its
// reconnect) can use "tcp:<addr>". The listener closes when ctx is done.
func StartOVSDBRelay(ctx context.Context, exec Executor, pod PodRef, db OVSDB, logger *slog.Logger) (string, error) {
	if logger == nil {
		logger = slog.Default()
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("ovsdb relay listen: %w", err)
	}
	cmd := BuildOVSDBRelayCommand(db)
	go func() {
		<-ctx.Done()
		_ = ln.Close()
	}()
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				if !errors.Is(err, net.ErrClosed) {
					logger.Warn("ovsdb relay accept", "db", db, "error", err)
				}
				return
			}
			go relayConn(ctx, exec, pod, cmd, conn, db, logger)
		}
	}()
	return ln.Addr().String(), nil
}

func relayConn(ctx context.Context, exec Executor, pod PodRef, cmd []string, conn net.Conn, db OVSDB, logger *slog.Logger) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var once sync.Once
	closeConn := func() { once.Do(func() { _ = conn.Close() }) }
	defer closeConn()
	go func() {
		<-ctx.Done()
		closeConn()
	}()
	logger.Debug("ovsdb relay stream open", "db", db, "pod", pod)
	if err := exec.Stream(ctx, pod, cmd, conn, conn, io.Discard); err != nil && ctx.Err() == nil {
		logger.Warn("ovsdb relay stream ended", "db", db, "pod", pod, "error", err)
	}
}
