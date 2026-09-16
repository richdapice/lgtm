package tui

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/richdapice/lgtm/internal/ceremony"
	"github.com/richdapice/lgtm/internal/diffparse"
	"github.com/richdapice/lgtm/internal/finding"
	"github.com/richdapice/lgtm/internal/run"
)

const sample = `diff --git a/src/main/sync.ts b/src/main/sync.ts
--- a/src/main/sync.ts
+++ b/src/main/sync.ts
@@ -136,6 +136,9 @@
 export async function flush(batch: Event[]) {
   const started = Date.now()
+  try {
+    await push(batch)
+  } catch (err) { /* retry later */ }
   log(started)
 }
`

// TestReviewFrame renders the panel with color off. Run with -v to see it;
// the README's hero frame is this output.
func TestReviewFrame(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	files, _ := diffparse.Parse(sample)
	m := newModel(context.Background(), func() {})
	m.width = 96
	m.files = files
	m.canFix = true
	m.run = run.Run{Branch: "worktree-sync-throttle", Base: "main", CostUSD: 0.41}
	m.open = []finding.Finding{
		{ID: "a1", Severity: finding.Block, Lens: "correctness", Path: "src/main/sync.ts", Line: 140, Rule: "swallowed-error",
			Body: "The retry never happens — nothing schedules it. Either enqueue the retry here or let the error surface; silently dropping it means a failed sync looks identical to no sync."},
		{ID: "b2", Severity: finding.Ask, Lens: "tests", Path: "src/main/sync.test.ts", Line: 88, Rule: "missing-assert", Body: "x"},
	}
	m.pending = &decideMsg{}
	m.marks = map[string]ceremony.Decision{}
	out := m.View()
	if os.Getenv("SHOW") != "" {
		fmt.Println(out)
	}
	for _, want := range []string{"FINDINGS", "▶ 1", "[ undecided ]", "FINDING 1 · correctness", "▶  140", "WHAT DO YOU WANT TO DO WITH FINDING 1?", "▶ Fix"} {
		if !contains(out, want) {
			t.Errorf("frame missing %q\n%s", want, out)
		}
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// TestProgressFrame is the between-decisions view: gates and rounds.
func TestProgressFrame(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	m := newModel(context.Background(), func() {})
	m.width = 96
	var set finding.Set
	for i := 0; i < 7; i++ {
		set.Add(finding.Finding{Path: "a", Anchor: fmt.Sprint(i), Rule: "r", Severity: finding.Ask})
	}
	set.Close()
	for i := 0; i < 3; i++ {
		set.Transition(set.Findings[i].ID, finding.Fixed, 1)
	}
	set.Transition(set.Findings[3].ID, finding.Accepted, 1)
	m.run = run.Run{Branch: "worktree-sync-throttle", Base: "main", CostUSD: 0.62, Phase: run.Fix,
		Step: "fix", StepNote: "agent working on 3 finding(s)", Round: 2, MaxRounds: 3,
		Lenses: []run.Lens{{Name: "review"}}, Findings: set,
		Rounds: []run.RoundSummary{{Fixed: 3, Filed: 1, Open: 3}}}
	out := m.View()
	if os.Getenv("SHOW") != "" {
		fmt.Println(out)
	}
	for _, want := range []string{"GATES", "✓ review", "✓ decide", "fix       agent working", "○ check", "ROUNDS", "round 1: 3 fixed"} {
		if !contains(out, want) {
			t.Errorf("frame missing %q\n%s", want, out)
		}
	}
}

func TestSplashFrame(t *testing.T) {
	lipgloss.SetColorProfile(termenv.Ascii)
	m := newModel(context.Background(), func() {})
	m.width = 96
	m.frame = 260 // fully decoded
	m.run = run.Run{Branch: "b", Base: "main", Step: "review", StepNote: "4 lenses",
		Lenses: []run.Lens{{Name: "review", State: run.Running}}}
	out := m.View()
	if os.Getenv("SHOW") != "" {
		fmt.Println(out)
	}
	for _, want := range []string{"███████╗", "> review · 4 lenses", "q cancel"} {
		if !contains(out, want) {
			t.Errorf("splash missing %q\n%s", want, out)
		}
	}
	// early frames are mostly noise: the mark must not be fully there yet
	m.frame = 2
	if contains(m.View(), "██╗      ██████╗ ████████╗███╗   ███╗") {
		t.Error("mark decoded instantly")
	}
}
