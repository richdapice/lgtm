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
	"path/filepath"
	"sort"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/richdapice/lgtm/internal/config"
)

type Detected struct {
	Projects []config.Project
	Notes    []string
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
		d.Notes = append(d.Notes, "no package.json / go.mod / pyproject.toml / Cargo.toml at the root; set commands by hand")
	}
	d.Projects = append(d.Projects, rootP)
	return d
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

// Prompt confirms each detected command. Enter keeps the default, "-" clears
// it, anything else replaces it. yes skips the questions entirely.
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
	fmt.Fprintf(out, "%d project(s) detected. Enter keeps the default, - clears it.\n", len(r.Projects))
	for i := range r.Projects {
		p := &r.Projects[i]
		fmt.Fprintf(out, "\n%s\n", p.Path)
		p.Test = ask("test", p.Test)
		p.Lint = ask("lint", p.Lint)
	}
	fmt.Fprintln(out)
	r.Settings.Mode = ask("mode (manual|auto)", r.Settings.Mode)
	fmt.Sscanf(ask("max fix rounds", "3"), "%d", &r.Settings.MaxFixRounds)
	r.Settings.Dispatch = ask("dispatch (batch|parallel)", r.Settings.Dispatch)
	return r
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
