---
name: datadog/remote-host-diagnostics
description: Diagnose using ./rshell
---

# Remote Host Diagnostics

Use `./rshell` for every remote diagnostic command.

## Rules

- Keep checks read-only, low-impact, and bounded; do not restart, modify state, write files, or run destructive probes.
- Validate the target and narrow each command with time windows, filters, or limits before running it to reduce noisy or repeated probes.
- Prefer a few targeted observations over broad searches; stop once the evidence supports a clear finding or the remaining uncertainty is explicit.
- When a command fails or output is missing, record that fact and try one narrower safe alternative rather than repeating the same approach.

## Fast workflow

1. Orient: restate the reported symptom, affected target, and timeframe you are using.
2. Baseline: sample current health, resource pressure, process/service status, recent errors, and relevant configuration/metadata.
3. Correlate: compare observations against the timeframe and each other; follow the strongest anomaly first.
4. Narrow: use focused follow-up checks to confirm the likely cause and to test only necessary alternatives.
5. Conclude: avoid negative claims unless directly checked; otherwise say what remains unknown.

## Final answer

- Include the `./rshell` commands run or a concise summary of them.
- Ground the likely cause in the key observed evidence.
- State confidence, uncertainty, and safe next steps without performing risky remediation.
