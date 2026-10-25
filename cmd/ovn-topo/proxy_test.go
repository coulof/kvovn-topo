package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coulof/ovn-topology/pkg/graph"
	"github.com/coulof/ovn-topology/pkg/kubeproxy"
	"github.com/coulof/ovn-topology/pkg/mcp"
	"github.com/coulof/ovn-topology/pkg/server"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// fakeAPIServer stands in for the Kubernetes API server (or Rancher's
// /k8s/clusters/<id>): it requires the kubeconfig's bearer token, serves the
// daemon Service from namespace ns, and proxies .../services/http:<svc>:http/proxy/
// to a real pkg/server over the fixture graph. Anything else is 404.
func fakeAPIServer(t *testing.T, ns, svc string) (*httptest.Server, *[]string) {
	t.Helper()
	snap := graph.LoadTestSnapshot(t)
	store := graph.NewStore(snap.NodeMapper)
	store.Apply(func(d *graph.Snapshot) []string { *d = *snap; return []string{"*"} })
	store.SetReady(true)
	daemon := server.NewServer(store, server.VersionConfig{APIVersion: "v1"}, nil)

	service := corev1.Service{
		TypeMeta:   metav1.TypeMeta{Kind: "Service", APIVersion: "v1"},
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: svc, Labels: map[string]string{"app.kubernetes.io/name": "ovn-topology"}},
		Spec:       corev1.ServiceSpec{Ports: []corev1.ServicePort{{Name: "http", Port: 8080}}},
	}
	proxyPrefix := fmt.Sprintf("/api/v1/namespaces/%s/services/http:%s:http/proxy", ns, svc)
	var seen []string

	// TLS like a real API server: clientcmd only sends credentials over https.
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.URL.Path)
		if r.Header.Get("Authorization") != "Bearer test-token" {
			http.Error(w, `{"kind":"Status","code":401}`, http.StatusUnauthorized)
			return
		}
		switch {
		case strings.HasPrefix(r.URL.Path, proxyPrefix+"/"):
			r2 := r.Clone(r.Context())
			r2.URL.Path = strings.TrimPrefix(r.URL.Path, proxyPrefix)
			daemon.ServeHTTP(w, r2)
		case r.URL.Path == fmt.Sprintf("/api/v1/namespaces/%s/services/%s", ns, svc):
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(service)
		case r.URL.Path == "/api/v1/services" && r.URL.Query().Get("labelSelector") == kubeproxy.ChartSelector:
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(corev1.ServiceList{TypeMeta: metav1.TypeMeta{Kind: "ServiceList", APIVersion: "v1"}, Items: []corev1.Service{service}})
		default:
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = fmt.Fprintf(w, `{"kind":"Status","apiVersion":"v1","status":"Failure","reason":"NotFound","code":404}`)
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &seen
}

func writeKubeconfig(t *testing.T, api *httptest.Server) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kubeconfig")
	ca := base64.StdEncoding.EncodeToString(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: api.Certificate().Raw}))
	cfg := fmt.Sprintf(`apiVersion: v1
kind: Config
clusters: [{name: c, cluster: {server: %q, certificate-authority-data: %s}}]
users: [{name: u, user: {token: test-token}}]
contexts: [{name: ctx, context: {cluster: c, user: u}}]
current-context: ctx
`, api.URL, ca)
	if err := os.WriteFile(path, []byte(cfg), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// `ovn-topo mcp` with no flags must reach the daemon through the API server's
// service proxy, with the kubeconfig credentials, finding the Service by its
// default name or, elsewhere, by the chart label.
func TestMCPDefaultUsesServiceProxy(t *testing.T) {
	for _, tc := range []struct{ ns, svc string }{
		{"kube-system", "ovn-topologyd"}, // chart default
		{"tools", "topo"},                // found by label
	} {
		t.Run(tc.ns, func(t *testing.T) {
			api, seen := fakeAPIServer(t, tc.ns, tc.svc)
			loader := kubeproxy.Loader(writeKubeconfig(t, api), "")
			backend := serviceProxyBackend(loader, kubeproxy.Default, false, slog.New(slog.DiscardHandler))

			ctx := context.Background()
			st, ct := sdk.NewInMemoryTransports()
			ss, err := mcp.NewServer(backend, mcp.Options{Version: "test"}).Connect(ctx, st, nil)
			if err != nil {
				t.Fatal(err)
			}
			cs, err := sdk.NewClient(&sdk.Implementation{Name: "t", Version: "t"}, nil).Connect(ctx, ct, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = cs.Close(); _ = ss.Wait() }()

			res, err := cs.CallTool(ctx, &sdk.CallToolParams{Name: "find", Arguments: map[string]any{"query": "10.244.0.10"}})
			if err != nil {
				t.Fatal(err)
			}
			if res.IsError {
				t.Fatalf("find through the service proxy: %s", res.Content[0].(*sdk.TextContent).Text)
			}
			out, _ := json.Marshal(res.StructuredContent)
			if !strings.Contains(string(out), `"backend":"daemon"`) || !strings.Contains(string(out), "lsp:lsp-pod1-uuid") {
				t.Fatalf("unexpected result: %s", out)
			}
			want := fmt.Sprintf("/api/v1/namespaces/%s/services/http:%s:http/proxy/api/v1/find", tc.ns, tc.svc)
			if !contains(*seen, want) {
				t.Fatalf("no request to %s; saw %v", want, *seen)
			}
		})
	}
}

// The TUI's default connection (no --daemon-url) goes the same way.
func TestTUIDefaultUsesServiceProxy(t *testing.T) {
	api, seen := fakeAPIServer(t, "kube-system", "ovn-topologyd")
	conn, err := kubeproxy.Connect(context.Background(), kubeproxy.Loader(writeKubeconfig(t, api), ""), kubeproxy.Default, false)
	if err != nil {
		t.Fatal(err)
	}
	m := initialModel(conn.BaseURL, "", "default", false, nil, "")
	m.httpClient = conn.Client
	msg, _ := m.fetchGraphCmd()().(graphLoadedMsg)
	if msg.err != nil || msg.resp == nil || len(msg.resp.Nodes) == 0 {
		t.Fatalf("graph through the service proxy: %+v", msg)
	}
	if !contains(*seen, "/api/v1/namespaces/kube-system/services/http:ovn-topologyd:http/proxy/api/v1/graph") {
		t.Fatalf("graph did not go through the service proxy: %v", *seen)
	}
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}
