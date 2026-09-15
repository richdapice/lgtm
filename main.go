// lgtm reviews a branch before it becomes a pull request: a few lenses read the
// diff, findings get worked in bounded rounds, then it opens the PR and watches
// CI. It holds no git state of its own.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/richdapice/lgtm/internal/agent"
	"github.com/richdapice/lgtm/internal/ceremony"
	"github.com/richdapice/lgtm/internal/config"
	"github.com/richdapice/lgtm/internal/finding"
	"github.com/richdapice/lgtm/internal/gitx"
	"github.com/richdapice/lgtm/internal/render"
	"github.com/richdapice/lgtm/internal/run"
	"github.com/richdapice/lgtm/internal/setup"
	"github.com/richdapice/lgtm/internal/skill"
	"github.com/richdapice/lgtm/internal/tui"
	"golang.org/x/term"
)

var version = "dev"

func main() {
	// there is always a log: a run you want to understand after the fact is
	// exactly the one you didn't think to set LGTM_DEBUG for
	logger := log.New(io.Discard, "", log.Ldate|log.Ltime)
	logPath := os.Getenv("LGTM_DEBUG")
	if logPath == "" {
		if cwd, err := os.Getwd(); err == nil {
			if common, err := gitx.FindCommonDir(cwd); err == nil {
				os.MkdirAll(filepath.Join(common, "lgtm"), 0o755)
				logPath = filepath.Join(common, "lgtm", "lgtm.log")
			}
		}
	}
	if logPath != "" {
		if f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			logger.SetOutput(f)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	args := os.Args[1:]
	cmd := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		cmd, args = args[0], args[1:]
	}
	var err error
	switch cmd {
	case "", "run":
		err = cmdRun(ctx, args, logger)
	case "statusline":
		err = cmdStatusline(args)
	case "status":
		err = cmdStatus(args)
	case "findings":
		err = cmdFindings(args)
	case "dismiss":
		err = cmdDismiss(ctx, args)
	case "doctor":
		err = cmdDoctor(ctx)
	case "init":
		err = cmdInit(ctx, args)
	case "decide":
		err = cmdDecide(ctx, args)
	case "continue":
		err = cmdContinue(ctx, args, logger)
	case "version":
		fmt.Println("lgtm", version)
	case "help", "-h", "--help":
		usage()
	default:
		fatal("unknown command %q (try `lgtm help`)", cmd)
	}
	if errors.Is(err, ceremony.ErrHeld) {
		os.Exit(2)
	}
	if err != nil {
		fatal("%v", err)
	}
}

func usage() {
	fmt.Print(`lgtm — review a branch, then open the PR

  lgtm [--auto] [--intent TEXT] [--draft] [--no-pr] [--plain]
                                                      review the current branch, then open the PR
  lgtm statusline                                     render the status bar (reads Claude Code JSON on stdin)
  lgtm decide ID fix|accept|dismiss|skip [-b BRANCH]  record a decision on a held run (no terminal needed)
  lgtm continue [--auto] [--no-pr] [-b BRANCH]        apply recorded decisions and carry on
  lgtm status [--json] [-b BRANCH]                    current run for this branch
  lgtm findings [--json]                              the finding set
  lgtm dismiss ID [-r REASON]                         never see this finding again (commits to .lgtm/dismissed.toml)
  lgtm init [-y] [--statusline] [--skill]             detect projects, write .lgtm.toml; wire the status bar and the /lgtm skill
  lgtm doctor                                         check each configured agent answers
  lgtm version

Exit codes: 0 done · 1 error · 2 held (findings need you; run lgtm again)
Log: .git/lgtm/lgtm.log (or LGTM_DEBUG=/path)   Config: LGTM_CONFIG=/path/to/config.toml
`)
}

func cmdRun(ctx context.Context, args []string, logger *log.Logger) error {
	fs := flag.NewFlagSet("lgtm", flag.ExitOnError)
	auto := fs.Bool("auto", false, "fix every finding without asking, up to max_fix_rounds")
	branch := fs.String("b", "", "branch (any worktree of this repo)")
	intent := fs.String("intent", "", "what the change is for (default: the branch's commit messages)")
	draft := fs.Bool("draft", false, "open the PR as a draft")
	noPR := fs.Bool("no-pr", false, "review only; do not push or open a PR")
	plain := fs.Bool("plain", false, "line prompts instead of the screen (default when stdout is not a terminal)")
	fs.Parse(args)
	cwd, err := dirFor(ctx, *branch)
	if err != nil {
		return err
	}
	opts := ceremony.Options{Dir: cwd, Auto: *auto, Intent: *intent, Draft: *draft, NoPR: *noPR, Log: logger}
	if *plain || !term.IsTerminal(int(os.Stdout.Fd())) || !term.IsTerminal(int(os.Stdin.Fd())) {
		return ceremony.Run(ctx, opts)
	}
	transcript, err := tui.Run(ctx, opts)
	if transcript != "" {
		fmt.Print(transcript)
	}
	return err
}

