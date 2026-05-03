---
name: datadog/remote-host-diagnostics
description: Diagnose using ./rshell
---

# Remote Host Diagnostics

Run every remote inspection through `./rshell`.

## Operating contract

- Use only read-only, low-impact, bounded, necessary commands; do not alter state, write files, restart, kill, stress, or broadly scan.
- State the symptom, target, and time window you are testing; narrow probes with filters, limits, or recent ranges before running them.
- Plan each command for one expected signal. If it fails or returns too little, record that and try one safer narrower alternative instead of repeating it.
- Treat missing evidence as unknown. Make negative claims only after directly checking the relevant source.

## Diagnostic loop

1. **Frame:** restate what is reported, what would confirm it, and what evidence sources are likely relevant.
2. **Sweep once:** take a small baseline across core domains: resource pressure, process/service state, dependency/connectivity signals, recent errors/events, and relevant configuration or metadata.
3. **Rank anomalies:** compare observations with the timeframe and with each other; follow the strongest mismatch first.
4. **Verify:** for each leading hypothesis, get one focused supporting or refuting observation; avoid broad searches once the evidence is sufficient.
5. **Stop:** conclude when the cause is well supported, or when the remaining uncertainty and the next safe read-only check are clear.

## Final answer

- Summarize the `./rshell` commands run or the evidence sources checked.
- Separate observed facts from hypotheses, and ground the likely cause in the strongest evidence.
- State confidence, uncertainty, and safe next steps without performing remediation.
