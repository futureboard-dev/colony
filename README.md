# colony

**Colony is an agentic engineering toolkit.**

It is a CLI-first toolkit that drives an autonomous swarm of role-specialised
agents — product manager, software engineer, designer, marketer, QA, reviewer —
through the full lifecycle of a software project: ideation → blueprint →
implementation → review → docs → release notes. Each role is a prompt persona
backed by an LLM (currently external APIs, eventually local models), and each
task is a Go subcommand that orchestrates one or more agents through Eino
chains/graphs.

## Objectives

- **Fullstack coverage.** A single binary that owns every step of building
  software, not just code generation. Frontend, backend, infra, copy, growth —
  any role we can describe in a prompt is a first-class agent.
- **Agentic, not chat.** Subcommands compose multi-step agent flows
  (planner → executor → critic → writer) via Eino's `compose.Graph`. Humans
  invoke a goal; colony runs the loop until the goal is verified or fails.
- **Provider-agnostic.** External APIs today (Anthropic, OpenAI) with a clean
  path to local models (Ollama, llama.cpp). Swapping is a config change, never
  a code change — see "Service-per-call rule".
- **Markdown-first prompts.** Roles and tasks live as `.md` files under
  `pkg/prompt/`, embedded into the binary. Prompt iteration is a doc edit, not
  a Go change.
- **Composable modules.** Each capability (`blueprint`, `swarm`, `task`,
  `log`, …) is a self-contained subcommand + service package. New roles and
  new tasks plug in without touching unrelated code.
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
│   │   ├── config.go          # .colony/config.json loader
│   │   ├── blueprint.go       # `colony blueprint`
│   │   ├── log.go             # `colony log`
│   │   ├── swarm.go           # `colony swarm`
│   │   ├── task.go            # `colony task`
│   │   ├── task_done.go       # `colony task done`
│   │   └── task_list.go       # `colony task list`
│   │
│   ├── llm/                   # thin wrapper over Eino's ChatModel
│   │   ├── factory.go         # NewChatModel(ctx, cfg) — returns model.ChatModel
│   │   ├── anthropic.go       # eino-ext/components/model/claude
│   │   ├── openai.go          # eino-ext/components/model/openai
│   │   └── ollama.go          # eino-ext/components/model/ollama (local)
│   │
│   ├── service/               # business logic — one package per capability
│   │   ├── blueprint/         # used by `colony blueprint`
│   │   │   ├── service.go     # type Service struct{ chat model.ChatModel }; New(...)
│   │   │   └── service_test.go
│   │   ├── log/               # used by `colony log`
│   │   ├── swarm/             # used by `colony swarm`
│   │   └── task/              # used by `colony task*`
│   │
│   ├── prompt/                # prompts as markdown, embedded via //go:embed
│   │   ├── prompt.go          # Load(name) / Render(name, data) helpers
│   │   ├── roles/             # persona system prompts
│   │   │   ├── marketing.md
│   │   │   ├── software-engineer.md
│   │   │   ├── product-manager.md
│   │   │   └── critic.md
│   │   └── tasks/             # per-subcommand task prompts
│   │       ├── blueprint.md
│   │       ├── log-summarize.md
│   │       └── swarm-plan.md
│   │
│   └── module/                # shared helpers used by multiple subcommands
│       ├── workspace.go       # project-root + .colony/ resolution
│       └── io.go              # stdin/stdout/file helpers
│
├── isolation/                 # legacy standalone binaries — being ported into pkg/cmd
└── .colony/                   # per-project config (created by `colony init`)
    └── config.json
```

## Module pattern (one subcommand = one module)

Each subcommand file exposes exactly one `*cobra.Command` and is registered in
`root.go`'s `init()`. Subcommands stay thin — they parse flags, build messages,
hand them to a freshly-constructed Eino `ChatModel`, and render the result.

```go
// pkg/cmd/blueprint.go
var blueprintCmd = &cobra.Command{
    Use:   "blueprint",
    Short: "Generate a project blueprint",
    RunE:  runBlueprint,
}

