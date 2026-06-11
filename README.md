# colony

**Colony is an agentic engineering toolkit.**

It is a CLI-first toolkit that drives an autonomous swarm of role-specialised
agents — coordinator, software engineer, reviewer, scout — through the full
lifecycle of a software task: spec → craft → implementation → review → docs.
Each role is a prompt persona, and each task is a Go subcommand that
orchestrates one or more agents in isolated git worktrees.

**Colony orchestrates; it is not itself the agent.** The actual agent loop —
tool calls, file edits, MCP, retries — is delegated to a coding-agent CLI:

- `provider: anthropic` → the **`claude`** CLI
- any other provider → the **`crush`** CLI

This is the key design choice. Colony owns the multi-step orchestration the
CLIs don't do (swarm planning, multi-lens review, worktree lifecycle, task
state); the CLI owns the hard part (the agent loop). Using `claude` also means
the toolkit runs on a team's existing Claude subscription seats — no per-token
API billing required for the default path.

## Objectives

- **Fullstack coverage.** A single binary that owns every step of building
  software, not just code generation — spec authoring, implementation, review,
  and release docs are all first-class subcommands.
- **Agentic, not chat.** Subcommands compose multi-step agent flows
  (coordinator → engineer → reviewer) in isolated worktrees. A human invokes a
  goal; colony runs the loop until the goal is verified or fails.
- **CLI-delegated execution.** Colony shells out to `claude` / `crush` for the
  agent loop. Swapping the backend is a config change (`provider`), never a
  code change. `claude` preserves Claude subscription usage; `crush` covers
  OpenAI / DeepSeek / OpenRouter / local models via API key.
- **Per-role models.** Different roles can run on different models — e.g.
  cheap Haiku for review lenses, Sonnet for synthesis — set in config, no code.
- **Markdown-first prompts.** Roles and tasks live as `.md` files under
  `pkg/prompt/`, embedded into the binary via `//go:embed`. Prompt iteration is
  a doc edit, not a Go change.
- **Local-first ergonomics.** Project state lives in `.colony/` at the repo
  root. No SaaS, no account, no lock-in. Runs in CI the same way it runs on a
  laptop.

## Layout

```
colony/
├── main.go                    # one-liner: cmd.Execute()
├── go.mod
├── pkg/
│   ├── cmd/                   # one file per subcommand (Cobra convention)
│   │   ├── root.go            # rootCmd, version, init() registers subcommands
│   │   ├── config.go          # loadConfig helper
│   │   ├── install.go         # `colony install` / `uninstall`
│   │   ├── craft.go           # `colony craft`
│   │   ├── swarm.go           # `colony swarm`
│   │   ├── review.go          # `colony review`
│   │   ├── spec_feature.go    # spec authoring
│   │   ├── log.go             # `colony log`
│   │   ├── mission.go         # `colony mission`
│   │   ├── task.go            # `colony task`
│   │   ├── task_done.go       # `colony task done`
│   │   └── task_list.go       # `colony task list`
│   │
│   ├── llm/                   # CLI delegation layer
│   │   ├── exec.go            # Executor: shells out to claude / crush
│   │   └── stream.go          # parses claude stream-json into a compact view
│   │
│   ├── config/               # .colony/config.json loader
│   │   └── config.go          # Config, LLMConfig, per-role resolution
│   │
│   ├── mission/              # multi-step orchestration graph
│   │   ├── graph.go           # node/edge graph
│   │   ├── runner.go          # executes the graph
│   │   └── registry.go        # node registry
│   │
│   ├── prompt/               # prompts as markdown, embedded via //go:embed
│   │   ├── prompt.go          # Load / Render helpers
│   │   ├── *.md               # role + task prompts (coordinator, build, fix, …)
│   │   └── module-prompts/    # review lenses, architect, estimator, …
│   │
│   ├── module/               # shared helpers (workspace, worktree, gates, io)
│   ├── storage/              # sqlite-backed task storage
│   └── output/               # terminal output formatting
│
└── .colony/                  # per-project state (created by `colony init`)
    └── config.json
```

