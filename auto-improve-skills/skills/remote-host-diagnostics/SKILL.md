---
name: datadog/remote-host-diagnostics
description: Use when diagnosing remote host, service, network, or system issues through this repository's rshell. Guides safe, bounded, read-only diagnostics with ./rshell, explicit allowed paths, help-based capability discovery, and evidence-grounded final answers.
---

# Remote Host Diagnostics

Use this skill for remote host, service, log, socket, or system diagnostics that should be performed through this repository's `./rshell`.

## Hard Rules

- Run diagnostics only with `./rshell --allow-all-commands -c "<shell command/script>"`.
- Keep commands read-only, bounded, and narrow. Do not write files, install packages, mutate services, restart processes, or run broad repetitive scans.
- If a diagnostic needs file access, pass an explicit narrow allowlist with `--allowed-paths=/literal/root` or `--allowed-paths=/root1,/root2`. Use literal roots in commands so the transcript proves the sandbox boundary.
- Use `help` inside rshell before assuming support for a command, feature, or flag. Production rshell deployments may restrict, omit, or extend capabilities; `help` in the target environment is the source of truth.
- Do not claim you connected to, SSHed into, or accessed a real remote/customer host unless the user explicitly provided such evidence. Usually you are inspecting local fixture or mounted logs through rshell.

## Efficient Evidence Loop

Aim to answer most log investigations in five to eight rshell invocations. Stop once the finding, impact, competing-hypothesis status, and remaining uncertainty are supported.

1. Restate the symptom, target service or host, relevant time window, prompt-provided roots, and teammate hypothesis.
2. Run one capability check, such as `help` plus only the command topics you expect to use. Do not spend time probing external tools that top-level `help` does not list.
3. Inventory each prompt-provided root once with bounded depth and sorted output. If a primary root is empty and a host-mounted or fallback root is provided, inspect both, use explicit allowlists for both, and say so in the final answer.
4. Choose candidate files from names and symptoms: current log for the affected component, one rotated or noise file for the competing hypothesis, and one independent layer when available. Avoid repeated all-file sweeps.
5. Triage candidates with bounded filters before opening context: `grep -Hn -m`, `grep -Hc`, `wc -l`, `head`, `tail`, and small `sed -n` windows around already-found lines. Do not use `grep -R` or `find ... -exec grep`.
6. Correlate cause to consequence across time, component, and source. Capture exact message fragments, counts/statuses, stable identifiers, and whether the condition recovered or is still unsupported.
7. Test the teammate hypothesis with one targeted query against the relevant current and historical files. Classify it as current cause, historical/recovered, different source, unrelated noise, or unsupported.

## Command Shape

- Prefer combining related read-only checks for the same allowlisted root in one rshell script. One command can inventory, count, and show bounded context if outputs stay small.
- Keep file lists explicit after inventory. If there are many files, narrow by filename first instead of grepping every file repeatedly.
- Use patterns derived from the prompt and evidence classes: timestamps, route or check names, status classes, failure verbs, authentication phrases, certificate words, transaction/request IDs, and recovery/success words.
- When a command or flag fails, recover quickly: read `help <command>`, run the supported subset, and record the limitation.

## Domain Checks

- Authentication anomalies: count failures by source, quote the failure and success event phrases exactly from the log, check accepted/success events for the same source, and distinguish different-source successes from compromise.
- HTTP/service outages: tie user-visible status or route failures to backend/service evidence and one independent layer such as proxy, app, system, dependency, or agent logs. Reject older or recovered decoys by timestamp and source.
- Agent/check failures: connect configuration, credential, certificate, network, or dependency errors to the affected check/service behavior. Include downstream effects and separate healthy sibling paths from the failing one.
- Containerized layouts: if the primary in-container log path is empty and host logs are mounted elsewhere, explicitly inventory both and base the finding on the host-mounted evidence.
- Socket checks: run `help ss` first. If process/PID flags are absent, do not rely on them; collect listening TCP address/port data with supported flags such as the help-advertised TCP, listening, and numeric options. If the supported query fails because of platform or permission limits, report that limitation instead of trying unrelated tools.

## Final Answer Contract

Use this concise structure:

- `Commands run`: exact commands or a faithful compact summary, including literal `--allowed-paths` roots, key files queried, and any unsupported-command evidence.
- `Finding`: one sentence naming the likely cause and affected service/check/traffic/source.
- `Evidence`: cite concrete file names, absolute date/time windows, exact message fragments, identifiers or counts/statuses, and downstream symptoms. Include date and time together when available.
- `Not supported`: explicitly dispose of misleading hypotheses, historical rotated-log matches, recovered noise, different-source successes, or unavailable command capabilities.
- `Uncertainty / next checks`: state what is not proven and suggest only safe read-only validation, audit, config/source review, rollback planning, owner follow-up, or capability follow-up. Do not propose remediation commands.

Before finalizing, check that the answer names the source file or command output for every important claim, includes the raw wording that proves the diagnosis, avoids real-host access claims, and does not keep investigating after enough evidence is collected.
