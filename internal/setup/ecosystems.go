package setup

import (
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/richdapice/lgtm/internal/config"
)

//go:embed ecosystems.toml
var builtinEcosystems string

// Ecosystem is one row of ecosystems.toml: a marker file and the commands (or
// hints) it implies. See the file's header for the matching rules.
type Ecosystem struct {
	Name     string   `toml:"name"`
	Marker   string   `toml:"marker"` // glob, relative to the project dir
	Contains []string `toml:"contains,omitempty"`
	// Script narrows a package.json-style marker to one whose "scripts" map
	// has each key, with the value containing the given text ("" = any).
	Script map[string]string `toml:"script,omitempty"`
	Test   string            `toml:"test,omitempty"`
	Lint   string            `toml:"lint,omitempty"`
	Suite  string            `toml:"suite,omitempty"`
	Hint   []string          `toml:"hint,omitempty"`
}

type ecosystemFile struct {
	Ecosystems []Ecosystem `toml:"ecosystem"`
}

// ecosystems is the user's table, if any, followed by the built-in one. A
// broken user file is an error, not an empty table: init would otherwise
// look like it simply didn't recognize the override.
func ecosystems() ([]Ecosystem, error) {
	var user, builtin ecosystemFile
	if _, err := toml.Decode(builtinEcosystems, &builtin); err != nil {
		panic("setup: embedded ecosystems.toml: " + err.Error())
	}
	path := config.EcosystemsPath()
	b, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
	case err != nil:
		return nil, err
	default:
		if err := toml.Unmarshal(b, &user); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
	}
	return append(user.Ecosystems, builtin.Ecosystems...), nil
}

// match fills whatever the project still lacks from the ecosystems whose
// marker is in dir. The names it returns are the languages first and then
// every narrower entry that actually contributed a command or a hint.
func match(dir string, p *config.Project, table []Ecosystem) (kinds, hints []string) {
	var base, used []string
	seen := map[string]bool{}
	for _, e := range table {
		if seen[e.Name] { // same name, same advice: one is enough
			continue
		}
		files, _ := filepath.Glob(filepath.Join(dir, e.Marker))
		if len(files) == 0 || !containsAll(files[0], e.Contains) || !hasScripts(files[0], e.Script) {
			continue
		}
		name := strings.TrimSuffix(filepath.Base(files[0]), filepath.Ext(files[0]))
		contributed := false
		set := func(dst *string, v string) {
			if *dst == "" && v != "" {
				*dst, contributed = v, true
			}
		}
		set(&p.Test, e.Test)
		set(&p.Lint, e.Lint)
		set(&p.Suite, e.Suite)
		for _, h := range e.Hint {
			hints = append(hints, strings.ReplaceAll(h, "{name}", name))
			contributed = true
		}
		// An entry without a contains clause names the language itself (go,
		// python, node) and is always worth showing; a narrower one (vitest,
		// ruff) only when it supplied something.
		general := len(e.Contains) == 0 && len(e.Script) == 0
		if !(contributed || general) {
			continue
		}
		seen[e.Name] = true
		if general {
			base = append(base, e.Name)
		} else {
			used = append(used, e.Name)
		}
	}
	return append(base, used...), hints
}

// hasScripts reads the marker as JSON and checks its "scripts" map: each key
// must exist and its value contain the wanted text.
func hasScripts(path string, want map[string]string) bool {
	if len(want) == 0 {
		return true
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var pkg struct {
		Scripts map[string]string `json:"scripts"`
	}
	if json.Unmarshal(b, &pkg) != nil {
		return false
	}
	for k, sub := range want {
		v, ok := pkg.Scripts[k]
		if !ok || !strings.Contains(v, sub) {
			return false
		}
	}
	return true
}

func containsAll(path string, needles []string) bool {
	if len(needles) == 0 {
		return true
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, n := range needles {
		if !strings.Contains(string(b), n) {
			return false
		}
	}
	return true
}
