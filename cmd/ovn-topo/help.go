package main

// The help screen (`?`): This (the selected object and the current view),
// Glossary, Keys. Content is in helptext.go.

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

var helpTabs = []string{"This", "Glossary", "Keys"}

func (m model) helpView() []string {
	st := m.styles
	w := m.w() - 2
	var tabs []string
	for i, t := range helpTabs {
		label := fmt.Sprintf(" %d %s ", i+1, t)
		if i == m.helpTab {
			tabs = append(tabs, st.Pill.Render(label))
		} else {
			tabs = append(tabs, st.MutedText.Render(label))
		}
	}
	head := " " + strings.Join(tabs, " ") + st.MutedText.Render("   ←/→ tab · j/k scroll · esc close")
	var body []string
	switch m.helpTab {
	case 0:
		body = m.helpThisLines(w)
	case 1:
		body = m.helpGlossaryLines(w)
	default:
		body = m.helpKeyLines()
	}
	h := max(m.bodyH()-2, 1)
	off := min(m.helpOff, max(len(body)-h, 0))
	end := min(off+h, len(body))
	lines := append([]string{head, ""}, body[off:end]...)
	return box(lines, m.w(), m.areaH(), st.Label.Render("Help"), st.Border)
}

// para renders a labelled paragraph wrapped to w.
func (m model) para(label, text string, w int) []string {
	st := m.styles
	out := []string{" " + st.Value.Render(label)}
	for _, l := range wrapText(plain(text), w-4) {
		out = append(out, "   "+l)
	}
	return out
}

func (m model) helpThisLines(w int) []string {
	st := m.styles
	out := m.para("This view", viewHelp[m.mode], w)
	n := m.selectedXNode()
	c, ok := concepts[m.conceptKey(n)]
	if !ok {
		return append(out, "", " "+st.MutedText.Render("Select an object to see what it is."))
	}
	out = append(out, "", " "+st.Label.Render(c.title)+st.MutedText.Render("  "+n.name), "")
	out = append(out, m.para("In OVN", c.ovn, w)...)
	out = append(out, m.para("In Kube-OVN", c.kubeOVN, w)...)
	out = append(out, m.para("Reading the pane", c.read, w)...)
	out = append(out, m.para("What goes wrong", c.problems, w)...)
	return append(out, "", " "+st.MutedText.Render("More: "+conceptsURL+"#"+c.anchor))
}

func (m model) helpGlossaryLines(w int) []string {
	st := m.styles
	out := []string{" " + st.MutedText.Render(pad("", 17)+pad("Kube-OVN", 28)+pad("OVN", 30)+"ovn-kubernetes")}
	for _, g := range glossary {
		out = append(out, fit(" "+st.Value.Render(pad(g.term, 17))+pad(g.kubeOVN, 28)+pad(g.ovn, 30)+g.ovnk, w))
		for _, l := range wrapText(g.text, w-21) {
			out = append(out, " "+pad("", 17)+st.MutedText.Render(l))
		}
	}
	return append(out, "", " "+st.MutedText.Render("More: "+conceptsURL))
}

// plain drops the Markdown marks the entries share with docs/concepts.md.
func plain(s string) string { return strings.NewReplacer("`", "", "*", "").Replace(s) }

func (m model) updateHelp(s string) (tea.Model, tea.Cmd) {
	switch s {
	case "esc", "q", "?":
		m.scr = scrMain
	case "right", "l", "tab":
		m.helpTab, m.helpOff = (m.helpTab+1)%len(helpTabs), 0
	case "left", "h", "shift+tab":
		m.helpTab, m.helpOff = (m.helpTab+len(helpTabs)-1)%len(helpTabs), 0
	case "1", "2", "3":
		m.helpTab, m.helpOff = int(s[0]-'1'), 0
	case "j", "down":
		m.helpOff++
	case "k", "up":
		m.helpOff = max(m.helpOff-1, 0)
	case "g":
		m.helpOff = 0
	}
	return m, nil
}
