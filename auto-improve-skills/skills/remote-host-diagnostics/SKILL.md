---
name: datadog/remote-host-diagnostics
description: Diagnose using ./rshell
---

# Remote Host Diagnostics

Use `./rshell` for all remote diagnostics.

- Start with a quick checklist before deep dives: confirm the symptom and timeframe, identify the affected component, sample host/service health, recent logs, relevant configuration, and resource signals, then pursue the strongest lead.
- Stay safe and bounded: use read-only checks, avoid restarts/writes/destructive probes, and stop collecting once evidence is sufficient.
- If a check fails, note the failure and adjust with a narrower alternative instead of repeating blindly.
- Final answer: list commands run, key evidence, likely cause, and safe next steps; only rule out or call something absent if directly verified, otherwise state the uncertainty.