## Execution model (`pkg/llm`)

Every agent call goes through `llm.Executor`, which selects a CLI from the
provider and shells out to it. Colony never talks to an LLM API directly.

```go
// pkg/llm/exec.go
type Executor struct {
    cfg config.LLMConfig
}

func New(cfg config.LLMConfig) *Executor { return &Executor{cfg: cfg} }

// anthropic → claude, everything else → crush
func (e *Executor) CLI() string {
    if e.cfg.Provider == "anthropic" {
        return "claude"
    }
    return "crush"
}
```

Three execution modes:

| Method           | Use                                  | claude flags                                   |
| ---------------- | ------------------------------------ | ---------------------------------------------- |
| `RunHeadless`    | pure text gen, no tools (coordinator, scout, reviewer) | `-p --output-format text` + all tools disabled |
| `RunAgent`       | autonomous coding with file tools (build, fix)         | `-p --output-format stream-json --verbose` + Write/Edit/Bash/Read/Glob/Grep |
| `RunInteractive` | hands-on session, blocks until exit  | `--add-dir <workdir>`                          |

For `claude`, `RunAgent` reads the `stream-json` event feed and renders it
through `streamClaude` into a compact one-line-per-action view. `crush` has no
structured stream format, so its output is passed through raw with `--verbose`.

