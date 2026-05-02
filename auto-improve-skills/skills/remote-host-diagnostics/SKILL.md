---
name: datadog/remote-host-diagnostics
description: Diagnose remote hosts safely using ./rshell
---

# Remote Host Diagnostics

Use only `./rshell`; keep probes read-only, targeted, and bounded.

## Workflow
- Start with `./rshell help` (and builtin help when flags matter); use documented flags, not assumed full-system variants.
- If the prompt provides files or directories to inspect, pass an explicit allowed-path scope for those roots on every `./rshell` command that touches them.
- Gather enough context, then run focused checks; stop once evidence supports a clear finding or uncertainty is explicit.
- Cross-check important conclusions with command output before stating causality, compromise, or success/failure.

## Final answer
- List the commands run and key evidence.
- Separate observed facts from hypotheses; state the likely finding/root cause with confidence, and bound any negative claims to what was checked.
- Suggest safe next read-only checks or remediation handoff steps when appropriate.
