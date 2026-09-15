package finding

import (
	"errors"
	"fmt"
)

// Set is the closed finding set F. It is append-only while discover runs and
// shrink-only after Close: Add is refused once closed, and the only way a later
// observation gets in is File, which records it as Filed and never blocks.
//
// This is the convergence guarantee. A verify round can move findings out of
// Open but can never grow the set it is verifying, so the loop terminates.
type Set struct {
	Findings []Finding `json:"findings"`
	Closed   bool      `json:"closed"`
	index    map[string]int
}

var ErrClosed = errors.New("finding: set is closed")

func (s *Set) reindex() {
	s.index = make(map[string]int, len(s.Findings))
	for i, f := range s.Findings {
		s.index[f.ID] = i
	}
}

// Add inserts a discovered finding. Returns false without error when the ID is
// already present, so two lenses flagging the same thing collapse to one.
func (s *Set) Add(f Finding) (bool, error) {
	if s.Closed {
		return false, ErrClosed
	}
	f.EnsureID()
	if s.index == nil {
		s.reindex()
	}
	if _, dup := s.index[f.ID]; dup {
		return false, nil
	}
	if f.State != Open {
		f.State = Open
	}
	s.Findings = append(s.Findings, f)
	s.index[f.ID] = len(s.Findings) - 1
	return true, nil
}

// Close ends discover. After this the set can only shrink. File-severity
// findings are moved to Filed here: "worth recording, not worth stopping for"
// is what Filed means, and it keeps Open equal to "needs a human" everywhere
// the bar and the prompt count it.
func (s *Set) Close() {
	for i := range s.Findings {
		if s.Findings[i].State == Open && s.Findings[i].Severity == File {
			s.Findings[i].State = Filed
		}
	}
	s.Closed = true
}

// File records something noticed after Close. It is always allowed, always
// Filed, and never counted as open.
func (s *Set) File(f Finding, round int) {
	f.EnsureID()
	if s.index == nil {
		s.reindex()
	}
	if _, dup := s.index[f.ID]; dup {
		return
	}
	f.State = Filed
	f.Round = round
	s.Findings = append(s.Findings, f)
	s.index[f.ID] = len(s.Findings) - 1
}

// ByID returns a pointer into the set, or nil.
func (s *Set) ByID(id string) *Finding {
	if s.index == nil {
		s.reindex()
	}
	i, ok := s.index[id]
	if !ok {
		return nil
	}
	return &s.Findings[i]
}

// Transition moves an Open finding to a resolved state. Only Open findings
// move; a resolved finding stays resolved, and Filed ones are records, not work.
func (s *Set) Transition(id string, to State, round int) error {
	f := s.ByID(id)
	if f == nil {
		return fmt.Errorf("finding: no such id %q", id)
	}
	if f.State != Open {
		return fmt.Errorf("finding: %s is %s, not open", id, f.State)
	}
	switch to {
	case Fixed, Accepted, Dismissed:
	default:
		return fmt.Errorf("finding: cannot transition to %s", to)
	}
	f.State = to
	f.Round = round
	return nil
}

// Open returns the findings still needing action, in discover order.
func (s *Set) Open() []Finding {
	var out []Finding
	for _, f := range s.Findings {
		if f.State == Open {
			out = append(out, f)
		}
	}
	return out
}

// NeedsYou counts open findings a human has to decide — file-severity ones
// never count, whatever state an older run file left them in.
func (s *Set) NeedsYou() int {
	n := 0
	for _, f := range s.Findings {
		if f.State == Open && f.Severity != File {
			n++
		}
	}
	return n
}

// Blocking reports whether any open finding has Block severity.
func (s *Set) Blocking() bool {
	for _, f := range s.Findings {
		if f.State == Open && f.Severity == Block {
			return true
		}
	}
	return false
}

// Counts is what the status bar shows.
type Counts struct {
	Open, Fixed, Accepted, Dismissed, Filed int
}

func (s *Set) Counts() Counts {
	var c Counts
	for _, f := range s.Findings {
		switch f.State {
		case Open:
			c.Open++
		case Fixed:
			c.Fixed++
		case Accepted:
			c.Accepted++
		case Dismissed:
			c.Dismissed++
		case Filed:
			c.Filed++
		}
	}
	return c
}

// Discovered is the size of the closed set — everything except Filed. This is
// the number that must never rise between rounds.
func (s *Set) Discovered() int {
	n := 0
	for _, f := range s.Findings {
		if f.State != Filed {
			n++
		}
	}
	return n
}
