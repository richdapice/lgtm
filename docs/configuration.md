# Configuration

`lgtm init` writes `.lgtm.toml` at the repo root, per repo. Everything has a default, so a working config is short:

```toml
[lgtm]
mode = "auto"

[[project]]
path = "."
test = "npx vitest related {files} --run"
lint = "npm run typecheck && npx eslint {files}"
```

The rest of this section is what else you can set.

### Mode and rounds

```toml
[lgtm]
mode = "auto"            # auto | manual
max_fix_rounds = 3       # fix → recheck → verify cycles before it stops
```

### The review

```toml
[lgtm]
dispatch = "batch"       # batch: one agent call carrying every lens (default)
                         # parallel: one call per lens at once, each on its own model
passes = ["sonnet", "opus"]   # review the diff more than once, each pass on its own model;
                              # findings merge. Default: one pass on the agent's model.
max_budget_usd = 0.75    # cap per agent call; unset = none
ignore = ["**/*.md", "docs/**", ".github/**"]   # changes to these never trigger checks

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

[[project]]              # a native app: nothing fast enough to run at every gate
path = "ios"
lint = "swiftlint lint --strict"
suite = "xcodebuild test -scheme App -destination 'platform=iOS Simulator,name=iPhone 16' -quiet"
```

`test` and `lint` run at `check` (on your diff, before the review) and at every `recheck` (on what the fixer touched). A fix round that no check validated is not committed. `suite` is for a slow whole-project run that can't be scoped to changed files: once, after the fix rounds, before anything is pushed. A failed suite opens the PR as a draft with the output in the body, and `lgtm push` refuses to push.

`init` proposes these from `package.json`, `go.mod`, `pyproject.toml`, and `Cargo.toml`, two levels deep; anything else you write by hand.

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
