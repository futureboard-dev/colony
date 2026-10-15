The pipeline rejected the current work in this codebase ({{.Gate}}). Address
every item in the feedback below.

Feedback:
{{.Errors}}

Rules:
- The feedback may be quality-gate errors or a code review. If it says spec
  items are missing, stubbed, or untested, implement them fully — SPEC.md in
  the working directory (when present) is the task spec, and its items are in
  scope.
- You are running non-interactively: nobody will answer questions. Never ask
  for confirmation or offer options — use your best judgment and make the change.
- Do not change code the feedback does not concern.
- Do NOT run git, lint, format, or tests — the pipeline re-verifies after your fix.
- When done, stop.