// cmdStatusline must stay fast: no git exec, no network. It resolves the repo
// from the cwd Claude Code passes on stdin, reads run state, renders.
func cmdStatusline(args []string) error {
	var in struct {
		Workspace struct {
			CurrentDir string `json:"current_dir"`
		} `json:"workspace"`
		RateLimits *struct {
			FiveHour *struct {
				Used  float64 `json:"used_percentage"`
				Reset int64   `json:"resets_at"`
			} `json:"five_hour"`
			SevenDay *struct {
				Used  float64 `json:"used_percentage"`
				Reset int64   `json:"resets_at"`
			} `json:"seven_day"`
		} `json:"rate_limits"`
	}
	_ = json.NewDecoder(os.Stdin).Decode(&in)
	dir := in.Workspace.CurrentDir
	if dir == "" {
		dir, _ = os.Getwd()
	}
	cols := 100
	if c, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && c > 20 {
		cols = c
	}
	var plan *render.PlanUsage
	if rl := in.RateLimits; rl != nil && (rl.FiveHour != nil || rl.SevenDay != nil) {
		plan = &render.PlanUsage{}
		if rl.FiveHour != nil {
			plan.FiveHourPct = int(rl.FiveHour.Used)
			if rl.FiveHour.Reset > 0 {
				plan.FiveHourReset = time.Unix(rl.FiveHour.Reset, 0)
			}
		}
		if rl.SevenDay != nil {
			plan.SevenDayPct = int(rl.SevenDay.Used)
			if rl.SevenDay.Reset > 0 {
				plan.SevenDayReset = time.Unix(rl.SevenDay.Reset, 0)
			}
		}
	}
	style := render.Style{Cols: cols, Color: os.Getenv("NO_COLOR") == ""}
	common, err := gitx.FindCommonDir(dir)
	if err != nil {
		// outside a repo there's no run to show, but the plan windows are
		// worth a row anywhere
		home, _ := os.UserHomeDir()
		ref := dir
		if home != "" && strings.HasPrefix(dir, home) {
			ref = "~" + strings.TrimPrefix(dir, home)
		}
		fmt.Println(render.Render(render.Input{IdleRef: ref, NoRepo: true, Now: time.Now(), Plan: plan}, style))
		return nil
	}
	runs, _ := run.All(common)
	hist, _ := run.History(common, 20)
	fmt.Println(render.Render(render.Input{Runs: runs, History: hist, IdleRef: gitx.BranchFast(dir), Now: time.Now(), Plan: plan}, style))
	return nil
}

// dirFor is cwd, or the worktree that has -b BRANCH checked out.
func dirFor(ctx context.Context, branch string) (string, error) {
	cwd, _ := os.Getwd()
	if branch == "" {
		return cwd, nil
	}
	return gitx.WorktreeFor(ctx, cwd, branch)
}

func loadCurrent(ctx context.Context, branch string) (*run.Run, string, error) {
	cwd, err := dirFor(ctx, branch)
	if err != nil {
		return nil, "", err
	}
	common, err := gitx.CommonDir(ctx, cwd)
	if err != nil {
		return nil, "", err
	}
	if branch == "" {
		if branch, err = gitx.Branch(ctx, cwd); err != nil {
			return nil, "", err
		}
	}
	r, err := run.Load(common, branch)
	if err != nil {
		return nil, common, fmt.Errorf("no run for %s", branch)
	}
	return r, common, nil
}

func cmdStatus(args []string) error {
	args = flagsFirst(args)
	fs := flag.NewFlagSet("status", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "machine-readable")
	branch := fs.String("b", "", "branch (any worktree of this repo)")
	fs.Parse(args)
	r, _, err := loadCurrent(context.Background(), *branch)
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	}
	c := r.Findings.Counts()
	phase := string(r.Phase)
	if r.Phase == run.Held {
		phase = fmt.Sprintf("needs you (%d)", r.Findings.NeedsYou())
	}
	fmt.Printf("%s → %s  %s  %s  round %d/%d  ≈$%.2f\n", r.Branch, r.Base, r.Mode, phase, r.Round, r.MaxRounds, r.CostUSD)
	fmt.Printf("%d open · %d fixed · %d accepted · %d dismissed · %d filed\n", c.Open, c.Fixed, c.Accepted, c.Dismissed, c.Filed)
	if r.PR != nil {
		fmt.Println(r.PR.URL)
	}
	if r.Error != "" {
		fmt.Println("error:", r.Error)
	}
	return nil
}

