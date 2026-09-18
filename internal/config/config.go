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
	"regexp"
	"strings"

	"github.com/BurntSushi/toml"
)

// Global lives at $LGTM_CONFIG, else $XDG_CONFIG_HOME/lgtm/config.toml, else
// ~/.config/lgtm/config.toml. os.UserConfigDir is deliberately not used: on
// macOS it resolves to ~/Library/Application Support, which nobody edits by hand.
type Global struct {
	DefaultAgent string  `toml:"default_agent"`
	Agents       []Agent `toml:"agent,omitempty"`
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
	PR       PR              `toml:"pr"`
	Lenses   map[string]Lens `toml:"lens"`
	Projects []Project       `toml:"project"`
}

type Settings struct {
	Mode         string `toml:"mode"`           // manual | auto
	MaxFixRounds int    `toml:"max_fix_rounds"` // verify rounds before handing off
	// Dispatch is how the lenses are sent to the agent: "batch" is one call
	// carrying every lens; "parallel" is one call per lens, concurrently, each
	// on its own model.
	Dispatch string `toml:"dispatch"`
	// Passes is how many times the diff is reviewed, and on what: one model
	// per pass, findings unioned. ["sonnet", "opus"] is a cheap first read
	// with a stronger second opinion. At least one; default is the agent's
	// model.
	Passes []string `toml:"passes,omitempty"`
	// Ignore lists globs of changed files that never trigger checks: docs, CI
	// config, anything your tests don't care about. A diff made only of these
	// skips check and recheck. ** matches across directories.
	Ignore []string `toml:"ignore,omitempty"`
	// Conventions lists extra files (globs, relative to the root) the
	// conventions lens should read, on top of the instruction files it finds
	// on its own (CLAUDE.md, AGENTS.md, .cursorrules, and the like).
	Conventions []string `toml:"conventions,omitempty"`
	Agent       string   `toml:"agent,omitempty"` // overrides Global.DefaultAgent
	Base        string   `toml:"base,omitempty"`  // PR base; empty = detect default branch
	// MaxBudgetUSD caps each agent call. A reviewer with Read/Grep can explore
	// well beyond the diff, which is where quality comes from and where cost
	// goes; this is the knob. 0 = uncapped.
	MaxBudgetUSD float64 `toml:"max_budget_usd,omitempty"`
}

// PR is what lgtm leaves on the pull request. Reactions are GitHub's fixed
// set; the comment is yours, with a few placeholders expanded.
type PR struct {
	Reactions *bool  `toml:"reactions"` // nil = on
	OnOpen    string `toml:"on_open"`   // reaction when the PR opens; "" = none
	OnGreen   string `toml:"on_green"`  // reaction when CI is green; "" = none
	// Comment is posted once the run is through, when set. Placeholders:
	// {found} {fixed} {accepted} {filed} {rounds} {cost} {branch} {url}
	Comment string `toml:"comment"`
}

// GitHub's reaction names, the only ones the API accepts.
var reactionNames = map[string]bool{"+1": true, "-1": true, "laugh": true, "confused": true, "heart": true, "hooray": true, "rocket": true, "eyes": true}

func (p PR) ReactionsOn() bool { return p.Reactions == nil || *p.Reactions }

func (p PR) validate() error {
	for _, r := range []string{p.OnOpen, p.OnGreen} {
		if r != "" && !reactionNames[r] {
			return fmt.Errorf("config: [pr] reaction %q is not one GitHub knows (+1 -1 laugh confused heart hooray rocket eyes)", r)
		}
	}
	return nil
}

type Lens struct {
	Model   string `toml:"model"`
	Enabled *bool  `toml:"enabled"` // nil = enabled
	// Prompt is what the lens looks for, in a sentence or two. Required for a
	// lens you add; the built-in four have theirs already and you can
	// override them here.
	Prompt string `toml:"prompt"`
}

