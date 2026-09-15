// Package ceremony is the run: discover findings, work them in bounded rounds,
// open the PR, watch CI. It owns no git state — it reads the branch, commits
// fixes like a person would, and pushes once. Everything it knows between
// steps is in the run file so the status bar can draw it and a held run can
// resume.
package ceremony

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/richdapice/lgtm/internal/agent"
	"github.com/richdapice/lgtm/internal/config"
	"github.com/richdapice/lgtm/internal/diffparse"
	"github.com/richdapice/lgtm/internal/finding"
	"github.com/richdapice/lgtm/internal/gh"
	"github.com/richdapice/lgtm/internal/gitx"
	"github.com/richdapice/lgtm/internal/lens"
	"github.com/richdapice/lgtm/internal/project"
	"github.com/richdapice/lgtm/internal/run"
)

// ErrHeld is returned when the run stopped for a human. It is not a failure;
// the run file is kept so `lgtm` resumes it.
var ErrHeld = errors.New("held: findings need you")

type Options struct {
	Dir    string
	Auto   bool
	Intent string
	Draft  bool
	NoPR   bool // review only; skip push/PR/CI
	In     io.Reader
	Out    io.Writer
	Log    *log.Logger
}

type Ceremony struct {
	o        Options
	global   *config.Global
	repo     *config.Repo
	reviewer agent.Adapter
	fixer    *agent.Adapter // nil when the agent has no fix_command
	gh       gh.Client

	root, common, branch, base, baseRef, mergeBase, tree string
	diff                                                 string
	files                                                []diffparse.FileDiff
	changed                                              []string
	intent                                               string
	conventions                                          string
	dismiss                                              *finding.DismissList

	run *run.Run
	mu  sync.Mutex
}

func Run(ctx context.Context, o Options) error {
	c, err := prepare(ctx, o)
	if err != nil {
		return err
	}
	if err := c.discover(ctx); err != nil {
		return c.fail(err)
	}
	if err := c.rounds(ctx); err != nil {
		return c.fail(err)
	}
	if c.run.Phase == run.Held {
		c.save()
		c.printHeld()
		return ErrHeld
	}
	if o.NoPR {
		return c.finish(run.Done)
	}
	if err := c.openPR(ctx); err != nil {
		return c.fail(err)
	}
	c.watchCI(ctx)
	return c.finish(run.Done)
}