func runBlueprint(cmd *cobra.Command, args []string) error {
    cfg, err := loadConfig(".")
    if err != nil {
        return err
    }

    // Fresh ChatModel per invocation — the Go way: no package-level
    // singletons, no hidden global state, easy to swap, easy to test.
    chat, err := llm.NewChatModel(cmd.Context(), cfg.LLM)
    if err != nil {
        return fmt.Errorf("init chat model: %w", err)
    }

    msg, err := chat.Generate(cmd.Context(), []*schema.Message{
        schema.SystemMessage(blueprintSystemPrompt),
        schema.UserMessage(strings.Join(args, " ")),
    })
    if err != nil {
        return fmt.Errorf("blueprint: %w", err)
    }
    fmt.Println(msg.Content)
    return nil
}
```

For multi-step flows (planner → critic → writer) use Eino's `compose.Chain` /
`compose.Graph` instead of hand-rolled orchestration — still constructed fresh
inside `RunE`, never cached at package scope.

## Services

`pkg/service/<name>/` holds the actual work. The Cobra command in `pkg/cmd/`
just parses flags and delegates; the service owns prompt selection, message
construction, Eino composition, and post-processing. This keeps subcommands
trivial and makes services reusable (tests, future HTTP server, other CLIs).

```go
// pkg/service/blueprint/service.go
package blueprint

type Service struct {
    chat   model.ChatModel
    role   string // e.g. "software-engineer"
}

func New(chat model.ChatModel, role string) *Service {
    return &Service{chat: chat, role: role}
}

func (s *Service) Generate(ctx context.Context, idea string) (string, error) {
    sys, err := prompt.Render("roles/"+s.role, nil)
    if err != nil { return "", err }
    task, err := prompt.Render("tasks/blueprint", map[string]any{"Idea": idea})
    if err != nil { return "", err }

    msg, err := s.chat.Generate(ctx, []*schema.Message{
        schema.SystemMessage(sys),
        schema.UserMessage(task),
    })
    if err != nil { return "", err }
    return msg.Content, nil
}
```

```go
// pkg/cmd/blueprint.go — thin
func runBlueprint(cmd *cobra.Command, args []string) error {
    cfg, _ := loadConfig(".")
    chat, err := llm.NewChatModel(cmd.Context(), cfg.LLM)
    if err != nil { return err }

    out, err := blueprint.New(chat, roleFlag).Generate(cmd.Context(), strings.Join(args, " "))
    if err != nil { return err }
    fmt.Println(out)
    return nil
}
```

## Prompts

Prompts live as plain markdown under `pkg/prompt/` and ship inside the binary
via `//go:embed`. Two subdirectories:

- **`roles/`** — persona system prompts (`marketing.md`, `software-engineer.md`,
  …). Pick one per invocation via `--role` or service config.
- **`tasks/`** — per-subcommand user-message templates (`blueprint.md`,
  `log-summarize.md`, …). Rendered with Go's `text/template` so flags / context
  can be interpolated.

```go
// pkg/prompt/prompt.go
package prompt

import (
    "bytes"
    "embed"
    "fmt"
    "text/template"
)

//go:embed roles/*.md tasks/*.md
var fs embed.FS

func Render(name string, data any) (string, error) {
    raw, err := fs.ReadFile(name + ".md")
    if err != nil {
        return "", fmt.Errorf("prompt %q not found: %w", name, err)
    }
    if data == nil {
        return string(raw), nil
    }
    tmpl, err := template.New(name).Parse(string(raw))
    if err != nil { return "", err }
    var buf bytes.Buffer
    if err := tmpl.Execute(&buf, data); err != nil { return "", err }
    return buf.String(), nil
}
```

Example `pkg/prompt/roles/software-engineer.md`:

```markdown
You are a senior software engineer. Be concrete, cite tradeoffs, prefer the
simplest design that solves the problem. Output runnable code, not pseudocode.
```

Example `pkg/prompt/tasks/blueprint.md`:

```markdown
Produce a project blueprint for the following idea:

{{.Idea}}

Include: stack choice, directory layout, first-week milestones.
```

