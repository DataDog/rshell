---
name: datadog/remote-host-diagnostics
description: Diagnose remote hosts safely using ./rshell
---

# Remote Host Diagnostics

Use only `./rshell`; keep probes read-only, targeted, and bounded. Do not mutate host state (services, packages, files, permissions, users, network settings, credentials) or run broad scans; prefer narrow queries with explicit scope and output limits when available.

## Workflow
- Start with `./rshell help` (and builtin help when flags matter); use documented flags, not assumed full-system variants.
- If the prompt provides files or directories to inspect, pass an explicit allowed-path scope for those roots on every `./rshell` command that touches them.
- Plan a short read-only checklist up front: confirm safety/scope and target, clarify symptoms and timeframe, take one cheap overview, sample the relevant layers (health/resources, process/service/listener state, configuration, recent logs/events), then test the leading hypothesis; if a probe fails or returns no data, use help/error text to choose a narrower documented alternative and note the limitation.
- Before each probe, know the expected signal; bound output by path, time, unit, or row limit. Afterward verify success, target/scope, and completeness before using the result; avoid rerunning equivalent checks unless new evidence changes the question.
- After each major result, update the leading hypotheses; cross-check conclusions with command output, rule out only what direct evidence supports, and stop once evidence is sufficient.

## Final answer
- Lead with a concise diagnosis: likely finding/root cause, impact, confidence, and the main remaining uncertainty.
- List the commands run and key evidence, redacting secrets or sensitive values.
- Separate observed facts from hypotheses, and bound any negative claims to what was checked.
- Suggest safe next read-only checks or remediation handoff steps when appropriate.
