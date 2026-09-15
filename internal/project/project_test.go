package project

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/richdapice/lgtm/internal/config"
)

func TestPlanRoutesAndExpands(t *testing.T) {
	cfg := &config.Repo{Projects: []config.Project{
		{Path: "website", Lint: "eslint {files}"},
		{Path: ".", Test: "vitest related {files} --run", Lint: "eslint {files}"},
	}}
	checks := Plan(cfg, []string{"src/b.ts", "website/app/page.tsx", "src/a.ts"})
	if len(checks) != 4 {
		t.Fatalf("checks = %+v", checks)
	}
	// "." sorts before "website"
	if checks[0].Project != "." || checks[0].Kind != "test" ||
		checks[0].Command != "vitest related 'src/a.ts' 'src/b.ts' --run" {
		t.Fatalf("root test = %+v", checks[0])
	}
	if checks[2].Project != "website" || checks[2].Kind != "test" || checks[2].Command != "" {
		t.Fatalf("website test should be empty: %+v", checks[2])
	}
	if checks[3].Command != "eslint 'app/page.tsx'" {
		t.Fatalf("website lint not relative to project: %q", checks[3].Command)
	}
}

func TestRunParallelAndSkipped(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "sub"), 0o755)
	checks := []Check{
		{Project: ".", Kind: "test", Command: "printf ok; sleep 0.3"},
		{Project: "sub", Kind: "lint", Command: "pwd; sleep 0.3"},
		{Project: ".", Kind: "lint", Command: "echo boom >&2; exit 1"},
		{Project: "sub", Kind: "test", Command: ""},
	}
	rs := Run(context.Background(), root, checks)
	ok, skipped := AllOK(rs)
	if ok || skipped != 1 {
		t.Fatalf("ok=%v skipped=%d", ok, skipped)
	}
	if !rs[0].OK || rs[0].Output != "ok" {
		t.Fatalf("r0 = %+v", rs[0])
	}
	if !strings.HasSuffix(strings.TrimSpace(rs[1].Output), "sub") {
		t.Fatalf("sub check did not run in sub/: %q", rs[1].Output)
	}
	if rs[2].OK || !strings.Contains(rs[2].Output, "boom") {
		t.Fatalf("failing check = %+v", rs[2])
	}
	// two 300ms sleeps ran concurrently if the slower took well under 600ms
	if rs[0].Duration+rs[1].Duration < 500*1e6 && rs[1].Duration > 550*1e6 {
		t.Fatal("checks appear to have run serially")
	}
}
