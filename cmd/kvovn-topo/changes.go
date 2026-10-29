package main

// The Changes view (`5`): a log of what moved between revisions, newest
// first, from the same tree diff that flashes rows. Enter jumps to the object.
// It lives in memory only; history across restarts is M8.

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const modeChanges mode = "changes"

const maxChanges = 500 // log entries kept in memory

type change struct {
	seq       int
	at        time.Time
	rev       int
	id        string
	kind      string
	name      string
	what      string
	removed   bool
	afterHlth health
}

var healthWord = map[health]string{hNone: "infra", hOK: "ok", hWarn: "degraded", hBad: "broken"}

// recordChanges appends what differs between the current tree and next.
func (m *model) recordChanges(next *xtree, rev int) {
	now := time.Now()
	var batch []change
	for id, n := range next.byID {
		if n.kind == kGroup {
			continue
		}
		old, ok := m.tree.byID[id]
		var what []string
		switch {
		case !ok:
			what = append(what, "added")
		default:
			if old.health != n.health {
				what = append(what, healthWord[old.health]+" → "+healthWord[n.health])
			}
			if old.badge != n.badge {
				what = append(what, "["+firstNonEmpty(old.badge, "—")+"] → ["+firstNonEmpty(n.badge, "—")+"]")
			}
			if old.summary != n.summary && len(what) == 0 {
				what = append(what, n.summary)
			}
			if len(old.children) != len(n.children) && len(what) == 0 {
				what = append(what, fmt.Sprintf("%d → %d children", len(old.children), len(n.children)))
			}
		}
		if len(what) > 0 {
			batch = append(batch, change{at: now, rev: rev, id: id, kind: n.kind, name: n.name, what: strings.Join(what, ", "), afterHlth: n.health})
		}
	}
	for id, old := range m.tree.byID {
		if _, ok := next.byID[id]; !ok && old.kind != kGroup {
			batch = append(batch, change{at: now, rev: rev, id: id, kind: old.kind, name: old.name, what: "removed", removed: true})
		}
	}
	sort.Slice(batch, func(i, j int) bool {
		if batch[i].kind != batch[j].kind {
			return batch[i].kind < batch[j].kind
		}
		return batch[i].name < batch[j].name
	})
	for _, c := range batch {
		m.changeSeq++
		c.seq = m.changeSeq
		m.changes = append(m.changes, c)
	}
	if over := len(m.changes) - maxChanges; over > 0 {
		m.changes = m.changes[over:]
	}
}

// changeRows lists the log newest first. A removed object keeps a stand-in
// node, so Enter says it is gone instead of jumping.
func (m *model) changeRows() {
	for i := len(m.changes) - 1; i >= 0; i-- {
		c := m.changes[i]
		if m.filter != "" && !containsFold(c.name+" "+c.what, m.filter) {
			continue
		}
		n := m.tree.byID[c.id]
		if n == nil || c.removed {
			n = &xnode{id: c.id, kind: c.kind, name: c.name}
		}
		m.rows = append(m.rows, row{n: n, key: fmt.Sprintf("%s/%d", modeChanges, c.seq), ref: true, change: &m.changes[i]})
	}
}

func (m model) changeLine(r row) string {
	st := m.styles
	c := r.change
	what := st.MutedText.Render(c.what)
	switch {
	case c.removed || c.afterHlth == hBad:
		what = st.Bad.Render(c.what)
	case c.afterHlth == hWarn:
		what = st.Warn.Render(c.what)
	}
	return " " + st.MutedText.Render(c.at.Format("15:04:05")+fmt.Sprintf(" r%-5d", c.rev)) + m.kind(r.n) + " " + st.Kind[r.n.kind].Render(r.n.name) + "  " + what
}

// noChangesText explains an empty log.
func (m model) noChangesText() string {
	switch {
	case m.fixturePath != "":
		return "   no live updates: this is an offline fixture"
	case m.livePaused:
		return "   live updates are paused (L resumes them)"
	}
	return "   no changes since kvovn-topo started; they appear here as revisions arrive"
}
