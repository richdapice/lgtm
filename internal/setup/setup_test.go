package setup

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/richdapice/lgtm/internal/config"
)

func detect(t *testing.T, root string) Detected {
	t.Helper()
	d, err := Detect(root)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func write(t *testing.T, root, rel, body string) {
	t.Helper()
	p := filepath.Join(root, rel)
	os.MkdirAll(filepath.Dir(p), 0o755)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDetectMonorepo(t *testing.T) {
	root := t.TempDir()
	write(t, root, "package.json", `{"scripts":{"test":"vitest run","lint":"eslint . --ext .ts","typecheck":"tsc --noEmit"}}`)
	write(t, root, "website/package.json", `{"scripts":{"test":"jest"}}`)
	write(t, root, "server/go.mod", "module x\n")
	write(t, root, "node_modules/dep/package.json", `{}`)

	d := detect(t, root)
	if len(d.Projects) != 3 {
		t.Fatalf("projects = %+v", d.Projects)
	}
	if d.Projects[0].Path != "server" || d.Projects[0].Test != "go test ./..." || d.Projects[0].Kind != "go" {
		t.Fatalf("server = %+v", d.Projects[0])
	}
	if d.Projects[1].Path != "website" || d.Projects[1].Test != "npx jest --findRelatedTests {files}" || d.Projects[1].Lint != "" {
		t.Fatalf("website = %+v", d.Projects[1])
	}
	r := d.Projects[2]
	if r.Path != "." || r.Test != "npx vitest related {files} --run" || r.Lint != "npm run typecheck && npx eslint {files}" {
		t.Fatalf("root = %+v", r)
	}
	if r.Kind != "node · vitest · typecheck · eslint" {
		t.Fatalf("kind = %q", r.Kind)
	}
}

func TestRunnersComeFirst(t *testing.T) {
	root := t.TempDir()
	write(t, root, "go.mod", "module x\n")
	write(t, root, "Makefile", "CC := gcc\n\n.PHONY: test lint\ntest: build\n\tgo test ./...\nlint:\n\tgolangci-lint run\ntest-all:\n\tgo test -tags integration ./...\n")
	d := detect(t, root)
	p := d.Projects[0]
	if p.Test != "make test" || p.Lint != "make lint" || p.Suite != "make test-all" {
		t.Fatalf("got %+v", p.Project)
	}
	if p.Kind != "Makefile · go" {
		t.Fatalf("kind = %q", p.Kind)
	}
}

func TestJustAndTaskfile(t *testing.T) {
	root := t.TempDir()
	write(t, root, "justfile", "set shell := [\"bash\", \"-c\"]\n\n# run tests\ntest *ARGS:\n    cargo test {{ARGS}}\n\ncheck:\n    cargo clippy\n")
	write(t, root, "Cargo.toml", "[package]\nname = \"x\"\n")
	p := detect(t, root).Projects[0]
	if p.Test != "just test" || p.Lint != "just check" {
		t.Fatalf("just: %+v", p.Project)
	}

	root = t.TempDir()
	write(t, root, "Taskfile.yml", "version: '3'\n\nvars:\n  X: 1\n\ntasks:\n  build:\n    cmds: [go build]\n  test:\n    cmds: [go test ./...]\n  lint:\n    cmds: [golangci-lint run]\n")
	p = detect(t, root).Projects[0]
	if p.Test != "task test" || p.Lint != "task lint" {
		t.Fatalf("task: %+v", p.Project)
	}
}

func TestMoreEcosystems(t *testing.T) {
	cases := []struct {
		files      map[string]string
		test, lint string
	}{
		{map[string]string{"mix.exs": ""}, "mix test", "mix format --check-formatted {files}"},
		{map[string]string{"Gemfile": "gem 'rspec'\ngem 'rubocop'"}, "bundle exec rspec", "bundle exec rubocop {files}"},
		{map[string]string{"pyproject.toml": "[tool.ruff]\n"}, "pytest", "ruff check {files}"},
		{map[string]string{"App.csproj": ""}, "dotnet test", "dotnet format --verify-no-changes"},
		{map[string]string{"pubspec.yaml": "dependencies:\n  flutter:\n"}, "flutter test", "flutter analyze"},
		{map[string]string{"composer.json": `{"require-dev":{"phpunit/phpunit":"^10","phpstan/phpstan":"^1"}}`}, "vendor/bin/phpunit", "vendor/bin/phpstan analyse {files}"},
	}
	for _, c := range cases {
		root := t.TempDir()
		for f, b := range c.files {
			write(t, root, f, b)
		}
		p := detect(t, root).Projects[0]
		if p.Test != c.test || p.Lint != c.lint {
			t.Errorf("%v: got test=%q lint=%q", c.files, p.Test, p.Lint)
		}
	}
}

func TestUserEcosystemsWin(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("LGTM_CONFIG", filepath.Join(cfg, "config.toml"))
	write(t, cfg, "ecosystems.toml", "[[ecosystem]]\nname = \"mine\"\nmarker = \"go.mod\"\ntest = \"gotestsum ./...\"\n")
	root := t.TempDir()
	write(t, root, "go.mod", "module x\n")
	p := detect(t, root).Projects[0]
	if p.Test != "gotestsum ./..." || p.Lint != "go vet ./..." || p.Kind != "mine · go" {
		t.Fatalf("got %+v kind=%q", p.Project, p.Kind)
	}
}

func TestHintsRecognizeXcode(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "App.xcodeproj"), 0o755)
	p := detect(t, root).Projects[0]
	if p.Kind != "xcode" || len(p.Hints) == 0 || !strings.Contains(strings.Join(p.Hints, "\n"), "-scheme App ") {
		t.Fatalf("got kind=%q hints=%v", p.Kind, p.Hints)
	}
	if !p.blank() {
		t.Fatalf("xcode should not guess commands: %+v", p.Project)
	}
}