func prepare(ctx context.Context, o Options) (*Ceremony, error) {
	if o.Log == nil {
		o.Log = log.New(io.Discard, "", 0)
	}
	if o.Out == nil {
		o.Out = os.Stdout
	}
	if o.In == nil {
		o.In = os.Stdin
	}
	c := &Ceremony{o: o}
	var err error
	if c.root, err = gitx.Root(ctx, o.Dir); err != nil {
		return nil, err
	}
	if c.common, err = gitx.CommonDir(ctx, o.Dir); err != nil {
		return nil, err
	}
	if c.branch, err = gitx.Branch(ctx, c.root); err != nil {
		return nil, err
	}
	clean, err := gitx.IsClean(ctx, c.root)
	if err != nil {
		return nil, err
	}
	if !clean {
		return nil, errors.New("working tree has uncommitted changes; commit or stash them first (the fix round edits the tree)")
	}

	if c.global, err = config.LoadGlobal(); err != nil {
		return nil, err
	}
	if c.repo, err = config.LoadRepo(c.root); err != nil {
		return nil, err
	}
	ag, ok := c.global.Agent(c.repo.Settings.Agent)
	if !ok {
		return nil, fmt.Errorf("agent %q not in %s", c.repo.Settings.Agent, config.GlobalPath())
	}
	cap, err := agent.ParseCapability(ag.Schema)
	if err != nil {
		return nil, err
	}
	c.reviewer = agent.Adapter{Name: ag.Name, Command: ag.Command, Model: ag.Model, Cap: cap,
		MaxBudgetUSD: c.repo.Settings.MaxBudgetUSD}
	if len(ag.FixCommand) > 0 {
		f := c.reviewer
		f.Command = ag.FixCommand
		c.fixer = &f
	}

	c.base = c.repo.Settings.Base
	if c.base == "" {
		if b, err := c.gh.DefaultBranch(ctx); err == nil && b != "" {
			c.base = b
		} else {
			c.base = "main"
		}
	}
	if c.branch == c.base {
		return nil, fmt.Errorf("on %s; check out the branch you want reviewed", c.base)
	}
	c.baseRef = c.base
	if gitx.RefExists(ctx, c.root, "origin/"+c.base) {
		c.baseRef = "origin/" + c.base
	}
	if c.mergeBase, err = gitx.MergeBase(ctx, c.root, c.baseRef, "HEAD"); err != nil {
		return nil, err
	}
	if err := c.refreshDiff(ctx); err != nil {
		return nil, err
	}
	if strings.TrimSpace(c.diff) == "" {
		return nil, fmt.Errorf("nothing to review: no changes between %s and %s", c.baseRef, c.branch)
	}
	if c.tree, err = gitx.TreeHash(ctx, c.root, "HEAD"); err != nil {
		return nil, err
	}
	c.intent = o.Intent
	if c.intent == "" {
		c.intent, _ = gitx.Run(ctx, c.root, "log", "--format=%s%n%b", c.mergeBase+"..HEAD")
	}
	c.conventions = gatherConventions(c.root, c.changed)
	if c.dismiss, err = finding.LoadDismissList(c.root); err != nil {
		return nil, err
	}

	// resume a held run on the same tree rather than paying for discover again
	if prev, err := run.Load(c.common, c.branch); err == nil && prev.Phase == run.Held && prev.Tree == c.tree {
		c.run = prev
		c.run.PID = os.Getpid()
		if o.Auto {
			c.run.Mode = "auto"
		}
		c.log("resuming held run (%d open)", c.run.Findings.Counts().Open)
		return c, nil
	}

	mode := c.repo.Settings.Mode
	if o.Auto {
		mode = "auto"
	}
	lenses := c.repo.EnabledLenses()
	models := map[string]string{}
	for _, l := range lenses {
		models[l] = c.repo.Lenses[l].Model
		if models[l] == "" {
			models[l] = ag.Model
		}
	}
	rows := lenses
	if c.repo.Settings.Fanout != "parallel" {
		// one call, one row; per-lens counts go to the log
		rows = []string{"review"}
		models["review"] = ag.Model
	}
	c.run = run.New(c.branch, c.base, mode, c.repo.Settings.MaxFixRounds, rows, models)
	c.run.Tree = c.tree
	c.save()
	return c, nil
}

func (c *Ceremony) refreshDiff(ctx context.Context) error {
	var err error
	if c.diff, err = gitx.Diff(ctx, c.root, c.mergeBase, "HEAD"); err != nil {
		return err
	}
	if c.files, err = diffparse.Parse(c.diff); err != nil {
		return fmt.Errorf("parse diff: %w", err)
	}
	c.changed, err = gitx.ChangedFiles(ctx, c.root, c.mergeBase, "HEAD")
	return err
}

