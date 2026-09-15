// Package render draws the status bar from run state. It never does work: the
// bar is re-run on every refresh tick and cancelled if it is slow, so the
// only thing this package may read is what is already on disk.
//
// Layout rules, in order of how much they mattered when we tried the
// alternatives: a fixed five-wide label gutter so rows align down one column;
// lens rows as a bracket, because they are concurrent and a stack of bars says
// otherwise; eighth-block fills so a bar glides at a 1s tick instead of
// stepping; one accent color for whatever is running, everything else dim or
// plain; the right column flush to the terminal edge.
package render

import (
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/richdapice/lgtm/internal/run"
)

const (
	gutter = 5
	barW   = 13
	eighth = " ▏▎▍▌▋▊▉█"
	spark  = "▁▂▃▄▅▆▇█"
)

type Style struct {
	Color bool
	Cols  int
}

func (s Style) c(code, text string) string {
	if !s.Color || text == "" {
		return text
	}
	return "\033[" + code + "m" + text + "\033[0m"
}

const (
	dim    = "2"
	accent = "36"
	good   = "32"
	warn   = "33"
	bad    = "31"
	bold   = "1"
)

// Input is everything the bar can show. Runs is what's on disk; History feeds
// the idle sparkline; IdleRef is the branch or repo name to show when nothing
// is running.
type Input struct {
	Runs    []*run.Run
	History []run.Summary
	IdleRef string
	NoRepo  bool // cwd isn't a git repo: show the plan windows, nothing else
	Now     time.Time
	// Plan is subscription-window usage from Claude Code's statusline JSON,
	// when the host provides it. On Pro/Max the dollar figures are estimates
	// at API list price, not charges; this is what actually depletes.
	Plan *PlanUsage
}

type PlanUsage struct {
	FiveHourPct   int
	SevenDayPct   int
	FiveHourReset time.Time // zero when unknown
	SevenDayReset time.Time
}

// render shows each window as "used ↺ time-until-reset". A percentage without
// its reset is just anxiety; the reset is what tells you whether to wait.
func (p *PlanUsage) render(st Style, now time.Time) string {
	if p == nil {
		return ""
	}
	col := func(pct int) string {
		switch {
		case pct >= 90:
			return bad
		case pct >= 70:
			return warn
		}
		return dim
	}
	win := func(label string, pct int, reset time.Time) string {
		s := st.c(col(pct), fmt.Sprintf("%s %d%%", label, pct))
		if !reset.IsZero() && reset.After(now) {
			s += st.c(dim, " ↺ "+until(reset.Sub(now)))
		}
		return s
	}
	return win("5h", p.FiveHourPct, p.FiveHourReset) + st.c(dim, " · ") + win("7d", p.SevenDayPct, p.SevenDayReset)
}

// until is a compact "time from now": 42m, 2h23m, 3d04h.
func until(d time.Duration) string {
	d = d.Round(time.Minute)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd%02dh", int(d.Hours())/24, int(d.Hours())%24)
	}
}

func cost(usd float64, st Style) string { return st.c(dim, fmt.Sprintf("≈$%.2f", usd)) }

func Render(in Input, st Style) string {
	if st.Cols <= 0 {
		st.Cols = 100
	}
	if in.Now.IsZero() {
		in.Now = time.Now()
	}
	live := in.Runs[:0:0]
	for _, r := range in.Runs {
		if r.Phase != run.Done && r.Phase != run.Failed || r.Alive() {
			live = append(live, r)
		}
	}
	switch len(live) {
	case 0:
		return idle(in, st)
	case 1:
		return single(live[0], in.Now, st, in.Plan)
	default:
		return multi(live, in.Now, st)
	}
}

