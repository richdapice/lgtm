# The status bar

```sh
lgtm init --statusline      # adds it to Claude Code's status line
```

![the status bar mid-run](bar.png)

It renders in about four milliseconds from a state file the run writes as it goes, so it costs nothing and never blocks. It shows in every Claude Code session and changes shape with the run.

**Header.** Branch → base, the state (`auto`, `manual`, `2 need you`, `passed`), elapsed, estimated cost, and your subscription windows when Claude Code passes them along: `5h 37% ↺ 2h23m` is how much of the 5-hour window is used and when it resets; `7d` is the week.

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

Several runs at once (one per worktree) collapse to a line each. `lgtm demo bar` plays the whole thing with no agent.

![the status bar through a whole run](bar.gif)