A `Preflight()` check guards the `crush` path against a known tool-calling
regression (charmbracelet/crush#1322); it is a no-op for `anthropic`.

## Config

`.colony/config.json` at the project root, created by `colony init`. The
default provider is `anthropic` (the `claude` CLI), which manages its own auth
and needs no API key in env. Non-anthropic providers require their API key env
var (validated before each run).

```json
{
  "root": "/abs/path/to/project",
  "llm": {
    "provider": "anthropic",
    "model": "claude-sonnet-4-6"
  },
  "roles": {
    "lens_reviewer": {
      "provider": "anthropic",
      "model": "claude-haiku-4-5-20251001"
    }
  }
}
```

- **`llm`** — the default model for every role.
- **`roles`** — per-role overrides. `Config.Role(name)` falls back to `llm`;
  review lenses resolve via `Config.LensRole(lens)` → `<lens>_lens` →
  `lens_reviewer` → `llm`. This is how the review engine runs cheap per-lens
  analysis on Haiku while keeping synthesis on the default model.

To use a non-Anthropic provider (billed per-token via crush + API key):

```json
{
  "llm": { "provider": "openai", "model": "gpt-4o", "api_key_env": "OPENAI_API_KEY" }
}
```

> Note: non-anthropic providers go through `crush` and bill per token against
> the API key. The `anthropic` path goes through `claude` and uses your Claude
> subscription. Choose per your billing situation.

## Prompts

Prompts live as plain markdown under `pkg/prompt/` and ship inside the binary
via `//go:embed`. `prompt.go` exposes `Load(name)` / `Render(name, data)`
(rendered with Go's `text/template` so flags and context interpolate). Roles
and task prompts sit at the top level (`coordinator.md`, `build.md`, `fix.md`,
`scout.md`, `review.md`, …); review lenses and other module personas live under
`module-prompts/` (`review-bugs.md`, `review-security.md`, `architect.md`, …).

**Why markdown + embed:**
- Prompts are reviewed and diffed like docs, edited by non-Go contributors.
- `embed.FS` ships them in the single binary — no runtime path lookup.
- Templates keep dynamic data out of the prompt body.

## Adding a new subcommand

1. Create `pkg/cmd/<name>.go` with `var <name>Cmd = &cobra.Command{...}`.
2. Register it in `pkg/cmd/root.go` `init()`.
3. If it talks to a model, load config and construct a fresh
   `llm.New(cfg.Role("<role>"))` inside `RunE` — don't hold a long-lived
   executor.
4. Put shared logic in `pkg/module/`, prompts in `pkg/prompt/`.

## Quick start

### Prerequisites

- Go 1.25+ (see `go.mod`)
- The `claude` CLI (default path) — https://claude.ai/code
- Or, for non-Anthropic providers, the `crush` CLI —
  `brew install charmbracelet/tap/crush` and the matching API key:
  ```bash
  export OPENAI_API_KEY=sk-...   # only for non-anthropic providers
  ```

### First-time setup

```bash
# 1. Add ~/.local/bin to PATH (one-time)
echo 'export PATH="$HOME/.local/bin:$PATH"' >> ~/.zshrc && source ~/.zshrc

# 2. Build and symlink into PATH
make install

# 3. Verify
which colony          # → /Users/<you>/.local/bin/colony
colony version
```

> `make install` symlinks `~/.local/bin/colony` → `./colony` in this repo.
> After any code change, run `make build` — the symlink picks it up immediately.

### Usage

```bash
colony init                                        # create .colony/config.json
colony craft --spec SPEC.md --lang go              # run agent pipeline: spec → code → commit
colony log                                         # summarise git log
colony swarm "add rate limiting to the API"        # run the swarm planner
colony review --branch feat/auth                   # multi-lens AI code review
colony task add "write unit tests for auth"        # create a task
colony task list                                   # list tasks
colony task done <id>                              # mark task complete
```

### Code review (`colony review`)

Multi-lens AI code review. Each diff is analysed by four specialised lenses in
parallel, then merged by a synthesiser into a single verdict (`PASS`, `WARN`,
or `FAIL`).

| Lens         | What it looks for                                              |
| ------------ | -------------------------------------------------------------- |
| `bugs`       | logic errors, nil derefs, races, off-by-one, edge cases        |
| `slop`       | AI-generated filler, obvious comments, boilerplate, stubs      |
| `duplication`| copy-paste, near-duplicates, missing abstractions              |
| `security`   | secrets, injection, XSS/CSRF, auth bypass, IDOR                |

```bash
# Review a branch against main
colony review --branch feat/auth --base main

# Review a patch file (or stdin)
colony review --diff changes.patch
git diff main | colony review --diff -

# Pick a subset of lenses
colony review --branch feat/auth --lens bugs,security

# CI mode: JSON to stdout, exit 0/1/2 for PASS/WARN/FAIL
colony review --branch feat/auth --ci

# One-liner summary
colony review --branch feat/auth --summary
```

Reports and raw lens output are persisted under `.colony/logs/reviews/review-<ts>/`:

```
review-20260528-104530/
├── diff.patch
├── raw/
│   ├── bugs.json
│   ├── slop.json
│   ├── duplication.json
│   └── security.json
├── report.json     # synthesised verdict + findings
└── decision.txt    # PASS | WARN | FAIL
```

#### In the swarm (`colony swarm --review-depth`)

The swarm's reviewer step uses the same engine. Toggle cost vs. depth per run:

```bash
colony swarm --spec X.md --lang go                       # deep (default): 5 LLM calls/subtask
colony swarm --spec X.md --lang go --review-depth fast   # fast: 1 LLM call/subtask
```

- `deep` runs all four lenses + synthesiser. `FAIL` blocks the swarm; `PASS`
  and `WARN` approve (a `WARN` is logged as "APPROVED with warnings").
- `fast` runs the legacy single-prompt reviewer — cheap for large swarms where
  the per-subtask diff is small.

### Switching LLM provider

Edit `.colony/config.json` — no code changes needed. Anthropic uses the
`claude` CLI (subscription); any other provider uses `crush` (API key):

```json
{
  "llm": { "provider": "deepseek", "model": "deepseek-chat", "api_key_env": "DEEPSEEK_API_KEY" }
}
```

### Uninstall

```bash
rm ~/.local/bin/colony
```
