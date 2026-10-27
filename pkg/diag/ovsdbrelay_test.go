package diag

import (
	"bufio"
	"context"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
)

// echoExecutor stands in for the exec stream to socat: it echoes stdin to stdout
// and records the command and pod of each stream.
type echoExecutor struct {
	mu    sync.Mutex
	calls []MockExecCall
}

func (e *echoExecutor) Exec(context.Context, PodRef, []string) ([]byte, []byte, error) {
	return nil, nil, nil
}

func (e *echoExecutor) Stream(_ context.Context, pod PodRef, cmd []string, stdin io.Reader, stdout, _ io.Writer) error {
	e.mu.Lock()
	e.calls = append(e.calls, MockExecCall{Pod: pod, Cmd: cmd})
	e.mu.Unlock()
	_, err := io.Copy(stdout, stdin)
	return err
}

func TestBuildOVSDBRelayCommand(t *testing.T) {
	if got := strings.Join(BuildOVSDBRelayCommand(NB), " "); got != "socat STDIO UNIX-CONNECT:/var/run/ovn/ovnnb_db.sock" {
		t.Errorf("NB relay command = %q", got)
	}
	if got := strings.Join(BuildOVSDBRelayCommand(SB), " "); got != "socat STDIO UNIX-CONNECT:/var/run/ovn/ovnsb_db.sock" {
		t.Errorf("SB relay command = %q", got)
	}
}

func TestStartOVSDBRelayRoundTrip(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	exec := &echoExecutor{}
	pod := PodRef{Namespace: "openshift-ovn-kubernetes", Name: "ovnkube-node-a", Container: "sbdb"}
	addr, err := StartOVSDBRelay(ctx, exec, pod, SB, nil)
	if err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 2; i++ { // every connection gets its own stream (libovsdb reconnects)
		conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Now().Add(2 * time.Second))
		if _, err := conn.Write([]byte(`{"method":"echo","params":[],"id":"x"}` + "\n")); err != nil {
			t.Fatal(err)
		}
		line, err := bufio.NewReader(conn).ReadString('\n')
		if err != nil || !strings.Contains(line, `"echo"`) {
			t.Fatalf("round trip %d: %q, %v", i, line, err)
		}
		_ = conn.Close()
	}

	exec.mu.Lock()
	defer exec.mu.Unlock()
	if len(exec.calls) != 2 {
		t.Fatalf("streams = %d, want 2", len(exec.calls))
	}
	for _, c := range exec.calls {
		if c.Pod != pod || strings.Join(c.Cmd, " ") != strings.Join(BuildOVSDBRelayCommand(SB), " ") {
			t.Errorf("stream %v %v, want the allowlisted SB relay in %v", c.Pod, c.Cmd, pod)
		}
	}

	cancel()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := net.DialTimeout("tcp", addr, 200*time.Millisecond); err != nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Errorf("relay still listening on %s after cancel", addr)
}

func TestFindOVNKZonePod(t *testing.T) {
	ns := "openshift-ovn-kubernetes"
	pod := func(name, node string, phase corev1.PodPhase) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: map[string]string{"app": "ovnkube-node"}},
			Spec:       corev1.PodSpec{NodeName: node},
			Status:     corev1.PodStatus{Phase: phase},
		}
	}
	l := NewPodLocator(fake.NewSimpleClientset(
		pod("ovnkube-node-a", "node-a", corev1.PodRunning),
		pod("ovnkube-node-b", "node-b", corev1.PodRunning),
		pod("ovnkube-node-c", "node-c", corev1.PodPending),
	), ns)
	ctx := context.Background()

	got, err := l.FindOVNKZonePod(ctx, "node-b", NB)
	if err != nil || got != (PodRef{Namespace: ns, Name: "ovnkube-node-b", Container: "nbdb"}) {
		t.Errorf("node-b NB = %v, %v", got, err)
	}
	if got, err := l.FindOVNKZonePod(ctx, "node-a", SB); err != nil || got.Container != "sbdb" || got.Name != "ovnkube-node-a" {
		t.Errorf("node-a SB = %v, %v", got, err)
	}
	if _, err := l.FindOVNKZonePod(ctx, "node-c", NB); err == nil {
		t.Errorf("a pod that is not running must not be a target")
	}
	if _, err := l.FindOVNKZonePod(ctx, "node-x", NB); err == nil {
		t.Errorf("an unknown node must fail")
	}
	zones, err := l.ListOVNKZones(ctx)
	if err != nil || strings.Join(zones, ",") != "node-a,node-b" {
		t.Errorf("zones = %v, %v", zones, err)
	}
}

