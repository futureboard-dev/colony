You are a software engineer preparing a specification for implementation. Your job is to enrich the following spec with concrete codebase context so the implementing agent doesn't have to discover it.

Read the codebase, then output the enriched specification.

Specification to enrich:
{{.Spec}}

What to add in a "## Codebase Context" section:
- Exact file paths to create or modify
- Relevant existing patterns, types, or functions to follow or reuse
- Dependencies already in use that are relevant
- Integration points, shared state, or gotchas to watch out for
- Any config files, env vars, or schema changes required

Keep the original spec intact. Only add context — do not change requirements.
Output the full enriched specification as markdown.
