package main

// `!` runs an Inspect line of the details pane, k9s-plugin style: the TUI is
// suspended, the command owns the terminal, and Enter brings the TUI back.
// Tier 1 (--diag), Kube-OVN only, and only argv built by pkg/diag's typed
// read-only builders: the text shown is exactly what runs (ADR-0001).

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/coulof/kvovn-topo/pkg/diag"
)

// kubeTarget is the cluster the TUI shows, so a run hits the same one.
type kubeTarget struct {
	kubeconfig     string // --kubeconfig (else kubectl's defaults)
	context        string // --context (else the current context)
	currentContext string // the kubeconfig's current-context
}

type inspectDoneMsg struct {
	text string
	code int
	err  error
}

// runnableInspect is the selection's runnable Inspect commands, in pane order.
func (m model) runnableInspect() []inspectCmd {
	n := m.selectedXNode()
	if n == nil {
		return nil
	}
	var out []inspectCmd
	for _, c := range m.inspectCommands(n) {
		if c.argv != nil {
			out = append(out, c)
		}
	}
	return out
}

// startInspect is the `!` key: run the only command, or open the picker.
func (m model) startInspect() (model, tea.Cmd) {
	if !m.diagMode {
		m.setFlash("running commands is Tier 1: start kvovn-topo with --diag", true)
		return m, nil
	}
	cmds := m.runnableInspect()
	switch len(cmds) {
	case 0:
		m.setFlash("nothing to run here (Inspect commands run on Kube-OVN only)", false)
		return m, nil
	case 1:
		return m.runInspect(cmds[0])
	}
	if !m.showPane() { // the picker draws in the details pane
		m.setFlash(fmt.Sprintf("%d commands: open the details pane (i, %d+ columns) to pick one", len(cmds), paneMinW), false)
		return m, nil
	}
	m.inspectPick, m.inspectSel = true, 0
	return m, nil
}

// updateInspectPick handles keys while the picker is open.
func (m model) updateInspectPick(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	cmds := m.runnableInspect()
	s := k.String()
	switch {
	case s == "esc" || s == "q" || len(cmds) == 0:
		m.inspectPick = false
	case s == "j" || s == "down":
		m.inspectSel = min(m.inspectSel+1, len(cmds)-1)
	case s == "k" || s == "up":
		m.inspectSel = max(m.inspectSel-1, 0)
	case s == "enter":
		m.inspectPick = false
		return m.runInspect(cmds[min(m.inspectSel, len(cmds)-1)])
	case len(s) == 1 && s[0] >= '1' && s[0] <= '9':
		if i := int(s[0] - '1'); i < len(cmds) {
			m.inspectPick = false
			return m.runInspect(cmds[i])
		}
	}
	return m, nil
}

// inspectPickLines renders the picker in place of the details pane body.
func (m model) inspectPickLines(w int) []string {
	st := m.styles
	out := []string{"", " " + st.Value.Render("Run which command?"), ""}
	for i, c := range m.runnableInspect() {
		line := st.Key.Render(fmt.Sprint(i+1)) + " " + c.text
		if i == m.inspectSel {
			line = solid(line, w-1, st.Selected)
		}
		out = append(out, " "+fit(line, w-1))
	}
	return append(out, "", "   "+st.MutedText.Render("enter/1-9 run (the TUI is suspended) · esc cancel"))
}

func (m model) runInspect(c inspectCmd) (model, tea.Cmd) {
	pc, err := m.inspectExec(c)
	if err != nil {
		m.setFlash(err.Error(), true)
		return m, nil
	}
	return m, tea.Exec(pc, func(err error) tea.Msg { return inspectDoneMsg{text: c.text, code: pc.code, err: err} })
}

// lookPath is exec.LookPath, replaceable in tests.
var lookPath = exec.LookPath

// inspectExec prepares c for the user's kubeconfig and context, or says why it
// cannot run.
func (m model) inspectExec(c inspectCmd) (*pausedCmd, error) {
	if len(c.argv) < 2 || c.argv[0] != "kubectl" {
		return nil, fmt.Errorf("not runnable: %s", c.text)
	}
	argv, ok := diag.WithKubeContext(c.argv, m.kube.context)
	if !ok && m.kube.context != m.kube.currentContext {
		return nil, fmt.Errorf("kubectl ko uses the kubeconfig's current context (%s), not --context %s",
			firstNonEmpty(m.kube.currentContext, "none"), m.kube.context)
	}
	if _, err := lookPath("kubectl"); err != nil {
		return nil, errors.New("kubectl not found in PATH")
	}
	if argv[1] == "ko" {
		if _, err := lookPath("kubectl-ko"); err != nil {
			return nil, errors.New("kubectl ko plugin not found in PATH (Kube-OVN's kubectl-ko)")
		}
	}
	env := os.Environ()
	if m.kube.kubeconfig != "" {
		env = append(env, "KUBECONFIG="+m.kube.kubeconfig)
	}
	return &pausedCmd{argv: argv, env: env}, nil
}

// pausedCmd runs argv on the terminal, then waits for Enter so the output
// stays readable before the TUI redraws. No shell is involved.
type pausedCmd struct {
	argv     []string
	env      []string
	stdin    io.Reader
	out, err io.Writer
	code     int
}

func (p *pausedCmd) SetStdin(r io.Reader)  { p.stdin = r }
func (p *pausedCmd) SetStdout(w io.Writer) { p.out = w }
func (p *pausedCmd) SetStderr(w io.Writer) { p.err = w }

func (p *pausedCmd) Run() error {
	_, _ = fmt.Fprintf(p.out, "$ %s\n\n", strings.Join(p.argv, " "))
	cmd := exec.Command(p.argv[0], p.argv[1:]...)
	cmd.Env, cmd.Stdin, cmd.Stdout, cmd.Stderr = p.env, p.stdin, p.out, p.err
	err := cmd.Run()
	var exit *exec.ExitError
	switch {
	case errors.As(err, &exit):
		p.code, err = exit.ExitCode(), nil
	case err != nil:
		p.code = -1
	}
	_, _ = fmt.Fprintf(p.out, "\n[exit %d] press Enter to return to kvovn-topo", p.code)
	if p.stdin != nil {
		_, _ = bufio.NewReader(p.stdin).ReadString('\n')
	}
	return err
}
