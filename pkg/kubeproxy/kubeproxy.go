// Package kubeproxy reaches the kvovn-topod API through the Kubernetes API
// server's service proxy, the only supported way in (AGENTS.md §4): the API
// server authenticates the caller and checks services/proxy RBAC. Nothing is
// exposed by IP, LoadBalancer or Ingress. Tier 0: it only gets and lists
// Services.
package kubeproxy

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

// ChartSelector matches the Service the kvovn-topo chart creates
// (selectorLabels in charts/kvovn-topo/templates/_helpers.tpl).
const ChartSelector = "app.kubernetes.io/name=kvovn-topo"

// ServiceRef locates the daemon Service. Port empty means "pick from the
// Service": the port named http, else the first one.
type ServiceRef struct {
	Namespace, Name, Port string
	// How tells how the Service was found: "default", "label" or "flags".
	How string
}

// Default is where the chart installs the daemon with default values.
var Default = ServiceRef{Namespace: "kube-system", Name: "kvovn-topod"}

// String renders namespace/name:port.
func (r ServiceRef) String() string { return r.Namespace + "/" + r.Name + ":" + r.Port }

// URL is the daemon API base URL (ending in /api/v1) through the API server's
// service proxy. Callers need get on services/proxy for that Service.
func URL(apiServer string, ref ServiceRef) string {
	return fmt.Sprintf("%s/api/v1/namespaces/%s/services/http:%s:%s/proxy/api/v1",
		strings.TrimRight(apiServer, "/"), url.PathEscape(ref.Namespace), url.PathEscape(ref.Name), url.PathEscape(ref.Port))
}

// Discover finds the daemon Service: def when it exists, otherwise the single
// Service carrying ChartSelector in any namespace. Ambiguity is an error,
// never a silent pick.
func Discover(ctx context.Context, client kubernetes.Interface, def ServiceRef) (ServiceRef, error) {
	svc, err := client.CoreV1().Services(def.Namespace).Get(ctx, def.Name, metav1.GetOptions{})
	switch {
	case err == nil:
		return withPort(def, svc, "default"), nil
	case apierrors.IsForbidden(err):
		return ServiceRef{}, fmt.Errorf("reading Service %s/%s is forbidden: the kubeconfig user needs get on services, or pass --namespace and --service", def.Namespace, def.Name)
	case !apierrors.IsNotFound(err):
		return ServiceRef{}, fmt.Errorf("reading Service %s/%s: %w", def.Namespace, def.Name, err)
	}

	list, err := client.CoreV1().Services(metav1.NamespaceAll).List(ctx, metav1.ListOptions{LabelSelector: ChartSelector})
	if apierrors.IsForbidden(err) {
		return ServiceRef{}, fmt.Errorf("no Service at %s/%s, and discovery needs list on services cluster-wide; pass --namespace and --service", def.Namespace, def.Name)
	}
	if err != nil {
		return ServiceRef{}, fmt.Errorf("listing Services (%s): %w", ChartSelector, err)
	}
	switch len(list.Items) {
	case 0:
		return ServiceRef{}, fmt.Errorf("no kvovn-topod Service found (not at %s/%s, none labelled %s); install the chart or pass --namespace and --service", def.Namespace, def.Name, ChartSelector)
	case 1:
		found := def
		found.Namespace, found.Name = list.Items[0].Namespace, list.Items[0].Name
		return withPort(found, &list.Items[0], "label"), nil
	}
	names := make([]string, 0, len(list.Items))
	for _, s := range list.Items {
		names = append(names, s.Namespace+"/"+s.Name)
	}
	sort.Strings(names)
	return ServiceRef{}, fmt.Errorf("several kvovn-topod Services found (%s); pass --namespace and --service", strings.Join(names, ", "))
}

func withPort(ref ServiceRef, svc *corev1.Service, how string) ServiceRef {
	ref.How = how
	if ref.Port != "" || len(svc.Spec.Ports) == 0 {
		return ref
	}
	ref.Port = svc.Spec.Ports[0].Name
	for _, p := range svc.Spec.Ports {
		if p.Name == "http" {
			ref.Port = p.Name
			break
		}
	}
	if ref.Port == "" { // unnamed single port: the proxy accepts the number
		ref.Port = strconv.Itoa(int(svc.Spec.Ports[0].Port))
	}
	return ref
}

// Conn is a resolved path to the daemon API.
type Conn struct {
	// BaseURL ends in /api/v1.
	BaseURL string
	// Client authenticates as the kubeconfig user. It has no overall timeout
	// (SSE streams are long-lived): bound each request with its context.
	Client  *http.Client
	Service ServiceRef
}

// Connect loads the kubeconfig and resolves the daemon Service. With pinned
// set, ref is used as given (port defaults to http) and no Service is read.
func Connect(ctx context.Context, loader clientcmd.ClientConfig, ref ServiceRef, pinned bool) (*Conn, error) {
	cfg, err := loader.ClientConfig()
	if err != nil {
		return nil, fmt.Errorf("loading kubeconfig (set KUBECONFIG or pass --kubeconfig): %w", err)
	}
	client, err := rest.HTTPClientFor(cfg)
	if err != nil {
		return nil, fmt.Errorf("building Kubernetes HTTP client: %w", err)
	}
	if pinned {
		ref.How = "flags"
		if ref.Port == "" {
			ref.Port = "http"
		}
	} else {
		clientset, err := kubernetes.NewForConfigAndClient(cfg, client)
		if err != nil {
			return nil, fmt.Errorf("building Kubernetes client: %w", err)
		}
		if ref, err = Discover(ctx, clientset, ref); err != nil {
			return nil, err
		}
	}
	return &Conn{BaseURL: URL(cfg.Host, ref), Client: client, Service: ref}, nil
}

// Loader returns the standard kubeconfig loading rules, with an explicit path
// and context when given (empty means default).
func Loader(kubeconfig, context string) clientcmd.ClientConfig {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	rules.ExplicitPath = kubeconfig
	return clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, &clientcmd.ConfigOverrides{CurrentContext: context})
}
