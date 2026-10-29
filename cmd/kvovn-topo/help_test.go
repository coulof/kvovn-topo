package main

import (
	"os"
	"regexp"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/x/ansi"
)

// TestEveryObjectHasHelp: every object the dev fixture and the lab capture
// (with a NAT gateway) can show has a concept entry, and every entry's link
// lands on a heading of docs/concepts.md.
func TestEveryObjectHasHelp(t *testing.T) {
	for _, m := range []model{loaded(t, 140, 40, false), withNATGateway(t, labModel(t))} {
		for _, n := range m.tree.byID {
			if n.kind == kGroup || n.kind == kNS {
				continue
			}
			if _, ok := concepts[m.conceptKey(n)]; !ok {
				t.Errorf("no help for %s %s (key %q)", n.kind, n.name, m.conceptKey(n))
			}
		}
	}
	doc, err := os.ReadFile("../../docs/concepts.md")
	if err != nil {
		t.Fatal(err)
	}
	anchors := map[string]bool{}
	for _, h := range regexp.MustCompile(`(?m)^## (.+)$`).FindAllStringSubmatch(string(doc), -1) {
		anchors[strings.ReplaceAll(strings.ToLower(regexp.MustCompile(`[^\w\- ]`).ReplaceAllString(h[1], "")), " ", "-")] = true
	}
	for k, c := range concepts {
		if !anchors[c.anchor] {
			t.Errorf("%s: docs/concepts.md has no #%s", k, c.anchor)
		}
	}
}

func TestHelpTabs(t *testing.T) {
	m := press(selectRow(t, withNATGateway(t, labModel(t)), kLSP, "vpc-nat-gw-one-water-nat-0.subnet-onewater"), "?")
	this := ansi.Strip(m.View())
	for _, want := range []string{"1 This", "VPC NAT gateway interface", "In Kube-OVN", "iptables", "concepts.md#vpc-nat-gateway"} {
		if !strings.Contains(this, want) {
			t.Errorf("This tab: expected %q", want)
		}
	}
	if strings.Contains(this, "`") {
		t.Errorf("Markdown backticks must not reach the terminal")
	}
	if g := ansi.Strip(press(m, "2").View()); !strings.Contains(g, "src-ip route") || !strings.Contains(g, "ovn-kubernetes") {
		t.Errorf("Glossary tab missing entries:\n%s", g)
	}
	if k := ansi.Strip(press(m, "right", "right").View()); !strings.Contains(k, "XRAY ROOTS") {
		t.Errorf("→ twice must reach the Keys tab")
	}
	if got := press(m, "x"); got.scr != scrHelp {
		t.Errorf("an unrelated key must not close help")
	}
	if got := press(m, "esc"); got.scr != scrMain {
		t.Errorf("esc must close help")
	}
	for _, size := range [][2]int{{80, 24}, {60, 14}} {
		mm := send(m, tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		for i, l := range strings.Split(mm.View(), "\n") {
			if w := ansi.StringWidth(l); w > size[0] {
				t.Errorf("%dx%d: help line %d is %d wide", size[0], size[1], i, w)
			}
		}
	}
}
