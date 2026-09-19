// Package triage asks Jev two things about every finding the lenses raised:
// is it real, and what should the author do about it. The lens that raised a
// finding is a reasoning model reading a whole diff; this is a second, cheap,
// calibrated opinion on each finding in isolation, with the code it points
// at. A finding that Jev thinks is not real is still shown — it just arrives
// with that opinion attached, and autopilot can decline to pay a fix round
// for it.
package triage

import (
	"context"
	"fmt"
	"strings"

	"github.com/richdapice/lgtm/internal/diffparse"
	"github.com/richdapice/lgtm/internal/finding"
	"github.com/richdapice/lgtm/internal/jev"
)

// Batch is how many findings share one request. Jev's state budget is 32k
// tokens; twenty findings with a dozen lines of context each stay well under
// it, and two questions per finding keeps a request at forty questions.
const Batch = 20

// contextLines is how far around the anchor the hunk is quoted, each way.
const contextLines = 8

// item is one finding as Jev sees it. Field names are part of the questions
// (`findings[3].context`), so they are chosen to read well there.
type item struct {
	Lens     string `json:"lens"`
	Path     string `json:"path"`
	Line     int    `json:"line,omitempty"`
	Rule     string `json:"rule"`
	Severity string `json:"severity"`
	Claim    string `json:"claim"`
	Anchor   string `json:"anchor,omitempty"`
	Context  string `json:"context,omitempty"`
}

type state struct {
	Intent   string `json:"intent,omitempty"`
	Findings []item `json:"findings"`
}

// Result is the per-request accounting the ceremony logs.
type Result struct {
	Asked   int
	Scored  int
	CostUSD float64
}

// Run fills f.Triage on each finding it can score and leaves the rest alone.
// Findings are mutated in place through the slice. An error from one batch
// stops the run; what was scored before it stays scored.
func Run(ctx context.Context, c *jev.Client, fs []finding.Finding, files []diffparse.FileDiff, intent string) (Result, error) {
	var res Result
	for lo := 0; lo < len(fs); lo += Batch {
		hi := lo + Batch
		if hi > len(fs) {
			hi = len(fs)
		}
		batch := fs[lo:hi]
		st, qs := Build(batch, files, intent)
		resp, err := c.Ask(ctx, st, qs)
		res.Asked += len(batch)
		res.CostUSD += resp.CostUSD()
		if err != nil {
			return res, err
		}
		for i := range batch {
			if t, ok := Decode(resp, i); ok {
				batch[i].Triage = &t
				res.Scored++
			}
		}
	}
	return res, nil
}

// Build is the request for one batch: state carrying every finding with the
// hunk around its anchor, and two questions per finding addressed to it by
// index. Exported so the shape can be tested without a server.
func Build(fs []finding.Finding, files []diffparse.FileDiff, intent string) (any, map[string]jev.Question) {
	st := state{Intent: clip(intent, 2000)}
	qs := make(map[string]jev.Question, 2*len(fs))
	for i, f := range fs {
		st.Findings = append(st.Findings, item{
			Lens: f.Lens, Path: f.Path, Line: f.Line, Rule: f.Rule, Severity: string(f.Severity),
			Claim: f.Body, Anchor: f.Anchor, Context: hunk(files, f.Path, f.Line),
		})
		ref := fmt.Sprintf("`findings[%d]`", i)
		qs[realKey(i)] = jev.Question{
			Type: "noul",
			Instructions: "Is " + ref + " a genuine problem in the code shown in " + ref + ".context — something a careful reviewer would insist on addressing before merge — " +
				"as opposed to a style preference, a hypothetical with no evidence in the code, or a misreading of what the code does? " +
				"Judge the claim in " + ref + ".claim against the code, not against the severity the reviewer assigned.",
			Criteria: map[string]string{
				"true":  "The code shown has the problem the claim describes, and it matters for correctness, safety, or the project's stated conventions.",
				"false": "The claim is mistaken about the code, describes a preference rather than a defect, or speculates about a situation the code does not exhibit.",
			},
		}
		qs[actionKey(i)] = jev.Question{
			Type:         "choice",
			Instructions: "What should the author of this branch do about " + ref + "?",
			Criteria: map[string]string{
				"fix":     "Change the code: the claim is right and the change is worth making before this merges.",
				"accept":  "Leave the code as-is: the concern is reasonable but the current code is fine here, or the tradeoff it makes is acceptable.",
				"dismiss": "The claim is wrong or does not apply to this code, and it should not be raised again on this code.",
			},
		}
	}
	return st, qs
}

// Decode reads the two answers for finding i out of a response. False when
// either is missing or malformed, so a partial response scores what it can.
func Decode(resp jev.Response, i int) (finding.Triage, bool) {
	real, ok1 := resp.Answers[realKey(i)]
	act, ok2 := resp.Answers[actionKey(i)]
	if !ok1 || !ok2 || real.Type != "noul" || act.Type != "choice" {
		return finding.Triage{}, false
	}
	switch act.Choice {
	case "fix", "accept", "dismiss":
	default:
		return finding.Triage{}, false
	}
	return finding.Triage{Real: real.Noul, Suggest: act.Choice, Confidence: act.Confidence}, true
}

func realKey(i int) string   { return fmt.Sprintf("real_%d", i) }
func actionKey(i int) string { return fmt.Sprintf("action_%d", i) }

// hunk renders the diff lines around the anchor the way a reviewer reads
// them: a +/-/space marker and the new-file line number. Empty when the
// finding is unanchored or the line is not in the diff.
func hunk(files []diffparse.FileDiff, path string, newLine int) string {
	if newLine <= 0 {
		return ""
	}
	for _, fd := range files {
		if fd.Path() != path {
			continue
		}
		for _, h := range fd.Hunks {
			for i, l := range h.Lines {
				if l.NewNum != newLine {
					continue
				}
				lo, hi := i-contextLines, i+contextLines
				if lo < 0 {
					lo = 0
				}
				if hi >= len(h.Lines) {
					hi = len(h.Lines) - 1
				}
				var b strings.Builder
				for _, l := range h.Lines[lo : hi+1] {
					mark := " "
					switch l.Kind {
					case diffparse.Added:
						mark = "+"
					case diffparse.Removed:
						mark = "-"
					}
					num := "    "
					if l.NewNum > 0 {
						num = fmt.Sprintf("%4d", l.NewNum)
					}
					fmt.Fprintf(&b, "%s %s %s\n", num, mark, l.Text)
				}
				return b.String()
			}
		}
	}
	return ""
}

func clip(s string, n int) string {
	if len(s) > n {
		return s[:n] + "…"
	}
	return s
}