func idle(in Input, st Style) string {
	if in.NoRepo {
		head := st.c(bold, "lgtm") + " ▸ " + st.c(dim, in.IdleRef+"   not a repo")
		if p := in.Plan.render(st, in.Now); p != "" {
			head += "   " + p
		}
		return row(st, "", head, "", "lgtm")
	}
	today, held := 0, 0
	y, m, d := in.Now.Date()
	for _, h := range in.History {
		if hy, hm, hd := h.EndedAt.Date(); hy == y && hm == m && hd == d {
			today++
			if h.Outcome == run.Held {
				held++
			}
		}
	}
	head := st.c(bold, "lgtm") + " ▸ " + in.IdleRef + "   " + st.c(dim, "idle") + "   " + st.c(dim, fmt.Sprintf("%d runs today", today))
	if p := in.Plan.render(st, in.Now); p != "" {
		head += "   " + p
	}
	rows := []string{row(st, "", head, "", "lgtm")}
	if len(in.History) > 0 {
		rows = append(rows, row(st, "", historyRow(in.History, held, st), "", ""))
	}
	return strings.Join(rows, "\n")
}

func historyRow(h []run.Summary, held int, st Style) string {
	lo, hi := h[0].Duration, h[0].Duration
	for _, s := range h {
		if s.Duration > hi {
			hi = s.Duration
		}
		if s.Duration < lo {
			lo = s.Duration
		}
	}
	var b strings.Builder
	for _, s := range h {
		// min-max so the shortest run is ▁ and the longest █; a flat history
		// sits mid-height rather than pretending to be fast
		lvl := 3
		if hi > lo {
			lvl = int(float64(s.Duration-lo) / float64(hi-lo) * 7)
		}
		ch := string([]rune(spark)[lvl])
		if s.Outcome == run.Held {
			ch = st.c(warn, ch)
		} else if s.Outcome == run.Failed {
			ch = st.c(bad, ch)
		}
		b.WriteString(ch)
	}
	med := median(h)
	out := st.c(dim, fmt.Sprintf("%d runs  ", len(h))) + b.String() +
		st.c(dim, fmt.Sprintf("  median %s · %d held", dur(med), held))
	// consecutive runs that shipped without needing you
	streak := 0
	for i := len(h) - 1; i >= 0 && h[i].Outcome == run.Done; i-- {
		streak++
	}
	if streak >= 2 {
		out += st.c(good, fmt.Sprintf(" · streak %d", streak))
	}
	return out
}

func single(r *run.Run, now time.Time, st Style, plan *PlanUsage) string {
	var rows []string
	mode := r.Mode
	switch r.Phase {
	case run.Done:
		mode = st.c(good, "passed")
	case run.Failed:
		mode = st.c(bad, "failed")
	case run.Held:
		n := r.Findings.NeedsYou()
		mode = st.c(warn, fmt.Sprintf("%d need you", n))
		if n == 1 {
			mode = st.c(warn, "1 needs you")
		}
	default:
		if !r.Alive() {
			mode = st.c(dim, "stale")
		}
	}
	// everything left-anchored: on a wide terminal a flush-right column ends
	// up under Claude Code's notification area and reads as a separate thing
	head := st.c(bold, "lgtm") + " ▸ " + st.c(accent, r.Branch) + st.c(dim, " → "+r.Base) +
		"   " + mode + "   " + st.c(dim, dur(r.Elapsed(now))) + "   " + cost(r.CostUSD, st)
	if p := plan.render(st, now); p != "" {
		head += "   " + p
	}
	rows = append(rows, row(st, "", head, "", "lgtm"))

	// lenses while reviewing; after that the gate track tells the story
	showLenses := r.Phase == run.Discover
	if showLenses && len(r.Lenses) > 0 {
		for i, l := range r.Lenses {
			stem := "│"
			if i == 0 {
				stem = "╭"
			}
			if i == len(r.Lenses)-1 {
				stem = "╰"
			}
			if len(r.Lenses) == 1 {
				stem = "─"
			}
			rows = append(rows, row(st, "", lensRow(l, now, stem, st), "", ""))
		}
	}
	if r.Step != "" && r.Step != "review" && r.Phase != run.Done {
		var parts []string
		reached := false
		for _, g := range run.Gates {
			switch {
			case g == r.Step && !reached:
				reached = true
				parts = append(parts, st.c(accent, "∴ "+g))
			case !reached:
				parts = append(parts, st.c(good, "✓ ")+g)
			default:
				parts = append(parts, st.c(dim, "○ "+g))
			}
		}
		rows = append(rows, row(st, "gates", strings.Join(parts, st.c(dim, " ─ ")), "", ""))
	}
	if r.Round > 0 || r.Findings.Closed {
		c := r.Findings.Counts()
		done := min(r.Round, r.MaxRounds)
		dots := strings.TrimRight(st.c(good, strings.Repeat("● ", done))+st.c(dim, strings.Repeat("○ ", max(r.MaxRounds-done, 0))), " ")
		open := fmt.Sprintf("%d open", c.Open)
		if c.Open > 0 {
			open = st.c(warn, open)
		} else {
			open = st.c(good, open)
		}
		body := dots + st.c(dim, fmt.Sprintf("  round %d/%d", r.Round, r.MaxRounds)) + "      " +
			open + st.c(dim, " · ") + st.c(good, fmt.Sprintf("%d fixed", c.Fixed)) +
			st.c(dim, fmt.Sprintf(" · %d filed", c.Filed))
		rows = append(rows, row(st, "loop", body, "", ""))
	}
	if r.PR != nil || r.CI != nil {
		rows = append(rows, row(st, "ci", ciRow(r, now, st), "", ""))
	}
	switch r.Phase {
	case run.Held:
		body := st.c(dim, "lgtm -b "+r.Branch+" · or /lgtm in Claude Code")
		rows = append(rows, row(st, st.c(warn, "→"), body, "", "→"))
	case run.Failed:
		rows = append(rows, row(st, st.c(bad, "✗"), st.c(bad, r.Error), "", "✗"))
	}
	return strings.Join(rows, "\n")
}

