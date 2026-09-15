package lens

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/richdapice/lgtm/internal/finding"
)

// VerifySchema is the shape of a verify round. Results is keyed by finding ID
// so the model can only speak to the closed set; anything else it noticed goes
// in New, which the caller files and never adds.
var VerifySchema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "results": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "id":        {"type": "string"},
          "addressed": {"type": "boolean"},
          "note":      {"type": "string"}
        },
        "required": ["id", "addressed", "note"],
        "additionalProperties": false
      }
    },
    "new": {
      "type": "array",
      "items": {
        "type": "object",
        "properties": {
          "lens":     {"type": "string"},
          "path":     {"type": "string"},
          "line":     {"type": "integer"},
          "anchor":   {"type": "string"},
          "severity": {"type": "string", "enum": ["block", "ask", "file"]},
          "rule":     {"type": "string"},
          "body":     {"type": "string"}
        },
        "required": ["lens", "path", "line", "anchor", "severity", "rule", "body"],
        "additionalProperties": false
      }
    }
  },
  "required": ["results", "new"],
  "additionalProperties": false
}`)

func BuildVerifyPrompt(open []finding.Finding, diff string) string {
	var b strings.Builder
	b.WriteString("A fix round has been applied to a branch. For each finding below, say whether the current diff addresses it. ")
	b.WriteString("Be strict: addressed means the specific problem is gone, not that something nearby changed.\n\n")
	b.WriteString("Do not re-review the diff. If you notice something new and important while checking, put it under \"new\" — it will be recorded, not acted on.\n\n")
	b.WriteString("--- FINDINGS TO VERIFY ---\n")
	for _, f := range open {
		fmt.Fprintf(&b, "id: %s\nlens: %s\npath: %s:%d\nrule: %s\nanchor: %s\nbody: %s\n\n",
			f.ID, f.Lens, f.Path, f.Line, f.Rule, f.Anchor, f.Body)
	}
	b.WriteString("--- DIFF (unified, merge-base..head, after the fix) ---\n")
	b.WriteString(diff)
	b.WriteString("\n")
	return b.String()
}

type VerifyResult struct {
	ID        string
	Addressed bool
	Note      string
}

type verifyResponse struct {
	Results []struct {
		ID        string `json:"id"`
		Addressed bool   `json:"addressed"`
		Note      string `json:"note"`
	} `json:"results"`
	New []raw `json:"new"`
}

// DecodeVerify returns the verdicts and any newly-noticed findings (already
// validated, ready to File). Verdicts for unknown IDs are dropped: the model
// cannot invent members of the set.
func DecodeVerify(out json.RawMessage, known map[string]bool, allowed []string) ([]VerifyResult, []finding.Finding, error) {
	dec := json.NewDecoder(bytes.NewReader(out))
	dec.DisallowUnknownFields()
	var resp verifyResponse
	if err := dec.Decode(&resp); err != nil {
		return nil, nil, fmt.Errorf("lens: decode verify: %w", err)
	}
	var vs []VerifyResult
	for _, r := range resp.Results {
		if !known[r.ID] {
			continue
		}
		vs = append(vs, VerifyResult{ID: r.ID, Addressed: r.Addressed, Note: strings.TrimSpace(r.Note)})
	}
	wrapped, _ := json.Marshal(response{Findings: resp.New})
	newFs, _, err := Decode(wrapped, allowed, nil)
	if err != nil {
		return vs, nil, err
	}
	return vs, newFs, nil
}

// BuildFixPrompt asks the edit-capable agent to fix exactly these findings and
// nothing else. The checks that follow are what catch it if it does more.
func BuildFixPrompt(fs []finding.Finding, conventions string, autopilot bool) string {
	var b strings.Builder
	b.WriteString("Fix the findings below in the working tree. Make the smallest change that resolves each one. ")
	b.WriteString("Do not refactor, do not touch code outside what a finding names, do not add comments explaining the fix, and do not commit — the caller commits.\n")
	if autopilot {
		b.WriteString("AUTOPILOT: the author has delegated judgement calls to you. Where a finding has more than one reasonable resolution, choose the smallest, least surprising one and do it. Leave a finding only if every resolution would change product behavior in a way the author could not want; then reply with one line starting \"declined:\" that names why. Otherwise reply with one line per finding saying what you changed.\n\n")
	} else {
	b.WriteString("If a finding cannot be fixed without a decision the author should make, leave it and reply with one line starting \"declined:\" that names the decision. Otherwise reply with one line per finding saying what you changed.\n\n")
	}
	if conventions != "" {
		b.WriteString("--- CONVENTIONS ---\n" + conventions + "\n\n")
	}
	b.WriteString("--- FINDINGS ---\n")
	for i, f := range fs {
		fmt.Fprintf(&b, "%d. [%s] %s:%d  %s\n   anchor: %s\n   %s\n", i+1, f.Severity, f.Path, f.Line, f.Rule, f.Anchor, f.Body)
		if strings.HasPrefix(f.Note, "author: ") {
			fmt.Fprintf(&b, "   THE AUTHOR HAS DECIDED: %s\n   Do it that way; this is no longer a judgement call.\n", strings.TrimPrefix(f.Note, "author: "))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// BuildPRPrompt produces title + body in the house style observed in the repo:
// problem-first prose, what changed with bold lead-ins, literal verification
// output, a before-merging block, and nothing that looks like a footer.
func BuildPRPrompt(intent, diff, checks string, fixed, filed []finding.Finding) string {
	var b strings.Builder
	b.WriteString(`Write a pull request title and body for the diff below.

Output format: the first line is the title, then a blank line, then the body in Markdown. Nothing else.

Title: imperative, describes the outcome for the user, lowercase after the first word, under 70 characters. Example: "Throttle the service-agreement settings poll to 5 minutes".

Body, in this order:
1. An unheaded lead paragraph stating the problem in prose and why it mattered — the real consequence, not "this PR fixes".
2. "### What changed" — bullets with a bold lead-in each ("- **Migration 034** adds…"). Use a table if several surfaces are affected.
3. "## Tests" — the literal check output provided below, verbatim, with any pre-existing failures explicitly excused.
4. "### Before merging" — only if there is a real caveat (sequencing, a follow-up, something not done). Omit the section otherwise.

Do not add labels, sign-offs, attribution, "Generated with", session links, or emoji. Do not describe the review process.
`)
	if intent != "" {
		b.WriteString("\n--- AUTHOR'S INTENT (commit messages) ---\n" + intent + "\n")
	}
	if len(fixed) > 0 {
		b.WriteString("\n--- FIXED DURING REVIEW (mention briefly under What changed if user-visible) ---\n")
		for _, f := range fixed {
			fmt.Fprintf(&b, "- %s:%d %s — %s\n", f.Path, f.Line, f.Rule, f.Body)
		}
	}
	if len(filed) > 0 {
		b.WriteString("\n--- NOTED, NOT ADDRESSED (candidates for Before merging) ---\n")
		for _, f := range filed {
			fmt.Fprintf(&b, "- %s:%d %s — %s\n", f.Path, f.Line, f.Rule, f.Body)
		}
	}
	b.WriteString("\n--- CHECK OUTPUT ---\n" + checks + "\n")
	b.WriteString("\n--- DIFF ---\n" + diff + "\n")
	return b.String()
}

// SplitPR separates the first line (title) from the rest (body).
func SplitPR(text string) (title, body string) {
	text = strings.TrimSpace(text)
	i := strings.IndexByte(text, '\n')
	if i < 0 {
		return text, ""
	}
	return strings.TrimSpace(text[:i]), strings.TrimSpace(text[i+1:])
}
