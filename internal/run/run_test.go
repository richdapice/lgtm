package run

import (
	"testing"
	"time"

	"github.com/richdapice/lgtm/internal/finding"
)

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	r := New("feat/x", "main", "manual", 3, []string{"correctness", "tests"}, map[string]string{"tests": "sonnet"})
	r.Findings.Add(finding.Finding{Path: "a.ts", Anchor: "x", Rule: "r", Severity: finding.Ask})
	r.Findings.Close()
	r.CostUSD = 0.043
	if err := r.Save(dir); err != nil {
		t.Fatal(err)
	}
	got, err := Load(dir, "feat/x")
	if err != nil {
		t.Fatal(err)
	}
	if got.Branch != "feat/x" || len(got.Lenses) != 2 || got.Lenses[1].Model != "sonnet" {
		t.Fatalf("loaded = %+v", got)
	}
	if !got.Findings.Closed || got.Findings.Discovered() != 1 {
		t.Fatalf("findings did not round-trip: %+v", got.Findings)
	}
	if got.PID <= 0 || !got.Alive() {
		t.Fatal("own PID should read as alive")
	}
	all, _ := All(dir)
	if len(all) != 1 {
		t.Fatalf("All = %d runs", len(all))
	}
	if err := Remove(dir, "feat/x"); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(dir, "feat/x"); err == nil {
		t.Fatal("run still loadable after Remove")
	}
}

func TestHistoryTail(t *testing.T) {
	dir := t.TempDir()
	for i := 0; i < 25; i++ {
		AppendHistory(dir, Summary{Branch: "b", EndedAt: time.Now(), Duration: time.Duration(i) * time.Second, Outcome: Done})
	}
	h, err := History(dir, 20)
	if err != nil {
		t.Fatal(err)
	}
	if len(h) != 20 || h[0].Duration != 5*time.Second || h[19].Duration != 24*time.Second {
		t.Fatalf("history tail wrong: len=%d first=%v last=%v", len(h), h[0].Duration, h[len(h)-1].Duration)
	}
}

func TestDeadPIDNotAlive(t *testing.T) {
	r := &Run{PID: 999999999}
	if r.Alive() {
		t.Fatal("nonexistent PID reported alive")
	}
}
