# Feature: hive-orchrestration-pipeline

---

# Agent Task Spec

## 1. Task (one sentence, one deliverable)
Build the `colony mission` command suite that loads a `*.mission.yaml`, executes a multi-agent graph (linear, fan-out, fan-in, cyclic) using a JSON-envelope protocol, persists every step to SQLite at `.colony/missions.db` with a queryable `audit` subcommand, and adds provider-aware API key validation to the config layer so every LLM-backed node fails fast with a clear error if the required key is missing.

---

## 2. Files In Scope

CREATE:
- `cmd/colony/mission.go` — cobra root for `colony mission` plus `run`, `init`, `audit` subcommands
- `pkg/mission/mission.go` — `Mission`, `Agent`, `Edge` structs + YAML loader
- `pkg/mission/graph.go` — `Graph` builder (mission → executable graph), validation
- `pkg/mission/node.go` — `Node` interface, `Input`, `Output`, `Envelope` types
- `pkg/mission/runner.go` — `Runner` interface + default implementation (goroutines, `sync.WaitGroup`, channels, cycle guard via `max_cycles`)
- `pkg/mission/registry.go` — `role string → Node factory` registry, self-registration pattern
- `pkg/mission/llm_node.go` — generic LLM-backed `Node` that calls `ValidateKey()` before execution, loads a module prompt, injects `INPUT`, parses the JSON envelope
- `pkg/storage/sqlite.go` — open/migrate `.colony/missions.db`, `InsertSession`, `UpdateSession`, `InsertStep`, audit queries
- `pkg/storage/schema.sql` — embedded `sessions` + `steps` DDL
- `pkg/prompt/module-prompts/project-manager.md` — new module prompt in standard format
- `pkg/mission/runner_test.go` — unit tests for linear/fan-out/fan-in/cycle
- `pkg/mission/mission_test.go` — YAML loading + graph validation tests
- `pkg/storage/sqlite_test.go` — session + step persistence tests
- `examples/feature-design.mission.yaml` — example mission matching the spec

MODIFY:
- `pkg/config/config.go` — add `KeyEnvName()` and `ValidateKey()` to `LLMConfig`; change `Init()` default model to `claude-sonnet-4-6` and drop `api_key_env` from the generated config (it is now derived from `provider`)
- `pkg/llm/exec.go` — call `e.cfg.ValidateKey()` at the top of `RunHeadless`, `RunAgent`, and `RunInteractive`
- `pkg/config/config_test.go` — add tests for `KeyEnvName()` and `ValidateKey()` covering all three providers and the unknown-provider error path
- `.colony/config.json` — update model to `claude-sonnet-4-6`, remove `api_key_env` field
- `pkg/prompt/module-prompts/architect.md` — conform to standard contract (`# ROLE / # CONTEXT / # PROCESS / # OUTPUT FORMAT / # RULES / # INPUT`)
- `pkg/prompt/module-prompts/business-analyst.md` — same conformance pass
- `cmd/colony/root.go` (or equivalent root) — register the new `mission` cobra command
- `go.mod` / `go.sum` — add `modernc.org/sqlite`, `gopkg.in/yaml.v3`

DO NOT TOUCH:
- Any existing non-mission CLI command code paths
- Other module prompts beyond `architect.md` / `business-analyst.md`
- Any unrelated package under `pkg/` not listed above
- `.env*`, secrets, credential files

---

## 3. Done Criteria

