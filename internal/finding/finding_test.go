package finding

import (
	"encoding/json"
	"testing"
)

func TestIDSurvivesReflow(t *testing.T) {
	before := "console.warn('[mirror] backfill image step warned (rows still enqueued):', (err as Error).message)"
	after := "console.warn(\n      '[mirror] backfill image step warned (rows still enqueued):',\n      (err as Error).message\n    )"
	if ComputeID("scripts/mirror-to-cloud.ts", before, "swallowed-error") !=
		ComputeID("scripts/mirror-to-cloud.ts", after, "swallowed-error") {
		t.Fatal("prettier rewrap changed the finding ID")
	}
}

func TestIDDistinguishesRuleAndPath(t *testing.T) {
	a := ComputeID("a.ts", "x = 1", "unused")
	b := ComputeID("a.ts", "x = 1", "shadowed")
	c := ComputeID("b.ts", "x = 1", "unused")
	if a == b || a == c {
		t.Fatal("IDs collided across rule or path")
	}
}

func TestSetDedupesAndCloses(t *testing.T) {
	var s Set
	f := Finding{Path: "a.ts", Anchor: "x", Rule: "r", Severity: Ask}
	if ok, _ := s.Add(f); !ok {
		t.Fatal("first add refused")
	}
	if ok, _ := s.Add(f); ok {
		t.Fatal("duplicate add accepted")
	}
	s.Close()
	if _, err := s.Add(Finding{Path: "b.ts", Anchor: "y", Rule: "r"}); err != ErrClosed {
		t.Fatalf("add after close: got %v, want ErrClosed", err)
	}
	if s.Discovered() != 1 {
		t.Fatalf("discovered = %d, want 1", s.Discovered())
	}
}

func TestVerifyLoopNeverGrowsSet(t *testing.T) {
	var s Set
	for _, a := range []string{"a", "b", "c"} {
		s.Add(Finding{Path: "f.ts", Anchor: a, Rule: "r", Severity: Ask})
	}
	s.Close()
	base := s.Discovered()

	for round := 1; round <= 3; round++ {
		// a verify round notices something new — it must be Filed, not added
		s.File(Finding{Path: "f.ts", Anchor: "new-" + string(rune('0'+round)), Rule: "r"}, round)
		s.Transition(s.Findings[round-1].ID, Fixed, round)
		if s.Discovered() != base {
			t.Fatalf("round %d: discovered grew to %d", round, s.Discovered())
		}
	}
	c := s.Counts()
	if c.Open != 0 || c.Fixed != 3 || c.Filed != 3 {
		t.Fatalf("counts = %+v", c)
	}
}

func TestCloseFilesFileSeverity(t *testing.T) {
	var s Set
	s.Add(Finding{Path: "a", Anchor: "x", Rule: "r", Severity: Ask})
	s.Add(Finding{Path: "a", Anchor: "y", Rule: "r", Severity: File})
	s.Close()
	c := s.Counts()
	if c.Open != 1 || c.Filed != 1 || s.Discovered() != 1 {
		t.Fatalf("counts=%+v discovered=%d", c, s.Discovered())
	}
}

func TestTransitionOnlyFromOpen(t *testing.T) {
	var s Set
	s.Add(Finding{Path: "a", Anchor: "x", Rule: "r"})
	id := s.Findings[0].ID
	if err := s.Transition(id, Fixed, 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Transition(id, Accepted, 2); err == nil {
		t.Fatal("re-transition of a resolved finding was allowed")
	}
	if err := s.Transition(id, Open, 2); err == nil {
		t.Fatal("transition to Open was allowed")
	}
}

func TestStateJSONRoundTrip(t *testing.T) {
	in := Finding{ID: "abc", State: Dismissed, Severity: Block}
	b, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	var out Finding
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if out.State != Dismissed {
		t.Fatalf("state = %v", out.State)
	}
	if err := json.Unmarshal([]byte(`{"state":"bogus"}`), &out); err == nil {
		t.Fatal("bogus state accepted")
	}
}

func TestDismissListRoundTripAndPrune(t *testing.T) {
	root := t.TempDir()
	dl, err := LoadDismissList(root)
	if err != nil {
		t.Fatal(err)
	}
	f := Finding{Path: "a.ts", Anchor: "x", Rule: "nit"}
	f.EnsureID()
	dl.Add(f, "style preference")
	if err := dl.Save(root); err != nil {
		t.Fatal(err)
	}
	dl2, err := LoadDismissList(root)
	if err != nil {
		t.Fatal(err)
	}
	if !dl2.Contains(f.ID) {
		t.Fatal("dismissal did not survive save/load")
	}

	var s Set
	s.Add(f)
	s.Add(Finding{Path: "b.ts", Anchor: "y", Rule: "real"})
	if n := dl2.Prune(&s); n != 1 || len(s.Findings) != 1 || s.Findings[0].Path != "b.ts" {
		t.Fatalf("prune removed %d, left %v", n, s.Findings)
	}
	if s.ByID(f.ID) != nil {
		t.Fatal("pruned finding still indexed")
	}
}