func TestPromptDetectedIsOneQuestion(t *testing.T) {
	root := t.TempDir()
	d := Detected{Projects: []Project{{Project: config.Project{Path: ".", Test: "go test ./...", Lint: "go vet ./..."}, Kind: "go"}}}
	var out strings.Builder
	r, ok := Prompt(bufio.NewReader(strings.NewReader("\n")), &out, d, false) // Enter = write
	if !ok {
		t.Fatal("Enter should write")
	}
	if r.Projects[0].Test != "go test ./..." || r.Settings.Mode != "auto" || r.Settings.MaxFixRounds != 3 {
		t.Fatalf("kept = %+v", r)
	}
	if strings.Count(out.String(), "Write this?") != 1 || strings.Contains(out.String(), "Enter keeps") {
		t.Fatalf("expected exactly one question:\n%s", out.String())
	}
	if err := Write(root, r); err != nil {
		t.Fatal(err)
	}
	back, _ := config.LoadRepo(root)
	if back.Projects[0].Test != "go test ./..." {
		t.Fatalf("round trip = %+v", back)
	}
	if back.PR.OnOpen != "eyes" || back.PR.OnGreen != "+1" {
		t.Fatalf("init must not silence PR reactions: %+v", back.PR)
	}
}

func TestPromptEditWalksEveryField(t *testing.T) {
	d := Detected{Projects: []Project{{Project: config.Project{Path: ".", Test: "go test ./...", Lint: "go vet ./..."}, Kind: "go"}}}
	var out strings.Builder
	// e -> lint: keep; test: clear; suite: "true"
	r, _ := Prompt(bufio.NewReader(strings.NewReader("e\n\n-\ntrue\n")), &out, d, false)
	p := r.Projects[0]
	if p.Lint != "go vet ./..." || p.Test != "" || p.Suite != "true" {
		t.Fatalf("got %+v", p)
	}
}

func TestPromptBlankProjectValidatesCommands(t *testing.T) {
	d := Detected{Projects: []Project{{Project: config.Project{Path: "."}, Kind: "xcode", Hints: []string{"Xcode project. The usual shape:"}}}}
	var out strings.Builder
	// blank project goes straight to edit: lint "iij" (not on PATH) -> use anyway? n -> "true"; test empty; suite empty
	r, _ := Prompt(bufio.NewReader(strings.NewReader("iij\nn\ntrue\n\n\n")), &out, d, false)
	if r.Projects[0].Lint != "true" || r.Projects[0].Test != "" {
		t.Fatalf("got %+v", r.Projects[0])
	}
	s := out.String()
	if !strings.Contains(s, "Xcode project") || !strings.Contains(s, `"iij" isn't on your PATH`) || strings.Contains(s, "Write this?") {
		t.Fatalf("prompt output:\n%s", s)
	}
}