func TestDetectFlavor(t *testing.T) {
	ctx := context.Background()
	pod := func(ns, name, app, node string) *corev1.Pod {
		return &corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, Labels: map[string]string{"app": app}},
			Spec:       corev1.PodSpec{NodeName: node},
			Status:     corev1.PodStatus{Phase: corev1.PodRunning},
		}
	}
	ovnk := []runtime.Object{
		pod("openshift-ovn-kubernetes", "ovnkube-node-a", "ovnkube-node", "node-a"),
		pod("openshift-ovn-kubernetes", "ovnkube-node-b", "ovnkube-node", "node-b"),
	}

	// Kube-OVN in kube-system wins the auto-detection.
	l := NewPodLocator(fake.NewSimpleClientset(append(ovnk, pod("kube-system", "ovn-central-0", "ovn-central", "node-a"))...), "")
	if f, ns := l.Detect(ctx); f != FlavorKubeOVN || ns != "kube-system" {
		t.Errorf("Kube-OVN cluster detected as %v in %s", f, ns)
	}
	if p, _ := l.FindTracePod(ctx, "node-b"); p.Container != "ovn-central" {
		t.Errorf("Kube-OVN trace pod = %v", p)
	}

	// Only ovnkube-node: ovn-kubernetes, every action on the target's own node.
	l = NewPodLocator(fake.NewSimpleClientset(ovnk...), "")
	if f, ns := l.Detect(ctx); f != FlavorOVNK || ns != "openshift-ovn-kubernetes" {
		t.Fatalf("ovn-kubernetes cluster detected as %v in %s", f, ns)
	}
	want := func(name string, got PodRef, err error, pod, container string) {
		t.Helper()
		if err != nil || got.Name != pod || got.Container != container || got.Namespace != "openshift-ovn-kubernetes" {
			t.Errorf("%s = %v, %v; want %s/%s", name, got, err, pod, container)
		}
	}
	p, err := l.FindTracePod(ctx, "node-b")
	want("trace", p, err, "ovnkube-node-b", "sbdb")
	p, err = l.FindNodeOVSPod(ctx, "node-a")
	want("ovs", p, err, "ovnkube-node-a", "ovn-controller")
	p, err = l.FindCapturePod(ctx, "node-b")
	want("capture", p, err, "ovnkube-node-b", "ovn-controller")
	p, _, err = l.FindProbePod(ctx)
	if err != nil || p.Container != "sbdb" {
		t.Errorf("probe = %v, %v", p, err)
	}
	if _, err := l.FindTracePod(ctx, ""); err == nil {
		t.Errorf("a trace with no node and no home zone must fail")
	}
	l.SetHomeZone("node-a")
	p, err = l.FindTracePod(ctx, "")
	want("trace (home zone)", p, err, "ovnkube-node-a", "sbdb")
	if _, err := l.FindNodeOVSPod(ctx, "node-x"); err == nil {
		t.Errorf("an unknown node must fail, never fall back to another node")
	}
	if out, err := l.ControllerLog(ctx, nil, PodRef{Namespace: "openshift-ovn-kubernetes", Name: "ovnkube-node-a", Container: "ovn-controller"}, 5); err != nil || len(out) == 0 {
		t.Errorf("ovn-k controller log = %q, %v", out, err)
	}

	// An explicit namespace is kept; its pods set the flavor.
	l = NewPodLocator(fake.NewSimpleClientset(ovnk...), "openshift-ovn-kubernetes")
	if f, _ := l.Detect(ctx); f != FlavorOVNK {
		t.Errorf("explicit ovn-k namespace detected as %v", f)
	}
}
