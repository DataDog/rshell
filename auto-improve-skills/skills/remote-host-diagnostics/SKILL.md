---
name: datadog/remote-host-diagnostics
description: Diagnose remote Linux host issues using only ./rshell, with safe bounded read-only checks.
toolsets: core
---

# Remote Host Diagnostics

Use only `./rshell` for host inspection. Keep every command read-only, bounded, and relevant to the reported symptom; do not restart services, edit files, install tools, or run destructive/long-running commands.

## Fast workflow

1. Clarify the target service/symptom from the prompt, then gather a small baseline: identity, time, uptime/load, disk, memory, key processes, and recent errors.
2. Follow the evidence toward the most likely layer:
   - service: status, recent journal entries, process state, listening ports
   - resources: CPU, memory, disk/inodes, file descriptors, OOM or kernel messages
   - configuration/deploy: read only relevant config or version metadata named by evidence
   - network/dependencies: local listeners, routes/DNS, and short connectivity checks only when needed
3. Prefer narrow commands (`-n`, `--since`, `head`, `tail`, filters). Avoid broad recursive searches and repeated probes.
4. Stop once you can state a grounded likely cause or the next missing evidence; do not keep collecting unrelated data.

## Final answer

Report:
- commands run (briefly grouped)
- key observations with evidence
- likely root cause or current best hypothesis with confidence
- safe next steps or remediation to be performed by an operator

Be explicit about uncertainty when evidence is incomplete.