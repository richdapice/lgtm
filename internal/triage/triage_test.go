package triage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/richdapice/lgtm/internal/diffparse"
	"github.com/richdapice/lgtm/internal/finding"
	"github.com/richdapice/lgtm/internal/jev"
)

const diff = `diff --git a/x.go b/x.go
--- a/x.go
+++ b/x.go
@@ -1,4 +1,5 @@
 package x

-func f() {}
+func f() error {
+	return nil
+}
`

func files(t *testing.T) []diffparse.FileDiff {
	t.Helper()
	fds, err := diffparse.Parse(diff)
	if err != nil {
		t.Fatal(err)
	}
	return fds
}

func TestBuildTwoQuestionsPerFindingWithContext(t *testing.T) {
	fs := []finding.Finding{
		{ID: "a", Lens: "correctness", Path: "x.go", Line: 4, Rule: "nil-error", Body: "always nil", Anchor: "return nil", Severity: finding.Ask},
		{ID: "b", Lens: "tests", Path: "x.go", Line: 0, Rule: "no-test", Body: "no test", Severity: finding.File},
	}
	st, qs := Build(fs, files(t), "make f return an error")
	if len(qs) != 4 {
		t.Fatalf("%d questions, want 2 per finding", len(qs))
	}
	raw, _ := json.Marshal(st)
	s := string(raw)
	for _, want := range []string{`"intent":"make f return an error"`, `"claim":"always nil"`, `"context":"`, `   4 + \treturn nil`} {
		if !strings.Contains(s, want) {
			t.Errorf("state missing %s:\n%s", want, s)
		}
	}
	if strings.Contains(s, `"line":0`) {
		t.Errorf("unanchored finding should omit line: %s", s)
	}
	q := qs["real_1"]
	if q.Type != "noul" || !strings.Contains(q.Instructions, "`findings[1]`") {
		t.Errorf("real_1 = %+v", q)
	}
	a := qs["action_0"]
	crit, _ := a.Criteria.(map[string]string)
	if a.Type != "choice" || len(crit) != 3 || crit["dismiss"] == "" {
		t.Errorf("action_0 = %+v", a)
	}
}

func TestDecodeNeedsBothAnswers(t *testing.T) {
	resp := jev.Response{Answers: map[string]jev.Answer{
		"real_0":   {Type: "noul", Noul: p(0.12)},
		"action_0": {Type: "choice", Choice: "accept", Confidence: 0.8},
		"real_1":   {Type: "noul", Noul: p(0.9)},
		"action_2": {Type: "choice", Choice: "maybe", Confidence: 0.5},
		"real_2":   {Type: "noul", Noul: p(0.5)},
		"real_3":   {Type: "noul"},
		"action_3": {Type: "choice", Choice: "accept", Confidence: 0.9},
		"real_4":   {Type: "noul", Noul: p(1.5)},
		"action_4": {Type: "choice", Choice: "accept", Confidence: 0.9},
	}}
	tr, ok := Decode(resp, 0)
	if !ok || tr.Real != 0.12 || tr.Suggest != "accept" || tr.Confidence != 0.8 {
		t.Fatalf("finding 0: %+v %v", tr, ok)
	}
	if _, ok := Decode(resp, 1); ok {
		t.Fatal("finding 1 has no action answer; must not decode")
	}
	if _, ok := Decode(resp, 2); ok {
		t.Fatal("finding 2 has an unknown choice; must not decode")
	}
	if _, ok := Decode(resp, 3); ok {
		t.Fatal("finding 3 has no probability; it must not read as zero")
	}
	if _, ok := Decode(resp, 4); ok {
		t.Fatal("finding 4 has a probability above 1; must not decode")
	}
}

func p(v float64) *float64 { return &v }

func TestRunScoresInPlaceAndBatches(t *testing.T) {
	requests := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		var req struct {
			State     state                   `json:"state"`
			Questions map[string]jev.Question `json:"questions"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		answers := map[string]jev.Answer{}
		for i := range req.State.Findings {
			answers[realKey(i)] = jev.Answer{Type: "noul", Noul: p(0.05)}
			answers[actionKey(i)] = jev.Answer{Type: "choice", Choice: "dismiss", Confidence: 0.95}
		}
		json.NewEncoder(w).Encode(map[string]any{"model": "m", "answers": answers, "usage": map[string]int{"input_tokens": 100}})
	}))
	defer srv.Close()

	fs := make([]finding.Finding, Batch+2)
	for i := range fs {
		fs[i] = finding.Finding{ID: string(rune('a' + i)), Path: "x.go", Line: 4, Rule: "r", Body: "b", Severity: finding.Ask}
	}
	fs[3].Line = 0 // about the change as a whole: nothing to show Jev
	c := &jev.Client{Key: "k", BaseURL: srv.URL, Model: "m"}
	res, err := Run(context.Background(), c, fs, files(t), "")
	if err != nil {
		t.Fatal(err)
	}
	if requests != 2 || res.Asked != Batch+1 || res.Scored != Batch+1 || res.Unanchored != 1 {
		t.Fatalf("requests=%d asked=%d scored=%d unanchored=%d", requests, res.Asked, res.Scored, res.Unanchored)
	}
	for i, f := range fs {
		if i == 3 {
			if f.Triage != nil {
				t.Fatalf("unanchored finding must stay unscored: %+v", f.Triage)
			}
			continue
		}
		if f.Triage == nil || f.Triage.Suggest != "dismiss" || f.Triage.Real != 0.05 {
			t.Fatalf("finding %s: %+v", f.ID, f.Triage)
		}
	}
	if res.CostUSD <= 0 {
		t.Fatal("cost must be accounted")
	}
}
