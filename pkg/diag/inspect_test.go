package diag

import (
	"reflect"
	"testing"
)

func TestInspectBuilders(t *testing.T) {
	for _, c := range []struct {
		name string
		got  func() ([]string, error)
		want []string
	}{
		{"route list", func() ([]string, error) { return BuildKoNbctlCommand("lr-route-list", "vpc-onewater") },
			[]string{"kubectl", "ko", "nbctl", "lr-route-list", "vpc-onewater"}},
		{"list LRP", func() ([]string, error) {
			return BuildKoNbctlCommand("list Logical_Router_Port", "vpc-onewater-external")
		},
			[]string{"kubectl", "ko", "nbctl", "list", "Logical_Router_Port", "vpc-onewater-external"}},
		{"find binding", func() ([]string, error) {
			return BuildKoSbctlCommand("find Port_Binding logical_port", "vm-2.onewater.onewater.onewater.ovn")
		}, []string{"kubectl", "ko", "sbctl", "find", "Port_Binding", "logical_port=vm-2.onewater.onewater.onewater.ovn"}},
		{"iptables", func() ([]string, error) {
			return BuildNATGatewayIptablesCommand("kube-system", "vpc-nat-gw-one-water-nat-0")
		},
			[]string{"kubectl", "-n", "kube-system", "exec", "vpc-nat-gw-one-water-nat-0", "--", "iptables-save", "-t", "nat"}},
	} {
		got, err := c.got()
		if err != nil || !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: got %q, %v; want %q", c.name, got, err, c.want)
		}
	}
}

// TestInspectBuildersRejectHostileInput: names come from OVN and Kubernetes,
// which cluster users control; none may smuggle in another command or option.
func TestInspectBuildersRejectHostileInput(t *testing.T) {
	for _, obj := range []string{"", "a b", "x;reboot", "x'y", `x"y`, "$(id)", "a\nb", "--db=tcp:1.2.3.4:6641", "--db", "-v"} {
		if argv, err := BuildKoNbctlCommand("show", obj); err == nil {
			t.Errorf("nbctl show %q: accepted as %q", obj, argv)
		}
		if argv, err := BuildKoSbctlCommand("find Port_Binding logical_port", obj); err == nil {
			t.Errorf("sbctl find %q: accepted as %q", obj, argv)
		}
	}
	for _, q := range []string{"lr-route-add", "destroy Logical_Router", "show; ls", "set Chassis"} {
		if _, err := BuildKoNbctlCommand(q, "x"); err == nil {
			t.Errorf("query %q must not be allowed", q)
		}
		if _, err := BuildKoSbctlCommand(q, "x"); err == nil {
			t.Errorf("sb query %q must not be allowed", q)
		}
	}
	for _, ref := range [][2]string{{"kube-system", "-it"}, {"kube system", "p"}, {"ns", "p;id"}, {"NS", "p"}} {
		if argv, err := BuildNATGatewayIptablesCommand(ref[0], ref[1]); err == nil {
			t.Errorf("pod %v accepted as %q", ref, argv)
		}
	}
}

func TestWithKubeContext(t *testing.T) {
	exec := []string{"kubectl", "-n", "kube-system", "exec", "p", "--", "iptables-save", "-t", "nat"}
	if got, ok := WithKubeContext(exec, "hv-01"); !ok || got[1] != "--context" || got[2] != "hv-01" || got[3] != "-n" {
		t.Errorf("exec with context: %q %v", got, ok)
	}
	if got, ok := WithKubeContext(exec, ""); !ok || !reflect.DeepEqual(got, exec) {
		t.Errorf("no context must leave argv alone: %q", got)
	}
	if _, ok := WithKubeContext([]string{"kubectl", "ko", "nbctl", "show", "x"}, "hv-01"); ok {
		t.Errorf("kubectl ko cannot take --context")
	}
}
