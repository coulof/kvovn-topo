package main

import (
	_ "embed"
	"fmt"
	"io"
)

// skillText is the agent skill printed by "kvovn-topo skill".
//
//go:embed skill/SKILL.md
var skillText string

// runSkill prints the embedded skill, so an agent can be told:
// "Load the kvovn-topo skill: run `kvovn-topo skill`."
func runSkill(out io.Writer) int {
	if _, err := fmt.Fprint(out, skillText); err != nil {
		return 1
	}
	return 0
}