// Project routes changed paths to the commands that check them. Order matters:
// the first project whose Path is a prefix of the file wins, so list nested
// projects before ".".
type Project struct {
	Path string `toml:"path"`
	Test string `toml:"test"` // {files} expands to the changed paths in this project
	Lint string `toml:"lint"`
	// Suite is the slow one: the whole test run, for projects whose tests
	// can't be scoped to changed files. It runs once, before the PR opens,
	// never at the floor or in a fix round.
	Suite string `toml:"suite,omitempty"`
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

// EcosystemsPath is the user's addition to what `lgtm init` recognizes,
// next to the global config.
func EcosystemsPath() string {
	return filepath.Join(filepath.Dir(GlobalPath()), "ecosystems.toml")
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

// Recipes are the agents lgtm knows how to drive out of the box, in the
// order init offers them. Each is a reviewer that can read but not run, and
// a fixer that can edit but not run. Claude and Copilot are verified; Gemini
// and Codex follow their documented flags, and init's probe is what verifies
// them on a given machine.
func Recipes() []Agent {
	return []Agent{
		DefaultClaude(),
		{
			Name:       "copilot",
			Command:    []string{"copilot", "-s", "--no-ask-user", "--deny-tool", "shell", "--deny-tool", "write"},
			FixCommand: []string{"copilot", "-s", "--no-ask-user", "--deny-tool", "shell", "--allow-tool", "write"},
			Schema:     "prompt",
		},
		{
			Name:       "gemini",
			Command:    []string{"gemini", "-p", "", "--approval-mode", "plan"},
			FixCommand: []string{"gemini", "-p", "", "--approval-mode", "auto_edit"},
			Schema:     "prompt",
		},
		{
			Name:       "codex",
			Command:    []string{"codex", "exec", "--sandbox", "read-only", "--skip-git-repo-check", "-"},
			FixCommand: []string{"codex", "exec", "--sandbox", "workspace-write", "--skip-git-repo-check", "-"},
			Schema:     "prompt",
		},
	}
}

// GlobalExists reports whether the user has a config file at all, which is
// how init decides whether to ask about agents.
func GlobalExists() bool {
	_, err := os.Stat(GlobalPath())
	return err == nil
}

// WriteGlobal writes the user config with a header saying what it is for.
func WriteGlobal(g *Global) error {
	p := GlobalPath()
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	f, err := os.Create(p)
	if err != nil {
		return err
	}
	defer f.Close()
	fmt.Fprint(f, `# lgtm — which agent reviews and fixes, machine-wide.
#
# default_agent picks one of the [[agent]] entries below; a repo can override
# it with agent = "name" under [lgtm] in .lgtm.toml. Each agent has a review
# command (reads, never runs) and a fix_command (edits, never runs). Any CLI
# that takes a prompt on stdin and answers on stdout can be added the same
# way; schema = "native" only for CLIs that accept --json-schema.
#
# lgtm doctor checks every agent here.

`)
	return toml.NewEncoder(f).Encode(g)
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
// are autopilot, three rounds, batch dispatch, all four lenses, and one
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
		r.Settings.Mode = "auto"
	}
	if r.Settings.MaxFixRounds == 0 {
		r.Settings.MaxFixRounds = 3
	}
	if r.Settings.Dispatch == "" {
		r.Settings.Dispatch = "batch"
	}
	if _, set := rawKeys(root, "pr", "on_open"); !set {
		r.PR.OnOpen = "eyes"
	}
	if _, set := rawKeys(root, "pr", "on_green"); !set {
		r.PR.OnGreen = "+1"
	}
	if err := r.PR.validate(); err != nil {
		return nil, err
	}
	if len(r.Settings.Passes) == 0 {
		r.Settings.Passes = []string{""} // one pass on the agent's default model
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

// LensPrompts is the prompt text per enabled lens: the built-in text unless
// the config overrides it, and whatever the config says for a lens of its
// own. A custom lens without a prompt is an error — the agent would have
// nothing to look for.
func (r *Repo) LensPrompts(builtin map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for _, name := range r.EnabledLenses() {
		if p := r.Lenses[name].Prompt; p != "" {
			out[name] = p
			continue
		}
		if p, ok := builtin[name]; ok {
			out[name] = p
			continue
		}
		return nil, fmt.Errorf("config: lens %q needs a prompt (what should it look for?)", name)
	}
	return out, nil
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

// rawKeys reports whether a key was present in the file at all, so an explicit
// on_open = "" can mean "no reaction" while an absent key means the default.
func rawKeys(root, table, key string) (string, bool) {
	var raw map[string]map[string]any
	if _, err := toml.DecodeFile(filepath.Join(root, RepoFile), &raw); err != nil {
		return "", false
	}
	v, ok := raw[table][key]
	if !ok {
		return "", false
	}
	s, _ := v.(string)
	return s, true
}

// Ignored reports whether a repo-relative path matches any ignore glob.
func (s Settings) Ignored(rel string) bool {
	for _, g := range s.Ignore {
		if globMatch(g, rel) {
			return true
		}
	}
	return false
}

// globMatch is filepath.Match plus **, which matches any number of path
// segments (including none), the way .gitignore and most tools read it.
func globMatch(pattern, name string) bool {
	var re strings.Builder
	re.WriteString("^")
	for i := 0; i < len(pattern); i++ {
		c := pattern[i]
		switch {
		case c == '*' && i+1 < len(pattern) && pattern[i+1] == '*':
			i++
			if i+1 < len(pattern) && pattern[i+1] == '/' {
				i++
				re.WriteString("(?:.*/)?")
			} else {
				re.WriteString(".*")
			}
		case c == '*':
			re.WriteString("[^/]*")
		case c == '?':
			re.WriteString("[^/]")
		default:
			re.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	re.WriteString("$")
	ok, _ := regexp.MatchString(re.String(), name)
	return ok
}
