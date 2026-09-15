// Package config is two TOML files: a user-level one naming the agents you have
// installed, and a repo-level .lgtm.toml saying how this repo wants to be
// reviewed. They are split because agents are a property of the machine and
// lenses are a property of the codebase.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Global lives at $LGTM_CONFIG, else $XDG_CONFIG_HOME/lgtm/config.toml, else
// ~/.config/lgtm/config.toml. os.UserConfigDir is deliberately not used: on
// macOS it resolves to ~/Library/Application Support, which nobody edits by hand.
type Global struct {
	DefaultAgent string  `toml:"default_agent"`
	Agents       []Agent `toml:"agent"`
}

// Agent is a CLI that reads a prompt on stdin and answers on stdout. Schema says
// how it takes a JSON schema: "native" means it has a flag for one and returns
// validated output; "prompt" means we paste the schema into the prompt and parse
// leniently. See internal/agent for what each tier costs.
type Agent struct {
	Name    string   `toml:"name"`
	Command []string `toml:"command"`
	Schema  string   `toml:"schema"` // native | prompt
	Model   string   `toml:"model"`  // default model; a lens may override

	// FixCommand is the same CLI with edit permission, for the fix round.
	// Reviewing and fixing are different postures and get different argv.
	// Empty means fixes are unavailable for this agent (manual mode still
	// works; you just do the edits yourself).
	FixCommand []string `toml:"fix_command"`
}

// Repo is .lgtm.toml at the repository root.
type Repo struct {
	Settings Settings        `toml:"lgtm"`
	Lenses   map[string]Lens `toml:"lens"`
	Projects []Project       `toml:"project"`
}

type Settings struct {
	Mode         string `toml:"mode"`           // manual | auto
	MaxFixRounds int    `toml:"max_fix_rounds"` // verify rounds before handing off
	Fanout       string `toml:"fanout"`         // single | parallel
	Agent        string `toml:"agent"`          // overrides Global.DefaultAgent
	Base         string `toml:"base"`           // PR base; empty = detect default branch
	// MaxBudgetUSD caps each agent call. A reviewer with Read/Grep can explore
	// well beyond the diff, which is where quality comes from and where cost
	// goes; this is the knob. 0 = uncapped.
	MaxBudgetUSD float64 `toml:"max_budget_usd"`
}

type Lens struct {
	Model   string `toml:"model"`
	Enabled *bool  `toml:"enabled"` // nil = enabled
}

// Project routes changed paths to the commands that check them. Order matters:
// the first project whose Path is a prefix of the file wins, so list nested
// projects before ".".
type Project struct {
	Path string `toml:"path"`
	Test string `toml:"test"` // {files} expands to the changed paths in this project
	Lint string `toml:"lint"`
}

const RepoFile = ".lgtm.toml"

var DefaultLenses = []string{"correctness", "conventions", "security", "tests"}

func GlobalPath() string {
	if p := os.Getenv("LGTM_CONFIG"); p != "" {
		return p
	}
	if x := os.Getenv("XDG_CONFIG_HOME"); x != "" {
		return filepath.Join(x, "lgtm", "config.toml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "config.toml"
	}
	return filepath.Join(home, ".config", "lgtm", "config.toml")
}

// LoadGlobal reads the user config. A missing file yields the built-in default:
// claude in read-only print mode, which is the only agent we can assume on a
// machine running Claude Code.
func LoadGlobal() (*Global, error) {
	var g Global
	if _, err := toml.DecodeFile(GlobalPath(), &g); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("config: %s: %w", GlobalPath(), err)
		}
		g = Global{}
	}
	if len(g.Agents) == 0 {
		g.Agents = []Agent{DefaultClaude()}
	}
	if g.DefaultAgent == "" {
		g.DefaultAgent = g.Agents[0].Name
	}
	return &g, nil
}

// DefaultClaude is the read-only posture: no Bash, no WebFetch, nothing that
// would prompt. A reviewer reads the diff; it does not run things.
func DefaultClaude() Agent {
	return Agent{
		Name:    "claude",
		Command: []string{"claude", "-p", "--restricted", "--permission-prompts", "none", "--output-format", "json"},
		Schema:  "native",
		Model:   "opus",
		FixCommand: []string{"claude", "-p", "--permission-mode", "acceptEdits", "--permission-prompts", "none",
			"--allowedTools", "Read,Edit,Write,Grep,Glob", "--output-format", "json"},
	}
}

func (g *Global) Agent(name string) (Agent, bool) {
	if name == "" {
		name = g.DefaultAgent
	}
	for _, a := range g.Agents {
		if a.Name == name {
			return a, true
		}
	}
	return Agent{}, false
}

// LoadRepo reads .lgtm.toml from the repo root. Missing is fine: the defaults
// are manual mode, three rounds, single-call fanout, all four lenses, and one
// project at "." with no commands (so the fix loop will apply edits but not
// re-run checks until you tell it how).
func LoadRepo(root string) (*Repo, error) {
	var r Repo
	if _, err := toml.DecodeFile(filepath.Join(root, RepoFile), &r); err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			return nil, fmt.Errorf("config: %s: %w", RepoFile, err)
		}
	}
	if r.Settings.Mode == "" {
		r.Settings.Mode = "manual"
	}
	if r.Settings.MaxFixRounds == 0 {
		r.Settings.MaxFixRounds = 3
	}
	if r.Settings.Fanout == "" {
		r.Settings.Fanout = "single"
	}
	if r.Lenses == nil {
		r.Lenses = map[string]Lens{}
	}
	for _, l := range DefaultLenses {
		if _, ok := r.Lenses[l]; !ok {
			r.Lenses[l] = Lens{}
		}
	}
	if len(r.Projects) == 0 {
		r.Projects = []Project{{Path: "."}}
	}
	return &r, nil
}

// EnabledLenses returns lens names in a stable order — DefaultLenses first, then
// any extra ones from the file sorted by name — so the status bar doesn't
// reorder rows between ticks.
func (r *Repo) EnabledLenses() []string {
	var out []string
	seen := map[string]bool{}
	for _, l := range DefaultLenses {
		if cfg, ok := r.Lenses[l]; ok && (cfg.Enabled == nil || *cfg.Enabled) {
			out = append(out, l)
		}
		seen[l] = true
	}
	var extra []string
	for name, cfg := range r.Lenses {
		if !seen[name] && (cfg.Enabled == nil || *cfg.Enabled) {
			extra = append(extra, name)
		}
	}
	sortStrings(extra)
	return append(out, extra...)
}

// ProjectFor routes a repo-relative path to its project. "." matches
// everything, so it must be listed last to act as the fallback.
func (r *Repo) ProjectFor(rel string) Project {
	rel = filepath.ToSlash(rel)
	for _, p := range r.Projects {
		if p.Path == "." || p.Path == "" {
			continue
		}
		prefix := filepath.ToSlash(p.Path) + "/"
		if rel == p.Path || len(rel) > len(prefix) && rel[:len(prefix)] == prefix {
			return p
		}
	}
	for _, p := range r.Projects {
		if p.Path == "." || p.Path == "" {
			return p
		}
	}
	return Project{Path: "."}
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}
