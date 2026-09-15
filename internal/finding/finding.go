// Package finding is the review's unit of work: one thing a lens noticed about a
// diff.
//
// The load-bearing decision here is identity. A finding has to survive the fix
// round that acts on it, because the round after that asks "is this one
// addressed yet?" — and a fix reflows the file, so a line number is not an
// identity. IDs therefore hash the anchor text with whitespace collapsed, which
// means a reformat (prettier rewrapping a line, say) leaves the ID alone.
package finding

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// State is where a finding sits in the run. A finding starts Open and only ever
// leaves the set downward — Filed is the escape hatch for something noticed
// during a verify round, which must not reopen the closed set.
type State int

const (
	Open      State = iota // needs action
	Fixed                  // a fix round applied a change and verify confirmed it
	Accepted               // the user looked and said it's fine as-is
	Dismissed              // the user said "not this, not ever" — goes to the dismiss-list
	Filed                  // noticed too late to act on; recorded, not blocking
)

var stateNames = map[State]string{
	Open: "open", Fixed: "fixed", Accepted: "accepted",
	Dismissed: "dismissed", Filed: "filed",
}

func (s State) String() string {
	if n, ok := stateNames[s]; ok {
		return n
	}
	return fmt.Sprintf("state(%d)", int(s))
}

func (s State) MarshalJSON() ([]byte, error) {
	n, ok := stateNames[s]
	if !ok {
		return nil, fmt.Errorf("finding: unknown state %d", int(s))
	}
	return json.Marshal(n)
}

func (s *State) UnmarshalJSON(b []byte) error {
	var n string
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	for st, name := range stateNames {
		if name == n {
			*s = st
			return nil
		}
	}
	return fmt.Errorf("finding: unknown state %q", n)
}

// Severity is what the finding costs you. Only Block stops the ceremony; Ask
// waits for a human in manual mode; File never blocks anything.
type Severity string

const (
	Block Severity = "block"
	Ask   Severity = "ask"
	File  Severity = "file"
)

// Finding is one observation about the diff.
type Finding struct {
	ID       string   `json:"id"`
	Lens     string   `json:"lens"`
	Path     string   `json:"path"`
	Line     int      `json:"line"`           // 0 means unanchored
	Side     string   `json:"side,omitempty"` // RIGHT or LEFT
	Anchor   string   `json:"anchor"`         // source text the finding is about
	Severity Severity `json:"severity"`
	Rule     string   `json:"rule"` // short stable slug, e.g. "hardcoded-secret"
	Body     string   `json:"body"`
	State    State    `json:"state"`
	Round    int      `json:"round"`          // verify round that last touched it
	Note     string   `json:"note,omitempty"` // what the fixer or verifier said about it
	// FixDeclined is set when the fixer looked and refused — it needs a human
	// decision. Autopilot never re-sends one of these; a person can.
	FixDeclined bool `json:"fix_declined,omitempty"`
}

// Anchored reports whether the finding points at a specific line. An unanchored
// finding is still real — it folds into the PR review body rather than being
// dropped.
func (f Finding) Anchored() bool { return f.Path != "" && f.Line > 0 }

// ComputeID derives the stable identity. Whitespace in the anchor is collapsed
// so that reformatting does not mint a new finding, and the rule slug is part of
// the hash so two lenses flagging the same line for different reasons stay
// distinct.
func ComputeID(path, anchor, rule string) string {
	h := sha256.New()
	h.Write([]byte(path))
	h.Write([]byte{0})
	h.Write([]byte(NormalizeAnchor(anchor)))
	h.Write([]byte{0})
	h.Write([]byte(rule))
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// NormalizeAnchor strips every whitespace character. Collapsing runs to one
// space is not enough: a formatter inserts whitespace where there was none
// (prettier turns `warn('x'` into `warn(\n  'x'`), so only removal makes an ID
// survive a reflow. Two anchors that differ only in whitespace are the same
// code for review purposes.
func NormalizeAnchor(s string) string { return strings.Join(strings.Fields(s), "") }

// EnsureID fills in the ID if a lens did not supply one.
func (f *Finding) EnsureID() {
	if f.ID == "" {
		f.ID = ComputeID(f.Path, f.Anchor, f.Rule)
	}
}
