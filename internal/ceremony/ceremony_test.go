package ceremony

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/richdapice/lgtm/internal/gitx"
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
