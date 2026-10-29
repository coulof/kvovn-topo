package main

// `kvovn-topo mcp`: MCP server over stdio for AI agents (ADR-0003). Tier 0 only:
// this path never builds a pkg/diag executor, whatever other flags say.

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/coulof/kvovn-topo/pkg/graph"
	"github.com/coulof/kvovn-topo/pkg/kubeproxy"
	"github.com/coulof/kvovn-topo/pkg/mcp"
	"github.com/coulof/kvovn-topo/pkg/ovsdb"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"golang.org/x/term"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// version is set at build time (Makefile LDFLAGS).
var version = "0.1.0-dev"

const mcpUsage = `Usage: kvovn-topo mcp [flags]

Serves read-only OVN topology tools to an AI agent over stdio (Model Context Protocol).
Backends, first match wins:
  --fixture / --fixture-dir   offline fixture data
  --nb and --sb               embedded graph against OVSDB (e.g. through kubectl port-forward)
  --daemon-url                kvovn-topod API at a URL you can reach
  (default)                   kvovn-topod through the Kubernetes service proxy of your kubeconfig

Flags:
`

func runMCP(args []string, stderr io.Writer) int {
	fs := flag.NewFlagSet("mcp", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		_, _ = fmt.Fprint(stderr, mcpUsage)
		fs.PrintDefaults()
	}
	var (
		kubeconfig  = fs.String("kubeconfig", getEnvDefault("KUBECONFIG", ""), "path to kubeconfig (default: standard loading rules)")
		kubeContext = fs.String("context", "", "kubeconfig context (default: current context)")
		namespace   = fs.String("namespace", "kube-system", "namespace of the kvovn-topod Service; setting it (or --service) skips discovery")
		service     = fs.String("service", "kvovn-topod", "name of the kvovn-topod Service; setting it (or --namespace) skips discovery")
		port        = fs.String("port", "", "Service port name or number (default: the port named http)")
		daemonURL   = fs.String("daemon-url", "", "kvovn-topod API base URL (e.g. http://127.0.0.1:8080/api/v1); skips the service proxy")
		nb          = fs.String("nb", "", "comma-separated OVN NB endpoints (embedded backend)")
		sb          = fs.String("sb", "", "comma-separated OVN SB endpoints (embedded backend)")
		ovnCert     = fs.String("ovn-cert", "", "OVN client certificate (embedded backend)")
		ovnKey      = fs.String("ovn-key", "", "OVN client key (embedded backend)")
		ovnCA       = fs.String("ovn-ca", "", "OVN CA certificate (embedded backend)")
		fixture     = fs.Bool("fixture", false, "serve the bundled dev fixture (run from the repository)")
		fixtureDir  = fs.String("fixture-dir", "", "serve a captured scenario (testdata/scenarios/<name>)")
		logLevel    = fs.String("log-level", "info", "stderr log level: debug, info, warn, error")
	)
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	pinned := false // --namespace or --service given: use them, skip discovery
	fs.Visit(func(f *flag.Flag) { pinned = pinned || f.Name == "namespace" || f.Name == "service" })

	var level slog.Level
	if err := level.UnmarshalText([]byte(*logLevel)); err != nil {
		level = slog.LevelInfo
	}
	// stdout carries the protocol; everything else goes to stderr.
	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{Level: level}))

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	loader := kubeproxy.Loader(*kubeconfig, *kubeContext)

	var backend mcp.Backend
	switch {
	case *fixtureDir != "":
		snap, err := ovsdb.LoadFixture(*fixtureDir)
		if err != nil {
			logger.Error("loading fixture", "dir", *fixtureDir, "error", err)
			return 1
		}
		backend = mcp.NewSnapshotBackend(mcp.StaticSource{Snapshot: snap})
		logger.Info("mcp backend: fixture", "dir", *fixtureDir)
	case *fixture:
		backend = mcp.NewSnapshotBackend(mcp.StaticSource{Snapshot: graph.LoadTestSnapshot(nil)})
		logger.Info("mcp backend: dev fixture")
	case *nb != "" || *sb != "":
		if *nb == "" || *sb == "" {
			logger.Error("the embedded backend needs both --nb and --sb")
			return 2
		}
		store, err := startEmbedded(ctx, loader, *nb, *sb, *ovnCert, *ovnKey, *ovnCA, logger)
		if err != nil {
			logger.Error("starting embedded backend", "error", err)
			return 1
		}
		backend = mcp.NewSnapshotBackend(store)
		logger.Info("mcp backend: embedded", "nb", *nb, "sb", *sb)
	case *daemonURL != "":
		backend = mcp.NewHTTPBackend(*daemonURL, &http.Client{Timeout: 30 * time.Second})
		logger.Info("mcp backend: daemon", "url", *daemonURL)
	default:
		// Like kvtop, a cluster that cannot be reached does not stop the
		// server: the handshake succeeds and every tool returns the reason.
		lazy := serviceProxyBackend(loader, kubeproxy.ServiceRef{Namespace: *namespace, Name: *service, Port: *port}, pinned, logger)
		go func() { _ = lazy.Prime(ctx) }() // logs the outcome; tool calls retry a failure
		backend = lazy
	}

	logger.Info("mcp server ready on stdio", "version", version, "backend", backend.Name())
	if stdinIsTerminal() {
		logger.Info("stdin is a terminal: this command speaks MCP JSON-RPC on stdin/stdout; register it in an MCP client (docs/mcp.md) or try hack/mcp-smoke.sh")
	}

	srv := mcp.NewServer(backend, mcp.Options{Version: version, Logger: logger})
	if err := srv.Run(ctx, &sdk.StdioTransport{}); err != nil && ctx.Err() == nil {
		logger.Error("mcp server stopped", "error", err)
		return 1
	}
	return 0
}

