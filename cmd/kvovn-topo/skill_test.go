package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunSkill(t *testing.T) {
	var b bytes.Buffer
	if rc := runSkill(&b); rc != 0 {
		t.Fatalf("rc = %d", rc)
	}
	if !strings.HasPrefix(b.String(), "---\nname: kvovn-topo\n") {
		t.Errorf("missing frontmatter: %.40q", b.String())
	}
}