func lensRow(l run.Lens, now time.Time, stem string, st Style) string {
	name := pad(l.Name, 13)
	// only the running lens gets a bright bar; a finished one goes quiet so
	// four done lenses don't stack into a solid block
	var bar, found, color string
	switch l.State {
	case run.LensDone:
		bar, color = st.c(dim, strings.Repeat("─", barW)), good
		found = st.c(good, "✓") + fmt.Sprintf("%3d", l.Found)
	case run.Running:
		bar, color = fill(l.Frac, st, accent), accent
		found = "   –"
	case run.LensFailed:
		bar, color = st.c(bad, strings.Repeat("▁", barW)), bad
		found = "   " + st.c(bad, "✗")
	default:
		bar, color = st.c(dim, strings.Repeat("░", barW)), dim
		found = "   ·"
	}
	el := ""
	if l.State != run.Pending {
		el = dur(l.Elapsed(now))
	}
	return st.c(dim, stem) + " " + st.c(color, name) + bar + "  " + found + "   " +
		st.c(dim, pad(l.Model, 7)) + st.c(dim, rpad(el, 6))
}

func ciRow(r *run.Run, now time.Time, st Style) string {
	var b strings.Builder
	if r.PR != nil {
		num := fmt.Sprintf("#%d", r.PR.Number)
		if st.Color && r.PR.URL != "" {
			num = "\033]8;;" + r.PR.URL + "\033\\" + num + "\033]8;;\033\\"
		}
		b.WriteString("◍ " + num)
	}
	if r.CI != nil {
		c := r.CI
		done := c.Passed + c.Failed
		bar := st.c(good, strings.Repeat("▰", c.Passed)) + st.c(bad, strings.Repeat("▰", c.Failed)) +
			st.c(dim, strings.Repeat("▱", max(c.Total-done, 0)))
		fmt.Fprintf(&b, "   %s   check %d/%d", bar, done, c.Total)
		if c.Failed > 0 {
			b.WriteString(st.c(bad, fmt.Sprintf(" · %d failed", c.Failed)))
		}
		if !c.Since.IsZero() {
			b.WriteString(st.c(dim, " · "+dur(now.Sub(c.Since))))
		}
	}
	return b.String()
}

