---
name: datadog/remote-host-diagnostics
description: Diagnose remote Linux host issues using only ./rshell, with safe bounded read-only checks.
toolsets: core
---

# Remote Host Diagnostics

Use only `./rshell` for host inspection. Keep every command read-only, bounded, and relevant to the reported symptom; do not restart services, edit files, install tools, or run destructive/long-running commands.

## Fast workflow

1. Clarify the target service/symptom and failure window from the prompt, then gather a small labeled baseline in as few `./rshell` calls as practical: identity, host time, uptime/load, disk/inodes, memory, key processes/listeners, and recent system errors.
2. Follow the evidence toward the most likely layer:
   - service: unit/status, recent scoped journal entries, process state, listening ports
   - resources: CPU, memory, disk/inodes, file descriptors, OOM or kernel messages
   - configuration/deploy: read only relevant config, environment, or version metadata named by evidence
   - network/dependencies: local listeners, routes/DNS, and short connectivity checks only when needed
3. Prefer narrow, time-bounded commands (`-n`, `--since`, `head`, `tail`, filters, `timeout`). Avoid broad recursive searches and repeated probes.
4. Correlate observations by timestamp and service owner; if the service is unclear, identify likely units/processes from listeners, process tree, or logs before reading configs.
5. Stop once you can state a grounded likely cause or the next missing evidence; do not keep collecting unrelated data.

## Final answer

Report:
- commands run (briefly grouped)
- key observations with evidence, including relevant timestamps/log lines
- likely root cause or current best hypothesis with confidence and any important uncertainty
- safe next steps or remediation to be performed by an operator

Be explicit about uncertainty when evidence is incomplete.