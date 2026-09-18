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

// Project is one detected project: the commands plus what we recognized it
// as and any hints for the parts we won't guess.
type Project struct {
	config.Project
	Kind  string   // "go", "node · vitest · eslint", "Makefile"; "" when nothing matched
	Hints []string // shown under the project when the table has advice but no command
}

type Detected struct {
	Projects []Project
}

// skipDirs are never projects: dependencies, build output, tool caches.
var skipDirs = map[string]bool{
	"node_modules": true, "vendor": true, "dist": true, "build": true, "out": true, "target": true,
	"bin": true, "obj": true, "coverage": true, "deps": true, "_build": true, "Pods": true,
	"DerivedData": true, "__pycache__": true, "site-packages": true, "bower_components": true,
	"Carthage": true, "gems": true, "tmp": true, "log": true, "logs": true,
}

// Detect walks two levels down for anything that looks like a project and
// proposes commands from what it finds: task runners first, then the
// ecosystem table. Nested projects come first so the root acts as the
// fallback in ProjectFor.
func Detect(root string) (Detected, error) {
	table, err := ecosystems()
	if err != nil {
		return Detected{}, err
	}
	var nested []Project
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
		if pr := detectDir(p, filepath.ToSlash(rel), table); pr.Kind != "" {
			nested = append(nested, pr)
			return filepath.SkipDir
		}
		return nil
	})
	sort.Slice(nested, func(i, j int) bool { return nested[i].Path < nested[j].Path })
	return Detected{Projects: append(nested, detectDir(root, ".", table))}, nil
}

func detectDir(dir, rel string, table []Ecosystem) Project {
	pr := Project{Project: config.Project{Path: rel}}
	var kinds []string
	if k := runner(dir, &pr.Project); k != "" {
		kinds = append(kinds, k)
	}
	more, hints := match(dir, &pr.Project, table)
	kinds = append(kinds, more...)
	pr.Kind = strings.Join(kinds, " · ")
	pr.Hints = hints
	return pr
}

func (p Project) blank() bool { return p.Test == "" && p.Lint == "" && p.Suite == "" }

// Prompt shows every project the same way, asks one question, and only walks
// the fields of a project when you ask to edit or when it's blank. A command
// that isn't on your PATH is questioned before it's written. Mode, rounds,
// and dispatch keep their defaults; the file says how to change them. The
// second return is false when the answer was no: nothing should be written.
func Prompt(in *bufio.Reader, out io.Writer, d Detected, yes bool) (config.Repo, bool) {
	// PR defaults are written out explicitly: an on_open key that is present
	// but empty means "no reaction", so leaving the struct zero would turn
	// reactions off for every repo init touched.
	r := config.Repo{
		Settings: config.Settings{Mode: "auto", MaxFixRounds: 3, Dispatch: "batch"},
		PR:       config.PR{OnOpen: "eyes", OnGreen: "+1"},
	}
	for _, p := range d.Projects {
		r.Projects = append(r.Projects, p.Project)
	}

	fmt.Fprintln(out, "lgtm init · finds what checks your code, writes .lgtm.toml")
	fmt.Fprintln(out)
	for _, p := range d.Projects {
		show(out, p)
	}
	fmt.Fprintln(out, "lint and test run on the changed files at every check. suite runs once, before the PR.")
	fmt.Fprintln(out, "{files} expands to the changed paths. Empty means skipped, never a pass.")
	if yes {
		return withoutBlankNested(r), true
	}

	rd := in
	eof := false
	readLine := func() string {
		line, err := rd.ReadString('\n')
		if err != nil {
			eof = true
		}
		return strings.TrimSpace(line)
	}
	confirm := func(q string, def bool) bool {
		d := "y/N"
		if def {
			d = "Y/n"
		}
		fmt.Fprintf(out, "%s [%s]: ", q, d)
		switch strings.ToLower(readLine()) {
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
			if eof { // input ran out: keep the defaults rather than ask forever
				return def
			}
			fmt.Fprintf(out, "    %-6s [%s]: ", label, orDash(def))
			v := readLine()
			switch v {
			case "":
				v = def
			case "-":
				v = ""
			}
			if v == "" || commandExists(v) {
				return v
			}
			fmt.Fprintf(out, "    %q isn't on your PATH.", firstWord(v))
			if confirm(" use it anyway?", false) {
				return v
			}
		}
	}
	edit := func(i int) {
		p := &r.Projects[i]
		fmt.Fprintf(out, "\n  %s   (Enter keeps, - clears)\n", p.Path)
		p.Lint = askCmd("lint", p.Lint)
		p.Test = askCmd("test", p.Test)
		p.Suite = askCmd("suite", p.Suite)
	}

	fmt.Fprintln(out)
	blank := 0
	for _, p := range d.Projects {
		if p.blank() {
			blank++
		}
	}
	all := false
	if blank < len(d.Projects) {
		fmt.Fprint(out, "Write this? [Y/n/e]  (e edits every project): ")
		switch strings.ToLower(readLine()) {
		case "e", "edit":
			all = true
		case "n", "no":
			return r, false
		}
	}
	for i, p := range d.Projects {
		if all || p.blank() {
			edit(i)
		}
	}
	return withoutBlankNested(r), true
}

// withoutBlankNested drops nested projects left with no commands: written as
// is, they would route every file under them to a project that runs no
// checks, and a fix round with no check to validate it is reverted. The root
// stays even when blank, so the file says where to put commands.
func withoutBlankNested(r config.Repo) config.Repo {
	kept := r.Projects[:0]
	for _, p := range r.Projects {
		if p.Path == "." || p.Test != "" || p.Lint != "" || p.Suite != "" {
			kept = append(kept, p)
		}
	}
	r.Projects = kept
	return r
}

// show prints one project block: path, what it was recognized as, the three
// commands, and any hints for the ones we didn't guess.
func show(out io.Writer, p Project) {
	kind := p.Kind
	if kind == "" {
		kind = "not recognized"
	}
	fmt.Fprintf(out, "  %-12s %s\n", p.Path, kind)
	fmt.Fprintf(out, "    %-6s %s\n    %-6s %s\n    %-6s %s\n", "lint", orDash(p.Lint), "test", orDash(p.Test), "suite", orDash(p.Suite))
	for _, h := range p.Hints {
		fmt.Fprintf(out, "    %s\n", h)
	}
	fmt.Fprintln(out)
}

func orDash(s string) string {
	if s == "" {
		return "-"
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
