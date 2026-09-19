package triage

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/richdapice/lgtm/internal/diffparse"
	"github.com/richdapice/lgtm/internal/jev"
)

const twoHunks = `diff --git a/a.go b/a.go
--- a/a.go
+++ b/a.go
@@ -1,3 +1,4 @@
 package a
+// a comment

 func f() {}
@@ -10,3 +11,4 @@
 func g() error {
-	return nil
+	_ = risky()
+	return nil
 }
`

func TestRankScoresSortsAndAnchors(t *testing.T) {
	var seen rankState
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			State     rankState               `json:"state"`
			Questions map[string]jev.Question `json:"questions"`
		}
		json.NewDecoder(r.Body).Decode(&req)
		seen = req.State
		if len(req.Questions) != 2*len(req.State.Hunks) {
			t.Errorf("%d questions for %d hunks", len(req.Questions), len(req.State.Hunks))
		}
		answers := map[string]jev.Answer{
			riskKey(0): {Type: "noul", Noul: p(0.1)}, lensKey(0): {Type: "choice", Choice: "none", Confidence: 0.9},
			riskKey(1): {Type: "noul", Noul: p(0.8)}, lensKey(1): {Type: "choice", Choice: "correctness", Confidence: 0.7},
		}
		json.NewEncoder(w).Encode(map[string]any{"model": "m", "answers": answers, "usage": map[string]int{"input_tokens": 50}})
	}))
	defer srv.Close()

	files, err := diffparse.Parse(twoHunks)
	if err != nil {
		t.Fatal(err)
	}
	c := &jev.Client{Key: "k", BaseURL: srv.URL, Model: "m"}
	scores, res, err := Rank(context.Background(), c, files, "add risky call")
	if err != nil {
		t.Fatal(err)
	}
	if res.Asked != 2 || res.Scored != 2 {
		t.Fatalf("res = %+v", res)
	}
	if len(seen.Hunks) != 2 || !strings.Contains(seen.Hunks[1].Diff, "+\t_ = risky()") || !strings.Contains(seen.Hunks[1].Diff, "-\treturn nil") {
		t.Fatalf("state sent = %+v", seen.Hunks)
	}
	if scores[0].Risk != 0.8 || scores[0].Lens != "correctness" || scores[0].NewStart != 11 || scores[0].NewEnd != 14 {
		t.Fatalf("top = %+v", scores[0])
	}
	if !scores[0].Contains("a.go", 12) || scores[0].Contains("a.go", 2) || scores[0].Contains("b.go", 12) {
		t.Fatal("Contains must be by path and new-line range")
	}
	if scores[1].Preview != "// a comment" {
		t.Fatalf("preview = %q", scores[1].Preview)
	}
}

func TestRankLeavesMalformedUnscored(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		answers := map[string]jev.Answer{
			riskKey(0): {Type: "noul"}, lensKey(0): {Type: "choice", Choice: "none"},
			riskKey(1): {Type: "noul", Noul: p(0.5)}, lensKey(1): {Type: "choice", Choice: "style"},
		}
		json.NewEncoder(w).Encode(map[string]any{"model": "m", "answers": answers})
	}))
	defer srv.Close()
	files, _ := diffparse.Parse(twoHunks)
	c := &jev.Client{Key: "k", BaseURL: srv.URL, Model: "m"}
	scores, res, err := Rank(context.Background(), c, files, "")
	if err != nil {
		t.Fatal(err)
	}
	if res.Scored != 0 || len(scores) != 2 {
		t.Fatalf("res=%+v scores=%d", res, len(scores))
	}
	for _, s := range scores {
		if s.Risk != 0 || s.Lens != "" {
			t.Fatalf("malformed answer must leave the hunk unscored: %+v", s)
		}
	}
}

func TestRenderHunkTruncates(t *testing.T) {
	h := diffparse.Hunk{}
	for i := 0; i < 400; i++ {
		h.Lines = append(h.Lines, diffparse.Line{Kind: diffparse.Added, Text: strings.Repeat("x", 40)})
	}
	text, truncated := renderHunk(h)
	if !truncated || len(text) > hunkChars+100 || !strings.HasSuffix(text, "(truncated)\n") {
		t.Fatalf("truncated=%v len=%d", truncated, len(text))
	}
}
