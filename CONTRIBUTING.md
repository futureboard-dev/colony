# Contributing to Colony

Thanks for your interest in contributing! This guide covers how to get set up
and what we expect from changes.

## Prerequisites

- Go 1.25+ (see `go.mod`)
- `golangci-lint`
- For running the agent: the `claude` CLI (Anthropic) or `crush` CLI
  (`brew install charmbracelet/tap/crush`) for other providers

## Getting Started

```bash
git clone https://github.com/futureboard-dev/colony
cd colony
go build ./...
go test ./...
```

## Development Workflow

1. Fork the repo and create a branch off `main`.
2. Make your change, keeping it focused — one concern per pull request.
3. Ensure all quality gates pass (see below).
4. Open a pull request describing what changed and why.

## Quality Gates

All of these must pass before a PR is merged:

```bash
go fmt ./...            # no diffs
go vet ./...            # zero findings
golangci-lint run ./... # zero issues
go test ./...           # 100% pass
```

## Code Standards

- Match the style of the surrounding code.
- Exported functions and types need doc comments.
- Wrap errors with context: `fmt.Errorf("failed to X: %w", err)`.
- Every new function should have at least one test covering success and error
  cases. Don't mock what can be tested for real.
- Keep changes minimal — no speculative features or unrelated refactors.

## Reporting Bugs

Open an issue with a clear description, reproduction steps, and your environment
(OS, Go version, Colony version). For security issues, see [SECURITY.md](SECURITY.md).