// serviceProxyBackend reaches the daemon through the API server's service
// proxy (pkg/kubeproxy). The kubeconfig is loaded and, unless pinned, the
// Service discovered on the first tool call; failures are retried per call.
func serviceProxyBackend(loader clientcmd.ClientConfig, ref kubeproxy.ServiceRef, pinned bool, logger *slog.Logger) *mcp.LazyBackend {
	return mcp.NewLazyBackend("daemon", func(ctx context.Context) (mcp.Backend, error) {
		conn, err := kubeproxy.Connect(ctx, loader, ref, pinned)
		if err != nil {
			logger.Warn("mcp backend: daemon via service proxy unavailable", "error", err)
			return nil, err
		}
		logger.Info("mcp backend: daemon via service proxy", "service", conn.Service.String(), "found_by", conn.Service.How, "url", conn.BaseURL)
		client := *conn.Client
		client.Timeout = 30 * time.Second // tools make plain requests; no streams here
		return mcp.NewHTTPBackend(conn.BaseURL, &client), nil
	})
}

// startEmbedded runs the daemon's OVSDB syncer in process. Kubernetes
// enrichment (node names, workloads) is used when the kubeconfig loads, and
// skipped otherwise: topology still comes from OVSDB only.
func startEmbedded(ctx context.Context, loader clientcmd.ClientConfig, nbEP, sbEP, cert, key, ca string, logger *slog.Logger) (*graph.Store, error) {
	var mapper graph.NodeMapper = &graph.StaticNodeMapper{}
	var resolver graph.WorkloadResolver = &graph.StaticWorkloadResolver{}
	var clientset kubernetes.Interface
	if cfg, err := loader.ClientConfig(); err == nil {
		if cs, err := kubernetes.NewForConfig(cfg); err == nil {
			clientset = cs
		}
	}
	var kubeMapper *graph.KubeNodeMapper
	var kubeResolver *graph.KubeWorkloadResolver
	if clientset != nil {
		kubeMapper = graph.NewKubeNodeMapper(clientset, logger)
		kubeResolver = graph.NewKubeWorkloadResolver(clientset, logger)
		mapper, resolver = kubeMapper, kubeResolver
	} else {
		logger.Info("no kubeconfig: chassis keep OVN hostnames and ports have no workload identity")
	}

	store := graph.NewStore(mapper, resolver)
	if clientset != nil {
		go graph.WatchKubernetes(ctx, clientset, kubeMapper, kubeResolver, store, 2*time.Second, logger)
	}

	conf := func(eps string) ovsdb.Config {
		return ovsdb.Config{Endpoints: splitList(eps), CertFile: cert, KeyFile: key, CAFile: ca,
			InactivityCheck: 30 * time.Second, ReconnectTimeout: 10 * time.Second}
	}
	nbClient, err := ovsdb.NewNBClient(ctx, conf(nbEP))
	if err != nil {
		return nil, fmt.Errorf("NB client: %w", err)
	}
	sbClient, err := ovsdb.NewSBClient(ctx, conf(sbEP))
	if err != nil {
		return nil, fmt.Errorf("SB client: %w", err)
	}
	syncer := ovsdb.NewSyncer(nbClient, sbClient, store, logger)
	go func() {
		if err := syncer.Start(ctx); err != nil && !errors.Is(err, context.Canceled) {
			logger.Error("OVSDB syncer stopped", "error", err)
		}
	}()
	go func() {
		<-ctx.Done()
		syncer.Close()
	}()
	return store, nil
}

// stdinIsTerminal reports whether a person, not an MCP client, is on stdin.
func stdinIsTerminal() bool {
	return term.IsTerminal(int(os.Stdin.Fd()))
}

func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