**Why markdown + embed:**
- Prompts are reviewed and diffed like docs, edited by non-Go contributors.
- `embed.FS` ships them in the single binary — no runtime path lookup, no
  "where's the prompt file" deploy problem.
- Templates keep dynamic data out of the prompt body.

## Service-per-call rule

> **Every external call constructs a fresh `ChatModel`.**

- No package-level clients. No `init()`-time API connections.
- `llm.NewChatModel(ctx, cfg)` returns Eino's `model.ChatModel` interface; the
  concrete implementation (`claude`, `openai`, `ollama`, …) is chosen from
  config.
- Subcommands depend on the Eino interface, never on a concrete provider —
  swapping Anthropic for a local Ollama model is a config change, not a code
  change.
- Construction is cheap (struct init + `http.Client`); auth and model load
  happen lazily on first `Generate` / `Stream`.

```go
// pkg/llm/factory.go
package llm

import (
    "context"
    "fmt"

    "github.com/cloudwego/eino/components/model"
    claude  "github.com/cloudwego/eino-ext/components/model/claude"
    openai  "github.com/cloudwego/eino-ext/components/model/openai"
    ollama  "github.com/cloudwego/eino-ext/components/model/ollama"
)

type Config struct {
    Provider  string `json:"provider"`            // anthropic | openai | ollama
    Model     string `json:"model"`
    APIKeyEnv string `json:"api_key_env,omitempty"`
    Endpoint  string `json:"endpoint,omitempty"`  // for ollama / self-hosted
}

func NewChatModel(ctx context.Context, cfg Config) (model.ChatModel, error) {
    switch cfg.Provider {
    case "anthropic":
        return claude.NewChatModel(ctx, &claude.Config{
            APIKey: os.Getenv(cfg.APIKeyEnv),
            Model:  cfg.Model,
        })
    case "openai":
        return openai.NewChatModel(ctx, &openai.ChatModelConfig{
            APIKey: os.Getenv(cfg.APIKeyEnv),
            Model:  cfg.Model,
        })
    case "ollama":
        return ollama.NewChatModel(ctx, &ollama.ChatModelConfig{
            BaseURL: cfg.Endpoint,
            Model:   cfg.Model,
        })
    default:
        return nil, fmt.Errorf("unknown llm provider: %q", cfg.Provider)
    }
}
```

## Config

`.colony/config.json` at the project root. Created by `colony init`.

```json
{
  "root": "/abs/path/to/project",
  "llm": {
    "provider": "anthropic",
    "model": "claude-opus-4-7",
    "api_key_env": "ANTHROPIC_API_KEY"
  }
}
```

To switch to a local model later:

```json
{
  "llm": { "provider": "ollama", "model": "qwen2.5-coder:7b", "endpoint": "http://localhost:11434" }
}
```

## Adding a new subcommand

1. Create `pkg/cmd/<name>.go` with `var <name>Cmd = &cobra.Command{...}`.
2. Register it in `pkg/cmd/root.go` `init()`.
3. If it talks to a model, call `llm.NewChatModel(ctx, cfg.LLM)` inside
   `RunE` — don't hold a long-lived client.
4. Put shared logic in `pkg/module/`, never in `pkg/cmd/`.

## Quick start

### Prerequisites

- Go 1.21+
- An API key for your chosen provider (default: Anthropic)

```bash
export ANTHROPIC_API_KEY=sk-ant-...
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

> `make install` creates a symlink from `~/.local/bin/colony` → `./colony` in this repo.
> After any code change, run `make build` — the symlink picks it up immediately.

### Usage

```bash
colony init                                        # create .colony/config.json
colony blueprint "a URL shortener in Go"           # generate a project blueprint
colony log                                         # summarise git log
colony swarm "add rate limiting to the API"        # run the swarm planner
colony task add "write unit tests for auth"        # create a task
colony task list                                   # list tasks
colony task done <id>                              # mark task complete
```

### Switching LLM provider

Edit `.colony/config.json` — no code changes needed:

```json
{
  "llm": { "provider": "ollama", "model": "qwen2.5-coder:7b", "endpoint": "http://localhost:11434" }
}
```

### Uninstall

```bash
rm ~/.local/bin/colony
```
