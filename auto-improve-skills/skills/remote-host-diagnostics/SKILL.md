---
name: datadog/remote-host-diagnostics
description: Use when diagnosing remote host, service, network, or system issues through this repository's rshell. Guides safe, bounded, read-only diagnostics with ./rshell, explicit allowed paths, help-based capability discovery, and evidence-grounded final answers.
---

# Remote Host Diagnostics

Use this skill for remote host or service diagnostics that should be performed through this repository's `./rshell`.

## Hard Rules

- Run diagnostics only with `./rshell --allow-all-commands -c "<shell command/script>"`.
- Keep commands read-only, bounded, and narrow. Do not write files, install packages, mutate services, or run broad repetitive scans.
- If a diagnostic needs file access, pass an explicit narrow allowlist with `--allowed-paths=/literal/root` or `--allowed-paths=/root1,/root2`. Prefer the literal path in the command over shell variables so the transcript proves the sandbox boundary.
- Use the `help` builtin inside rshell before assuming support for a command, feature, or flag. Production rshell deployments may restrict, omit, or extend capabilities; `help` in the target environment is the source of truth.
- Do not claim you connected to, SSHed into, or accessed a real remote/customer host unless the user explicitly provided such evidence. Usually you are inspecting local fixture or mounted logs through rshell.

## Fast Evidence Workflow

1. Clarify the symptom, target host/service, and any known constraints from the prompt.
2. Run one capability check, for example `./rshell --allow-all-commands -c "help"` or `./rshell --allow-all-commands -c "help grep; help find; help ss"`, selecting only topics you may use.
3. Inventory only the relevant roots. Use bounded depth and sorted output, for example `find /literal/root -maxdepth 3 -type f -print | sort`. If the prompt provides a primary root and a host-mounted or fallback root, inspect both and mention if the primary is empty.
4. Pick a small set of candidate files from the inventory. Do not use `grep -R` or `find ... -exec grep` across the whole root. Prefer explicit file lists plus bounded filters: `grep -Hn -m 80 -E 'error|fail|denied|timeout|refused|status=50|x509|invalid|stopped|recovered' file1 file2 | head -n 120`, `grep -Hc -E 'pattern' file1 file2`, `wc -l file1 file2`, and narrow `sed -n` windows around line numbers found by grep.
5. Correlate, then stop. For each plausible cause collect: absolute timestamp window, file/component, stable identifier if present, exact failure message/status, downstream symptom, and whether it later recovered. Cross-check at least one independent layer when available, such as app plus proxy/system logs, agent plus syslog, or failure lines plus accepted/success lines.
6. Actively test the teammate's or prompt's competing hypothesis. Search just enough to classify it as current, historical/recovered, unrelated, or unsupported; do not let older rotated entries or different sources override current-window evidence.
7. For unsupported commands or flags, recover instead of stopping. Read `help <command>`, run the supported subset, and state the limitation. For socket checks, if help shows process/PID flags are unavailable, collect listening TCP address/port data with a supported query and explicitly say process names/PIDs cannot be obtained through this rshell build.

## Evidence Patterns

- Log-root investigations: start with file inventory, then use counts and max-count grep before opening context. Large outputs slow the investigation and obscure the answer.
- Authentication anomalies: quantify the suspicious source with counts, show the user/auth pattern, separately search accepted/success lines for that same source, and distinguish accepted logins from other sources.
- HTTP or service outages: tie the user-visible error to backend evidence in another layer, then reject unrelated older or recovered errors with timestamps.
- Agent or check failures: connect configuration, credential, certificate, network, or dependency errors to the affected check/agent behavior, and cite recovery or continuing-failure lines when present.
- Containerized layouts: if the primary log path is empty and a host-mounted path is available, inspect both with explicit allowlists and call out the fallback in the final answer.

## Final Answer Contract

Use a concise structure:

- `Commands run`: list the exact rshell commands or a faithful compact summary, including literal `--allowed-paths` roots.
- `Finding`: one sentence naming the likely cause and the affected service/check/traffic.
- `Evidence`: cite concrete file names, absolute timestamps, identifiers, counts/statuses/messages, and the downstream symptom. Include the incident date and time together, not just time-of-day.
- `Not supported`: explicitly dispose of misleading hypotheses, historical rotated-log matches, recovered noise, or different-source successes.
- `Uncertainty / next checks`: say what is not proven and suggest only safe read-only checks, validation, audit, rollback planning, or owner follow-up. Do not propose remediation commands.
