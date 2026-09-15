// Package project runs a repo's own checks against the files a change touched.
// A monorepo is several projects with several toolchains; the routing lives in
// config, this package just groups files by project, expands {files}, and runs
// what it's told — in parallel, because typecheck, lint, and tests don't share
// state and the wall-clock difference is large.
package project

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/richdapice/lgtm/internal/config"
)

type Check struct {
	Project string // config.Project.Path
	Kind    string // test | lint
	Command string // fully expanded
	Files   []string
}

type Result struct {
	Check
	OK       bool
	Skipped  bool
	Reason   string // why skipped
	Output   string // combined stdout+stderr, tail-trimmed
	Duration time.Duration
}

const maxOutput = 8 * 1024

// Plan groups changed repo-relative paths by project and produces one check per
// configured command. Projects with no command for a kind produce a Skipped
// result so the run can say "website: no lint configured" rather than nothing.
func Plan(cfg *config.Repo, changed []string) []Check {
	byProj := map[string][]string{}
	for _, f := range changed {
		p := cfg.ProjectFor(f)
		byProj[p.Path] = append(byProj[p.Path], f)
	}
	var names []string
	for n := range byProj {
		names = append(names, n)
	}
	sort.Strings(names)
	var checks []Check
	for _, n := range names {
		var p config.Project
		for _, cand := range cfg.Projects {
			if cand.Path == n {
				p = cand
			}
		}
		files := byProj[n]
		sort.Strings(files)
		for _, kv := range []struct{ kind, cmd string }{{"test", p.Test}, {"lint", p.Lint}} {
			checks = append(checks, Check{
				Project: n, Kind: kv.kind, Files: files,
				Command: expand(kv.cmd, n, files),
			})
		}
	}
	return checks
}

// expand substitutes {files} with the paths made relative to the project
// directory and shell-quoted, so `npx vitest related {files}` gets what vitest
// expects when run from inside the project.
func expand(cmd, proj string, files []string) string {
	if cmd == "" {
		return ""
	}
	var q []string
	for _, f := range files {
		rel := f
		if proj != "." && proj != "" {
			if r, err := filepath.Rel(proj, f); err == nil {
				rel = r
			}
		}
		q = append(q, "'"+strings.ReplaceAll(rel, "'", `'\''`)+"'")
	}
	return strings.ReplaceAll(cmd, "{files}", strings.Join(q, " "))
}

// Run executes every check concurrently, each in its project's directory.
func Run(ctx context.Context, repoRoot string, checks []Check) []Result {
	results := make([]Result, len(checks))
	var wg sync.WaitGroup
	for i, c := range checks {
		results[i] = Result{Check: c}
		if c.Command == "" {
			results[i].Skipped = true
			results[i].Reason = "no " + c.Kind + " command configured for " + c.Project
			continue
		}
		wg.Add(1)
		go func(i int, c Check) {
			defer wg.Done()
			results[i] = runOne(ctx, repoRoot, c)
		}(i, c)
	}
	wg.Wait()
	return results
}

func runOne(ctx context.Context, repoRoot string, c Check) Result {
	dir := repoRoot
	if c.Project != "." && c.Project != "" {
		dir = filepath.Join(repoRoot, c.Project)
	}
	cmd := exec.CommandContext(ctx, "sh", "-c", c.Command)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &out
	start := time.Now()
	err := cmd.Run()
	r := Result{Check: c, OK: err == nil, Duration: time.Since(start), Output: tail(out.String())}
	if err != nil && ctx.Err() != nil {
		r.Reason = ctx.Err().Error()
	}
	return r
}

func tail(s string) string {
	if len(s) <= maxOutput {
		return s
	}
	return "…" + s[len(s)-maxOutput:]
}

// AllOK is the gate: every non-skipped check passed. Skipped is not a pass — it
// is reported separately so a run can never claim green on a project it did
// not check.
func AllOK(rs []Result) (ok bool, skipped int) {
	ok = true
	for _, r := range rs {
		if r.Skipped {
			skipped++
			continue
		}
		if !r.OK {
			ok = false
		}
	}
	return ok, skipped
}
