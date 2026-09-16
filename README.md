# LGTM

**Get to LGTM before you open the PR.**

<p align="center"><img src="docs/hero.svg" alt="a pull request gets reviewed, fixed, and stamped LGTM" width="960"></p>

Most of the code in a branch today was written by an AI, and it needs a harder review than human code, not a softer one. It compiles, it looks clean, and it hides things:

- AI-assisted pull requests carry **1.7× the defects** of human-only ones; logic and control-flow mistakes are 75% more frequent. ([CodeRabbit](https://www.coderabbit.ai/blog/state-of-ai-vs-human-code-generation-report))
- **44%** of AI code-generation tasks introduce a known vulnerability. ([Veracode, 2026](https://www.veracode.com/blog/2026-genai-code-security-report-ai-risk/))
- Swallowed exceptions and empty catch blocks are up **47%**: code that fails silently instead of loudly. ([GitClear](https://www.gitclear.com/the_ai_code_quality_maintainability_gap))

84% of developers use AI tools; 29% trust what they produce. ([source](https://interclypse.com/happenings/the-ai-trust-gap-what-developers-actually-believe-about-ai-code)) The gap is a review nobody has time to do.

`lgtm` does that review, on every branch, before the PR exists. Four lenses read the diff the way a careful colleague would. What they find gets fixed, and because the fixer is an AI too, nothing it writes is trusted either: every fix runs through your own tests and lint before it's committed, and a fix that fails is thrown away. Then it opens the pull request. On autopilot it never asks you anything.

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

**Claude Code, wired in.** A live status bar in every session, and a `/lgtm` skill so a held run can be worked from any chat.

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

Everything else is in [Commands](docs/commands.md).

## Inside Claude Code

```sh
lgtm init --statusline --skill      # once; applies to every repo
```

**The status bar.** Every Claude Code session shows the gate track and the rounds of every run in the repo, live, in about four milliseconds.

![the status bar mid-run](docs/bar.png)

**The `/lgtm` skill.** Type `/lgtm` in any chat and Claude tells you what's waiting on a held run, records what you say ("accept the first, fix the second"), and continues it. No terminal, no finding the right worktree. It drives `lgtm decide` and `lgtm continue` underneath, which you can also run yourself from anywhere with `-b BRANCH`.

Every row of the bar is explained in [The status bar](docs/status-bar.md); the commands the skill uses are in [Commands](docs/commands.md).

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
