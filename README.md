# LGTM

**Fresh eyes on your branch before the PR opens.**

<p align="center"><img src="docs/hero.svg" alt="a pull request gets reviewed, fixed, and stamped LGTM" width="960"></p>

`lgtm` reviews a branch the way a careful colleague would, fixes what it can, runs your own checks on the fixes, opens the pull request, and watches CI. It uses the coding agent you already have (Claude Code by default) and it runs on autopilot unless you tell it to ask.

```sh
go install github.com/richdapice/lgtm@latest
cd your-repo
lgtm init            # finds your projects, writes .lgtm.toml
lgtm doctor          # confirms the agent answers
git checkout -b my-change  # ...commit your work...
lgtm                 # review, fix, open the PR
```

You need `git`, `gh` logged in, and `claude` on your PATH (or another agent; see [Agents](#agents)).

## What happens when you run `lgtm`

![lgtm reviewing a branch in the terminal: findings, the gates, the stamp](docs/demo.gif)

1. **check.** Your own test and lint commands run on the files your branch changed, before a single token is spent. A diff that doesn't pass its own checks is sent back, not reviewed.
2. **review.** Four lenses read the diff between your branch and its base: correctness, your project's conventions (from `CLAUDE.md` / `AGENTS.md`), security, tests. The agent can read the rest of the repo while it thinks; that's where the good findings come from.
3. **fix.** The agent edits your working tree. (In manual mode there's a **decide** gate first, where it asks you.)
4. **recheck.** The same checks again, on the files it touched. A fix that fails them is reverted, not committed.
5. **verify.** Each fix is confirmed against the new diff. Anything still open goes around again, up to `max_fix_rounds`. The list of findings only ever gets shorter, so this always ends.
6. **pr, ci.** It pushes, writes the PR body from the diff and the literal check output, opens the PR, reacts 👀, watches CI, reacts 👍.

```
 lgtm · worktree-sync-throttle → main      ⠋ fix · round 2 of 3      ≈$0.62

 GATES
   ✓ check     your checks, on the diff
   ✓ review    4 lenses · 7 found
   ⠋ fix       agent working on 3 finding(s)
   ○ recheck
   ○ verify
   ○ pr
   ○ ci

 ROUNDS  ● ◐ ○    round 1: 3 fixed · 1 filed · 3 open
```

When it's through:

```
  ╭──────╮
  │ LGTM │  2 found · 2 fixed · 0 accepted · 0 filed · 3m48s · ≈$1.10
  ╰──────╯
  https://github.com/you/repo/pull/126
```

## Commands

| Command | What it does |
|---|---|
| `lgtm` | Review, fix, open the PR, watch CI. Autopilot. |
| `lgtm --manual` | The same, but the panel asks you about each finding. |
| `lgtm push` | Review and fix, then `git push`. No PR. |
| `lgtm --no-pr` | Review and fix only. Nothing pushed. |
| `lgtm --draft` | Open the PR as a draft. |
| `lgtm -b BRANCH` | Any of the above, on a branch checked out in another worktree. |
| `lgtm status [--json]` | Where the run is: phase, counts, cost. |
| `lgtm findings [--json]` | Every finding, with the fixer's notes. |
| `lgtm decide ID fix\|accept\|dismiss\|skip` | Record a decision on a waiting run. No terminal needed. `-m "…"` gives the fixer a direction. |
| `lgtm continue [--auto]` | Apply recorded decisions and carry on. |
| `lgtm dismiss ID` | Never show this finding again; it goes on a list committed with the repo. |
| `lgtm init` | Detect projects, write `.lgtm.toml`. `--statusline` and `--skill` wire up Claude Code. |
| `lgtm doctor` | Check each configured agent answers. |
| `lgtm demo` · `lgtm demo bar` | A scripted run in the panel, or in the status bar. No agent, no repo. `--parallel`, `--passes N`. |

Exit codes: `0` done · `1` error · `2` findings need you (run `lgtm` again).

## Autopilot and manual

**Autopilot** is the default. It never asks. Every finding goes to the fixer with a mandate to pick the smallest reasonable resolution. Whatever comes back unfixed is filed and listed in the PR body under *Before merging*. The PR opens either way; if a `block` finding shipped unfixed, it opens as a draft.

**Manual** (`lgtm --manual`, or `mode = "manual"` in `.lgtm.toml`) stops after the review and asks you, one finding at a time, in a panel under your prompt:

```
 lgtm · worktree-sync-throttle → main      2 findings need a decision      ≈$0.41

 FINDINGS
 ▶ 1  block  src/main/sync.ts:140         swallowed-error                        [ undecided ]
   2  ask    src/main/sync.test.ts:88     missing-assert                         [ undecided ]

 FINDING 1 · correctness · src/main/sync.ts
    138 +   try {
    139 +     await push(batch)
 ▶  140 +   } catch (err) { /* retry later */ }
    141     log(started)
   The retry never happens — nothing schedules it. Either enqueue the retry here or let the
   error surface; silently dropping it means a failed sync looks identical to no sync.

 WHAT DO YOU WANT TO DO WITH FINDING 1?
    ▶ Fix      Accept      Dismiss      Skip
   ↑↓ pick a finding · ←→ pick an action · Enter to apply · A autopilot · q quit for now
```

| Key | |
|---|---|
| `↑` `↓` | pick a finding |
| `←` `→` or `f` `a` `d` `s` | pick an action |
| `Enter` | apply it, move to the next undecided finding; once all are decided, submit |
| `A` | hand the rest to autopilot |
| `q` | quit for now; the run waits and `lgtm` resumes it |

| Action | |
|---|---|
| **Fix** | the agent changes the code; your checks run before it's committed |
| **Accept** | fine as-is |
| **Dismiss** | never show this again (committed to `.lgtm/dismissed.toml`) |
| **Skip** | not now; it stays open and the PR body says so |

Each finding has a severity: `block` must be fixed, `ask` is your call, `file` is worth writing down but never stops anything. If the fixer declines something because it needs a design decision, its reason stays on the finding, and you can answer it: `lgtm decide 0fe5 fix -m "crop the video for phones"`.

`--plain` swaps the panel for line prompts; that's also what you get when stdout isn't a terminal.

## The status bar

```sh
lgtm init --statusline      # adds it to Claude Code's status line
```

![the status bar mid-run](docs/bar.png)

It renders in about four milliseconds, from a state file the run writes as it goes, so it costs nothing and never blocks. It shows in every Claude Code session, and it changes shape with the run.

**Header.** Branch → base, then the mode or the state (`auto`, `manual`, `2 need you`, `passed`), elapsed time, estimated cost, and your subscription windows when Claude Code passes them along: `5h 37% ↺ 2h23m` is how much of the 5-hour window is used and when it resets; `7d` is the week.

**While reviewing.** One bar per agent call, filling as it runs:

```
      ─ review        ████████▊░░░░     –   opus      1m12s
```

With `dispatch = "parallel"` there's a bar per lens, and with `passes` a bar per pass, in a bracket. Finished ones go quiet with their count:

```
      ╭ correctness  ─────────────  ✓  7   opus      42s
      │ conventions  ████████▊░░░░     –   haiku     12s
      │ security     ─────────────  ✓  2   opus      38s
      ╰ tests        ██████▎░░░░░░     –   sonnet  1m04s
```

**After the review.** The gate track replaces the bars (✓ passed · ∴ now · ○ ahead), with the loop row under it. `recheck` is the same commands as `check`, run on what the fixer touched. `decide` shows only in manual mode.

```
gates ✓ check ─ ✓ review ─ ✓ fix ─ ✓ recheck ─ ∴ verify ─ ○ pr ─ ○ ci
 loop ● ● ○  round 2/3      2 open · 5 fixed · 0 filed
```

**Waiting on you** (manual mode). The count, and the exact command:

```
  lgtm ▸ worktree-sync-throttle → main   2 need you   3m16s   ≈$0.62
    → lgtm -b worktree-sync-throttle · or /lgtm in Claude Code
```

**PR open.** The PR number (a link) and CI as it comes in:

```
   ci ◍ #126   ▰▰▱▱   check 2/4 · 1m12s
```

**Idle.** Your last twenty runs as a sparkline, and how many in a row shipped without needing you:

```
  lgtm ▸ main   idle   12 runs today   5h 37% ↺ 2h23m · 7d 61% ↺ 3d04h
      20 runs  ▂▃▂▅▂▂▇▃▂▂▄▂▃▂▂▃▅▂▂▃  median 2m38s · 3 held · streak 4
```

Several runs at once (one per worktree) collapse to a line each. `lgtm demo bar` plays the whole sequence with no agent; `--parallel` and `--passes 2` show the other review shapes.

![the status bar through a whole run](docs/bar.gif)

## From anywhere

A waiting run is visible in every Claude Code session, so you shouldn't have to find the right terminal to act on it.

```sh
lgtm -b my-branch                       # the panel for that branch, from any directory in the repo
lgtm decide 0fe5 accept -b my-branch    # record a decision without a terminal (id prefixes work)
lgtm continue -b my-branch              # apply what's recorded, run the round, open the PR
```

`lgtm init --skill` installs a `/lgtm` skill for Claude Code. In any chat, `/lgtm` shows what's waiting; you say "accept the first, fix the second"; it records that and continues.

## Configuration

`lgtm init` writes `.lgtm.toml` at the repo root. Everything has a default; you only write the lines you want to change.

### Mode and rounds

```toml
[lgtm]
mode = "auto"            # auto | manual
max_fix_rounds = 3       # decide → fix → check → verify cycles before it stops
```

### The review

```toml
[lgtm]
dispatch = "batch"       # batch: one agent call carrying every lens (default)
                         # parallel: one call per lens at once, each on its own model
passes = ["sonnet", "opus"]   # review the diff more than once, each pass on its own model;
                              # findings merge. Default: one pass on the agent's model.
max_budget_usd = 0.75    # cap per agent call; unset = none

[lens.conventions]
model = "haiku"          # per-lens model, used when dispatch = "parallel"

[lens.security]
enabled = false          # turn a built-in lens off

[lens.perf]              # a lens of your own: name it, say what it looks for
prompt = "hot paths doing more work than they need to; N+1 queries; work inside loops that could happen once"
```

The four built-in lenses are `correctness`, `conventions`, `security`, and `tests`. A custom lens needs a `prompt`; you can also override a built-in's prompt the same way.

### Your checks

```toml
[[project]]              # monorepos: the first path-prefix match wins, so "." goes last
path = "website"
test = ""                # empty = skipped, and reported as skipped, never as a pass
lint = "npm run typecheck"

[[project]]
path = "."
test = "npx vitest related {files} --run"    # {files} = the changed paths, relative to the project
lint = "npm run typecheck && npx eslint {files}"
```

`init` detects these from `package.json`, `go.mod`, `pyproject.toml`, and `Cargo.toml`, two levels deep. These commands are the floor: they run on your diff before the review and on every fix after it. A fix round that no check validated is not committed.

### The pull request

```toml
[pr]
reactions = true         # false turns both off
on_open = "eyes"         # +1 -1 laugh confused heart hooray rocket eyes, or "" for none
on_green = "+1"
comment = "reviewed by lgtm: {found} found · {fixed} fixed · {filed} noted · {rounds} round(s) · ≈{cost}"
```

The comment is posted once the run is through, only if you set it. Placeholders: `{found} {fixed} {accepted} {filed} {rounds} {cost} {branch} {url}`.

### Agents

Agents live in `~/.config/lgtm/config.toml`, not the repo. Any CLI that reads a prompt on stdin and answers on stdout works.

```toml
default_agent = "claude"

[[agent]]
name = "claude"
command = ["claude", "-p", "--restricted", "--permission-prompts", "none", "--output-format", "json"]
fix_command = ["claude", "-p", "--permission-mode", "acceptEdits", "--permission-prompts", "none",
               "--allowedTools", "Read,Edit,Write,Grep,Glob", "--output-format", "json"]
schema = "native"        # the CLI validates JSON against a schema itself
model = "opus"

[[agent]]
name = "copilot"
command = ["copilot", "-s", "--no-ask-user", "--deny-tool", "shell", "--deny-tool", "write"]
schema = "prompt"        # schema goes in the prompt; the reply is parsed leniently, then validated
```

Two commands per agent because reviewing and fixing are different jobs: the reviewer can read but not run anything; the fixer can edit but not run anything. Your checks are the only thing that executes code. `lgtm doctor` round-trips each agent once.

## Costs

The `≈$` figures are what a call would cost at API list price, worked out locally. On a Claude Pro or Max subscription they aren't charges; usage counts against the 5-hour and 7-day windows the status bar shows. On a metered agent they're real, and `max_budget_usd` is the cap. A review of a small diff lands around a dollar or two, most of it the agent reading around the change.

## What it will never do

- Hold your branch. No proxy remote, no mirror ref, no holding area. It reads git and calls `gh`, so nothing can get stuck.
- Review the same diff twice. One review into a fixed list; after that it only checks whether items were addressed, and the list only gets shorter. New things it notices during a fix round get written down, not added. That's why it always finishes.
- Ship something your checks didn't see. A fix changes the tree; the checks run again, or nothing is committed.
- Run your code. Neither agent can execute anything. Only your configured checks do.

## Scripting it

`lgtm status --json` and `lgtm findings --json` are the machine interface. Exit code 2 means findings need a human. Anything that can run a command can drive it. `LGTM_DEBUG=/path` writes a log with every agent call's cost, turns, and model.

## Status

Young. It has opened real PRs on a real Electron monorepo. The review, the bar, the panel, `init`, and `doctor` have had the most use; fix rounds and the CI watcher have had less. If something surprises you, it's probably a bug. Open an issue with the debug log.

## License

MIT
