# lgtm

Fresh eyes on your branch before the PR opens.

`lgtm` reads your diff, works the findings with you, then opens the pull request and watches CI. It's the review that happens *before* you ask someone for one — run it instead of `gh pr create`.

```
$ lgtm

 lgtm ▸ worktree-sync-throttle → main   2 need you   ≈$0.41
 ▸ · block correctness  src/main/sync.ts:142  swallowed-error
   · ask   tests        src/main/sync.test.ts:88  missing-assert

    139 +   try {
    140 +     await push(batch)
  ▸ 142 +   } catch (err) { /* retry later */ }
    143 +   }

   The retry never happens — nothing schedules it. Either enqueue the retry here
   or let the error surface; silently dropping it means a failed sync looks
   identical to no sync.

   Fix   Accept   Dismiss   Skip     ↑↓ finding · ←→ action · ⏎ apply · A autopilot · q hold
```

Pick a finding, pick an action, press Enter. Nothing is applied until you do. When every finding has a decision the bar becomes `⏎ submit`; fixes run in place, your checks run on what changed, and the round's result comes back into the same panel.

When it's through:

```
round 1/3: 2 fixed · 0 filed · 0 open

  ╭──────╮
  │ LGTM │  2 found · 2 fixed · 0 accepted · 0 filed · 3m48s · ≈$1.10
  ╰──────╯
  https://github.com/you/repo/pull/126
```

When it isn't — it parks the run and tells you exactly what's waiting:

```
  not yet — 1 need you:
    ask   website/src/app/page.tsx:887  landscape-film-illegible-on-phones · fixer declined, needs your decision
  run `lgtm` to decide.
```

`lgtm` again resumes right there, without re-reviewing.

## What it does

1. **Discover.** Four lenses read the diff between your branch and its base — correctness, conventions (it reads your `CLAUDE.md` / `AGENTS.md`), security, tests. One agent call by default; one per lens in parallel if you ask.
2. **Rounds.** Each finding is `block`, `ask`, or `file`. In the panel you fix, accept, or dismiss each one, or flip to autopilot and let it fix what it can. Every applied fix re-runs your project's own checks before it's committed — a fix that breaks the tests is reverted, not shipped. When the fixer declines a finding, its reason stays on the finding and you decide with it in front of you.
3. **PR.** It pushes, writes the PR body from the diff and the literal check output, and opens the PR.
4. **CI.** It watches the checks and keeps the status bar honest. It never repairs and never force-pushes.

Manual mode is the default. Press `A` during a review to hand the rest to autopilot; `lgtm --auto` starts there. Autopilot fixes `block` findings; `ask` findings are yours by definition and come straight to you, and a finding the fixer has declined once is never sent again automatically.

## The panel

It draws inline under your prompt — no full-screen takeover, your scrollback stays — capped at 100 columns and sized to its content. One finding is in focus with the hunk it points at and the anchor line marked; the rest are one line each.

| | |
|---|---|
| `↑` `↓` · `j` `k` | choose a finding |
| `←` `→` · `tab` · `f` `a` `d` `s` | move the highlight across **Fix · Accept · Dismiss · Skip** |
| `⏎` | apply the highlighted action to the focused finding, then focus the next undecided one |
| `⏎` again, once all are decided | submit the round |
| `A` | autopilot from here: everything undecided becomes Fix |
| `q` | hold — park the run, exit 2, come back later |

`--plain` gets you line prompts instead, and that's what you get automatically when stdout isn't a terminal.

## The status bar

`lgtm init --statusline` puts a live bar in Claude Code's status line (or point any status line at `lgtm statusline`). It re-renders in about 4ms and breathes with the run:

```
── a review in flight
  lgtm ▸ worktree-sync-throttle → main   manual   1m12s   ≈$0.41   5h 37% · 7d 61%
      ─ review        ████████▊░░░░     –   opus      1m12s

── lenses in parallel (fanout = "parallel")
  lgtm ▸ worktree-sync-throttle → main   manual   2m14s   ≈$0.09
      ╭ correctness  ─────────────  ✓  7   opus      42s
      │ conventions  ████████▊░░░░     –   haiku     12s
      │ security     ─────────────  ✓  2   opus      38s
      ╰ tests        ██████▎░░░░░░     –   sonnet  1m04s
 loop ● ● ○  round 2/3      4 open · 6 fixed · 2 filed

── held, waiting on you
  lgtm ▸ worktree-sync-throttle → main   held   3m16s   ≈$0.62
 loop ● ● ●  round 3/3      2 open · 8 fixed · 3 filed
    → 2 need you    run lgtm on this branch to review · lgtm --auto to fix

── PR open, watching CI
  lgtm ▸ worktree-sync-throttle → main   passed   4m01s   ≈$0.71
   ci ◍ #126   ▰▰▱▱   check 2/4 · 1m12s

── idle
  lgtm ▸ main   idle   12 runs today   5h 37% · 7d 61%
      20 runs  ▂▃▂▅▂▂▇▃▂▂▄▂▃▂▂▃▅▂▂▃  median 2m38s · 3 held · streak 4
```

