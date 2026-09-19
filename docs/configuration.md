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

`lgtm init` proposes these by reading the repo, two levels deep. A task runner comes first: a Makefile, justfile, or Taskfile with `test`, `lint` (or `check`), and `test-all` targets has already said how the project wants to be checked, whatever the language. Then a table of markers fills in the rest: package.json (vitest, jest, eslint, typecheck scripts), go.mod, Cargo.toml, pyproject.toml (ruff), Gemfile (rspec, rubocop), mix.exs, composer.json (phpunit, phpstan), .sln and .csproj, pubspec.yaml, deno.json, build.zig, stack.yaml and .cabal, Package.swift. Xcode, Gradle, Maven, and CMake are recognized but not guessed at, since the right command depends on a scheme or a module; init shows the usual shape and asks.

The table is [ecosystems.toml](../internal/setup/ecosystems.toml) in the binary. Copy any of it to `~/.config/lgtm/ecosystems.toml` to add or override an entry; yours are tried first.

```toml
# ~/.config/lgtm/ecosystems.toml
[[ecosystem]]
name = "gotestsum"
marker = "go.mod"
test = "gotestsum ./..."
```

### Conventions

The conventions lens reads whatever the repo keeps its rules in, without being told: `CLAUDE.md`, `AGENTS.md`, `GEMINI.md`, `.cursorrules`, `.windsurfrules`, `.clinerules`, and `CONVENTIONS.md` at the root and in every directory above a changed file, plus `.github/copilot-instructions.md` and `.cursor/rules/*.mdc` at the root, plus your global Claude, Codex, and Gemini instruction files. Anything else goes here:

```toml
[lgtm]
conventions = ["CONTRIBUTING.md", "docs/style/*.md"]   # globs, relative to the root; * is one path segment, there is no **
```

### The pull request

```toml
[pr]
reactions = true         # false turns both off
on_open = "eyes"         # +1 -1 laugh confused heart hooray rocket eyes, or "" for none
on_green = "+1"
comment = "reviewed by lgtm: {found} found · {fixed} fixed · {filed} noted · {rounds} round(s) · ≈{cost}"
```

The comment is posted once the run is through, only if you set it. Placeholders: `{found} {fixed} {accepted} {filed} {rounds} {cost} {branch} {url}`.

### Triage

```toml
[triage]
enabled = true           # false turns it off for this repo, whatever the machine says
skip_below = 0.2         # autopilot files an `ask` under this probability of being real
                         # instead of paying a fix round; 0 = always fix
```

Triage is off until you opt in on the machine: `lgtm init --triage`, which sets `triage = true` in `~/.config/lgtm/config.toml` and probes the key. The first `lgtm init` on a machine asks, when a key is already in the environment. Once on, every finding that needs a decision — a `block` or `ask` pointing at a line — gets a second opinion from [Jev](https://docs.typesafe.ai), TypeSafe's System One model: a probability that it's a real problem in the code it points at, and a suggested action. A `file` finding never needs one, and a finding about the change as a whole shows no code to judge, so those go without. It's one request for the whole set, a fraction of a cent, about a second. The key stays in the environment; this file is committed.

The two switches are deliberate: a key in the environment is not consent, because it can be there for other tools, and turning triage on means diff hunks and commit messages leave the machine for every repo you review. The machine opts in; a repo can only opt out.

What it does with the opinion is deliberately small. In manual mode the panel pre-selects Jev's suggestion and shows the numbers under the finding; you still press Enter. In autopilot, an `ask` that Jev puts under `skip_below` is filed with the reason instead of sent to the fixer, and shows up in the PR body's list, so a person still sees it. A `block` always goes to the fixer, whatever Jev thinks. Without the opt-in nothing changes.

### Agents

Agents live in `~/.config/lgtm/config.toml`, not the repo. `lgtm init` writes it the first time: it looks for `claude`, `copilot`, `gemini`, and `codex` on your PATH, asks which one reviews and fixes, writes a recipe for every one it found, and probes your pick in both postures before it says done. `lgtm init --agent copilot` switches later. Any CLI that reads a prompt on stdin and answers on stdout works; add it by hand the same way. Two commands per agent because reviewing and fixing are different jobs: the reviewer can read but not run anything; the fixer can edit but not run anything. Your checks are the only thing that executes code.

`schema = "native"` means the CLI takes `--json-schema` and validates its own output (Claude Code). `schema = "prompt"` means the schema is pasted into the prompt and the reply is parsed leniently, then validated; use it for everything else. `model` is passed as `--model` when set, so leave it out for a CLI that spells the flag differently.

```toml
default_agent = "claude"

[[agent]]                # Claude Code: the default, and what the built-in config is
name = "claude"
command = ["claude", "-p", "--restricted", "--permission-prompts", "none", "--output-format", "json"]
fix_command = ["claude", "-p", "--permission-mode", "acceptEdits", "--permission-prompts", "none",
               "--allowedTools", "Read,Edit,Write,Grep,Glob", "--output-format", "json"]
schema = "native"
model = "opus"

[[agent]]                # GitHub Copilot CLI
name = "copilot"
command = ["copilot", "-s", "--no-ask-user", "--deny-tool", "shell", "--deny-tool", "write"]
fix_command = ["copilot", "-s", "--no-ask-user", "--deny-tool", "shell", "--allow-tool", "write"]
schema = "prompt"

[[agent]]                # Gemini CLI
name = "gemini"
command = ["gemini", "-p", "", "--approval-mode", "plan"]
fix_command = ["gemini", "-p", "", "--approval-mode", "auto_edit"]
schema = "prompt"

[[agent]]                # Codex CLI
name = "codex"
command = ["codex", "exec", "--sandbox", "read-only", "--skip-git-repo-check", "-"]
fix_command = ["codex", "exec", "--sandbox", "workspace-write", "--skip-git-repo-check", "-"]
schema = "prompt"
```

Then in any repo, `agent = "copilot"` under `[lgtm]` picks one, or set `default_agent`. The Claude and Copilot recipes are verified on the author's machine; Gemini and Codex follow their documented flags. The probe is what verifies any of them on yours: it asks the review command for a one-word answer, then has the fix command create a file in a scratch directory and checks that it did. `lgtm doctor` runs it for every agent in the file.

## Costs

The `≈$` figures are what a call would cost at API list price, worked out locally. On a Claude Pro or Max subscription they aren't charges; usage counts against the 5-hour and 7-day windows the status bar shows. On a metered agent they're real, and `max_budget_usd` is the cap. A review of a small diff lands around a dollar or two, most of it the agent reading around the change.