// discover runs the lenses — one call or one per lens — into the closed set.
func (c *Ceremony) discover(ctx context.Context) error {
	if c.run.Findings.Closed {
		return nil // resumed
	}
	c.run.Phase = run.Discover
	c.save()
	lenses := c.repo.EnabledLenses()

	type result struct {
		lenses []string
		res    agent.Result
		err    error
	}
	var results []result
	if c.repo.Settings.Fanout == "parallel" {
		results = make([]result, len(lenses))
		var wg sync.WaitGroup
		for i, l := range lenses {
			wg.Add(1)
			go func(i int, l string) {
				defer wg.Done()
				c.setLens(l, run.Running, 0)
				ad := c.reviewer
				ad.Model = c.run.Lens(l).Model
				res, err := ad.Ask(ctx, lens.BuildPrompt(lens.Input{Lenses: []string{l}, Diff: c.diff, Conventions: c.conventions, Intent: c.intent}), lens.Schema)
				results[i] = result{[]string{l}, res, err}
				c.addCost(res.CostUSD)
				c.logCall("discover/"+l, res)
				if err != nil {
					c.setLensFailed(l, err)
				}
			}(i, l)
		}
		wg.Wait()
	} else {
		c.setLens("review", run.Running, 0)
		res, err := c.reviewer.Ask(ctx, lens.BuildPrompt(lens.Input{Lenses: lenses, Diff: c.diff, Conventions: c.conventions, Intent: c.intent}), lens.Schema)
		c.addCost(res.CostUSD)
		c.logCall("discover", res)
		if err != nil {
			c.setLensFailed("review", err)
			return err
		}
		results = []result{{lenses, res, nil}}
	}

	var firstErr error
	for _, r := range results {
		if r.err != nil {
			if firstErr == nil {
				firstErr = r.err
			}
			continue
		}
		fs, st, err := lens.Decode(r.res.Output, r.lenses, c.files)
		if err != nil {
			c.log("decode (%v): %v\n%s", r.lenses, err, r.res.Raw)
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		c.log("lenses %v: received %d, snapped %d, unanchored %d, rejected %d", r.lenses, st.Received, st.Snapped, st.Unanchored, st.Rejected)
		per := map[string]int{}
		for _, f := range fs {
			if added, _ := c.run.Findings.Add(f); added {
				per[f.Lens]++
			}
		}
		if c.repo.Settings.Fanout == "parallel" {
			for _, l := range r.lenses {
				c.setLens(l, run.LensDone, per[l])
			}
		} else {
			total := 0
			for _, n := range per {
				total += n
			}
			c.setLens("review", run.LensDone, total)
		}
		c.log("per lens: %v", per)
	}
	// a single failed lens in parallel mode is a partial review, and a partial
	// review must not be reported as a review
	if firstErr != nil {
		return firstErr
	}
	if n := c.dismiss.Prune(&c.run.Findings); n > 0 {
		c.log("pruned %d dismissed finding(s)", n)
	}
	c.run.Findings.Close()
	c.save()
	return nil
}

// rounds is the bounded loop. The set only shrinks: fixes move findings to
// Fixed, the user moves them to Accepted or Dismissed, and anything the
// verifier notices is Filed. File-severity findings never gate anything.
func (c *Ceremony) rounds(ctx context.Context) error {
	for round := 1; round <= c.run.MaxRounds; round++ {
		actionable := c.actionable()
		if len(actionable) == 0 {
			return nil
		}
		var toFix []finding.Finding
		if c.run.Mode == "auto" {
			toFix = actionable
		} else {
			var quit bool
			toFix, quit = c.manual(actionable)
			if quit {
				c.run.Phase = run.Held
				return nil
			}
		}
		if len(toFix) == 0 {
			break
		}
		if c.fixer == nil {
			c.log("no fix_command configured; cannot fix")
			break
		}

		c.run.Round = round
		c.run.Phase = run.Fix
		c.save()
		fixRes, err := c.fixer.Ask(ctx, lens.BuildFixPrompt(toFix, c.conventions), nil)
		c.addCost(fixRes.CostUSD)
		c.logCall("fix", fixRes)
		if err != nil {
			return fmt.Errorf("fix round %d: %w", round, err)
		}
		touched, err := dirtyFiles(ctx, c.root)
		if err != nil {
			return err
		}
		if len(touched) == 0 {
			c.log("round %d: fixer changed nothing", round)
			c.println("round %d: the fixer made no changes", round)
			break
		}

		// nothing lands unvalidated: the repo's own checks run on what the
		// fixer touched, and a failing check reverts the round
		checks := project.Run(ctx, c.root, project.Plan(c.repo, touched))
		ok, _ := project.AllOK(checks)
		if !ok {
			c.println("round %d: checks failed after fix; reverting", round)
			c.println("%s", checksText(checks))
			if err := revert(ctx, c.root); err != nil {
				return err
			}
			c.run.Error = fmt.Sprintf("round %d fix reverted: checks failed", round)
			if c.run.Mode == "auto" {
				break
			}
			continue
		}
		if err := c.commitRound(ctx, touched, toFix, round); err != nil {
			return err
		}

		c.run.Phase = run.Verify
		c.save()
		if err := c.refreshDiff(ctx); err != nil {
			return err
		}
		open := c.run.Findings.Open()
		known := map[string]bool{}
		for _, f := range open {
			known[f.ID] = true
		}
		vres, err := c.reviewer.Ask(ctx, lens.BuildVerifyPrompt(open, c.diff), lens.VerifySchema)
		c.addCost(vres.CostUSD)
		c.logCall("verify", vres)
		if err != nil {
			return fmt.Errorf("verify round %d: %w", round, err)
		}
		verdicts, newFs, err := lens.DecodeVerify(vres.Output, known, c.repo.EnabledLenses())
		if err != nil {
			return err
		}
		fixed := 0
		for _, v := range verdicts {
			if v.Addressed {
				if c.run.Findings.Transition(v.ID, finding.Fixed, round) == nil {
					fixed++
				}
			} else {
				c.log("round %d: %s not addressed: %s", round, v.ID, v.Note)
			}
		}
		for _, f := range newFs {
			c.run.Findings.File(f, round)
		}
		c.log("round %d: %d fixed, %d filed, %d still open", round, fixed, len(newFs), c.run.Findings.Counts().Open)
		c.println("round %d/%d: %d fixed · %d filed · %d open", round, c.run.MaxRounds, fixed, len(newFs), c.run.Findings.Counts().Open)
		c.save()
	}
	if len(c.actionable()) > 0 {
		c.run.Phase = run.Held
	}
	return nil
}

func (c *Ceremony) actionable() []finding.Finding {
	var out []finding.Finding
	for _, f := range c.run.Findings.Open() {
		if f.Severity != finding.File {
			out = append(out, f)
		}
	}
	return out
}

func (c *Ceremony) commitRound(ctx context.Context, touched []string, fixed []finding.Finding, round int) error {
	paths := append([]string(nil), touched...)
	if _, err := os.Stat(filepath.Join(c.root, finding.DismissFile)); err == nil {
		paths = append(paths, finding.DismissFile)
	}
	// stage by path, never add -A: an untracked file the fixer did not create
	// must not ride along
	args := append([]string{"add", "--"}, paths...)
	if _, err := gitx.Run(ctx, c.root, args...); err != nil {
		return err
	}
	rules := map[string]bool{}
	var list []string
	for _, f := range fixed {
		if !rules[f.Rule] {
			rules[f.Rule] = true
			list = append(list, f.Rule)
		}
	}
	sort.Strings(list)
	msg := fmt.Sprintf("Address review findings (round %d)\n\n%s", round, strings.Join(list, ", "))
	_, err := gitx.Run(ctx, c.root, "commit", "-q", "-m", msg)
	return err
}

func (c *Ceremony) openPR(ctx context.Context) error {
	c.run.Phase = run.PR
	c.save()
	if err := gitx.Push(ctx, c.root, "origin", c.branch); err != nil {
		return err
	}
	if n, url, ok, err := c.gh.FindPR(ctx, c.branch); err == nil && ok {
		c.run.PR = &run.PRInfo{Number: n, URL: url}
		c.save()
		c.println("PR already open: %s", url)
		return nil
	}
	// literal check output for the Tests section — the body must not claim
	// anything this run did not see
	checks := project.Run(ctx, c.root, project.Plan(c.repo, c.changed))
	var fixed, filed []finding.Finding
	for _, f := range c.run.Findings.Findings {
		switch f.State {
		case finding.Fixed:
			fixed = append(fixed, f)
		case finding.Filed:
			filed = append(filed, f)
		}
	}
	res, err := c.reviewer.Ask(ctx, lens.BuildPRPrompt(c.intent, c.diff, checksText(checks), fixed, filed), nil)
	c.addCost(res.CostUSD)
	c.logCall("pr", res)
	if err != nil {
		return fmt.Errorf("pr body: %w", err)
	}
	var text string
	if json.Unmarshal(res.Output, &text) != nil {
		text = string(res.Output)
	}
	title, body := lens.SplitPR(text)
	if title == "" {
		return errors.New("pr body: agent returned no title")
	}
	n, url, err := c.gh.CreatePR(ctx, c.base, c.branch, title, body, c.o.Draft)
	if err != nil {
		return err
	}
	c.run.PR = &run.PRInfo{Number: n, URL: url}
	c.save()
	c.println("opened %s", url)
	return nil
}

// watchCI observes. It never repairs, never pushes, never times out into an
// error — CI's verdict is CI's, this just keeps the bar honest while it runs.
func (c *Ceremony) watchCI(ctx context.Context) {
	if c.run.PR == nil {
		return
	}
	owner, repo, err := c.gh.RepoNWO(ctx)
	if err != nil {
		c.log("ci: %v", err)
		return
	}
	c.run.Phase = run.CI
	c.run.CI = &run.CIStatus{Since: time.Now().UTC()}
	c.save()
	deadline := time.Now().Add(30 * time.Minute)
	for time.Now().Before(deadline) {
		pass, fail, pending, err := c.gh.Checks(ctx, owner, repo, c.run.PR.Number)
		if err != nil {
			c.log("ci: %v", err)
			return
		}
		c.run.CI.Passed, c.run.CI.Failed, c.run.CI.Pending = pass, fail, pending
		c.run.CI.Total = pass + fail + pending
		c.save()
		if pending == 0 && c.run.CI.Total > 0 {
			if fail > 0 {
				c.println("ci: %d check(s) failed — %s", fail, c.run.PR.URL)
			} else {
				c.println("ci: %d/%d green", pass, c.run.CI.Total)
			}
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(20 * time.Second):
		}
	}
}

func (c *Ceremony) finish(p run.Phase) error {
	c.run.Phase = p
	cnt := c.run.Findings.Counts()
	_ = run.AppendHistory(c.common, run.Summary{
		Branch: c.branch, EndedAt: time.Now().UTC(), Duration: time.Since(c.run.StartedAt),
		Outcome: p, Found: c.run.Findings.Discovered(), Fixed: cnt.Fixed, CostUSD: c.run.CostUSD,
	})
	if p == run.Done {
		return run.Remove(c.common, c.branch)
	}
	c.save()
	return nil
}

func (c *Ceremony) fail(err error) error {
	c.run.Phase = run.Failed
	c.run.Error = err.Error()
	c.save()
	_ = run.AppendHistory(c.common, run.Summary{
		Branch: c.branch, EndedAt: time.Now().UTC(), Duration: time.Since(c.run.StartedAt),
		Outcome: run.Failed, CostUSD: c.run.CostUSD,
	})
	return err
}

func (c *Ceremony) save() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := c.run.Save(c.common); err != nil {
		c.log("save: %v", err)
	}
}

