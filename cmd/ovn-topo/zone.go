package main

// --zone: live view of one ovn-kubernetes interconnect zone (one node's NB/SB),
// reached through pods/exec relays (pkg/diag, Tier 1, ADR-0001/ADR-0005). The
// embedded graph is served on loopback so the TUI runs unchanged, SSE included.

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/coulof/ovn-topology/pkg/diag"
	"github.com/coulof/ovn-topology/pkg/server"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// zoneNamespace returns the ovnkube-node namespace: the one given, else the
// first of diag.OVNKNamespaces that exists.
func zoneNamespace(ctx context.Context, cs kubernetes.Interface, given string) (string, error) {
	if given != "" {
		return given, nil
	}
	for _, ns := range diag.OVNKNamespaces {
		if _, err := cs.CoreV1().Namespaces().Get(ctx, ns, metav1.GetOptions{}); err == nil {
			return ns, nil
		}
	}
	return "", fmt.Errorf("no ovn-kubernetes namespace found (%v); is this an ovn-kubernetes cluster? Set --k8s-namespace", diag.OVNKNamespaces)
}

// zoneClients builds the clientset, executor and locator for the zone relays.
func zoneClients(ctx context.Context, loader clientcmd.ClientConfig, namespace string) (diag.Executor, *diag.PodLocator, error) {
	cfg, err := loader.ClientConfig()
	if err != nil {
		return nil, nil, fmt.Errorf("loading kubeconfig: %w", err)
	}
	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, nil, fmt.Errorf("creating Kubernetes client: %w", err)
	}
	ns, err := zoneNamespace(ctx, cs, namespace)
	if err != nil {
		return nil, nil, err
	}
	return diag.NewKubeExecutor(cs, cfg), diag.NewPodLocator(cs, ns), nil
}

// listZones prints the zones (nodes) --zone accepts.
func listZones(loader clientcmd.ClientConfig, namespace string, out io.Writer) error {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, locator, err := zoneClients(ctx, loader, namespace)
	if err != nil {
		return err
	}
	zones, err := locator.ListOVNKZones(ctx)
	if err != nil {
		return err
	}
	for _, z := range zones {
		_, _ = fmt.Fprintln(out, z)
	}
	return nil
}

// startZone relays the zone's NB and SB, builds the embedded graph on them and
// serves it on 127.0.0.1. It returns the API base URL once the first monitor
// snapshot is in. Everything stops when ctx is done.
func startZone(ctx context.Context, loader clientcmd.ClientConfig, namespace, zone string, progress io.Writer) (string, error) {
	// The TUI owns the terminal: background logs would corrupt it.
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	setup, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	exec, locator, err := zoneClients(setup, loader, namespace)
	if err != nil {
		return "", err
	}
	addrs := map[diag.OVSDB]string{}
	for _, db := range []diag.OVSDB{diag.NB, diag.SB} {
		pod, err := locator.FindOVNKZonePod(setup, zone, db)
		if err != nil {
			return "", err
		}
		if ok, reason, err := diag.CheckExecAccess(setup, exec, pod); !ok {
			if err != nil {
				return "", fmt.Errorf("exec into %s: %w", pod, err)
			}
			return "", fmt.Errorf("%s", reason)
		}
		addr, err := diag.StartOVSDBRelay(ctx, exec, pod, db, logger)
		if err != nil {
			return "", err
		}
		addrs[db] = addr
		_, _ = fmt.Fprintf(progress, "relaying %s of zone %s through %s\n", db, zone, pod)
	}

	store, err := startEmbedded(ctx, loader, "tcp:"+addrs[diag.NB], "tcp:"+addrs[diag.SB], "", "", "", logger)
	if err != nil {
		return "", err
	}
	ln, err := (&net.ListenConfig{}).Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		return "", fmt.Errorf("zone API listen: %w", err)
	}
	srv := &http.Server{Handler: server.NewServer(store, server.VersionConfig{Version: version, APIVersion: "v1"}, logger),
		ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()
	go func() {
		<-ctx.Done()
		_ = srv.Close()
	}()

	_, _ = fmt.Fprintf(progress, "waiting for the first NB/SB snapshot of zone %s…\n", zone)
	for !store.IsReady() {
		select {
		case <-setup.Done():
			return "", fmt.Errorf("zone %s: no OVSDB snapshot within 30s through the exec relay", zone)
		case <-time.After(200 * time.Millisecond):
		}
	}
	return "http://" + ln.Addr().String() + "/api/v1", nil
}
