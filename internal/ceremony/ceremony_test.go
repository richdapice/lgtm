package ceremony

import (
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/richdapice/lgtm/internal/config"
	"github.com/richdapice/lgtm/internal/finding"
	"github.com/richdapice/lgtm/internal/gitx"
	"github.com/richdapice/lgtm/internal/project"
	"github.com/richdapice/lgtm/internal/run"
)

func repo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	ctx := context.Background()
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@t"},
		{"config", "user.name", "t"},
	} {
		if _, err := gitx.Run(ctx, dir, args...); err != nil {
			t.Fatal(err)
		}
	}
	os.MkdirAll(filepath.Join(dir, "internal"), 0o755)
	os.WriteFile(filepath.Join(dir, "internal", "a.go"), []byte("package a\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b\n"), 0o644)
	gitx.Run(ctx, dir, "add", ".")
	gitx.Run(ctx, dir, "commit", "-q", "-m", "init")
	return dir
}

// The first dirty path once lost its leading character: porcelain output was
// trimmed before the status column was sliced off. Plain listings now.
func TestDirtyFilesKeepWholePaths(t *testing.T) {
	dir := repo(t)
	ctx := context.Background()
	os.WriteFile(filepath.Join(dir, "internal", "a.go"), []byte("package a // changed\n"), 0o644)
	os.Remove(filepath.Join(dir, "b.txt"))
	os.WriteFile(filepath.Join(dir, "new.go"), []byte("package new\n"), 0o644)
	got, err := dirtyFiles(ctx, dir)
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(got)
	if strings.Join(got, ",") != "b.txt,internal/a.go,new.go" {
		t.Fatalf("got %v", got)
	}
	if _, err := gitx.Run(ctx, dir, append([]string{"add", "--"}, got...)...); err != nil {
		t.Fatalf("the paths must be stageable as-is: %v", err)
	}
}

func TestGatherConventionsSources(t *testing.T) {
	t.Setenv("HOME", t.TempDir()) // keep the developer's own global files out
	root := t.TempDir()
	w := func(rel, body string) {
		p := filepath.Join(root, rel)
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, []byte(body), 0o644)
	}
	w("CLAUDE.md", "root claude")
	w("web/.cursorrules", "web cursor")
	w("web/deep/AGENTS.md", "deep agents")
	w("ios/GEMINI.md", "ios gemini (unchanged dir, must not appear)")
	w(".github/copilot-instructions.md", "copilot")
	w(".cursor/rules/a.mdc", "cursor mdc")
	w("docs/style/naming.md", "naming")
	os.WriteFile(filepath.Join(filepath.Dir(root), "outside.md"), []byte("SECRET"), 0o644)

	got := gatherConventions(root, []string{"web/deep/x.ts"}, []string{"docs/style/*.md", "../outside.md"})
	for _, want := range []string{"root claude", "web cursor", "deep agents", "copilot", "cursor mdc", "naming"} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	for _, no := range []string{"ios gemini", "SECRET"} {
		if strings.Contains(got, no) {
			t.Errorf("must not include %q in:\n%s", no, got)
		}
	}
}

func TestEnvironmentFailureIsAnchored(t *testing.T) {
	fail := func(out string) []project.Result {
		return []project.Result{{Check: project.Check{Project: ".", Kind: "test"}, OK: false, Output: out}}
	}
	for _, out := range []string{
		"sh: 1: vitest: command not found",
		"fork/exec /usr/bin/gotestsum: no such file or directory",
		"Error: Cannot find module 'react'",
		"ModuleNotFoundError: No module named 'pytest'",
	} {
		if environmentFailure(fail(out)) == "" {
			t.Errorf("should be an environment failure: %q", out)
		}
	}
	for _, out := range []string{
		"--- FAIL: TestLoad\n    open testdata/fixture.json: no such file or directory",
		"expected key: not found in map",
		"assert 2 == 3",
	} {
		if got := environmentFailure(fail(out)); got != "" {
			t.Errorf("ordinary failure flagged as environment (%q): %q", got, out)
		}
	}
	if environmentFailure([]project.Result{{OK: true, Output: "command not found"}}) != "" {
		t.Error("a passing check is never an environment failure")
	}
}

func TestSkipByTriageOnlyWavesThroughUnlikelyAsks(t *testing.T) {
	c := &Ceremony{repo: &config.Repo{Triage: config.Triage{SkipBelow: 0.2}}}
	low := &finding.Triage{Real: 0.1, Suggest: "accept", Confidence: 0.9}
	cases := []struct {
		name string
		f    finding.Finding
		want bool
	}{
		{"unlikely ask", finding.Finding{Severity: finding.Ask, Triage: low}, true},
		{"unlikely block still goes to the fixer", finding.Finding{Severity: finding.Block, Triage: low}, false},
		{"likely ask", finding.Finding{Severity: finding.Ask, Triage: &finding.Triage{Real: 0.6, Suggest: "accept"}}, false},
		{"jev says fix, however unlikely", finding.Finding{Severity: finding.Ask, Triage: &finding.Triage{Real: 0.1, Suggest: "fix"}}, false},
		{"unscored", finding.Finding{Severity: finding.Ask}, false},
	}
	for _, tc := range cases {
		if got := c.skipByTriage(tc.f); got != tc.want {
			t.Errorf("%s: got %v", tc.name, got)
		}
	}
	c.repo.Triage.SkipBelow = 0
	if c.skipByTriage(cases[0].f) {
		t.Error("skip_below = 0 must turn skipping off")
	}
}

// Autopilot's skip is only worth anything if the finding lands where a
// human sees it: Filed with the reason, which the PR body lists.
func TestAutopilotFilesSkippedFindingsWithReason(t *testing.T) {
	c := &Ceremony{
		repo:   &config.Repo{Triage: config.Triage{SkipBelow: 0.2}},
		common: t.TempDir(),
		o:      Options{Out: io.Discard, Log: log.New(io.Discard, "", 0)},
		run:    run.New("b", "main", "auto", 3, nil, nil),
	}
	c.run.Findings.Add(finding.Finding{ID: "skip", Path: "a.go", Line: 1, Anchor: "x", Rule: "r1", Severity: finding.Ask,
		Triage: &finding.Triage{Real: 0.05, Suggest: "accept", Confidence: 0.9}})
	c.run.Findings.Add(finding.Finding{ID: "keep", Path: "a.go", Line: 2, Anchor: "y", Rule: "r2", Severity: finding.Ask,
		Triage: &finding.Triage{Real: 0.9, Suggest: "fix", Confidence: 0.9}})
	c.run.Findings.Close()
	if err := c.rounds(context.Background()); err != nil {
		t.Fatal(err)
	}
	skip := c.run.Findings.ByID("skip")
	if skip.State != finding.Filed || !strings.Contains(skip.Note, "not sent to the fixer") || !strings.Contains(skip.Note, "5% real") {
		t.Fatalf("skipped finding = %s %q", skip.State, skip.Note)
	}
	keep := c.run.Findings.ByID("keep")
	if keep.State != finding.Filed || strings.Contains(keep.Note, "not sent") {
		t.Fatalf("kept finding (no fixer configured) = %s %q", keep.State, keep.Note)
	}
}
