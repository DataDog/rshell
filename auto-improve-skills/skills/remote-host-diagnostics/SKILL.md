---
name: datadog/remote-host-diagnostics
description: Diagnose using ./rshell
---

# Remote Host Diagnostics

Use only `./rshell`. Keep all actions read-only, non-destructive, and bounded.

## Workflow

1. Confirm the symptom and scope from the prompt before exploring.
2. Run a compact first-pass checklist before deep-diving: host identity/uptime, CPU/memory/disk/load pressure, relevant services/processes, recent errors, and dependency/connectivity signals. Prefer standard read-only commands; if one fails, note the limit and use the nearest safe alternative rather than retrying.
3. Follow the strongest clue with one or two targeted checks; stop once evidence supports a clear conclusion or further access is needed.
4. Do not repeat noisy commands or broad searches. Do not rule anything out unless the collected output supports it.

## Final Answer

Be concise and grounded in observed command output. Separate:
- **Observations**: key evidence and commands run.
- **Likely cause / hypothesis**: confidence and uncertainty.
- **Next steps**: safe validation or remediation for the operator.
