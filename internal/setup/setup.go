// Package setup is `lgtm init`: look at the repo, propose a config, ask a few
// questions npm-init style, write .lgtm.toml. It detects nested projects
// because monorepos are several toolchains and the root's lint usually
// ignores the others.
package setup

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/richdapice/lgtm/internal/config"
)

type Detected struct {
	Projects []config.Project
	Notes    []string
	// Hints are example commands for what the repo looks like, shown when we
	// recognize the project type but won't guess its commands (Xcode, Gradle).
	Hints []string
}

var skipDirs = map[string]bool{
	"node_modules": true, ".git": true, "dist": true, "build": true, "vendor": true,
	".claude": true, ".next": true, "out": true, "target": true, "coverage": true, ".venv": true,
}

// Detect walks two levels down for anything that looks like a project and
// proposes commands from what it finds. Nested projects come first so the
// root acts as the fallback in ProjectFor.
func Detect(root string) Detected {
	var d Detected
	var nested []config.Project
	filepath.WalkDir(root, func(p string, e os.DirEntry, err error) error {
		if err != nil || !e.IsDir() {
			return nil
		}
		rel, _ := filepath.Rel(root, p)
		if rel == "." {
			return nil
		}
		if skipDirs[e.Name()] || strings.HasPrefix(e.Name(), ".") {
			return filepath.SkipDir
		}
		if strings.Count(rel, string(filepath.Separator)) >= 2 {
			return filepath.SkipDir
		}
		if pr, ok := detectDir(p, filepath.ToSlash(rel)); ok {
			nested = append(nested, pr)
			return filepath.SkipDir
		}
		return nil
	})
	sort.Slice(nested, func(i, j int) bool { return nested[i].Path < nested[j].Path })
	d.Projects = append(d.Projects, nested...)
	rootP, ok := detectDir(root, ".")
	if !ok {
		rootP = config.Project{Path: "."}
		d.Hints = hints(root)
		if len(d.Hints) == 0 {
			d.Notes = append(d.Notes, "couldn't tell what kind of project this is; commands are yours to set")
		}
	}
	d.Projects = append(d.Projects, rootP)
	return d
}

// hints recognizes project types we deliberately don't write commands for
// (the right command depends on a scheme, a module, a simulator) and offers
// the shape of one instead.
func hints(root string) []string {
	var h []string
	has := func(glob string) bool { m, _ := filepath.Glob(filepath.Join(root, glob)); return len(m) > 0 }
	switch {
	case has("*.xcodeproj") || has("*.xcworkspace"):
		scheme := "<Scheme>"
		if m, _ := filepath.Glob(filepath.Join(root, "*.xcodeproj")); len(m) > 0 {
			scheme = strings.TrimSuffix(filepath.Base(m[0]), ".xcodeproj") // the usual default scheme
		}
		h = append(h,
			"Xcode project. Likely commands:",
			`  lint   swiftlint lint --strict`,
			`  test   (leave empty unless you have a fast unit target)`,
			`  suite  xcodebuild test -scheme `+scheme+` -destination 'platform=iOS Simulator,name=iPhone 16' -quiet`)
	case has("Package.swift"):
		h = append(h, "Swift package. Likely commands:", `  lint   swiftlint lint --strict`, `  test   swift test`)
	case has("build.gradle") || has("build.gradle.kts") || has("settings.gradle*"):
		h = append(h, "Gradle project. Likely commands:", `  lint   ./gradlew lint`, `  test   ./gradlew testDebugUnitTest`, `  suite  ./gradlew test`)
	case has("Makefile"):
		h = append(h, "There's a Makefile. Likely commands:", `  test   make test`, `  lint   make lint`)
	}
	return h
}

func detectDir(dir, rel string) (config.Project, bool) {
	p := config.Project{Path: rel}
	if b, err := os.ReadFile(filepath.Join(dir, "package.json")); err == nil {
		var pkg struct {
			Scripts map[string]string `json:"scripts"`
		}
		json.Unmarshal(b, &pkg)
		switch t := pkg.Scripts["test"]; {
		case strings.Contains(t, "vitest"):
			p.Test = "npx vitest related {files} --run"
		case strings.Contains(t, "jest"):
			p.Test = "npx jest --findRelatedTests {files}"
		case t != "":
			p.Test = "npm test"
		}
		var lint []string
		if _, ok := pkg.Scripts["typecheck"]; ok {
			lint = append(lint, "npm run typecheck")
		}
		switch l := pkg.Scripts["lint"]; {
		case strings.Contains(l, "eslint"):
			lint = append(lint, "npx eslint {files}")
		case l != "":
			lint = append(lint, "npm run lint")
		}
		p.Lint = strings.Join(lint, " && ")
		return p, true
	}
	if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
		p.Test, p.Lint = "go test ./...", "go vet ./..."
		return p, true
	}
	if b, err := os.ReadFile(filepath.Join(dir, "pyproject.toml")); err == nil {
		p.Test = "pytest"
		if strings.Contains(string(b), "ruff") {
			p.Lint = "ruff check {files}"
		}
		return p, true
	}
	if _, err := os.Stat(filepath.Join(dir, "Cargo.toml")); err == nil {
		p.Test, p.Lint = "cargo test", "cargo clippy -- -D warnings"
		return p, true
	}
	return p, false
}

