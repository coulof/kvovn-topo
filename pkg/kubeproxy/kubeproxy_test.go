package kubeproxy

import (
	"context"
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestURL(t *testing.T) {
	got := URL("https://rancher.example/k8s/clusters/c-1/", ServiceRef{Namespace: "kube-system", Name: "kvovn-topod", Port: "http"})
	want := "https://rancher.example/k8s/clusters/c-1/api/v1/namespaces/kube-system/services/http:kvovn-topod:http/proxy/api/v1"
	if got != want {
		t.Fatalf("got %s\nwant %s", got, want)
	}
}

func svc(ns, name string, labels map[string]string, ports ...corev1.ServicePort) *corev1.Service {
	return &corev1.Service{ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Labels: labels}, Spec: corev1.ServiceSpec{Ports: ports}}
}

func TestDiscover(t *testing.T) {
	chart := map[string]string{"app.kubernetes.io/name": "kvovn-topo"}
	httpPort := corev1.ServicePort{Name: "http", Port: 8080}
	def := ServiceRef{Namespace: "kube-system", Name: "kvovn-topod"}

	cases := []struct {
		name    string
		objs    []runtime.Object
		forbid  string // verb to forbid on services
		def     ServiceRef
		want    ServiceRef
		wantErr string
	}{
		{"default present", []runtime.Object{svc("kube-system", "kvovn-topod", chart, httpPort)}, "", def,
			ServiceRef{"kube-system", "kvovn-topod", "http", "default"}, ""},
		{"found by label elsewhere", []runtime.Object{svc("kvovn-topo", "topo", chart, corev1.ServicePort{Name: "metrics", Port: 9090}, httpPort)}, "", def,
			ServiceRef{"kvovn-topo", "topo", "http", "label"}, ""},
		{"unnamed port", []runtime.Object{svc("tools", "topo", chart, corev1.ServicePort{Port: 8080})}, "", def,
			ServiceRef{"tools", "topo", "8080", "label"}, ""},
		{"explicit port kept", []runtime.Object{svc("kube-system", "kvovn-topod", chart, httpPort)}, "", ServiceRef{Namespace: "kube-system", Name: "kvovn-topod", Port: "8080"},
			ServiceRef{"kube-system", "kvovn-topod", "8080", "default"}, ""},
		{"none", []runtime.Object{svc("default", "other", nil, httpPort)}, "", def, ServiceRef{}, "no kvovn-topod Service found"},
		{"ambiguous", []runtime.Object{svc("a", "topo", chart, httpPort), svc("b", "topo", chart, httpPort)}, "", def, ServiceRef{}, "a/topo, b/topo"},
		{"list forbidden", nil, "list", def, ServiceRef{}, "needs list on services"},
		{"get forbidden", nil, "get", def, ServiceRef{}, "needs get on services"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			client := fake.NewClientset(tc.objs...)
			if tc.forbid != "" {
				client.PrependReactor(tc.forbid, "services", func(k8stesting.Action) (bool, runtime.Object, error) {
					return true, nil, apierrors.NewForbidden(schema.GroupResource{Resource: "services"}, "", fmt.Errorf("denied"))
				})
			}
			got, err := Discover(context.Background(), client, tc.def)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("error %v, want one containing %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || got != tc.want {
				t.Fatalf("got %+v, %v; want %+v", got, err, tc.want)
			}
		})
	}
}