func (c *Ceremony) setLens(name string, st run.LensState, found int) {
	c.mu.Lock()
	l := c.run.Lens(name)
	if l != nil {
		l.State = st
		switch st {
		case run.Running:
			l.StartedAt = time.Now().UTC()
			l.Frac = 0.5 // one call, no progress signal; half is the honest midpoint
		case run.LensDone:
			l.EndedAt = time.Now().UTC()
			l.Frac = 1
			l.Found = found
		}
	}
	c.mu.Unlock()
	c.save()
}

func (c *Ceremony) setLensFailed(name string, err error) {
	c.mu.Lock()
	if l := c.run.Lens(name); l != nil {
		l.State = run.LensFailed
		l.EndedAt = time.Now().UTC()
		l.Error = err.Error()
	}
	c.mu.Unlock()
	c.save()
}

func (c *Ceremony) addCost(usd float64) {
	c.mu.Lock()
	c.run.CostUSD += usd
	c.mu.Unlock()
}

func (c *Ceremony) log(format string, a ...any) { c.o.Log.Printf(format, a...) }

// logCall is the cost ledger: one line per agent call with what it cost, how
// many turns it took, and on what model — the numbers to look at when a run
// is more expensive than expected.
func (c *Ceremony) logCall(what string, r agent.Result) {
	c.log("call %-12s $%.3f  %3d turns  %s  %s", what, r.CostUSD, r.NumTurns, r.Model, r.Duration.Round(time.Second))
}
func (c *Ceremony) println(format string, a ...any) { fmt.Fprintf(c.o.Out, format+"\n", a...) }

