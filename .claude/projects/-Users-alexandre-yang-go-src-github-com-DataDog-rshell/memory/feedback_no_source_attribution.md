---
name: No source attribution in tests
description: Do not include source attribution comments (e.g. "Derived from...") in test files
type: feedback
---

Do not mention the source/reference suite in test files — no "Derived from" comments.

**Why:** User explicitly asked not to mention the source.

**How to apply:** When writing YAML scenario tests, omit the `# Derived from ...` comment at the top of each file.