func cmdFindings(args []string) error {
	args = flagsFirst(args)
	fs := flag.NewFlagSet("findings", flag.ExitOnError)
	asJSON := fs.Bool("json", false, "machine-readable")
	branch := fs.String("b", "", "branch (any worktree of this repo)")
	fs.Parse(args)
	r, _, err := loadCurrent(context.Background(), *branch)
	if err != nil {
		return err
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(r.Findings.Findings)
	}
	for _, f := range r.Findings.Findings {
		loc := f.Path
		if f.Line > 0 {
			loc = fmt.Sprintf("%s:%d", f.Path, f.Line)
		}
		fmt.Printf("%s  %-9s %-5s %-12s %s  %s\n      %s\n", f.ID, f.State, f.Severity, f.Lens, loc, f.Rule, f.Body)
		if f.Note != "" {
			fmt.Printf("      ↳ %s\n", f.Note)
		}
	}
	return nil
}

func cmdDismiss(ctx context.Context, args []string) error {
	args = flagsFirst(args)
	fs := flag.NewFlagSet("dismiss", flag.ExitOnError)
	reason := fs.String("r", "", "why")
	branch := fs.String("b", "", "branch (any worktree of this repo)")
	fs.Parse(args)
	if fs.NArg() != 1 {
		return errors.New("usage: lgtm dismiss ID [-r REASON] [-b BRANCH]")
	}
	r, _, err := loadCurrent(ctx, *branch)
	if err != nil {
		return err
	}
	f := findByPrefix(r, fs.Arg(0))
	if f == nil {
		return fmt.Errorf("no finding %s in the current run", fs.Arg(0))
	}
	cwd, err := dirFor(ctx, *branch)
	if err != nil {
		return err
	}
	root, err := gitx.Root(ctx, cwd)
	if err != nil {
		return err
	}
	dl, err := finding.LoadDismissList(root)
	if err != nil {
		return err
	}
	dl.Add(*f, *reason)
	if err := dl.Save(root); err != nil {
		return err
	}
	fmt.Printf("dismissed %s (%s) — commit %s to make it stick\n", f.ID, f.Rule, finding.DismissFile)
	return nil
}

func cmdInit(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("init", flag.ExitOnError)
	yes := fs.Bool("y", false, "accept detected defaults without asking")
	bar := fs.Bool("statusline", false, "add lgtm to Claude Code's status bar (~/.claude/settings.json)")
	sk := fs.Bool("skill", false, "install the /lgtm skill for Claude Code (~/.claude/skills/lgtm)")
	fs.Parse(args)
	cwd, _ := os.Getwd()
	root, err := gitx.Root(ctx, cwd)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(root, config.RepoFile)); err == nil && !*yes {
		return fmt.Errorf("%s already exists; edit it, or delete it and run init again", config.RepoFile)
	}
	d := setup.Detect(root)
	r := setup.Prompt(os.Stdin, os.Stdout, d, *yes)
	if err := setup.Write(root, r); err != nil {
		return err
	}
	fmt.Printf("wrote %s (%d project(s))\n", config.RepoFile, len(r.Projects))
	if *bar {
		exe, _ := os.Executable()
		changed, err := setup.WireStatusline(exe)
		if err != nil {
			return err
		}
		if changed {
			fmt.Println("status bar: added to ~/.claude/settings.json (restart Claude Code to see it)")
		} else {
			fmt.Println("status bar: already wired")
		}
	}
	if *sk {
		path, changed, err := skill.Install()
		if err != nil {
			return err
		}
		if changed {
			fmt.Println("skill: wrote", path)
		} else {
			fmt.Println("skill: up to date")
		}
	}
	fmt.Println("next: lgtm doctor")
	return nil
}

// flagsFirst lets flags follow positionals (`lgtm decide ID accept -b X`):
// Go's flag package stops at the first non-flag, so move flags to the front.
func flagsFirst(args []string) []string {
	var flags, pos []string
	for i := 0; i < len(args); i++ {
		a := args[i]
		if strings.HasPrefix(a, "-") {
			flags = append(flags, a)
			// -b takes a value; boolean flags don't
			if (a == "-b" || a == "--b" || a == "-r" || a == "--r") && i+1 < len(args) {
				flags = append(flags, args[i+1])
				i++
			}
			continue
		}
		pos = append(pos, a)
	}
	return append(flags, pos...)
}

