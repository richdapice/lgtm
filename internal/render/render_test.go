package render

import (
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/richdapice/lgtm/internal/finding"
	"github.com/richdapice/lgtm/internal/run"
)

var now = time.Date(2026, 9, 15, 12, 0, 0, 0, time.UTC)

func discoverRun() *run.Run {
	r := run.New("worktree-r2-incremental-cache", "main", "manual", 3,
		[]string{"correctness", "conventions", "security", "tests"},
		map[string]string{"correctness": "opus", "conventions": "haiku", "security": "opus", "tests": "sonnet"})
	r.PID = os.Getpid()
	r.StartedAt = now.Add(-2*time.Minute - 14*time.Second)
	r.CostUSD = 0.09
	r.Lenses[0].State, r.Lenses[0].Found = run.LensDone, 7
	r.Lenses[0].StartedAt, r.Lenses[0].EndedAt = r.StartedAt, r.StartedAt.Add(42*time.Second)
	r.Lenses[1].State, r.Lenses[1].Frac, r.Lenses[1].Found = run.Running, .68, 3
	r.Lenses[1].StartedAt = now.Add(-12 * time.Second)
	r.Lenses[2].State, r.Lenses[2].Found = run.LensDone, 2
	r.Lenses[2].StartedAt, r.Lenses[2].EndedAt = r.StartedAt, r.StartedAt.Add(38*time.Second)
	r.Lenses[3].State, r.Lenses[3].Frac = run.Running, .49
	r.Lenses[3].StartedAt = now.Add(-64 * time.Second)
	return r
}

func lines(s string) []string { return strings.Split(s, "\n") }

func TestDiscoverLayout(t *testing.T) {
	out := Render(Input{Runs: []*run.Run{discoverRun()}, Now: now}, Style{Cols: 100})
	ls := lines(out)
	if len(ls) != 5 {
		t.Fatalf("want header + 4 lens rows, got %d:\n%s", len(ls), out)
	}
	for _, l := range ls {
		if width(l) > 100 {
			t.Fatalf("row wider than terminal: %q", l)
		}
	}
	if !strings.Contains(ls[1], "╭ correctness  ─────────────  ✓  7   opus      42s") {
		t.Fatalf("lens row 1 = %q", ls[1])
	}
	if !strings.Contains(ls[2], "│ conventions  ████████▊░░░░     –   haiku     12s") {
		t.Fatalf("lens row 2 (eighth-block fill) = %q", ls[2])
	}
	if !strings.HasPrefix(strings.TrimLeft(ls[4], " "), "╰ tests") {
		t.Fatalf("last lens row = %q", ls[4])
	}
	if !strings.Contains(ls[0], "manual   2m14s   ≈$0.09") {
		t.Fatalf("header right = %q", ls[0])
	}
}

func TestHeldLayout(t *testing.T) {
	r := discoverRun()
	r.Phase, r.Round = run.Held, 3
	for i := range r.Lenses {
		r.Lenses[i].State = run.LensDone
	}
	for i := 0; i < 4; i++ {
		r.Findings.Add(finding.Finding{Path: "a", Anchor: string(rune('a' + i)), Rule: "r", Severity: finding.Ask})
	}
	r.Findings.Close()
	out := Render(Input{Runs: []*run.Run{r}, Now: now}, Style{Cols: 100})
	ls := lines(out)
	if len(ls) != 3 {
		t.Fatalf("want header + loop + prompt, got %d:\n%s", len(ls), out)
	}
	r.UpdatedAt = now.Add(-30 * time.Second)
	out = Render(Input{Runs: []*run.Run{r}, Now: now.Add(10 * time.Minute)}, Style{Cols: 100})
	ls = lines(out)
	if !strings.Contains(ls[0], "4 need you   1m44s") {
		t.Fatalf("held elapsed should freeze at UpdatedAt: %q", ls[0])
	}
	if !strings.Contains(ls[1], "● ● ●") || !strings.Contains(ls[1], "round 3/3") || !strings.Contains(ls[1], "4 open") {
		t.Fatalf("loop row = %q", ls[1])
	}
	if !strings.Contains(ls[2], "lgtm -b worktree-r2-incremental-cache") {
		t.Fatalf("prompt row = %q", ls[2])
	}
}

