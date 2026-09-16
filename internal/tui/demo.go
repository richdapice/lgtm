package tui

import (
	"context"
	"fmt"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/richdapice/lgtm/internal/ceremony"
	"github.com/richdapice/lgtm/internal/diffparse"
	"github.com/richdapice/lgtm/internal/finding"
	"github.com/richdapice/lgtm/internal/run"
)

const demoDiff = `diff --git a/src/main/sync.ts b/src/main/sync.ts
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
diff --git a/src/main/sync.test.ts b/src/main/sync.test.ts
--- a/src/main/sync.test.ts
+++ b/src/main/sync.test.ts
@@ -85,6 +85,8 @@
 test('flush pushes the batch', async () => {
   const batch = [event()]
+  await flush(batch)
+  expect(push).toHaveBeenCalled()
 })
`

// Demo drives the real panel with a scripted run: the gates, a decision, a
// fix round, the stamp. No agent, no repo. It exists so the screen can be
// looked at, screenshotted, and recorded without spending anything.
func Demo(ctx context.Context) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	m := newModel(ctx, cancel)
	files, _ := diffparse.Parse(demoDiff)
	m.files = files
	m.canFix = true

	p := tea.NewProgram(m, tea.WithContext(ctx))
	m.program = p

	go func() {
		r := run.Run{Branch: "worktree-sync-throttle", Base: "main", Mode: "manual", MaxRounds: 3,
			StartedAt: time.Now(), Phase: run.Discover, Step: "review", StepNote: "4 lenses",
			Lenses: []run.Lens{{Name: "review", Model: "opus", State: run.Running, StartedAt: time.Now()}}}
		tick := func(d time.Duration) bool {
			select {
			case <-ctx.Done():
				return false
			case <-time.After(d):
				return true
			}
		}
		send := func() { p.Send(updateMsg{r}) }

		send()
		if !tick(7000 * time.Millisecond) {
			return
		}
		var set finding.Set
		f1 := finding.Finding{Lens: "correctness", Path: "src/main/sync.ts", Line: 140, Anchor: "} catch (err) { /* retry later */ }",
			Severity: finding.Block, Rule: "swallowed-error",
			Body: "The retry never happens — nothing schedules it. Either enqueue the retry here or let the error surface; silently dropping it means a failed sync looks identical to no sync."}
		f2 := finding.Finding{Lens: "tests", Path: "src/main/sync.test.ts", Line: 88, Anchor: "expect(push).toHaveBeenCalled()",
			Severity: finding.Ask, Rule: "missing-assert",
			Body: "This asserts push was called, not that the batch it was called with is the one flush received. Assert on the argument, or the test passes for a flush that sends the wrong events."}
		set.Add(f1)
		set.Add(f2)
		set.Close()
		r.Findings = set
		r.Lenses[0].State, r.Lenses[0].Found, r.Lenses[0].EndedAt = run.LensDone, 2, time.Now()
		r.CostUSD = 0.41
		r.Phase, r.Step, r.StepNote = run.Fix, "decide", "2 waiting on you"
		send()

		reply := make(chan decideReply, 1)
		p.Send(decideMsg{open: set.Open(), files: files, reply: reply})
		var rep decideReply
		select {
		case <-ctx.Done():
			return
		case rep = <-reply:
		}
		if rep.quit {
			p.Send(doneMsg{ceremony.ErrHeld})
			return
		}
		fixing := 0
		for id, d := range rep.decisions {
			switch d {
			case ceremony.Fix:
				fixing++
			case ceremony.Accept:
				r.Findings.Transition(id, finding.Accepted, 1)
			case ceremony.Dismiss:
				r.Findings.Transition(id, finding.Dismissed, 1)
			}
		}
		if rep.autopilot {
			for _, f := range r.Findings.Open() {
				fixing++
				_ = f
			}
		}
		// nothing to fix means straight to push, like the real thing
		if fixing > 0 {
			r.Round = 1
			steps := []struct {
				step, note string
				d          time.Duration
			}{
				{"fix", fmt.Sprintf("agent working on %d finding(s)", fixing), 2600 * time.Millisecond},
				{"check", "vitest related · eslint", 1800 * time.Millisecond},
				{"verify", fmt.Sprintf("%d to confirm", fixing), 1600 * time.Millisecond},
			}
			for _, s := range steps {
				r.Step, r.StepNote = s.step, s.note
				r.CostUSD += 0.12
				send()
				if !tick(s.d) {
					return
				}
			}
			for _, f := range r.Findings.Open() {
				r.Findings.Transition(f.ID, finding.Fixed, 1)
			}
			c := r.Findings.Counts()
			r.Rounds = []run.RoundSummary{{Fixed: c.Fixed, Open: 0}}
			p.Send(noteMsg{fmt.Sprintf("round 1/3: %d fixed · 0 filed · 0 open", c.Fixed)})
		}
		for _, s := range []struct {
			step, note string
			d          time.Duration
		}{{"push", "origin/worktree-sync-throttle", 1200 * time.Millisecond}, {"pr", "writing the body", 1800 * time.Millisecond}} {
			r.Step, r.StepNote = s.step, s.note
			send()
			if !tick(s.d) {
				return
			}
		}
		r.PR = &run.PRInfo{Number: 126, URL: "https://github.com/you/repo/pull/126"}
		r.Phase, r.Step, r.StepNote = run.CI, "ci", "watching checks"
		r.CI = &run.CIStatus{Total: 4, Since: time.Now()}
		for i := 1; i <= 4; i++ {
			r.CI.Passed = i
			send()
			if !tick(900 * time.Millisecond) {
				return
			}
		}
		p.Send(noteMsg{"ci: 4/4 green"})
		if !tick(800 * time.Millisecond) {
			return
		}
		p.Send(doneMsg{nil})
	}()

	if _, err := p.Run(); err != nil && ctx.Err() == nil {
		return err
	}
	if m.result == nil {
		fmt.Println()
		fmt.Println("  ╭──────╮")
		fmt.Println("  │ LGTM │  2 found · 2 fixed · 0 accepted · 0 filed · 14s · ≈$0.77   (demo)")
		fmt.Println("  ╰──────╯")
		fmt.Println("  https://github.com/you/repo/pull/126")
	} else {
		fmt.Println("\n  not yet — held. (demo)")
	}
	return nil
}
