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

## Default Run Plan

Most investigations should finish in three rshell invocations, plus one extra only when a fallback root, socket limitation, or missing independent layer requires it.

1. **Capabilities and map**: in one command, run `help` plus only the topics you expect to use, then inventory every prompt-provided root once with bounded `find`/`ls` output. Include each root literally in `--allowed-paths`; if a primary root is empty and a mounted/fallback root exists, inspect both and keep both in the final command summary.
2. **Focused triage**: after inventory, pick the current log for the likely component, one historical/noise file for the named hypothesis, and one independent layer if available. In one script, get `wc -l` plus bounded `grep -Hn -E -m` or `grep -Hc` results for those explicit files. Prefer narrow symptom/time/status/source words over broad "error|warn" sweeps.
3. **Confirmation**: run one final targeted query for decisive tokens already found: trigger, downstream impact, current/recovery state, and the teammate hypothesis. Use small `sed -n` windows only around known line numbers; avoid large context dumps.

Stop when you have: the likely cause line, a consequence line, one counter-hypothesis disposition, and the remaining uncertainty. More searches usually reduce quality by adding noise.

## Command Shape

- Prefer combining related read-only checks for the same allowlisted root in one rshell script. One command can inventory, count, and show bounded context if outputs stay small.
- Keep file lists explicit after inventory. If there are many files, narrow by filename first instead of grepping every file repeatedly.
- For high-volume logs, count first and then print only representative matches with `-m` or a narrow time/status filter. Avoid dumping every request in a window when a count plus a few samples proves the point.
- Use patterns derived from the prompt and evidence classes: timestamps, route or check names, status classes, failure verbs, authentication phrases, certificate words, transaction/request IDs, actor/source fields, and recovery/success words.
- When a command or flag fails, recover quickly: read `help <command>`, run one supported subset, and record the limitation. In the final answer, mention only commands actually run and outputs actually observed.
- If a command uses shell variables for shorter scripts, still put the exact prompt root in `--allowed-paths` and the exact files/roots in the final answer. If quoting or expansion breaks a query, rerun a corrected bounded query rather than reasoning from partial output.
- Never use `grep -R` or `find ... -exec grep`. Do not keep repeating all-file scans after candidate files are known.

## Evidence Ledger

For every important claim, preserve the raw fields that make it auditable: file name, line number if available, absolute timestamp/window, component/check/route/source, status/code, count, request/transaction/session/key ID, config/certificate field, actor/client field, and "since", "recovered", or success state. Quote short decisive lines exactly; for long lines, copy the exact tokens that distinguish the current event from historical or unrelated decoys.

## Domain Checks

- Authentication anomalies: count failures by source, include the numeric count from command output, quote failure phrases, check accepted/success events for the same source, and separately cite any successes from different sources. Do not infer compromise from failures alone.
- HTTP/service outages: tie route/status evidence to backend/service evidence and one independent layer such as proxy, system, dependency, or agent logs. Identify the limiting resource and actor/client if logs expose one; do not stop at generic 5xx. Explicitly dispose of named older, recovered, or unrelated decoys by timestamp/source/status.
- Agent/check failures: connect configuration, credential, certificate/timing, network, or dependency errors to the affected check/service behavior. Preserve decisive structured fields from the causal line and downstream effect line. Separate healthy sibling paths from the failing one, and distinguish bad material from environment timing when certificate evidence is involved.
- Containerized layouts: if the primary in-container log path is empty and host logs are mounted elsewhere, explicitly inventory both and base the finding on the host-mounted evidence.
- Socket checks: run `help ss` first, then run a help-advertised listening TCP query using supported TCP, listening, numeric, and optional no-header flags. If process/PID flags are absent, say process names/PIDs are unavailable and do not claim them. If the supported query fails because of platform or permission limits, report that limitation; do not invent socket rows from an attempted command.

## Final Answer Contract

Use this concise structure. Keep it short, but do not compress away the fields that prove the diagnosis.

- `Commands run`: exact commands or a faithful compact summary, including literal `--allowed-paths` roots, key files queried, and any unsupported-command evidence. Do not list probes you considered but did not run.
- `Finding`: one sentence naming the likely cause and affected service/check/traffic/source.
- `Evidence`: cite concrete file names, absolute date/time windows, exact message fragments, identifiers, line/config/certificate fields, counts/statuses, and downstream symptoms. Include date and time together when available, and preserve embedded "since" or recovery timestamps when they prove duration or recovery.
- `Not supported`: explicitly dispose of misleading hypotheses named by the user, historical rotated-log matches, recovered noise, different-source successes, missing same-source successes, or unavailable command capabilities.
- `Uncertainty / next checks`: state what is not proven and suggest only safe read-only validation, audit, config/source review, rollback planning, owner follow-up, or capability follow-up. Do not propose remediation commands.

Before finalizing, verify:

- The cause, consequence, and counter-hypothesis each have a source file or command output.
- The final answer includes exact raw wording for the decisive event and exact numeric counts when counts matter.
- Any historical/recovered/different-source evidence is labeled that way, not left implicit.
- You avoid real-host access claims, remediation commands, and unsupported process/PID/socket claims.
