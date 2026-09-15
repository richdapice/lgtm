package lens

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/richdapice/lgtm/internal/diffparse"
	"github.com/richdapice/lgtm/internal/finding"
)

const diff = `diff --git a/x.ts b/x.ts
--- a/x.ts
+++ b/x.ts
@@ -1,3 +1,4 @@
 const a = 1
+const pw = 'hunter2'
 const b = 2
 const c = 3
`

func parsed(t *testing.T) []diffparse.FileDiff {
	t.Helper()
	fs, err := diffparse.Parse(diff)
	if err != nil {
		t.Fatal(err)
	}
	return fs
}

func TestDecodeSalvagesAndValidates(t *testing.T) {
	out := json.RawMessage(`{"findings":[
	  {"lens":"security","path":"x.ts","line":2,"anchor":"const pw = 'hunter2'","severity":"block","rule":"Hardcoded Secret","body":"Secret in source."},
	  {"lens":"security","path":"x.ts","line":40,"anchor":"const pw = 'hunter2'","severity":"block","rule":"hardcoded-secret","body":"Same thing, wrong line."},
	  {"lens":"security","path":"x.ts","line":900,"anchor":"nothing","severity":"ask","rule":"far-away","body":"Nowhere near a hunk."},
	  {"lens":"perf","path":"x.ts","line":2,"anchor":"x","severity":"ask","rule":"r","body":"Lens not enabled."},
	  {"lens":"tests","path":"x.ts","line":2,"anchor":"x","severity":"critical","rule":"r","body":"Bad severity."},
	  {"lens":"tests","path":"x.ts","line":2,"anchor":"x","severity":"ask","rule":"r","body":"   "}
	]}`)
	fs, st, err := Decode(out, []string{"correctness", "security", "tests"}, parsed(t))
	if err != nil {
		t.Fatal(err)
	}
	if st.Received != 6 || st.Rejected != 3 {
		t.Fatalf("stats = %+v", st)
	}
	if len(fs) != 3 {
		t.Fatalf("kept %d findings: %+v", len(fs), fs)
	}
	if fs[0].Line != 2 || fs[0].Rule != "hardcoded-secret" {
		t.Fatalf("first = %+v", fs[0])
	}
	if fs[1].Line != 0 || st.Unanchored != 2 {
		t.Fatalf("line 40 (13 past hunk end) should be unanchored: %+v", fs[1])
	}
	if fs[2].Line != 0 {
		t.Fatalf("line 900 should be unanchored: %+v", fs[2])
	}
	// the mis-numbered duplicate has the same anchor+rule and must share the ID
	if fs[0].ID != fs[1].ID {
		t.Fatalf("same anchor+rule, different IDs: %s vs %s", fs[0].ID, fs[1].ID)
	}
}

func TestDecodeSnapsNearby(t *testing.T) {
	out := json.RawMessage(`{"findings":[{"lens":"correctness","path":"x.ts","line":5,"anchor":"const c = 3","severity":"ask","rule":"r","body":"one past the hunk"}]}`)
	fs, st, err := Decode(out, []string{"correctness"}, parsed(t))
	if err != nil || len(fs) != 1 {
		t.Fatal(err, fs)
	}
	if st.Snapped != 1 || fs[0].Line != 4 {
		t.Fatalf("line 5 should snap to 4: line=%d stats=%+v", fs[0].Line, st)
	}
}

func TestDecodeRejectsUnknownFields(t *testing.T) {
	out := json.RawMessage(`{"findings":[],"summary":"looks great!"}`)
	if _, _, err := Decode(out, nil, nil); err == nil {
		t.Fatal("unknown top-level field accepted")
	}
}

func TestSchemaIsValidJSON(t *testing.T) {
	var v any
	if err := json.Unmarshal(Schema, &v); err != nil {
		t.Fatal(err)
	}
}

func TestPromptIncludesLensesAndConventions(t *testing.T) {
	p := BuildPrompt(Input{Lenses: []string{"security", "tests"}, Diff: "DIFFBODY", Conventions: "No semicolons."})
	for _, want := range []string{"- security:", "- tests:", "No semicolons.", "DIFFBODY", "empty findings array"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt missing %q", want)
		}
	}
	if strings.Contains(p, "- correctness:") {
		t.Error("prompt included a lens that was not asked for")
	}
	_ = finding.Open
}

func TestDecodeVerifyDropsUnknownIDsAndFilesNew(t *testing.T) {
	out := json.RawMessage(`{"results":[
	  {"id":"known1","addressed":true,"note":"gone"},
	  {"id":"made-up","addressed":true,"note":"hallucinated"}
	],"new":[{"lens":"tests","path":"x.ts","line":0,"anchor":"","severity":"file","rule":"no-test","body":"No test covers this."}]}`)
	vs, newFs, err := DecodeVerify(out, map[string]bool{"known1": true}, []string{"tests"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vs) != 1 || vs[0].ID != "known1" || !vs[0].Addressed {
		t.Fatalf("verdicts = %+v", vs)
	}
	if len(newFs) != 1 || newFs[0].Rule != "no-test" {
		t.Fatalf("new = %+v", newFs)
	}
}

func TestSplitPR(t *testing.T) {
	title, body := SplitPR("\nFix the thing\n\nIt was broken.\n\n### What changed\n- **x**\n")
	if title != "Fix the thing" || !strings.HasPrefix(body, "It was broken.") {
		t.Fatalf("title=%q body=%q", title, body)
	}
}
