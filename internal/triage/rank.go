package triage

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/richdapice/lgtm/internal/diffparse"
	"github.com/richdapice/lgtm/internal/jev"
)

// Ranking every hunk is the experiment: before the reviewer reads the diff,
// ask Jev which hunks look like they carry something worth raising. It is
// not a review — Jev points, it never says what the problem is — but if the
// hunks it points at are the ones the reviewer later raises findings on,
// the ranking can focus the expensive call or skip it for a diff that
// scores low everywhere.

// HunkScore is Jev's read of one hunk.
type HunkScore struct {
	Path      string  `json:"path"`
	Header    string  `json:"header"`
	NewStart  int     `json:"new_start"`
	NewEnd    int     `json:"new_end"`
	Risk      float64 `json:"risk"` // probability a reviewer should look, 0..1
	Lens      string  `json:"lens"` // the kind of problem most likely: correctness|security|tests|conventions|none
	Confident float64 `json:"confidence"`
	Truncated bool    `json:"truncated,omitempty"`
	Preview   string  `json:"preview"` // first changed line, for the listing
}

// Contains reports whether a new-file line falls inside the hunk.
func (h HunkScore) Contains(path string, line int) bool {
	return h.Path == path && line >= h.NewStart && line <= h.NewEnd
}

const (
	// rankBatch caps hunks per request; rankChars caps the state. Jev's state
	// budget is 32k tokens; unified diff runs about four characters a token.
	rankBatch = 25
	rankChars = 90_000
	// hunkChars caps one hunk; past this the tail is cut and the hunk marked.
	hunkChars = 8_000
)

type rankHunk struct {
	Path      string `json:"path"`
	Header    string `json:"header"`
	NewFile   bool   `json:"new_file,omitempty"`
	Truncated bool   `json:"truncated,omitempty"`
	Diff      string `json:"diff"`
}

type rankState struct {
	Intent string     `json:"intent,omitempty"`
	Hunks  []rankHunk `json:"hunks"`
}

var lensCriteria = map[string]string{
	"correctness": "A bug, an edge case, error handling that swallows or mis-reports a failure, or logic that does not do what the change claims.",
	"security":    "A secret in source, injection, an auth or permission mistake, unsafe deserialization, or data written somewhere it should not be.",
	"tests":       "Behavior changed with no test changing, or a test that does not assert the new behavior.",
	"conventions": "A departure from the project's conventions or from the style of the surrounding code, beyond formatting.",
	"none":        "Nothing worth raising: documentation, renames, formatting, comments, added tests, or a change that is plainly correct.",
}

// Rank scores every hunk in files. Scores come back sorted by Risk,
// highest first. An error from one batch stops the run; what was scored
// before it is returned with the error.
func Rank(ctx context.Context, c *jev.Client, files []diffparse.FileDiff, intent string) ([]HunkScore, Result, error) {
	var res Result
	var all []HunkScore
	var hunks []rankHunk
	for _, fd := range files {
		if fd.IsBinary {
			continue
		}
		for _, h := range fd.Hunks {
			text, truncated := renderHunk(h)
			hunks = append(hunks, rankHunk{Path: fd.Path(), Header: h.Header, NewFile: fd.IsNew, Truncated: truncated, Diff: text})
			all = append(all, HunkScore{
				Path: fd.Path(), Header: strings.TrimSpace(h.Header), NewStart: h.NewStart, NewEnd: h.NewStart + max(h.NewCount, 1) - 1,
				Truncated: truncated, Preview: preview(h),
			})
		}
	}
	intent = clip(intent, 2000)
	for lo := 0; lo < len(hunks); {
		hi, chars := lo, 0
		for hi < len(hunks) && hi-lo < rankBatch && (hi == lo || chars+len(hunks[hi].Diff) <= rankChars) {
			chars += len(hunks[hi].Diff)
			hi++
		}
		batch := hunks[lo:hi]
		st := rankState{Intent: intent, Hunks: batch}
		qs := make(map[string]jev.Question, 2*len(batch))
		for i := range batch {
			ref := fmt.Sprintf("`hunks[%d]`", i)
			qs[riskKey(i)] = jev.Question{
				Type: "noul",
				Instructions: "Does " + ref + " introduce something a careful reviewer would raise before this merges — a bug or unhandled edge case, an error swallowed or mis-reported, " +
					"a security mistake, or a behavior change with no test — as opposed to a safe change such as documentation, a rename, formatting, comments, or added tests? " +
					"Judge the code in " + ref + ".diff; lines starting with + are new, lines starting with - were removed.",
				Criteria: map[string]string{
					"true":  "The new code plausibly has a defect or risk a reviewer would want to look at closely.",
					"false": "The change is mechanical, additive in a safe way, or plainly correct; a reviewer would pass over it.",
				},
			}
			qs[lensKey(i)] = jev.Question{
				Type:         "choice",
				Instructions: "Which kind of problem is a reviewer most likely to raise about " + ref + ", if any?",
				Criteria:     lensCriteria,
			}
		}
		resp, err := c.Ask(ctx, st, qs)
		res.Asked += len(batch)
		res.CostUSD += resp.CostUSD()
		if err != nil {
			return sorted(all), res, err
		}
		for i := range batch {
			risk, ok1 := resp.Answers[riskKey(i)]
			lens, ok2 := resp.Answers[lensKey(i)]
			if !ok1 || !ok2 || risk.Noul == nil || *risk.Noul < 0 || *risk.Noul > 1 || lensCriteria[lens.Choice] == "" {
				continue
			}
			s := &all[lo+i]
			s.Risk, s.Lens, s.Confident = *risk.Noul, lens.Choice, lens.Confidence
			res.Scored++
		}
		lo = hi
	}
	return sorted(all), res, nil
}

func riskKey(i int) string { return fmt.Sprintf("risk_%d", i) }
func lensKey(i int) string { return fmt.Sprintf("lens_%d", i) }

func sorted(s []HunkScore) []HunkScore {
	sort.SliceStable(s, func(i, j int) bool { return s[i].Risk > s[j].Risk })
	return s
}

// renderHunk is the hunk as unified diff text, capped at hunkChars.
func renderHunk(h diffparse.Hunk) (string, bool) {
	var b strings.Builder
	for _, l := range h.Lines {
		mark := " "
		switch l.Kind {
		case diffparse.Added:
			mark = "+"
		case diffparse.Removed:
			mark = "-"
		}
		b.WriteString(mark)
		b.WriteString(l.Text)
		b.WriteByte('\n')
		if b.Len() > hunkChars {
			return b.String() + "… (truncated)\n", true
		}
	}
	return b.String(), false
}

func preview(h diffparse.Hunk) string {
	for _, l := range h.Lines {
		if l.Kind == diffparse.Added || l.Kind == diffparse.Removed {
			if t := strings.TrimSpace(l.Text); t != "" {
				return t
			}
		}
	}
	return ""
}
