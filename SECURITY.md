# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in Colony, please report it privately.
**Do not open a public issue for security problems.**

Use GitHub's private vulnerability reporting:
[Report a vulnerability](https://github.com/futureboard-dev/colony/security/advisories/new)

Please include:

- A description of the vulnerability and its impact
- Steps to reproduce
- Affected version or commit

We aim to acknowledge reports within 5 business days and will keep you informed
as we work on a fix.

## Scope

Colony is an agentic engineering toolkit that executes shell commands, git
operations, and delegates to external agent CLIs (`claude`, `crush`). When
reporting, note that command execution against a workspace is the tool's intended
behavior — the security boundary is the workspace and the environment it runs in.
Reports about the tool running commands it was instructed to run are out of scope.

## Supported Versions

Only the latest release receives security updates while the project is pre-1.0.
