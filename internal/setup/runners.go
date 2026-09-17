package setup

import (
	"bufio"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/richdapice/lgtm/internal/config"
)

// Task runners are read before any language table: a repo with a Makefile
// that has a test target has already told us how it wants to be tested, in
// whatever language it happens to be.
var runners = []struct {
	file    string
	cmd     string
	targets func(string) []string
}{
	{"justfile", "just", justTargets},
	{"Justfile", "just", justTargets},
	{"Taskfile.yml", "task", taskfileTargets},
	{"Taskfile.yaml", "task", taskfileTargets},
	{"Makefile", "make", makeTargets},
	{"makefile", "make", makeTargets},
	{"GNUmakefile", "make", makeTargets},
}

// runner fills lint/test/suite from the first task runner in dir that has
// matching targets. The names it looks for: test, lint (or check), and any of
// test-all / test:all / integration for the suite.
func runner(dir string, p *config.Project) (kind string) {
	for _, r := range runners {
		b, err := os.ReadFile(filepath.Join(dir, r.file))
		if err != nil {
			continue
		}
		have := map[string]bool{}
		for _, t := range r.targets(string(b)) {
			have[t] = true
		}
		pick := func(names ...string) string {
			for _, n := range names {
				if have[n] {
					return r.cmd + " " + n
				}
			}
			return ""
		}
		test, lint, suite := pick("test"), pick("lint", "check"), pick("test-all", "test:all", "integration", "e2e")
		if test == "" && lint == "" && suite == "" {
			continue
		}
		if p.Test == "" {
			p.Test = test
		}
		if p.Lint == "" {
			p.Lint = lint
		}
		if p.Suite == "" {
			p.Suite = suite
		}
		return r.file
	}
	return ""
}

var (
	makeTarget = regexp.MustCompile(`^([A-Za-z0-9_.\-]+)\s*:([^=]|$)`)
	justRecipe = regexp.MustCompile(`^@?([A-Za-z0-9_\-]+)(\s+[^:]*)?:(\s|$)`)
	taskKey    = regexp.MustCompile(`^  ([A-Za-z0-9_:\-]+):`)
)

func makeTargets(src string) []string {
	var out []string
	for _, l := range lines(src) {
		if m := makeTarget.FindStringSubmatch(l); m != nil && !strings.HasPrefix(l, ".") {
			out = append(out, m[1])
		}
	}
	return out
}

func justTargets(src string) []string {
	var out []string
	for _, l := range lines(src) {
		if strings.HasPrefix(l, "set ") || strings.HasPrefix(l, "#") {
			continue
		}
		if m := justRecipe.FindStringSubmatch(l); m != nil {
			out = append(out, m[1])
		}
	}
	return out
}

func taskfileTargets(src string) []string {
	var out []string
	in := false
	for _, l := range lines(src) {
		switch {
		case strings.HasPrefix(l, "tasks:"):
			in = true
		case in && len(l) > 0 && l[0] != ' ' && l[0] != '#':
			in = false
		case in:
			if m := taskKey.FindStringSubmatch(l); m != nil {
				out = append(out, m[1])
			}
		}
	}
	return out
}

func lines(src string) []string {
	var out []string
	sc := bufio.NewScanner(strings.NewReader(src))
	for sc.Scan() {
		out = append(out, sc.Text())
	}
	return out
}