func findByPrefix(r *run.Run, id string) *finding.Finding {
	var hit *finding.Finding
	for i := range r.Findings.Findings {
		if strings.HasPrefix(r.Findings.Findings[i].ID, id) {
			if hit != nil {
				return nil // ambiguous
			}
			hit = &r.Findings.Findings[i]
		}
	}
	return hit
}

// cmdDecide records a decision on a held run without a terminal. The run
// file is the mailbox; `continue` reads it.
func cmdDecide(ctx context.Context, args []string) error {
	args = flagsFirst(args)
	fs := flag.NewFlagSet("decide", flag.ExitOnError)
	branch := fs.String("b", "", "branch (any worktree of this repo)")
	fs.Parse(args)
	if fs.NArg() != 2 {
		return errors.New("usage: lgtm decide ID fix|accept|dismiss|skip [-b BRANCH]")
	}
	if _, ok := ceremony.ParseDecision(fs.Arg(1)); !ok {
		return fmt.Errorf("decision must be fix, accept, dismiss, or skip; got %q", fs.Arg(1))
	}
	r, common, err := loadCurrent(ctx, *branch)
	if err != nil {
		return err
	}
	if r.Phase != run.Held {
		return fmt.Errorf("run is %s, not waiting on a decision", r.Phase)
	}
	f := findByPrefix(r, fs.Arg(0))
	if f == nil {
		return fmt.Errorf("no single finding matches %q — see lgtm findings", fs.Arg(0))
	}
	if f.State != finding.Open || f.Severity == finding.File {
		return fmt.Errorf("%s is %s and not waiting on you", f.ID, f.State)
	}
	if r.Decisions == nil {
		r.Decisions = map[string]string{}
	}
	r.Decisions[f.ID] = fs.Arg(1)
	if err := r.Save(common); err != nil {
		return err
	}
	fmt.Printf("%s → %s  (%d recorded, %d still open) · lgtm continue to apply\n", f.ID, fs.Arg(1), len(r.Decisions), r.Findings.NeedsYou())
	return nil
}

// cmdContinue resumes a held run with recorded decisions, no terminal needed.
func cmdContinue(ctx context.Context, args []string, logger *log.Logger) error {
	args = flagsFirst(args)
	fs := flag.NewFlagSet("continue", flag.ExitOnError)
	auto := fs.Bool("auto", false, "after applying decisions, fix remaining block findings without asking")
	noPR := fs.Bool("no-pr", false, "review only; do not push or open a PR")
	branch := fs.String("b", "", "branch (any worktree of this repo)")
	fs.Parse(args)
	cwd, err := dirFor(ctx, *branch)
	if err != nil {
		return err
	}
	r, _, err := loadCurrent(ctx, *branch)
	if err != nil {
		return err
	}
	if r.Phase != run.Held {
		return fmt.Errorf("run is %s; nothing to continue", r.Phase)
	}
	if len(r.Decisions) == 0 && !*auto {
		return errors.New("nothing decided yet — lgtm decide ID fix|accept|dismiss|skip, or continue --auto")
	}
	return ceremony.Run(ctx, ceremony.Options{Dir: cwd, Auto: *auto, NoPR: *noPR, Log: logger, Decider: &ceremony.RecordedDecider{}})
}

func cmdDoctor(ctx context.Context) error {
	g, err := config.LoadGlobal()
	if err != nil {
		return err
	}
	fmt.Printf("config  %s\n", config.GlobalPath())
	failed := 0
	for _, a := range g.Agents {
		cap, err := agent.ParseCapability(a.Schema)
		if err != nil {
			fmt.Printf("  ✗ %-10s %v\n", a.Name, err)
			failed++
			continue
		}
		ad := agent.Adapter{Name: a.Name, Command: a.Command, Model: a.Model, Cap: cap}
		start := time.Now()
		if err := ad.Probe(ctx); err != nil {
			fmt.Printf("  ✗ %-10s %v\n", a.Name, err)
			failed++
			continue
		}
		fix := "no fix_command (manual only)"
		if len(a.FixCommand) > 0 {
			fix = "fixes on"
		}
		fmt.Printf("  ✓ %-10s %s · %s · %s\n", a.Name, a.Schema, fix, time.Since(start).Round(100*time.Millisecond))
	}
	if failed > 0 {
		return fmt.Errorf("%d agent(s) failed", failed)
	}
	return nil
}

func fatal(format string, a ...any) {
	fmt.Fprintf(os.Stderr, "lgtm: "+format+"\n", a...)
	os.Exit(1)
}
