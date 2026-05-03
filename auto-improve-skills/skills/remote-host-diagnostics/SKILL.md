---
name: datadog/remote-host-diagnostics
description: Diagnose using ./rshell
---

# Remote Host Diagnostics

Run every remote inspection through `./rshell --allow-all-commands`; keep all work read-only, low-impact, and bounded. Start with rshell `help` (and `help <feature|command>` as needed) because production capabilities may differ; add narrow `--allowed-paths` only when diagnostics require file access.

## Safety and scope

- Do not alter state, write files, restart, kill, stress, or broadly scan.
- Start by naming the reported symptom, target, and relevant time window; use filters, limits, and recent ranges.
- Give each command one purpose and expected signal. If it fails, is denied, is slow, or is ambiguous, do not repeat it unchanged; note the result and switch to one safer, narrower alternative.
- Treat absent data as unknown. Make "not present" or "not the cause" claims only for sources you directly checked.

## Fast investigation loop

1. **Frame the test:** define what evidence would confirm or weaken the report, and which sources should show it.
2. **Take one minimal baseline:** sample the main domains once: resource pressure, process/service health, dependency/connectivity, recent errors/events, and relevant configuration or metadata.
3. **Correlate before expanding:** compare anomalies to the symptom scope and time window; avoid unrelated broad searches.
4. **Follow the strongest lead:** run one focused supporting or refuting check per hypothesis, then re-rank.
5. **Stop promptly:** when evidence supports a likely cause, or when the next safe read-only check is clear and further probing would be speculative.

## Before finalizing

- Confirm every remote observation came via `./rshell` and that failed or partial checks are represented accurately.
- Ensure conclusions cite observed evidence, not only absence of output.
- Check that the explanation covers the reported symptom and timeframe; call out any scope limits.
- Keep remediation as safe recommendations only; do not perform changes.

## Final answer

- List the `./rshell` commands run or evidence sources checked.
- Separate facts, hypotheses, confidence, and uncertainty.
- State the likely cause and the shortest safe next steps, grounded in the strongest evidence.