// Prompt asks as little as it can. Detected commands get one yes/no. Only a
// project we couldn't read gets asked field by field, with hints for what the
// repo looks like, and a command that isn't on your PATH is questioned before
// it's written. Mode, rounds, and dispatch keep their defaults; the file says
// how to change them.
func Prompt(in io.Reader, out io.Writer, d Detected, yes bool) config.Repo {
	r := config.Repo{
		Settings: config.Settings{Mode: "auto", MaxFixRounds: 3, Dispatch: "batch"},
		Projects: d.Projects,
	}
	for _, n := range d.Notes {
		fmt.Fprintln(out, "note:", n)
	}
	if yes {
		return r
	}
	rd := bufio.NewReader(in)
	ask := func(label, def string) string {
		fmt.Fprintf(out, "  %s [%s]: ", label, def)
		line, _ := rd.ReadString('\n')
		line = strings.TrimSpace(line)
		switch line {
		case "":
			return def
		case "-":
			return ""
		}
		return line
	}
	confirm := func(q string, def bool) bool {
		d := "y/N"
		if def {
			d = "Y/n"
		}
		fmt.Fprintf(out, "  %s [%s]: ", q, d)
		line, _ := rd.ReadString('\n')
		switch strings.ToLower(strings.TrimSpace(line)) {
		case "y", "yes":
			return true
		case "n", "no":
			return false
		}
		return def
	}
	// a command whose program isn't on PATH is probably a typo; say so
	askCmd := func(label, def string) string {
		for {
			v := ask(label, def)
			if v == "" || commandExists(v) {
				return v
			}
			fmt.Fprintf(out, "  %q isn't on your PATH.", firstWord(v))
			if confirm(" use it anyway?", false) {
				return v
			}
		}
	}

	detected, blank := 0, 0
	for _, p := range r.Projects {
		if p.Test != "" || p.Lint != "" {
			detected++
		} else {
			blank++
		}
	}
	if detected > 0 {
		fmt.Fprintf(out, "found %d project(s):\n", detected)
		for _, p := range r.Projects {
			if p.Test == "" && p.Lint == "" {
				continue
			}
			fmt.Fprintf(out, "  %-12s test: %s\n  %-12s lint: %s\n", p.Path, orNone(p.Test), "", orNone(p.Lint))
		}
		if !confirm("keep these?", true) {
			for i := range r.Projects {
				p := &r.Projects[i]
				if p.Test == "" && p.Lint == "" {
					continue
				}
				fmt.Fprintf(out, "\n%s  (Enter keeps, - clears)\n", p.Path)
				p.Test = askCmd("test", p.Test)
				p.Lint = askCmd("lint", p.Lint)
			}
		}
	}
	if blank > 0 {
		for _, h := range d.Hints {
			fmt.Fprintln(out, h)
		}
		fmt.Fprintln(out, "Commands run on the changed files; {files} expands to them. Empty means skipped (never counted as a pass).")
		for i := range r.Projects {
			p := &r.Projects[i]
			if p.Test != "" || p.Lint != "" {
				continue
			}
			fmt.Fprintf(out, "\n%s\n", p.Path)
			p.Lint = askCmd("lint  (fast; every check)", p.Lint)
			p.Test = askCmd("test  (fast; every check)", p.Test)
			p.Suite = askCmd("suite (slow; once, before the PR)", p.Suite)
		}
	}
	fmt.Fprintln(out, "\nautopilot, 3 fix rounds, one review call. Change any of it in .lgtm.toml.")
	return r
}

func orNone(s string) string {
	if s == "" {
		return "(none)"
	}
	return s
}

func firstWord(cmd string) string {
	f := strings.Fields(cmd)
	if len(f) == 0 {
		return ""
	}
	return f[0]
}

// commandExists checks the first word: on PATH, or a path that exists
// (./gradlew, ./scripts/test.sh). Anything after && is not checked.
func commandExists(cmd string) bool {
	w := firstWord(cmd)
	if w == "" {
		return true
	}
	if strings.ContainsAny(w, "/") {
		_, err := os.Stat(w)
		return err == nil
	}
	_, err := exec.LookPath(w)
	return err == nil
}

// Write emits .lgtm.toml with a header explaining the two things people edit.
func Write(root string, r config.Repo) error {
	f, err := os.Create(filepath.Join(root, config.RepoFile))
	if err != nil {
		return err
	}
	defer f.Close()
	fmt.Fprint(f, `# lgtm — review this repo before a PR opens.
#
# [[project]] routes changed files to the commands that check them; the first
# whose path prefixes a file wins, so nested projects go before ".". {files}
# expands to the changed paths relative to that project. An empty command is
# skipped and reported as skipped — never counted as a pass.
#
# [lens.<name>] model = "haiku" | "sonnet" | "opus" overrides the agent default.
# Add a lens of your own: [lens.perf] prompt = "what it should look for".

`)
	return toml.NewEncoder(f).Encode(r)
}

// WireStatusline points Claude Code's status bar at this binary. Idempotent;
// backs up settings.json the first time it changes it.
func WireStatusline(bin string) (changed bool, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return false, err
	}
	p := filepath.Join(home, ".claude", "settings.json")
	var settings map[string]any
	if b, err := os.ReadFile(p); err == nil {
		if err := json.Unmarshal(b, &settings); err != nil {
			return false, fmt.Errorf("%s: %w", p, err)
		}
	} else {
		settings = map[string]any{}
	}
	want := map[string]any{"type": "command", "command": bin + " statusline", "refreshInterval": 2}
	if cur, ok := settings["statusLine"].(map[string]any); ok && cur["command"] == want["command"] {
		return false, nil
	}
	if _, err := os.Stat(p); err == nil {
		os.WriteFile(p+".bak-lgtm", mustRead(p), 0o644)
	}
	settings["statusLine"] = want
	b, _ := json.MarshalIndent(settings, "", "  ")
	os.MkdirAll(filepath.Dir(p), 0o755)
	return true, os.WriteFile(p, append(b, '\n'), 0o644)
}

func mustRead(p string) []byte { b, _ := os.ReadFile(p); return b }
