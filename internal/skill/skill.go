// Package skill writes the /lgtm skill for Claude Code, so a held run can be
// worked from any chat session without finding the right terminal.
package skill

import (
	"os"
	"path/filepath"
)

const Text = `---
name: lgtm
description: Work an lgtm review from chat. Use when the user says /lgtm, asks what lgtm is waiting on, wants to accept/fix/dismiss findings, or wants to continue or autopilot a held run. lgtm is a CLI that reviews a branch before its PR opens; its status bar shows "N need you" when a run is held for decisions.
---

# /lgtm

lgtm holds a run when findings need a human. From here you act on them without leaving the chat. Every command below is non-interactive and prints JSON where noted.

## Find the run

If the user names a branch, pass it with -b. Otherwise use the current directory.

    lgtm status --json [-b BRANCH]      # phase, counts, cost; exits 1 if no run
    lgtm findings --json [-b BRANCH]    # every finding: id, state, severity, lens, path, line, rule, body, note, fix_declined

Show the user only findings with state "open" and severity "block" or "ask". For each: severity, path:line, rule, one line of body, and the note if fix_declined is true (that is the fixer explaining it needs a decision — do not offer to fix those again unless the user insists).

## Record decisions

    lgtm decide ID fix|accept|dismiss|skip [-b BRANCH]

One call per finding. Use the id prefix from findings --json. dismiss also adds the finding to the repo's committed dismiss-list, so it never comes back — confirm with the user before dismissing.

## Continue the run

    lgtm continue [-b BRANCH]           # applies recorded decisions, runs the round, opens the PR, watches CI
    lgtm continue --auto [-b BRANCH]    # same, then autopilot: fixes block findings without asking
    lgtm continue --no-pr [-b BRANCH]   # review only; stop before pushing

continue is synchronous and may take a few minutes (fix round, checks, PR, CI). Run it in the background and report when it finishes. Exit 0 means the PR is open and the run printed its LGTM stamp; exit 2 means it held again — run findings --json and show what is still waiting.

## Rules

- Never run bare lgtm from here: it opens an interactive panel that needs a terminal.
- Do not decide for the user. Present the findings, take their words, record exactly those.
- If status says no run for the branch, say so; a review is started by the user running lgtm in a terminal on that branch.
`

// Install writes the skill at the user level so it applies in every repo.
func Install() (path string, changed bool, err error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", false, err
	}
	dir := filepath.Join(home, ".claude", "skills", "lgtm")
	path = filepath.Join(dir, "SKILL.md")
	if cur, err := os.ReadFile(path); err == nil && string(cur) == Text {
		return path, false, nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", false, err
	}
	return path, true, os.WriteFile(path, []byte(Text), 0o644)
}
