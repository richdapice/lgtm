# The status bar

```sh
lgtm init --statusline      # adds it to Claude Code's status line
```

![the status bar mid-run](bar.png)

It renders in about four milliseconds from a state file the run writes as it goes, so it costs nothing and never blocks. It shows in every Claude Code session and changes shape with the run.

**Header.** One dark strip with `lgtm · repo · branch → base`, then the state as its own segment: gray while running (`auto`, `manual`), amber when findings need you, green when passed, red when failed. Then elapsed, estimated cost, and your subscription windows when Claude Code passes them along: `5h 37% ↺ 2h23m` is how much of the 5-hour window is used and when it resets; `7d` is the week. The percentage turns amber at 70 and red at 90.

**While reviewing.** One bar per agent call. An agent call has no progress signal, so the bar doesn't pretend to have one: a head sweeps across it while the call runs, and the elapsed time on the right is the real number.

```
      ─ review        ░░░░░░▒▓█░░░░     –   opus      1m12s
```

With `dispatch = "parallel"` there's a bar per lens, and with `passes` a bar per pass, in a bracket. Finished ones go quiet with their count:

```
      ╭ correctness  ─────────────  ✓  7   opus      42s
      │ conventions  ░░░░░░░░░░▒▓█     –   haiku     12s
      │ security     ─────────────  ✓  2   opus      38s
      ╰ tests        ▒▓█░░░░░░░░░░     –   sonnet  1m04s
```

**After the review.** The gate track replaces the bars (✓ passed · ∴ now · ○ ahead), with the loop row under it. `recheck` is the same commands as `check`, run on what the fixer touched. `decide` shows only in manual mode.

```
gates ✓ check ─ ✓ review ─ ✓ fix ─ ✓ recheck ─ ∴ verify ─ ○ pr ─ ○ ci
 loop ● ● ○  round 2/3      2 open · 5 fixed · 0 filed
```

**Waiting on you** (manual mode). The count, and the exact command:

```
  lgtm · crmaapp · sync-throttle → main  2 need you   3m16s   ≈$0.62
    → lgtm -b worktree-sync-throttle · or /lgtm in Claude Code
```

**PR open.** The PR number (a link) and CI as it comes in:

```
   ci ◍ #126   ▰▰▱▱   check 2/4 · 1m12s
```

**Idle.** The repo and branch this session is in, then the last run in this repo (whatever branch it was on), your last twenty runs across every repo as a sparkline, today's count, and how many in a row shipped without needing you:

```
  lgtm · crmaapp · main  idle   5h 37% ↺ 2h23m · 7d 61% ↺ 3d04h
      sync-throttle ✓ 3 found · 3 fixed  41m ago      ▂▃▂▅▂▂▇▃▂▂▄▂▃▂▂▃▅▂▂▃  12 today · streak 4
```

The sparkline is gray for runs that shipped, amber for ones that needed you, red for failures.

The bar follows the session's directory, which is where Claude Code was started or last moved to, not your shell's. That is why several sessions on `main` all say `main`; the repo name tells them apart, and the `last` row says what happened here most recently.

Several runs at once (one per worktree) collapse to a line each. `lgtm demo bar` plays the whole thing with no agent.

![the status bar through a whole run](bar.gif)