func (c *Ceremony) printHeld() {
	open := c.actionable()
	c.println("\nheld: %d finding(s) need you — run `lgtm` again to review, or `lgtm --auto`", len(open))
}

// dirtyFiles is what the fixer touched: modified, added, and untracked paths
// from porcelain status. The tree was clean before the fixer ran, so anything
// here is the fixer's.
func dirtyFiles(ctx context.Context, root string) ([]string, error) {
	out, err := gitx.Run(ctx, root, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		p := strings.TrimSpace(line[3:])
		if i := strings.Index(p, " -> "); i >= 0 {
			p = p[i+4:]
		}
		files = append(files, p)
	}
	return files, nil
}

func revert(ctx context.Context, root string) error {
	if _, err := gitx.Run(ctx, root, "checkout", "--", "."); err != nil {
		return err
	}
	_, err := gitx.Run(ctx, root, "clean", "-fd")
	return err
}

func checksText(rs []project.Result) string {
	var b strings.Builder
	for _, r := range rs {
		if r.Skipped {
			fmt.Fprintf(&b, "%s %s: skipped (%s)\n", r.Project, r.Kind, r.Reason)
			continue
		}
		status := "ok"
		if !r.OK {
			status = "FAIL"
		}
		fmt.Fprintf(&b, "$ %s   # %s %s, %s\n", r.Command, r.Project, r.Kind, status)
		if out := strings.TrimSpace(r.Output); out != "" {
			b.WriteString(out + "\n")
		}
	}
	return b.String()
}