func TestCIAndIdleAndMulti(t *testing.T) {
	r := discoverRun()
	r.Phase = run.CI
	r.PR = &run.PRInfo{Number: 119, URL: "https://github.com/x/y/pull/119"}
	r.CI = &run.CIStatus{Total: 4, Passed: 2, Since: now.Add(-72 * time.Second)}
	out := Render(Input{Runs: []*run.Run{r}, Now: now}, Style{Cols: 100})
	ls := lines(out)
	if len(ls) != 2 || !strings.Contains(ls[1], "◍ #119   ▰▰▱▱   check 2/4 · 1m12s") {
		t.Fatalf("ci layout:\n%s", out)
	}

	hist := []run.Summary{}
	for i := 0; i < 20; i++ {
		hist = append(hist, run.Summary{EndedAt: now, Duration: time.Duration(60+i*7) * time.Second, Outcome: run.Done})
	}
	hist[3].Outcome = run.Held
	out = Render(Input{History: hist, IdleRef: "main", Now: now}, Style{Cols: 100})
	ls = lines(out)
	if len(ls) != 2 || !strings.Contains(ls[0], "idle   20 runs today") || !strings.Contains(ls[1], "20 runs  ▁") || !strings.Contains(ls[1], "1 held") || !strings.Contains(ls[1], "streak 16") {
		t.Fatalf("idle layout:\n%s", out)
	}

	r2 := discoverRun()
	r2.Branch, r2.Phase, r2.Round = "worktree-mobile-field-validation", run.Verify, 2
	out = Render(Input{Runs: []*run.Run{r, r2}, Now: now}, Style{Cols: 100})
	ls = lines(out)
	if len(ls) != 3 || !strings.Contains(ls[0], "2 runs") || !strings.Contains(ls[2], "mobile-field-validation") || !strings.Contains(ls[2], "round 2/3") {
		t.Fatalf("multi layout:\n%s", out)
	}
}

func TestColorDoesNotChangeWidth(t *testing.T) {
	r := discoverRun()
	plain := Render(Input{Runs: []*run.Run{r}, Now: now}, Style{Cols: 100})
	color := Render(Input{Runs: []*run.Run{r}, Now: now}, Style{Cols: 100, Color: true})
	pl, cl := lines(plain), lines(color)
	for i := range pl {
		if width(pl[i]) != width(cl[i]) {
			t.Fatalf("row %d: plain %d vs color %d", i, width(pl[i]), width(cl[i]))
		}
	}
	if !strings.Contains(color, "\033[36m") {
		t.Fatal("no accent color emitted")
	}
}

func TestPlanUsageInHeader(t *testing.T) {
	plan := &PlanUsage{FiveHourPct: 37, SevenDayPct: 85, FiveHourReset: now.Add(2*time.Hour + 23*time.Minute), SevenDayReset: now.Add(76 * time.Hour)}
	out := Render(Input{Runs: []*run.Run{discoverRun()}, Now: now, Plan: plan}, Style{Cols: 120})
	l := lines(out)[0]
	if !strings.Contains(l, "≈$0.09   5h 37% ↺ 2h23m · 7d 85% ↺ 3d04h") {
		t.Fatalf("header = %q (width %d)", l, width(l))
	}
}

func TestDurRollsIntoHours(t *testing.T) {
	for d, want := range map[time.Duration]string{45 * time.Second: "45s", 72*time.Minute + 19*time.Second: "1h12m", 3*time.Hour + 5*time.Minute: "3h05m"} {
		if got := dur(d); got != want {
			t.Errorf("dur(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestWidthStripsOSC8(t *testing.T) {
	s := "\033]8;;https://x\033\\#119\033]8;;\033\\ ok"
	if w := width(s); w != 7 {
		t.Fatalf("width = %d", w)
	}
}

func TestGateTrackRow(t *testing.T) {
	r := discoverRun()
	r.Phase, r.Step, r.Round = run.Verify, "verify", 2
	r.Findings.Close()
	out := Render(Input{Runs: []*run.Run{r}, Now: now}, Style{Cols: 110})
	if !strings.Contains(out, "gates ✓ review ─ ✓ decide ─ ✓ fix ─ ✓ check ─ ∴ verify ─ ○ push ─ ○ pr ─ ○ ci") {
		t.Fatalf("no gate track:\n%s", out)
	}
	if os.Getenv("SHOW") != "" {
		fmt.Println(out)
	}
}
