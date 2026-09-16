# LGTM

**Get to LGTM before you open the PR.**

<p align="center"><img src="docs/hero.svg" alt="a pull request gets reviewed, fixed, and stamped LGTM" width="960"></p>

You already run the tests before you push. What you don't have is someone who *read the change*. `lgtm` is that reader: it reviews your branch the way a careful colleague would, fixes what it finds, proves each fix against your own checks, and opens the pull request. On autopilot it never asks you anything. It uses the coding agent you already pay for.

```sh
go install github.com/richdapice/lgtm@latest
cd your-repo && lgtm init        # finds your projects, writes .lgtm.toml
git checkout -b my-change        # ...commit your work...
lgtm                             # review, fix, open the PR
```

Needs `git`, `gh` (logged in), and `claude` on your PATH.

## What you get

**A real review, before anyone else has to do one.** Four lenses read the diff: correctness, your project's conventions (from your `CLAUDE.md`), security, tests. The agent reads around the change, which is where the findings that matter come from.

**Fixes that are proven, not just made.** Your own test and lint commands run on your diff before the review, and again on every fix. A fix that fails them is reverted. Nothing is committed that your checks didn't see.

**A loop that always ends.** The review produces one fixed list of findings. Fix rounds can only shorten it. Three rounds, then it ships or hands the rest to you.

**The PR, opened.** Pushed, body written from the diff and the literal check output, 👀 reacted, CI watched, 👍 when it's green.

**A live status bar in Claude Code**, in every session, showing which gate every run is at.

## What happens when you run `lgtm`

![lgtm on autopilot: check, review, fix, recheck, verify, the PR, the stamp](docs/demo.gif)

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

Eight gates. `check` is your commands on your diff. `review` is the agent reading it. `fix → recheck → verify` is the loop: the agent edits, your checks run on what it touched, each fix is confirmed against the new diff. Then `pr` and `ci`. When it's through:

```
  ╭──────╮
  │ LGTM │  2 found · 2 fixed · 0 accepted · 0 filed · 3m48s · ≈$1.10
  ╰──────╯
  https://github.com/you/repo/pull/126
```

## Three ways to run it

| | |
|---|---|
| `lgtm` | Review, fix, open the PR, watch CI. **Autopilot.** |
| `lgtm --manual` | The same, but it asks you about each finding in a panel. |
| `lgtm push` | Review and fix, then `git push`. No PR. |

Everything else, including acting on a run from any terminal or from a Claude Code chat with `/lgtm`, is in [Commands](docs/commands.md).

## The status bar

```sh
lgtm init --statusline
```

![the status bar mid-run](docs/bar.png)

Four milliseconds to render, every Claude Code session, the gate track and the rounds as they happen. Every row explained in [The status bar](docs/status-bar.md).

## Make it yours

`.lgtm.toml` in each repo. Everything has a default; a working config is four lines. What you can change:

- which commands check which files, per project, including a slow `suite` that runs once before the PR
- autopilot or manual, and how many fix rounds
- the lenses: turn one off, put one on a cheaper model, write your own
- one review call or one per lens, or several passes on different models
- what it leaves on the PR: reactions, a comment
- which agent, if not Claude Code

All of it in [Configuration](docs/configuration.md). Manual mode's panel and keys are in [Manual mode](docs/manual-mode.md).

## Guarantees

- **It never holds your branch.** No proxy remote, no mirror ref, no holding area. It reads git and calls `gh`, so nothing can get stuck.
- **It never reviews the same diff twice.** One review, one list, and the list only gets shorter. That's why it always finishes.
- **It never ships what your checks didn't see.** A fix changes the tree, the checks run again, or nothing is committed.
- **It never runs your code.** Neither agent can execute anything. Only your configured checks do.

## Costs

The `≈$` figures are estimates at API list price. On a Claude Pro or Max subscription they aren't charges; usage counts against your plan's windows, which the bar shows. A review of a small diff lands around a dollar or two.

## Status

Young. It has opened real PRs on a real Electron monorepo. If something surprises you, it's probably a bug: open an issue with the debug log (`LGTM_DEBUG=/tmp/lgtm.log lgtm`).

MIT.
