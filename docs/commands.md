# Commands

![lgtm on autopilot, in the terminal](demo.gif)

| | |
|---|---|
| `lgtm` | Review, fix, open the PR, watch CI. Autopilot. |
| `lgtm --manual` | The same, but it asks you about each finding. |
| `lgtm push` | Review and fix, then `git push`. No PR. |
| `lgtm --no-pr` | Review and fix only. Nothing pushed. |
| `lgtm -b BRANCH` | Any of the above, on a branch checked out in another worktree. |

<details>
<summary>Everything else</summary>

| | |
|---|---|
| `lgtm --draft` | Open the PR as a draft. |
| `lgtm status [--json]` | Where the run is: phase, counts, cost. |
| `lgtm findings [--json]` | Every finding, with the fixer's notes. |
| `lgtm decide ID fix\|accept\|dismiss\|skip` | Record a decision on a waiting run, no terminal needed. `-m "…"` gives the fixer a direction. |
| `lgtm continue [--auto]` | Apply recorded decisions and carry on. |
| `lgtm dismiss ID` | Never show this finding again. Goes on a list committed with the repo. |
| `lgtm init` | Read the repo (task runners, then [what it recognizes](configuration.md#your-checks)), show what it would run, write `.lgtm.toml`. The first time on a machine it also finds the agent CLIs on your PATH, asks which one reviews, writes `~/.config/lgtm/config.toml`, and probes it. `--agent NAME` picks or changes it; `--statusline` and `--skill` wire up Claude Code (once, globally). |
| `lgtm doctor` | Check each configured agent answers, and that its fixer can edit. |
| `lgtm demo` · `lgtm demo bar` | A scripted run in the panel or the status bar. No agent, no repo. `--parallel`, `--passes N`. |
| `lgtm -h` | The same list, with every flag. |

Exit codes: `0` done · `1` error · `2` findings need you (run `lgtm` again).

</details>



## From anywhere

A waiting run is visible in every Claude Code session, so you shouldn't have to find the right terminal to act on it.

```sh
lgtm -b my-branch                       # the panel for that branch, from any directory in the repo
lgtm decide 0fe5 accept -b my-branch    # record a decision without a terminal (id prefixes work)
lgtm continue -b my-branch              # apply what's recorded, run the round, open the PR
```

`lgtm init --skill` installs a `/lgtm` skill for Claude Code. In any chat, `/lgtm` shows what's waiting; you say "accept the first, fix the second"; it records that and continues.


## Scripting it

`lgtm status --json` and `lgtm findings --json` are the machine interface. Exit code 2 means findings need a human. Anything that can run a command can drive it. `LGTM_DEBUG=/path` writes a log with every agent call's cost, turns, and model.
