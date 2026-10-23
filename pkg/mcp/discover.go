package mcp

import (
	"context"
	"sort"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// ChartSelector matches the Service the ovn-topology chart creates
// (selectorLabels in charts/ovn-topology/templates/_helpers.tpl).
const ChartSelector = "app.kubernetes.io/name=ovn-topology"

// ServiceRef locates the daemon Service. Port empty means "pick from the
// Service": the port named http, else the first one.
type ServiceRef struct {
	Namespace, Name, Port string
	// How tells how the Service was found: "default" or "label".
	How string
}

// DiscoverService finds the daemon Service: def when it exists, otherwise the
// single Service carrying ChartSelector in any namespace. It only reads
// Services (get, list). Ambiguity is an error, never a silent pick.
func DiscoverService(ctx context.Context, client kubernetes.Interface, def ServiceRef) (ServiceRef, error) {
	svc, err := client.CoreV1().Services(def.Namespace).Get(ctx, def.Name, metav1.GetOptions{})
	switch {
	case err == nil:
		return withPort(def, svc, "default"), nil
	case apierrors.IsForbidden(err):
		return ServiceRef{}, toolErr(CodeBackend, "reading Service %s/%s is forbidden: the kubeconfig user needs get on services, or pass --namespace and --service", def.Namespace, def.Name)
	case !apierrors.IsNotFound(err):
		return ServiceRef{}, toolErr(CodeBackend, "reading Service %s/%s: %v", def.Namespace, def.Name, err)
	}

	list, err := client.CoreV1().Services(metav1.NamespaceAll).List(ctx, metav1.ListOptions{LabelSelector: ChartSelector})
	if apierrors.IsForbidden(err) {
		return ServiceRef{}, toolErr(CodeBackend, "Service %s/%s not found, and discovery needs list on services cluster-wide; pass --namespace and --service", def.Namespace, def.Name)
	}
	if err != nil {
		return ServiceRef{}, toolErr(CodeBackend, "listing Services (%s): %v", ChartSelector, err)
	}
	switch len(list.Items) {
	case 0:
		return ServiceRef{}, toolErr(CodeBackend, "no ovn-topologyd Service found (not at %s/%s, none labelled %s); install the chart or pass --namespace and --service", def.Namespace, def.Name, ChartSelector)
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
	return ServiceRef{}, toolErr(CodeBackend, "several ovn-topologyd Services found (%s); pass --namespace and --service", strings.Join(names, ", "))
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
		ref.Port = itoa(int(svc.Spec.Ports[0].Port))
	}
	return ref
}
