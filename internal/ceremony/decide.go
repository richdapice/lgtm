package ceremony

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"strings"

	"github.com/richdapice/lgtm/internal/finding"
	"github.com/richdapice/lgtm/internal/run"
)

// Decision is what a human said about one open finding.
type Decision int

const (
	Skip Decision = iota
	Fix
	Accept
	Dismiss
)

// Decider is the human in manual mode. The line prompt and the TUI both
// implement it; the ceremony doesn't know which it's talking to. autopilot
// means "fix everything from here on"; quit parks the run as held.
type Decider interface {
	Decide(ctx context.Context, open []finding.Finding, r *run.Run) (decisions map[string]Decision, autopilot, quit bool)
}

// LineDecider is the plain prompt: one finding at a time, a letter and Enter.
// It is what you get when stdout is not a terminal, and with --plain.
type LineDecider struct {
	In     io.Reader
	Out    io.Writer
	CanFix bool
}

func (d LineDecider) Decide(ctx context.Context, open []finding.Finding, r *run.Run) (map[string]Decision, bool, bool) {
	rd := bufio.NewReader(d.In)
	out := map[string]Decision{}
	fmt.Fprintf(d.Out, "\n%d finding(s) · f fix · a accept · d dismiss · s skip · A autopilot · q quit\n", len(open))
	for i := 0; i < len(open); i++ {
		f := open[i]
		loc := f.Path
		if f.Line > 0 {
			loc = fmt.Sprintf("%s:%d", f.Path, f.Line)
		}
		fmt.Fprintf(d.Out, "\n[%d/%d] %-5s %-12s %s  %s\n", i+1, len(open), f.Severity, f.Lens, loc, f.Rule)
		if a := strings.TrimSpace(f.Anchor); a != "" {
			fmt.Fprintf(d.Out, "      %s\n", a)
		}
		fmt.Fprintf(d.Out, "      %s\n", f.Body)
		if f.Note != "" {
			fmt.Fprintf(d.Out, "      ↳ %s\n", f.Note)
		}
	prompt:
		fmt.Fprint(d.Out, "      > ")
		line, err := rd.ReadString('\n')
		if err != nil {
			return out, false, true
		}
		switch strings.TrimSpace(line) {
		case "f":
			if !d.CanFix {
				fmt.Fprintln(d.Out, "      no fix_command configured for this agent; fix by hand or accept/dismiss/skip")
				goto prompt
			}
			out[f.ID] = Fix
		case "a":
			out[f.ID] = Accept
		case "d":
			out[f.ID] = Dismiss
		case "s", "":
			out[f.ID] = Skip
		case "A":
			for _, g := range open[i:] {
				out[g.ID] = Fix
			}
			return out, true, false
		case "q":
			return out, false, true
		default:
			fmt.Fprintln(d.Out, "      f, a, d, s, A, or q")
			goto prompt
		}
	}
	return out, false, false
}

// apply records accepts and dismissals on the set and returns what to fix.
func (c *Ceremony) apply(decisions map[string]Decision, open []finding.Finding) []finding.Finding {
	var toFix []finding.Finding
	dismissed := false
	for _, f := range open {
		switch decisions[f.ID] {
		case Fix:
			toFix = append(toFix, f)
		case Accept:
			_ = c.run.Findings.Transition(f.ID, finding.Accepted, c.run.Round)
		case Dismiss:
			_ = c.run.Findings.Transition(f.ID, finding.Dismissed, c.run.Round)
			c.dismiss.Add(f, "dismissed in lgtm review")
			dismissed = true
		}
	}
	if dismissed {
		if err := c.dismiss.Save(c.root); err != nil {
			c.log("save dismiss list: %v", err)
		}
	}
	c.save()
	return toFix
}