1. `go build ./...` succeeds with zero warnings.
2. `go test ./...` passes 100%.
3. `colony mission run --mission examples/feature-design.mission.yaml` executes end-to-end and prints the final `output` markdown to stdout.
4. After a run, `.colony/missions.db` exists and contains exactly one row in `sessions` with `status = 'completed'` and `finished_at` populated.
5. For the example mission, `steps` table has rows for every executed agent in order; fan-out steps share consecutive `step_num` values; cyclic re-entries record `sub_step` = `"b"`, `"c"`, etc.
6. `colony mission audit` lists the session; `--session <id>` prints all steps in order with agent_id, role, decision, duration_ms.
7. `--decision REJECTED` returns only steps whose `decision` column equals `REJECTED`.
8. Cycle guard: a mission whose `reviewer` always REJECTs terminates after `max_cycles` iterations and exits with non-zero status and `status = 'failed'` in `sessions`.
9. Fan-in node does not fire until every `from` source has produced an `Output` (asserted by test using deliberately staggered fake nodes).
10. Loading a mission with a role unknown to the registry fails with a clear error before any agent runs.
11. Every JSON envelope returned by an agent that fails to parse causes the step to be recorded with the raw text in `output_json` and the run to fail with status `failed`.
12. `architect.md`, `business-analyst.md`, `project-manager.md` all contain the six required sections in the specified order.
13. **Key validation — missing key:** running any LLM-backed node when the required env var is unset prints `missing API key for provider "<provider>": <KEY> is not set\n  Fix: export <KEY>=<your-key>` and exits non-zero — no LLM call is attempted.
14. **Key validation — unknown provider:** a config with `provider: "foo"` and no `api_key_env` returns an error before running.
15. `colony init` generates a config with `provider: "anthropic"`, `model: "claude-sonnet-4-6"`, and **no** `api_key_env` field.
16. `LLMConfig.KeyEnvName()` returns `ANTHROPIC_API_KEY` / `OPENROUTER_API_KEY` / `OPENAI_API_KEY` for the matching provider and `""` for unknown providers.

---

## 4. Explicit Decisions (do not infer these)

- Language: Go. CLI framework: `cobra` only. No Eino, no Docker, no orchestration framework.
- Concurrency: plain `sync.WaitGroup` + channels. No worker pools, no errgroup unless trivially swapped in.
- SQLite driver: `modernc.org/sqlite` (pure Go, no CGO). Do not use `mattn/go-sqlite3`.
- DB path: `.colony/missions.db` relative to the working directory. Create `.colony/` if missing. `COLONY_DB_PATH` env var overrides this path at runtime.
- Session ID format: `"<mission-name>-<YYYYMMDD-HHMMSS>"` in local time.
- Agent envelope schema is fixed: `{ "decision": "APPROVED|REJECTED|REPROCESS", "feedback": string, "output": string }`. No additional fields.
- Routing: `decision` drives `on_approve` / `on_reject` edges; `REPROCESS` follows `on_reject`.
- Mission YAML uses `__input__` and `__output__` as reserved node ids; do not allow agents to use these as their `id`.
- `Node` and `Runner` are interfaces from day one. Default implementation is direct in-process call; plugin loading is OUT of scope.
- Agent self-registration via `init()` in each role's package/file; no central role list to edit.
- `mission init` is in scope as a stub command that prints "not yet implemented" and exits 0 — interactive AI builder is deferred. Do not attempt to implement the interactive builder.
- Module prompt files are loaded via `embed.FS`.
- Do not add a logger dependency; use stdlib `log/slog`.
- No retries on LLM calls in this iteration — one shot, fail loud.
- **Config is the source of truth for provider and model. `COLONY_MODEL` env var does not exist and must not be introduced.** Model is set in `.colony/config.json` under `llm.model` (or per-role under `roles.<role>.model`).
- **API key resolution order:** (1) `api_key_env` field in config if present, (2) default for the provider (`ANTHROPIC_API_KEY` / `OPENROUTER_API_KEY` / `OPENAI_API_KEY`). No hardcoded fallback values.
- **`api_key_env` is optional.** `colony init` must not emit it. Users who need a non-standard env var name add it manually.
- Default model for `colony init` is `claude-sonnet-4-6`.
- `ValidateKey()` is called on the `LLMConfig` at the start of every `RunHeadless`, `RunAgent`, and `RunInteractive` call in `pkg/llm/exec.go`. Same applies to `llm_node.go` in the mission package — call `ValidateKey()` before dialing the LLM.

---

## 5. Environment Variables

| Variable | Required | Purpose |
|---|---|---|
| `ANTHROPIC_API_KEY` | When `provider: "anthropic"` | Anthropic API key — derived automatically, no config field needed |
| `OPENROUTER_API_KEY` | When `provider: "openrouter"` | OpenRouter API key — derived automatically |
| `OPENAI_API_KEY` | When `provider: "openai"` | OpenAI API key — derived automatically |
| `COLONY_DB_PATH` | No | Override default `.colony/missions.db` location |

**There is no `COLONY_MODEL` variable.** Set the model in `.colony/config.json`.

If the required key for the configured provider is not set, the tool exits with:
```
missing API key for provider "<provider>": <KEY> is not set
  Fix: export <KEY>=<your-key>
```

---

## 6. Tests to Write

