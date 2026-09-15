package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGlobalPathPrecedence(t *testing.T) {
	t.Setenv("LGTM_CONFIG", "/explicit/lgtm.toml")
	t.Setenv("XDG_CONFIG_HOME", "/xdg")
	if p := GlobalPath(); p != "/explicit/lgtm.toml" {
		t.Fatalf("LGTM_CONFIG not honored: %s", p)
	}
	t.Setenv("LGTM_CONFIG", "")
	if p := GlobalPath(); p != "/xdg/lgtm/config.toml" {
		t.Fatalf("XDG not honored: %s", p)
	}
}

func TestLoadGlobalDefaultsToClaude(t *testing.T) {
	t.Setenv("LGTM_CONFIG", filepath.Join(t.TempDir(), "missing.toml"))
	g, err := LoadGlobal()
	if err != nil {
		t.Fatal(err)
	}
	a, ok := g.Agent("")
	if !ok || a.Name != "claude" || a.Schema != "native" {
		t.Fatalf("default agent = %+v", a)
	}
	for _, want := range []string{"--restricted", "--permission-prompts", "--output-format"} {
		found := false
		for _, arg := range a.Command {
			if arg == want {
				found = true
			}
		}
		if !found {
			t.Errorf("default claude command missing %s: %v", want, a.Command)
		}
	}
}

func TestLoadRepoDefaultsAndRouting(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, RepoFile), []byte(`
[lgtm]
max_fix_rounds = 2

[lens.security]
enabled = false

[lens.perf]
model = "haiku"

[[project]]
path = "website"
lint = ""

[[project]]
path = "."
test = "npx vitest related {files} --run"
`), 0o644)

	r, err := LoadRepo(root)
	if err != nil {
		t.Fatal(err)
	}
	if r.Settings.Mode != "auto" || r.Settings.MaxFixRounds != 2 || r.Settings.Fanout != "single" {
		t.Fatalf("settings = %+v", r.Settings)
	}
	got := r.EnabledLenses()
	want := []string{"correctness", "conventions", "tests", "perf"}
	if len(got) != len(want) {
		t.Fatalf("lenses = %v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("lenses = %v, want %v", got, want)
		}
	}
	if p := r.ProjectFor("website/app/page.tsx"); p.Path != "website" {
		t.Fatalf("website file routed to %q", p.Path)
	}
	if p := r.ProjectFor("src/main/index.ts"); p.Path != "." || p.Test == "" {
		t.Fatalf("root file routed to %+v", p)
	}
	if p := r.ProjectFor("websiteish/x.ts"); p.Path != "." {
		t.Fatalf("prefix match too loose: %q", p.Path)
	}
}

func TestLoadRepoMissingIsDefaults(t *testing.T) {
	r, err := LoadRepo(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.EnabledLenses()) != 4 || len(r.Projects) != 1 {
		t.Fatalf("defaults = %+v", r)
	}
}
