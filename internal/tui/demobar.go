package tui

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/richdapice/lgtm/internal/finding"
	"github.com/richdapice/lgtm/internal/render"
	"github.com/richdapice/lgtm/internal/run"
)

// DemoBar plays the status bar through a scripted run: review, the gate
// track, waiting on you, CI, idle. It redraws in place so a recording of it
// looks like the bar does inside Claude Code.
// DemoBarOptions picks the review shape the scripted run draws.
type DemoBarOptions struct {
	Parallel bool // one row per lens, filling at different rates
	Passes   int  // review rows in sequence (batch dispatch); 1 = the default
}

func DemoBar(ctx context.Context, opt DemoBarOptions) error {
	cols := 100
	if c := os.Getenv("COLUMNS"); c != "" {
		fmt.Sscanf(c, "%d", &cols)
	}
	st := render.Style{Cols: cols, Color: os.Getenv("NO_COLOR") == ""}
	now := time.Now()
	plan := &render.PlanUsage{FiveHourPct: 37, SevenDayPct: 61, FiveHourReset: now.Add(2*time.Hour + 23*time.Minute), SevenDayReset: now.Add(76 * time.Hour)}
	if opt.Passes < 1 {
		opt.Passes = 1
	}
	r := &run.Run{Branch: "worktree-sync-throttle", Base: "main", Mode: "auto", MaxRounds: 3, PID: os.Getpid(),
		StartedAt: now, Phase: run.Discover, Step: "floor", StepNote: "2 changed files"}
	switch {
	case opt.Parallel:
		for _, l := range []struct{ name, model string }{{"correctness", "opus"}, {"conventions", "haiku"}, {"security", "opus"}, {"tests", "sonnet"}} {
			r.Lenses = append(r.Lenses, run.Lens{Name: l.name, Model: l.model, State: run.Pending})
		}
	case opt.Passes > 1:
		models := []string{"sonnet", "opus", "opus"}
		for i := 0; i < opt.Passes; i++ {
			r.Lenses = append(r.Lenses, run.Lens{Name: fmt.Sprintf("review %d", i+1), Model: models[i%len(models)], State: run.Pending})
		}
	default:
		r.Lenses = []run.Lens{{Name: "review", Model: "opus", State: run.Running, StartedAt: now}}
	}
	var set finding.Set
	for i := 0; i < 7; i++ {
		set.Add(finding.Finding{Path: "src/main/sync.ts", Anchor: fmt.Sprint(i), Rule: "r", Severity: finding.Ask})
	}
	hist := []run.Summary{}
	for i := 0; i < 20; i++ {
		hist = append(hist, run.Summary{EndedAt: now, Duration: time.Duration(90+(i*37)%160) * time.Second, Outcome: run.Done})
	}
	hist[6].Outcome, hist[13].Outcome = run.Held, run.Held

	var prev int
	draw := func(in render.Input) bool {
		if prev > 0 {
			fmt.Printf("\033[%dA\033[J", prev)
		}
		out := render.Render(in, st)
		fmt.Println(out)
		prev = strings.Count(out, "\n") + 1
		select {
		case <-ctx.Done():
			return false
		case <-time.After(180 * time.Millisecond):
			return true
		}
	}
	frame := func(d time.Duration) bool {
		end := time.Now().Add(d)
		for time.Now().Before(end) {
			if !draw(render.Input{Runs: []*run.Run{r}, History: hist, Now: time.Now(), Plan: plan}) {
				return false
			}
		}
		return true
	}
	// the floor: your checks first
	if !frame(1200 * time.Millisecond) {
		return nil
	}
	r.Step = "review"
	// review: the bars fill
	switch {
	case opt.Parallel:
		// all four start together and finish at different times
		for i := range r.Lenses {
			r.Lenses[i].State, r.Lenses[i].StartedAt = run.Running, time.Now()
		}
		speeds := []float64{1.0, 1.9, 1.15, 0.7}
		found := []int{3, 1, 2, 1}
		for step := 0; step <= 30; step++ {
			for i := range r.Lenses {
				if r.Lenses[i].State != run.Running {
					continue
				}
				f := float64(step) / 20 * speeds[i]
				if f >= 1 {
					r.Lenses[i].State, r.Lenses[i].Frac, r.Lenses[i].Found, r.Lenses[i].EndedAt = run.LensDone, 1, found[i], time.Now()
				} else {
					r.Lenses[i].Frac = f
				}
			}
			if !frame(160 * time.Millisecond) {
				return nil
			}
		}
	case opt.Passes > 1:
		// passes run one after another; the second is the stronger read
		for i := range r.Lenses {
			r.Lenses[i].State, r.Lenses[i].StartedAt = run.Running, time.Now()
			for step := 0; step <= 10; step++ {
				r.Lenses[i].Frac = float64(step) / 10
				if !frame(150 * time.Millisecond) {
					return nil
				}
			}
			r.Lenses[i].State, r.Lenses[i].Found, r.Lenses[i].EndedAt = run.LensDone, 4+i*2, time.Now()
		}
	default:
		for i := 0; i <= 10; i++ {
			r.Lenses[0].Frac = float64(i) / 10
			if !frame(220 * time.Millisecond) {
				return nil
			}
		}
		r.Lenses[0].State, r.Lenses[0].Found, r.Lenses[0].EndedAt = run.LensDone, 7, time.Now()
	}
	set.Close()
	r.Findings = set
	r.CostUSD = 0.41
	// the gate track
	for _, g := range []string{"decide", "fix", "check", "verify"} {
		r.Phase, r.Step = run.Fix, g
		if g == "fix" {
			r.Round = 1
		}
		if !frame(1500 * time.Millisecond) {
			return nil
		}
	}
	for i := 0; i < 5; i++ {
		set.Transition(set.Findings[i].ID, finding.Fixed, 1)
	}
	r.Rounds = []run.RoundSummary{{Fixed: 5, Filed: 0, Open: 2}}
	r.Round = 2
	for _, g := range []string{"fix", "check", "verify"} {
		r.Step = g
		if !frame(1200 * time.Millisecond) {
			return nil
		}
	}
	// waiting on you
	r.Phase, r.Step, r.UpdatedAt = run.Held, "decide", time.Now()
	r.CostUSD = 0.62
	if !frame(2800 * time.Millisecond) {
		return nil
	}
	// on to the PR and CI
	set.Transition(set.Findings[5].ID, finding.Accepted, 2)
	set.Transition(set.Findings[6].ID, finding.Fixed, 2)
	r.Rounds = append(r.Rounds, run.RoundSummary{Fixed: 1, Open: 0})
	r.Phase, r.Step = run.PR, "pr"
	if !frame(1800 * time.Millisecond) {
		return nil
	}
	r.PR = &run.PRInfo{Number: 126, URL: "https://github.com/you/repo/pull/126"}
	r.Phase, r.Step = run.CI, "ci"
	r.CI = &run.CIStatus{Total: 4, Since: time.Now()}
	for i := 1; i <= 4; i++ {
		r.CI.Passed = i
		if !frame(900 * time.Millisecond) {
			return nil
		}
	}
	r.Phase, r.UpdatedAt = run.Done, time.Now()
	r.CostUSD = 0.71
	if !frame(1800 * time.Millisecond) {
		return nil
	}
	// idle
	r.PID = 0
	hist = append(hist, run.Summary{EndedAt: time.Now(), Duration: 4*time.Minute + time.Second, Outcome: run.Done})
	end := time.Now().Add(2500 * time.Millisecond)
	for time.Now().Before(end) {
		if !draw(render.Input{History: hist, IdleRef: "main", Now: time.Now(), Plan: plan}) {
			return nil
		}
	}
	return nil
}
