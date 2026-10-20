package diag

import (
	"context"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestBuildTraceCommand(t *testing.T) {
	tests := []struct {
		name        string
		req         TraceRequest
		wantErr     bool
		errContains string
		wantCmd     string
	}{
		{
			name: "valid icmp trace",
			req: TraceRequest{
				Datapath: "default-subnet",
				Inport:   "test-pod.default",
				SrcMAC:   "00:00:00:aa:bb:cc",
				DstMAC:   "00:00:00:dd:ee:ff",
				SrcIP:    "10.244.0.10",
				DstIP:    "10.244.0.20",
				Protocol: "icmp",
			},
			wantErr: false,
			wantCmd: `ovn-trace --ct=new default-subnet inport == "test-pod.default" && eth.src == 00:00:00:aa:bb:cc && eth.dst == 00:00:00:dd:ee:ff && ip4.src == 10.244.0.10 && ip4.dst == 10.244.0.20 && ip.proto == 1 && icmp4.type == 8 && icmp4.code == 0`,
		},
		{
			name: "valid tcp trace with custom ports",
			req: TraceRequest{
				Datapath: "ovn_cluster",
				Inport:   "lrp-join",
				SrcIP:    "100.64.0.2",
				DstIP:    "10.244.1.5",
				Protocol: "tcp",
				SrcPort:  54321,
				DstPort:  8080,
				CTState:  "est",
			},
			wantErr: false,
			wantCmd: `ovn-trace --ct=est ovn_cluster inport == "lrp-join" && ip4.src == 100.64.0.2 && ip4.dst == 10.244.1.5 && ip.proto == 6 && tcp.src == 54321 && tcp.dst == 8080`,
		},
		{
			name: "injection attempt in inport",
			req: TraceRequest{
				Datapath: "default-subnet",
				Inport:   "test; rm -rf /",
				SrcIP:    "10.244.0.10",
				DstIP:    "10.244.0.20",
			},
			wantErr:     true,
			errContains: "invalid characters",
		},
		{
			name: "injection attempt in src IP",
			req: TraceRequest{
				Datapath: "default-subnet",
				Inport:   "test",
				SrcIP:    "10.0.0.1$(whoami)",
				DstIP:    "10.244.0.20",
			},
			wantErr:     true,
			errContains: "invalid characters",
		},
		{
			name: "missing datapath",
			req: TraceRequest{
				Inport: "test",
				SrcIP:  "10.0.0.1",
				DstIP:  "10.0.0.2",
			},
			wantErr:     true,
			errContains: "datapath cannot be empty",
		},
		{
			name: "invalid protocol",
			req: TraceRequest{
				Datapath: "default-subnet",
				Inport:   "test",
				SrcIP:    "10.0.0.1",
				DstIP:    "10.0.0.2",
				Protocol: "sctp",
			},
			wantErr:     true,
			errContains: "unsupported protocol",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := BuildTraceCommand(tt.req)
			if (err != nil) != tt.wantErr {
				t.Fatalf("BuildTraceCommand() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && tt.errContains != "" {
				if !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("expected error containing %q, got %v", tt.errContains, err)
				}
				return
			}
			if !tt.wantErr {
				joined := strings.Join(cmd, " ")
				if joined != tt.wantCmd {
					t.Errorf("BuildTraceCommand()\ngot:  %s\nwant: %s", joined, tt.wantCmd)
				}
			}
		})
	}
}

func TestParseTraceOutput(t *testing.T) {
	deliveredSample := `
# ingress(input: "pod1.default", mac: "00:00:00:01:02:03")
------------------------------------------------------------
 0. ls_in_port_sec_l2 (ovn-northd.c:7531): inport == "pod1.default", priority 100, uuid d49f57f6
    next;
 1. ls_in_pre_acl (ovn-northd.c:7640): ip, priority 100, uuid e1052fa9
    next;
 2. ls_in_acl (ovn-northd.c:8012): ip4, priority 1000, uuid fb01a912
    next;
13. ls_in_l2_lkup (ovn-northd.c:9200): eth.dst == 00:00:00:04:05:06, priority 50, uuid 37cae019
    outport = "pod2.default";
    output;

# egress(input: "pod2.default", output: "pod2.default")
-------------------------------------------------------
 0. ls_out_pre_acl (ovn-northd.c:7640): ip, priority 100, uuid d8ba458a
    next;
 8. ls_out_port_sec_l2 (ovn-northd.c:7531): outport == "pod2.default", priority 100, uuid c12fbc90
    output("pod2.default");
`
	res := ParseTraceOutput(deliveredSample)
	if res.Verdict != "delivered" {
		t.Errorf("expected verdict delivered, got %s", res.Verdict)
	}
	if res.FinalPort != "pod2.default" {
		t.Errorf("expected final port pod2.default, got %s", res.FinalPort)
	}
	if len(res.Stages) == 0 {
		t.Errorf("expected parsed stages, got 0")
	}

	dropSample := `
# ingress(input: "pod1.default")
 2. ls_in_acl: ip4 && tcp.dst == 22, priority 2000, uuid abc
    drop;
`
	dropRes := ParseTraceOutput(dropSample)
	if dropRes.Verdict != "dropped" {
		t.Errorf("expected verdict dropped, got %s", dropRes.Verdict)
	}
}

