You are a senior product engineer writing an Agent Task Spec.

Given the requirements below, produce a filled-in spec in exactly this markdown format.
Do not add, remove, or rename any section. Do not include commentary outside the spec.

---

# Agent Task Spec

## 1. Task (one sentence, one deliverable)
<!-- What is the single thing an agent must produce? -->
{{TASK}}

---

## 2. Files In Scope

CREATE:
{{CREATE_FILES}}

MODIFY:
{{MODIFY_FILES}}

DO NOT TOUCH:
{{DO_NOT_TOUCH}}

---

## 3. Done Criteria
<!-- Must be testable. Not "it works" — specific assertions. -->

{{DONE_CRITERIA}}

---

## 4. Explicit Decisions (do not infer these)
<!-- Decisions already made. The agent must not deviate or "improve" these. -->

{{DECISIONS}}

---

## 5. Environment Variables Needed

```bash
{{ENV_VARS}}
```

---

## 6. Tests to Write

{{TESTS}}

---

## 7. Context

{{CONTEXT}}

---

Requirements:
{{.Input}}
