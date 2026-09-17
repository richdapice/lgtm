// Package lens turns a diff into findings. One call can carry several lenses
// (batch dispatch, the default) or one (parallel dispatch); the prompt and schema are
// the same either way, which is what lets the two modes be a config switch.
//
// Decode is deliberately strict where the agent tier is lenient: unknown
// fields, bad enums, and empty bodies are rejected here, so the prompt-tier
// path gets the same validation the native tier gets from the CLI.
package lens

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/richdapice/lgtm/internal/diffparse"
	"github.com/richdapice/lgtm/internal/finding"
)

// Schema is what every lens call must return. It is passed as --json-schema on
// the native tier and pasted into the prompt on the prompt tier.
var Schema = json.RawMessage(`{
  "type": "object",
  "properties": {
    "findings": {
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
  "required": ["findings"],
  "additionalProperties": false
}`)

// Descriptions are the prompt text per lens. Kept short: the model knows what
// a security review is; what it needs is the boundary of this one.
var Descriptions = map[string]string{
	"correctness": "bugs, edge cases, error handling that swallows or mis-reports failures, logic that does not do what the diff claims to do",
	"conventions": "departures from the project's stated conventions (quoted below) and from the style of the surrounding code",
	"security":    "secrets in source, injection, auth and permission mistakes, unsafe IPC or deserialization, data written somewhere it should not be",
	"tests":       "behavior changed without a test changing, tests that do not assert the new behavior, tests that would pass if the change were reverted",
}

type Input struct {
	Lenses      []string
	Prompts     map[string]string // per lens; Descriptions when nil
	Diff        string
	Conventions string // the repo's instruction files (CLAUDE.md, AGENTS.md, .cursorrules, ...), may be empty
	Intent      string // optional: what the author says the change is for
}

func BuildPrompt(in Input) string {
	var b strings.Builder
	b.WriteString("You are reviewing a branch diff before it becomes a pull request. ")
	b.WriteString("Report findings only: things a careful reviewer would raise. Do not summarize, praise, or restate the diff.\n\n")
	b.WriteString("Lenses to apply:\n")
	for _, l := range in.Lenses {
		p := in.Prompts[l]
		if p == "" {
			p = Descriptions[l]
		}
		fmt.Fprintf(&b, "- %s: %s\n", l, p)
	}
	b.WriteString(`
Severity:
- block: must be fixed before this can merge — a bug, a secret, a broken invariant
- ask: a judgement call a human should make
- file: worth recording, not worth stopping for

For each finding give: lens; path; line number in the NEW file (0 if it is about the change as a whole); anchor — the exact source text of that line, copied verbatim, because that is how the finding is identified across fix rounds; rule — a short stable kebab-case slug such as hardcoded-secret or missing-null-check; body — one to three sentences saying what is wrong and what would fix it.

Do not report formatting, anything the linter already enforces, code outside the diff, or hypotheticals with no evidence in the code. If there is nothing worth raising, return an empty findings array.
`)
	if in.Intent != "" {
		b.WriteString("\n--- INTENT (from the author) ---\n")
		b.WriteString(in.Intent)
		b.WriteString("\n")
	}
	if in.Conventions != "" {
		b.WriteString("\n--- CONVENTIONS ---\n")
		b.WriteString(in.Conventions)
		b.WriteString("\n")
	}
	b.WriteString("\n--- DIFF (unified, merge-base..head) ---\n")
	b.WriteString(in.Diff)
	b.WriteString("\n")
	return b.String()
}

type raw struct {
	Lens     string `json:"lens"`
	Path     string `json:"path"`
	Line     int    `json:"line"`
	Anchor   string `json:"anchor"`
	Severity string `json:"severity"`
	Rule     string `json:"rule"`
	Body     string `json:"body"`
}

type response struct {
	Findings []raw `json:"findings"`
}

// Stats says what Decode did to the raw output, for the log and the bar.
type Stats struct {
	Received   int
	Snapped    int // line moved to the nearest commentable line
	Unanchored int // no commentable line nearby; kept as a general finding
	Rejected   int // failed validation
}

// Decode validates and salvages. A finding whose line is not in the diff is
// snapped to the nearest commentable line within 3, else kept unanchored — it
// is never dropped for a bad line number, because the observation is usually
// right even when the number is off by one.
func Decode(out json.RawMessage, allowed []string, files []diffparse.FileDiff) ([]finding.Finding, Stats, error) {
	dec := json.NewDecoder(bytes.NewReader(out))
	dec.DisallowUnknownFields()
	var resp response
	if err := dec.Decode(&resp); err != nil {
		return nil, Stats{}, fmt.Errorf("lens: decode: %w", err)
	}
	ok := map[string]bool{}
	for _, l := range allowed {
		ok[l] = true
	}
	var st Stats
	st.Received = len(resp.Findings)
	var fs []finding.Finding
	for _, r := range resp.Findings {
		if !ok[r.Lens] || r.Path == "" || strings.TrimSpace(r.Body) == "" || r.Rule == "" {
			st.Rejected++
			continue
		}
		sev := finding.Severity(r.Severity)
		if sev != finding.Block && sev != finding.Ask && sev != finding.File {
			st.Rejected++
			continue
		}
		f := finding.Finding{
			Lens: r.Lens, Path: r.Path, Line: r.Line, Side: "RIGHT",
			Anchor: r.Anchor, Severity: sev, Rule: slug(r.Rule), Body: strings.TrimSpace(r.Body),
			State: finding.Open,
		}
		if f.Line > 0 && !diffparse.Commentable(files, f.Path, f.Line, "RIGHT") {
			if n := diffparse.NearestCommentable(files, f.Path, f.Line, 3); n != 0 {
				f.Line = n
				st.Snapped++
			} else {
				f.Line = 0
				st.Unanchored++
			}
		}
		f.EnsureID()
		fs = append(fs, f)
	}
	return fs, st, nil
}

// slug normalizes the rule so two lenses spelling it differently still collide
// into one ID: lowercase, spaces and underscores to hyphens.
func slug(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.NewReplacer(" ", "-", "_", "-").Replace(s)
	return strings.Trim(s, "-")
}
