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
func DemoBar(ctx context.Context) error {
	cols := 100
	if c := os.Getenv("COLUMNS"); c != "" {
		fmt.Sscanf(c, "%d", &cols)
	}
	st := render.Style{Cols: cols, Color: os.Getenv("NO_COLOR") == ""}
	now := time.Now()
	plan := &render.PlanUsage{FiveHourPct: 37, SevenDayPct: 61, FiveHourReset: now.Add(2*time.Hour + 23*time.Minute), SevenDayReset: now.Add(76 * time.Hour)}
	r := &run.Run{Branch: "worktree-sync-throttle", Base: "main", Mode: "auto", MaxRounds: 3, PID: os.Getpid(),
		StartedAt: now, Phase: run.Discover, Step: "review",
		Lenses: []run.Lens{{Name: "review", Model: "opus", State: run.Running, StartedAt: now}}}
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
	// review: the bar fills
	for i := 0; i <= 10; i++ {
		r.Lenses[0].Frac = float64(i) / 10
		if !frame(220 * time.Millisecond) {
			return nil
		}
	}
	set.Close()
	r.Findings = set
	r.Lenses[0].State, r.Lenses[0].Found, r.Lenses[0].EndedAt = run.LensDone, 7, time.Now()
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
	for _, g := range []string{"push", "pr"} {
		r.Phase, r.Step = run.PR, g
		if !frame(1000 * time.Millisecond) {
			return nil
		}
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