func multi(runs []*run.Run, now time.Time, st Style) string {
	var total float64
	for _, r := range runs {
		total += r.CostUSD
	}
	head := st.c(bold, "lgtm") + " ▸ " + fmt.Sprintf("%d runs", len(runs)) + "   " + cost(total, st)
	rows := []string{row(st, "", head, "", "lgtm")}
	for _, r := range runs {
		name := strings.TrimPrefix(r.Branch, "worktree-")
		var state string
		switch r.Phase {
		case run.Discover:
			state = st.c(accent, "∴ review")
		case run.Fix, run.Verify:
			state = st.c(accent, fmt.Sprintf("∴ round %d/%d", r.Round, r.MaxRounds))
		case run.Held:
			state = st.c(warn, fmt.Sprintf("→ %d need you", r.Findings.NeedsYou()))
		case run.PR, run.CI:
			if r.CI != nil {
				state = fmt.Sprintf("◍ ci %d/%d", r.CI.Passed+r.CI.Failed, r.CI.Total)
			} else {
				state = "◍ pr"
			}
		default:
			state = string(r.Phase)
		}
		rows = append(rows, row(st, "", pad(name, 26)+rpad(state, 18)+st.c(dim, dur(r.Elapsed(now))), "", ""))
	}
	return strings.Join(rows, "\n")
}

// row lays out `<label> <left> ... <right>` with right flush to Cols. labelPlain
// is the label's visible text when label carries color codes.
func row(st Style, label, left, right, labelPlain string) string {
	if labelPlain == "" {
		labelPlain = label
	}
	lab := strings.Repeat(" ", max(gutter-width(labelPlain), 0)) + label
	line := lab + " " + left
	if right == "" {
		return line
	}
	gap := st.Cols - width(line) - width(right)
	if gap < 2 {
		gap = 2
	}
	return line + strings.Repeat(" ", gap) + right
}

func fill(frac float64, st Style, color string) string {
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	total := frac * barW
	full := int(total)
	rem := int((total - float64(full)) * 8)
	s := strings.Repeat("█", full)
	if rem > 0 && full < barW {
		s += string([]rune(eighth)[rem])
	}
	n := utf8.RuneCountInString(s)
	return st.c(color, s) + st.c(dim, strings.Repeat("░", max(barW-n, 0)))
}

// width is the visible cell count: strips ANSI and OSC 8, counts runes.
func width(s string) int {
	n := 0
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			// CSI ... final byte in @-~ ; OSC ... ST (ESC \)
			j := i + 1
			if j < len(s) && s[j] == ']' {
				for j < len(s) && !(s[j] == 0x1b && j+1 < len(s) && s[j+1] == '\\') {
					j++
				}
				i = j + 2
				continue
			}
			if j < len(s) && s[j] == '[' {
				j++ // CSI introducer; the final byte comes after the params
			}
			for j < len(s) && !(s[j] >= '@' && s[j] <= '~') {
				j++
			}
			i = j + 1
			continue
		}
		_, sz := utf8.DecodeRuneInString(s[i:])
		i += sz
		n++
	}
	return n
}

func pad(s string, w int) string {
	if n := utf8.RuneCountInString(s); n < w {
		return s + strings.Repeat(" ", w-n)
	}
	return string([]rune(s)[:w])
}

func rpad(s string, w int) string {
	if n := utf8.RuneCountInString(s); n < w {
		return strings.Repeat(" ", w-n) + s
	}
	return s
}

func dur(d time.Duration) string {
	d = d.Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	default:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	}
}

func median(h []run.Summary) time.Duration {
	if len(h) == 0 {
		return 0
	}
	ds := make([]time.Duration, len(h))
	for i, s := range h {
		ds[i] = s.Duration
	}
	for i := 1; i < len(ds); i++ {
		for j := i; j > 0 && ds[j] < ds[j-1]; j-- {
			ds[j], ds[j-1] = ds[j-1], ds[j]
		}
	}
	return ds[len(ds)/2]
}