func TestBuildTcpdumpCommand(t *testing.T) {
	tests := []struct {
		name    string
		req     TcpdumpRequest
		wantErr bool
		wantCmd []string
	}{
		{
			name: "standard port capture",
			req: TcpdumpRequest{
				Interface: "test_h",
				Filter:    "tcp port 80",
				Count:     50,
			},
			wantErr: false,
			wantCmd: []string{"tcpdump", "-i", "test_h", "-nn", "-c", "50", "tcp port 80"},
		},
		{
			name: "packet count capped at max",
			req: TcpdumpRequest{
				Interface: "geneve0",
				Count:     5000,
			},
			wantErr: false,
			wantCmd: []string{"tcpdump", "-i", "geneve0", "-nn", "-c", "1000", "ip"},
		},
		{
			name: "injection in filter",
			req: TcpdumpRequest{
				Interface: "eth0",
				Filter:    "tcp; rm -rf /",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cmd, err := BuildTcpdumpCommand(tt.req)
			if (err != nil) != tt.wantErr {
				t.Fatalf("BuildTcpdumpCommand() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr {
				if len(cmd) != len(tt.wantCmd) {
					t.Fatalf("expected %d args, got %d", len(tt.wantCmd), len(cmd))
				}
				for i := range cmd {
					if cmd[i] != tt.wantCmd[i] {
						t.Errorf("arg[%d] got %s, want %s", i, cmd[i], tt.wantCmd[i])
					}
				}
			}
		})
	}
}

func TestOvsCommands(t *testing.T) {
	ifaceCmd := BuildOvsVsctlListInterfaceCommand()
	if len(ifaceCmd) != 4 || ifaceCmd[0] != "ovs-vsctl" {
		t.Errorf("unexpected ifaceCmd: %v", ifaceCmd)
	}

	ctCmd := BuildConntrackDumpCommand(42)
	if len(ctCmd) != 3 || ctCmd[2] != "zone=42" {
		t.Errorf("unexpected ctCmd: %v", ctCmd)
	}

	flowsCmd, err := BuildOvsOfctlDumpFlows("br-int")
	if err != nil || len(flowsCmd) != 5 {
		t.Errorf("unexpected flowsCmd: %v, err=%v", flowsCmd, err)
	}

	_, err = BuildOvsOfctlDumpFlows("br-int; rm -rf /")
	if err == nil {
		t.Errorf("expected error on bridge injection attempt")
	}
}

func TestPodLocator(t *testing.T) {
	ctx := context.Background()
	fakeClient := fake.NewSimpleClientset(
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "ovn-central-58f79fbc94-x123",
				Namespace: "kube-system",
				Labels:    map[string]string{"app": "ovn-central"},
			},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
		&corev1.Pod{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "kube-ovn-cni-node1",
				Namespace: "kube-system",
				Labels:    map[string]string{"app": "kube-ovn-cni"},
			},
			Spec:   corev1.PodSpec{NodeName: "node1"},
			Status: corev1.PodStatus{Phase: corev1.PodRunning},
		},
	)

	locator := NewPodLocator(fakeClient, "kube-system")

	centralPod, err := locator.FindOvnCentralPod(ctx)
	if err != nil {
		t.Fatalf("FindOvnCentralPod failed: %v", err)
	}
	if centralPod.Name != "ovn-central-58f79fbc94-x123" {
		t.Errorf("unexpected central pod name: %s", centralPod.Name)
	}

	cniPod, err := locator.FindNodeCNIPod(ctx, "node1")
	if err != nil {
		t.Fatalf("FindNodeCNIPod failed: %v", err)
	}
	if cniPod.Name != "kube-ovn-cni-node1" {
		t.Errorf("unexpected cni pod name: %s", cniPod.Name)
	}
}

func TestMockExecutor(t *testing.T) {
	ctx := context.Background()
	mock := NewMockExecutor()
	mock.StdoutMap["ovn-trace"] = []byte("output(\"target-port\");\n")

	pod := PodRef{Namespace: "kube-system", Name: "ovn-central-0", Container: "ovn-central"}
	out, errOut, err := mock.Exec(ctx, pod, []string{"ovn-trace", "args"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(errOut) > 0 {
		t.Errorf("unexpected stderr: %s", string(errOut))
	}
	if !strings.Contains(string(out), "output(\"target-port\")") {
		t.Errorf("unexpected stdout: %s", string(out))
	}

	if len(mock.Calls) != 1 {
		t.Fatalf("expected 1 call recorded, got %d", len(mock.Calls))
	}
	if mock.Calls[0].Cmd[0] != "ovn-trace" {
		t.Errorf("expected cmd ovn-trace, got %s", mock.Calls[0].Cmd[0])
	}
}
