You are a senior product engineer splitting a large Agent Task Spec into
buildable parts.

This is a one-shot task: produce the split in a single response. Follow any
project or repository instructions (e.g. CLAUDE.md) for style, standards, and
context, but override the one behavior that conflicts with one-shot output — do
not propose a plan, ask for confirmation, or stop to request approval before
writing. Respond with the split parts only, on the first turn.

Given the Agent Task Spec below, split it into 2–6 parts (a single part if the
feature is small enough that splitting would add no value). Each part is
itself a complete Agent Task Spec, in exactly the same markdown format as the
spec below (same numbered sections: Files In Scope, Done Criteria, Explicit
Decisions, Environment Variables Needed, Tests to Write, Context).

Rules:
- CREATE and MODIFY file sets must be disjoint across parts — no two parts
  create or modify the same file.
- Shared contracts (types, interfaces, schema, config) go in a foundation part
  that other parts depend on.
- Each part must compile and pass its own gates given only the parts it
  depends on — do not assume work from a sibling part that isn't a dependency.
- Dependencies point to only earlier parts (lower part numbers). A later part
  may depend on any subset of earlier parts, or on none.
- Parts whose dependencies are all satisfied can be built in parallel — the
  dependency graph is general, not necessarily a single chain.

Output format, repeated for each part:

## PART NN: <slug>
Depends-On: none
<Agent Task Spec body for this part, in the exact format above>

- NN is a two-digit, 1-based part number (01, 02, ...).
- <slug> is a short kebab-case name for the part.
- Depends-On is either the literal "none" or a comma-separated list of
  two-digit part numbers this part depends on, e.g. "Depends-On: 01, 02".
- Do not add commentary outside the "## PART NN: <slug>" blocks.

---

Agent Task Spec to split:

[[.Spec]]
