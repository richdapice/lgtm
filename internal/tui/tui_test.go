package tui

import (
	"testing"

	"github.com/richdapice/lgtm/internal/ceremony"
	"github.com/richdapice/lgtm/internal/diffparse"
	"github.com/richdapice/lgtm/internal/finding"
)

const diff = `diff --git a/x.ts b/x.ts
--- a/x.ts
+++ b/x.ts
@@ -1,6 +1,7 @@
 const a = 1
 const b = 2
+const pw = 'hunter2'
 const c = 3
 const d = 4
 const e = 5
 const f = 6
`

func TestHunkAround(t *testing.T) {
	files, err := diffparse.Parse(diff)
	if err != nil {
		t.Fatal(err)
	}
	lines, idx := hunkAround(files, "x.ts", 3, 2)
	if len(lines) != 5 || idx != 2 || lines[idx].Text != "const pw = 'hunter2'" {
		t.Fatalf("lines=%d idx=%d", len(lines), idx)
	}
	if _, idx := hunkAround(files, "x.ts", 0, 2); idx != -1 {
		t.Fatal("unanchored finding should have no hunk")
	}
	if _, idx := hunkAround(files, "nope.ts", 3, 2); idx != -1 {
		t.Fatal("unknown path should have no hunk")
	}
}

func TestPreselectFollowsJev(t *testing.T) {
	m := &model{canFix: true, marks: map[string]ceremony.Decision{}}
	m.open = []finding.Finding{
		{ID: "a", Triage: &finding.Triage{Suggest: "accept"}},
		{ID: "b"},
		{ID: "c", Triage: &finding.Triage{Suggest: "fix"}},
		{ID: "d", Triage: &finding.Triage{Suggest: "dismiss"}},
	}
	m.cursor = 0
	m.preselect()
	if actions[m.action].d != ceremony.Accept {
		t.Fatalf("finding a: action %d", m.action)
	}
	m.cursor = 1
	m.preselect()
	if actions[m.action].d != ceremony.Fix {
		t.Fatalf("unscored finding should default to Fix, got %d", m.action)
	}
	m.marks["c"] = ceremony.Dismiss
	m.cursor = 2
	m.preselect()
	if actions[m.action].d != ceremony.Dismiss {
		t.Fatalf("a decision already made wins over jev, got %d", m.action)
	}
	m.cursor = 3
	m.preselect()
	if actions[m.action].d != ceremony.Accept {
		t.Fatalf("dismiss is permanent and must never be pre-selected, got %d", m.action)
	}
	m.canFix = false
	m.cursor = 1
	m.preselect()
	if actions[m.action].d != ceremony.Accept {
		t.Fatalf("no fixer: Fix must not be preselected, got %d", m.action)
	}
}