Only the running lens gets a bright bar; finished ones go quiet. The idle row is the last twenty runs as a sparkline, with your streak of runs that shipped without needing you. `#126` is a link.

The PR gets 👀 from your account when it opens and 👍 when CI goes green — the two reactions every PR gets anyway. `reactions = false` turns that off.

## Four promises

These are the design, stated as things it will not do:

- **It never holds your branch.** No proxy remote, no mirror ref, no holding area. It reads git and calls `gh`. There is nothing to get stuck and nothing to recover.
- **It never re-reviews cold.** Discover runs once into a closed set. After that it only *verifies* that set, which can only shrink. Anything new it notices during a fix round is filed, not added — so the loop terminates, by construction.
- **Nothing ships unvalidated.** The tree that gets pushed is a tree your checks ran on. If a fix round changes something, the checks run again.
- **Reviewers are read-only.** The review agent can read the repo; it cannot run commands. The fix agent can edit; it cannot run commands either. Your checks are the only thing that executes code.

## Install

```sh
go install github.com/richdapice/lgtm@latest
```

Needs `git`, `gh` (authenticated), and at least one review agent on your PATH — `claude` by default.

## Quick start

```sh
cd your-repo
lgtm init          # detects your projects, asks a few questions, writes .lgtm.toml
lgtm doctor        # checks each configured agent answers
git checkout -b my-change
# ... commit your work ...
lgtm               # review, fix, open the PR
```

`lgtm init --statusline` adds the live bar to Claude Code's status line. It re-renders in about 4ms.

## Configuration

`.lgtm.toml` at your repo root, written by `init`:

```toml
[lgtm]
mode = "manual"        # manual | auto
max_fix_rounds = 3
fanout = "single"      # single | parallel
# max_budget_usd = 0.75  # hard cap per agent call; 0 = uncapped

[lens.conventions]
model = "haiku"        # per-lens model when fanout = "parallel"

[[project]]            # monorepos: first path-prefix match wins, "." last
path = "website"
test = ""              # empty = skipped and reported as skipped, never counted as a pass
lint = "npm run typecheck"

[[project]]
path = "."
test = "npx vitest related {files} --run"
lint = "npm run typecheck && npx eslint {files}"
```

Agents live in `~/.config/lgtm/config.toml`. Any CLI that reads a prompt on stdin and answers on stdout works:

```toml
default_agent = "claude"

[[agent]]
name = "claude"
command = ["claude", "-p", "--restricted", "--permission-prompts", "none", "--output-format", "json"]
fix_command = ["claude", "-p", "--permission-mode", "acceptEdits", "--permission-prompts", "none",
               "--allowedTools", "Read,Edit,Write,Grep,Glob", "--output-format", "json"]
schema = "native"      # returns validated JSON for a schema
model = "opus"

[[agent]]
name = "copilot"
command = ["copilot", "-s", "--no-ask-user", "--deny-tool", "shell", "--deny-tool", "write"]
schema = "prompt"      # schema goes in the prompt; the reply is parsed leniently
```

`schema = "native"` uses the CLI's own structured output and gets validated objects back. `schema = "prompt"` pastes the schema into the prompt and extracts the JSON from whatever comes back — code fences and prose included — then validates it the same way. Either way, a finding with a bad line number is snapped to the nearest reviewable line rather than dropped.

## Costs

The `≈$` figures are estimates at API list price, computed locally. On a Claude Pro or Max subscription they are not charges — usage counts against your plan windows, which the status bar shows as `5h 37% · 7d 61%`. On a metered agent they are real; set `max_budget_usd`.

A review agent that can read the repo will read beyond the diff. That's where the good findings come from and where the cost goes.

## Other harnesses

`lgtm status --json` and `lgtm findings --json` are the machine interface. Exit code `2` means held — findings need a human. Anything that can run a command can drive it.

## Status

Early. Discover, the status bar, `init`, and `doctor` are exercised against real repos. Fix rounds, PR creation, and the CI watcher are built and unit-tested but have had less time in front of real diffs. Expect edges.

## License

MIT
