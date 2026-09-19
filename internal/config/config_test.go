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

passes = ["sonnet", "opus"]

[lens.security]
enabled = false

[lens.perf]
model = "haiku"
prompt = "hot paths doing more work than they need to"

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
	if len(r.Settings.Passes) != 2 || r.Settings.Passes[1] != "opus" {
		t.Fatalf("passes = %v", r.Settings.Passes)
	}
	if r.Settings.Mode != "auto" || r.Settings.MaxFixRounds != 2 || r.Settings.Dispatch != "batch" {
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

func TestLensPrompts(t *testing.T) {
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, RepoFile), []byte(`
[lens.perf]
prompt = "hot paths"
[lens.correctness]
prompt = "my own idea of correctness"
`), 0o644)
	r, _ := LoadRepo(root)
	ps, err := r.LensPrompts(map[string]string{"correctness": "builtin", "conventions": "b", "security": "b", "tests": "b"})
	if err != nil {
		t.Fatal(err)
	}
	if ps["perf"] != "hot paths" || ps["correctness"] != "my own idea of correctness" || ps["tests"] != "b" {
		t.Fatalf("prompts = %v", ps)
	}
	os.WriteFile(filepath.Join(root, RepoFile), []byte("[lens.mystery]\n"), 0o644)
	r, _ = LoadRepo(root)
	if _, err := r.LensPrompts(map[string]string{}); err == nil {
		t.Fatal("custom lens without a prompt accepted")
	}
}

func TestLoadRepoMissingIsDefaults(t *testing.T) {
	r, err := LoadRepo(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Settings.Passes) != 1 {
		t.Fatalf("default passes = %v", r.Settings.Passes)
	}
	if len(r.EnabledLenses()) != 4 || len(r.Projects) != 1 {
		t.Fatalf("defaults = %+v", r)
	}
}

func TestPRReactionsAndComment(t *testing.T) {
	root := t.TempDir()
	r, _ := LoadRepo(root)
	if r.PR.OnOpen != "eyes" || r.PR.OnGreen != "+1" || !r.PR.ReactionsOn() {
		t.Fatalf("defaults = %+v", r.PR)
	}
	os.WriteFile(filepath.Join(root, RepoFile), []byte(`
[pr]
on_open = ""
on_green = "rocket"
comment = "lgtm: {found} found, {fixed} fixed"
`), 0o644)
	r, err := LoadRepo(root)
	if err != nil {
		t.Fatal(err)
	}
	if r.PR.OnOpen != "" || r.PR.OnGreen != "rocket" || r.PR.Comment == "" {
		t.Fatalf("custom = %+v", r.PR)
	}
	os.WriteFile(filepath.Join(root, RepoFile), []byte("[pr]\non_green = \"thumbs\"\n"), 0o644)
	if _, err := LoadRepo(root); err == nil {
		t.Fatal("unknown reaction accepted")
	}
}

func TestIgnoreGlobs(t *testing.T) {
	st := Settings{Ignore: []string{"**/*.md", "docs/**", ".github/**", "*.lock"}}
	for path, want := range map[string]bool{
		"README.md": true, "src/deep/notes.md": true, "docs/a/b.png": true, ".github/workflows/ci.yml": true,
		"yarn.lock": true, "src/main/sync.ts": false, "docs.ts": false, "src/lock": false,
	} {
		if got := st.Ignored(path); got != want {
			t.Errorf("Ignored(%q) = %v, want %v", path, got, want)
		}
	}
}

func TestTriageDefaultsAndBounds(t *testing.T) {
	r, err := LoadRepo(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if !r.Triage.On() || r.Triage.SkipBelow != DefaultSkipBelow {
		t.Fatalf("triage defaults = %+v", r.Triage)
	}
	root := t.TempDir()
	os.WriteFile(filepath.Join(root, RepoFile), []byte("[triage]\nenabled = false\nskip_below = 0\n"), 0o644)
	r, err = LoadRepo(root)
	if err != nil {
		t.Fatal(err)
	}
	if r.Triage.On() || r.Triage.SkipBelow != 0 {
		t.Fatalf("explicit zero must stick: %+v", r.Triage)
	}
	os.WriteFile(filepath.Join(root, RepoFile), []byte("[triage]\nskip_below = 1.5\n"), 0o644)
	if _, err := LoadRepo(root); err == nil {
		t.Fatal("skip_below above 1 must be rejected")
	}
}

func TestGlobalTriageIsOffUntilWritten(t *testing.T) {
	t.Setenv("LGTM_CONFIG", filepath.Join(t.TempDir(), "config.toml"))
	g, err := LoadGlobal()
	if err != nil {
		t.Fatal(err)
	}
	if g.Triage {
		t.Fatal("triage must be off by default")
	}
	g.Triage = true
	if err := WriteGlobal(g); err != nil {
		t.Fatal(err)
	}
	g, err = LoadGlobal()
	if err != nil || !g.Triage {
		t.Fatalf("triage did not round-trip: %+v %v", g, err)
	}
}