func TestPromptYesSkipsQuestions(t *testing.T) {
	var out strings.Builder
	r, _ := Prompt(bufio.NewReader(strings.NewReader("")), &out, Detected{Projects: []Project{{Project: config.Project{Path: "."}}}}, true)
	if r.Settings.Mode != "auto" || r.Settings.MaxFixRounds != 3 {
		t.Fatalf("defaults = %+v", r.Settings)
	}
	if strings.Contains(out.String(), "]: ") {
		t.Fatalf("asked a question under -y:\n%s", out.String())
	}
}

func TestScriptNarrowsPackageJSON(t *testing.T) {
	root := t.TempDir()
	write(t, root, "package.json", `{"scripts":{"test":"mocha"},"devDependencies":{"vitest":"^2"}}`)
	p := detect(t, root).Projects[0]
	if p.Test != "npm test" {
		t.Fatalf("vitest in devDependencies must not win over the test script: %q", p.Test)
	}
}

func TestBrokenUserTableIsAnError(t *testing.T) {
	cfg := t.TempDir()
	t.Setenv("LGTM_CONFIG", filepath.Join(cfg, "config.toml"))
	write(t, cfg, "ecosystems.toml", "[[ecosystem]\nname = oops\n")
	if _, err := Detect(t.TempDir()); err == nil || !strings.Contains(err.Error(), "ecosystems.toml") {
		t.Fatalf("err = %v", err)
	}
}

func TestHintsNotRepeated(t *testing.T) {
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "App.xcodeproj"), 0o755)
	os.MkdirAll(filepath.Join(root, "App.xcworkspace"), 0o755)
	p := detect(t, root).Projects[0]
	if n := strings.Count(strings.Join(p.Hints, "\n"), "swiftlint"); n != 1 {
		t.Fatalf("hints repeated %d times: %v", n, p.Hints)
	}
}

func TestPromptDeclineWritesNothing(t *testing.T) {
	d := Detected{Projects: []Project{{Project: config.Project{Path: ".", Test: "go test ./..."}, Kind: "go"}}}
	if _, ok := Prompt(bufio.NewReader(strings.NewReader("n\n")), &strings.Builder{}, d, false); ok {
		t.Fatal("n must decline")
	}
}

func TestPromptStopsAtEOF(t *testing.T) {
	// a blank project is walked; stdin ends immediately. Must not loop.
	d := Detected{Projects: []Project{{Project: config.Project{Path: "."}, Kind: "xcode"}}}
	done := make(chan config.Repo, 1)
	go func() {
		r, _ := Prompt(bufio.NewReader(strings.NewReader("")), &strings.Builder{}, d, false)
		done <- r
	}()
	select {
	case r := <-done:
		if len(r.Projects) != 1 || r.Projects[0].Test != "" {
			t.Fatalf("got %+v", r.Projects)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("prompt loops at EOF")
	}
}

func TestBlankNestedProjectIsShownThenDropped(t *testing.T) {
	root := t.TempDir()
	write(t, root, "go.mod", "module x\n")
	os.MkdirAll(filepath.Join(root, "ios", "App.xcodeproj"), 0o755)
	d := detect(t, root)
	if len(d.Projects) != 2 || d.Projects[0].Path != "ios" || len(d.Projects[0].Hints) == 0 {
		t.Fatalf("ios must be shown with its hints: %+v", d.Projects)
	}
	var out strings.Builder
	r, _ := Prompt(bufio.NewReader(strings.NewReader("\n\n\n")), &out, d, false) // ios walked, left blank
	if len(r.Projects) != 1 || r.Projects[0].Path != "." {
		t.Fatalf("blank ios must not shadow the root: %+v", r.Projects)
	}
	if !strings.Contains(out.String(), "Xcode project") {
		t.Fatalf("hints not shown:\n%s", out.String())
	}
}
