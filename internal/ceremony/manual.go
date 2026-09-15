package ceremony

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/richdapice/lgtm/internal/finding"
)

// manual walks the open findings one at a time. It returns the ones the user
// asked to fix, and whether they quit. Accept and dismiss are applied here;
// dismissals also go to the committed list.
func (c *Ceremony) manual(open []finding.Finding) (toFix []finding.Finding, quit bool) {
	rd := bufio.NewReader(c.o.In)
	c.println("\n%d finding(s) · f fix · a accept · d dismiss · s skip · A autopilot · q quit", len(open))
	dismissed := false
	for i := 0; i < len(open); i++ {
		f := open[i]
		loc := f.Path
		if f.Line > 0 {
			loc = fmt.Sprintf("%s:%d", f.Path, f.Line)
		}
		c.println("\n[%d/%d] %-5s %-12s %s  %s", i+1, len(open), f.Severity, f.Lens, loc, f.Rule)
		if strings.TrimSpace(f.Anchor) != "" {
			c.println("      %s", strings.TrimSpace(f.Anchor))
		}
		c.println("      %s", f.Body)
	prompt:
		fmt.Fprint(c.o.Out, "      > ")
		line, err := rd.ReadString('\n')
		if err != nil {
			return toFix, true
		}
		switch strings.TrimSpace(line) {
		case "f":
			if c.fixer == nil {
				c.println("      no fix_command configured for this agent; fix it by hand or accept/dismiss/skip")
				goto prompt
			}
			toFix = append(toFix, f)
		case "a":
			_ = c.run.Findings.Transition(f.ID, finding.Accepted, c.run.Round)
		case "d":
			_ = c.run.Findings.Transition(f.ID, finding.Dismissed, c.run.Round)
			c.dismiss.Add(f, "dismissed in gate review")
			dismissed = true
		case "s", "":
		case "A":
			c.run.Mode = "auto"
			toFix = append(toFix, open[i:]...)
			c.save()
			c.println("      autopilot: fixing the remaining %d", len(open)-i)
			i = len(open)
		case "q":
			quit = true
			i = len(open)
		default:
			c.println("      f, a, d, s, A, or q")
			goto prompt
		}
		c.save()
	}
	if dismissed {
		if err := c.dismiss.Save(c.root); err != nil {
			c.log("save dismiss list: %v", err)
		}
	}
	return toFix, quit
}
