package main

import (
	"bytes"
	"runtime"
	"slices"
	"strings"
	"testing"
)

// stubLookPath pretends every binary is installed except the ones listed.
func stubLookPath(t *testing.T, missing ...string) {
	t.Helper()
	old := lookPath
	lookPath = func(f string) (string, error) {
		if slices.Contains(missing, f) {
			return "", &notFound{f}
		}
		return "/usr/bin/" + f, nil
	}
	t.Cleanup(func() { lookPath = old })
}

type notFound struct{ f string }

func (e *notFound) Error() string { return e.f + ": not found" }

func TestInspectRunNeedsDiag(t *testing.T) {
	m := selectRow(t, labModel(t), kLS, "subnet-onewater")
	m, cmd := m.startInspect()
	if cmd != nil || !m.flashErr || !strings.Contains(m.flash, "Tier 1") {
		t.Errorf("without --diag, ! must only explain the tier; flash=%q cmd=%v", m.flash, cmd != nil)
	}
	if strings.Contains(pane(m, kLS, "subnet-onewater", t), "! run") {
		t.Errorf("the pane must not offer ! without --diag")
	}
}

func TestInspectRunOneOrPick(t *testing.T) {
	stubLookPath(t)
	m := labModelDiag(t, true)

	// One runnable command: it runs straight away (the TUI is suspended).
	one := selectRow(t, m, kLS, "subnet-onewater")
	if got, cmd := one.startInspect(); cmd == nil || got.inspectPick {
		t.Errorf("a single command must run without a picker, flash=%q", got.flash)
	}

	// Next to a NAT gateway there are two: the picker opens and lists both.
	m = withNATGateway(t, m)
	two := selectRow(t, m, kLS, "subnet-onewater")
	two, cmd := two.startInspect()
	if cmd != nil || !two.inspectPick {
		t.Fatalf("two commands must open the picker")
	}
	view := strings.Join(two.inspectPickLines(80), "\n")
	for _, want := range []string{"1 kubectl ko nbctl show subnet-onewater", "2 kubectl -n kube-system exec vpc-nat-gw-one-water-nat-0"} {
		if !strings.Contains(view, want) {
			t.Errorf("picker: expected %q in\n%s", want, view)
		}
	}
	if r, cmd := two.updateInspectPick(keyMsg("2")); cmd == nil || r.(model).inspectPick {
		t.Errorf("2 must run the second command and close the picker")
	}
	if r, _ := two.updateInspectPick(keyMsg("esc")); r.(model).inspectPick {
		t.Errorf("esc must close the picker")
	}
}

func TestInspectExecTargetsTheClusterOnScreen(t *testing.T) {
	stubLookPath(t)
	iptables := inspectCmd{argv: []string{"kubectl", "-n", "kube-system", "exec", "p-0", "--", "iptables-save", "-t", "nat"}}
	ko := inspectCmd{argv: []string{"kubectl", "ko", "nbctl", "show", "join"}}

	m := model{kube: kubeTarget{kubeconfig: "/tmp/hv-01.yaml", context: "hv-01", currentContext: "rancher"}}
	pc, err := m.inspectExec(iptables)
	if err != nil || !slices.Equal(pc.argv[:3], []string{"kubectl", "--context", "hv-01"}) {
		t.Errorf("kubectl exec must be pinned to --context: %q %v", pc.argv, err)
	}
	if !slices.Contains(pc.env, "KUBECONFIG=/tmp/hv-01.yaml") {
		t.Errorf("the child must use --kubeconfig")
	}
	if _, err := m.inspectExec(ko); err == nil || !strings.Contains(err.Error(), "current context (rancher)") {
		t.Errorf("kubectl ko under another --context must be refused, got %v", err)
	}
	m.kube.currentContext = "hv-01"
	if pc, err := m.inspectExec(ko); err != nil || !slices.Equal(pc.argv, ko.argv) {
		t.Errorf("kubectl ko on the current context must run unchanged: %q %v", pc.argv, err)
	}

	stubLookPath(t, "kubectl-ko")
	if _, err := m.inspectExec(ko); err == nil || !strings.Contains(err.Error(), "plugin not found") {
		t.Errorf("a missing kubectl-ko must be reported, got %v", err)
	}
	if _, err := m.inspectExec(inspectCmd{text: "ovn-nbctl show x"}); err == nil {
		t.Errorf("display-only lines must not run")
	}
}

func TestPausedCmdReportsExitAndWaits(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses sh")
	}
	var out bytes.Buffer
	pc := &pausedCmd{argv: []string{"sh", "-c", "echo hello; exit 3"}}
	pc.SetStdin(strings.NewReader("\n"))
	pc.SetStdout(&out)
	pc.SetStderr(&out)
	if err := pc.Run(); err != nil || pc.code != 3 {
		t.Fatalf("Run: err=%v code=%d", err, pc.code)
	}
	for _, want := range []string{"$ sh -c echo hello; exit 3", "hello", "[exit 3] press Enter to return"} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("expected %q in %q", want, out.String())
		}
	}
}
