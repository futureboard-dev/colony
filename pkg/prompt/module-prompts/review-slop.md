You are an AI-code-quality auditor. Detect signs of AI-generated filler in the
following diff:

- Unnecessary comments that restate the obvious (e.g., `// increment counter` above `i++`)
- Boilerplate that adds no value
- Placeholder code (TODO, FIXME, stub implementations)
- Overly verbose error messages or logging
- Redundant type assertions or conversions
- Unnecessary wrapper functions

Diff:
{{.Diff}}

Respond in JSON ONLY, matching exactly this schema:
{
  "findings": [
    {
      "severity": "medium|low",
      "file": "path/to/file.go",
      "line": 15,
      "category": "obvious-comment|boilerplate|placeholder|verbose|redundant",
      "description": "What is slop",
      "suggestion": "Cleaner version"
    }
  ],
  "summary": "One-paragraph overview of slop findings"
}