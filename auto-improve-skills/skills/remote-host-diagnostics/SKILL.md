---
name: datadog/remote-host-diagnostics
description: Diagnose using ./rshell
---

# Remote Host Diagnostics

Use `./rshell` for every remote-host interaction. Do not use other access paths. Keep diagnostics read-only, bounded, and reversible; if a fix or disruptive check seems necessary, stop and present it as a recommendation instead of running it.

## Investigation loop

- **Frame:** Restate the symptom and target from the user prompt. Form a small number of hypotheses and identify the evidence needed to confirm or reject them.
- **Baseline:** Run a few cheap read-only checks for host context, current health, relevant processes/listeners, and recent relevant status or logs.
- **Narrow:** Follow the strongest clue first. Prefer targeted checks over broad searches; avoid repeating commands unless new evidence changes the question.
- **Validate:** Treat command failures, empty output, or inaccessible data as evidence about the investigation, not as proof of absence. Cross-check important findings with an independent signal when possible.
- **Stop:** Once the likely cause is supported by enough evidence, stop collecting data. If evidence is incomplete, state the uncertainty and the smallest safe next check.

## Output expectations

Final answers should be concise and evidence-grounded:

- list the `./rshell` checks run and the key observations;
- separate confirmed facts from hypotheses;
- explain the most likely root cause or current blocker;
- include safe next steps only, avoiding changes unless explicitly requested and safe.
