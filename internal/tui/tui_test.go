package tui

import (
	"testing"

	"github.com/richdapice/lgtm/internal/diffparse"
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
