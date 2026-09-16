# Manual mode

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