- `config_test.go` (additions to existing file)
  - `KeyEnvName()` returns `ANTHROPIC_API_KEY` for `provider: "anthropic"`.
  - `KeyEnvName()` returns `OPENROUTER_API_KEY` for `provider: "openrouter"`.
  - `KeyEnvName()` returns `OPENAI_API_KEY` for `provider: "openai"`.
  - `KeyEnvName()` returns `""` for an unknown provider with no `api_key_env`.
  - `api_key_env` override: `KeyEnvName()` returns the overridden name regardless of provider.
  - `ValidateKey()` returns nil when the env var is set.
  - `ValidateKey()` returns a descriptive error when the env var is unset.
  - `ValidateKey()` returns a descriptive error for an unknown provider with no `api_key_env`.
  - `Init()` generates config with `model: "claude-sonnet-4-6"` and no `api_key_env` field.

- `mission_test.go`
  - loads the example YAML and produces the expected `Graph` (node count, edge shape).
  - rejects YAML using `__input__` / `__output__` as agent ids.
  - rejects missing roles in registry.

- `runner_test.go` (uses fake `Node` implementations, no LLM)
  - linear A→B→C: outputs flow in order, 3 steps recorded.
  - fan-out A→[B,C]: B and C run concurrently (assert via timing or barrier channel).
  - fan-in [B,C]→D: D does not start until both B and C return.
  - cyclic with `max_cycles: 3`: REJECT loop terminates with failure after exactly 3 iterations and records `sub_step` `b`, `c`.
  - APPROVE path on a cyclic edge skips back-edge and proceeds to `__output__`.
  - malformed envelope from a node → run fails, step persisted with raw text.

- `sqlite_test.go`
  - schema migration is idempotent (run twice, no error).
  - `InsertSession` + `UpdateSession` round-trip.
  - audit queries: by mission name, by session id, by decision.

---

## 7. Context

Hive is the orchestrated multi-agent layer of `colony`. Users author a `*.mission.yaml` declaring agents (each with an `id` and a `role`), a `flow` graph using `from` / `to` edges (with `on_approve` / `on_reject` for cycles and `max_cycles` as the cycle guard), and an `input` file seeded into the reserved `__input__` node. The runner walks the graph, calling each `Node` with the upstream `Output`(s); `Node`s are LLM-backed agents that load a module prompt from `pkg/prompt/module-prompts/`, inject the input under the `# INPUT` section, and return a fixed JSON envelope (`decision` / `feedback` / `output`). Routing is decision-driven: `APPROVED` follows `on_approve` (or default `to`), `REJECTED` / `REPROCESS` follows `on_reject`, and cycles are bounded by `max_cycles`.

Every step is persisted to a single SQLite file at `.colony/missions.db` (`sessions` + `steps` tables) using `modernc.org/sqlite` so `colony mission audit` can answer "what did agent X decide on run Y?" without external infra. Concurrency uses plain Go (`sync.WaitGroup` for fan-out/fan-in, channels for collection) — no Eino, Docker, or orchestration framework. `Node` and `Runner` are defined as interfaces from day one so a future plugin loader can replace the in-process executor without touching callers; plugin discovery itself is out of scope.

The config layer (`pkg/config/config.go`) owns API key resolution. `LLMConfig` gains `KeyEnvName()` — which derives the correct env var from the provider (`ANTHROPIC_API_KEY`, `OPENROUTER_API_KEY`, `OPENAI_API_KEY`) unless `api_key_env` overrides it — and `ValidateKey()`, which checks the env var is set and returns a human-readable `export KEY=<value>` error if not. Every call site that dials an LLM (`RunHeadless`, `RunAgent`, `RunInteractive` in `pkg/llm/exec.go`, and `llm_node.go` in the mission package) calls `ValidateKey()` first, so the failure is immediate and actionable rather than buried in a CLI subprocess error. The model is always read from `.colony/config.json`; there is no `COLONY_MODEL` env override.

Three module prompts must conform to the standard six-section contract (`# ROLE`, `# CONTEXT`, `# PROCESS`, `# OUTPUT FORMAT`, `# RULES`, `# INPUT`): `architect.md` and `business-analyst.md` already exist and need to be aligned; `project-manager.md` is new. `mission init` is stubbed only — the interactive AI-assisted builder is deferred. Quality gates per project CLAUDE.md apply: lint clean, types clean (Go vet), tests 100% green before commit.