// gatherConventions collects the instruction files that apply to the changed
// paths: the user's global CLAUDE.md, then CLAUDE.md/AGENTS.md at the repo
// root and in every directory above a changed file. A one-line "@FILE" include
// is followed, since that is how per-project files point at AGENTS.md.
func gatherConventions(root string, changed []string) string {
	const cap = 24 * 1024
	seen := map[string]bool{}
	var parts []string
	add := func(p string) {
		if seen[p] {
			return
		}
		seen[p] = true
		b, err := os.ReadFile(p)
		if err != nil {
			return
		}
		text := strings.TrimSpace(string(b))
		if strings.HasPrefix(text, "@") && !strings.Contains(text, "\n") {
			inc := filepath.Join(filepath.Dir(p), strings.TrimPrefix(text, "@"))
			if ib, err := os.ReadFile(inc); err == nil {
				text = strings.TrimSpace(string(ib))
			}
		}
		if text != "" {
			rel, _ := filepath.Rel(root, p)
			if strings.HasPrefix(rel, "..") {
				rel = p
			}
			parts = append(parts, "# "+rel+"\n"+text)
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		add(filepath.Join(home, ".claude", "CLAUDE.md"))
	}
	dirs := map[string]bool{".": true}
	for _, f := range changed {
		for d := filepath.Dir(f); d != "." && d != "/"; d = filepath.Dir(d) {
			dirs[d] = true
		}
	}
	var ordered []string
	for d := range dirs {
		ordered = append(ordered, d)
	}
	sort.Strings(ordered)
	for _, d := range ordered {
		add(filepath.Join(root, d, "CLAUDE.md"))
		add(filepath.Join(root, d, "AGENTS.md"))
	}
	out := strings.Join(parts, "\n\n")
	if len(out) > cap {
		out = out[:cap] + "\n[… truncated …]"
	}
	return out
}
