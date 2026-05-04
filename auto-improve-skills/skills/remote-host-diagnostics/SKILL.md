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

Use two or three rshell invocations; add a fourth only for an empty-root fallback, socket limitation, or missing independent layer.

1. **Capabilities and map**: run `help` plus only expected command topics, then inventory every prompt-provided root once with bounded `find`/`ls`. Include each root literally in `--allowed-paths`; if a primary root is empty and a mounted/fallback root exists, inspect both in the same command and say which root produced evidence.
2. **Focused triage**: choose the current component log, one rotated/noise file for the named hypothesis, and one independent layer when available. In one script, collect `wc -l`, counts, and a few representative matches. Prefer anchored symptom/time/status/source patterns over bare timestamp windows or broad `error|warn` sweeps.
3. **Confirmation**: query only decisive tokens already found: cause, downstream impact, current/recovery state, and the teammate hypothesis. Use small `sed -n` windows around known line numbers only.

Stop when you have the cause line, consequence line, top alternative-hypothesis disposition, current/recovery state, and remaining uncertainty. More searches usually add noise.

## Command Shape

- Combine related read-only checks for the same allowlisted root in one rshell script when output stays small.
- Keep file lists explicit after inventory. With many files, narrow by filename first, then query only candidate current, rotated/noise, and independent-layer files.
- For high-volume logs, aggregate before sampling: use `grep -Hc`, `wc -l`, or `grep ... | sort | uniq -c` to find scale/top sources, then print representative matches with `-m`, `head`, or `tail`. Avoid dumping every request in a window.
- Use prompt/evidence patterns: timestamps, route/check names, status classes, failure verbs, auth phrases, certificate words, transaction/request IDs, actor/source fields, and recovery/success words. Give each named alternate hypothesis one narrow check; stop after it is disposed.
- When a command or flag fails, read `help <command>`, run one supported subset, and record the limitation. In the final answer, mention only commands actually run and outputs actually observed.
- If a command uses shell variables for shorter scripts, still put the exact prompt root in `--allowed-paths` and the exact files/roots in the final answer. If quoting or expansion breaks a query, rerun a corrected bounded query rather than reasoning from partial output.
- Never use `grep -R` or `find ... -exec grep`. Do not keep repeating all-file scans after candidate files are known.

## Evidence Ledger

For every important claim, preserve the raw fields that make it auditable: file name, line number if available, absolute timestamp/window, component/check/route/source, status/code, count, request/transaction/session/key ID, config/certificate field, actor/client field, and "since", "recovered", or success state. Quote short decisive lines exactly; for long lines, copy the exact tokens that distinguish the current event from historical or unrelated decoys.

## Domain Checks

- Authentication anomalies: aggregate failures by source early, include the numeric count, quote `Failed password`/`Invalid user` samples, check accepted/success events for the same source, and cite successes from different sources. Do not infer compromise from failures alone.
- HTTP/service outages: tie route/status evidence to backend/service evidence and one independent layer such as proxy, system, dependency, database, or agent logs. Identify the limiting resource and actor/client if exposed. Explicitly dispose of named older, recovered, or unrelated decoys by timestamp/source/status, including feature-flag, cache, DNS, and external-service theories when raised.
- Agent/check failures: connect configuration, credential, certificate/timing, network, or dependency errors to affected behavior. Preserve decisive structured fields from causal and downstream lines. Separate healthy siblings from the failing path. For certificates, pair the x509 line with `NotBefore`/`NotAfter`, clock/time-sync evidence, and kubelet/syslog/agent lines before choosing bad material versus timing.
- Containerized layouts: if the primary in-container log path is empty and host logs are mounted elsewhere, explicitly inventory both and base the finding on the host-mounted evidence.
- Socket checks: run `help ss`, then run a help-advertised listening TCP query using supported TCP, listening, numeric, and optional no-header flags. If process/PID flags are absent, do not run teammate-proposed process flags; say process names/PIDs are unavailable. If the supported query fails, quote the exact error and still explain the supported capability. Do not claim socket rows, fallback commands, or summary output unless the transcript contains them.

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
- The `Commands run` section matches the transcript exactly; remove any command, failure, row, or count not actually observed.
- You avoid real-host access claims, remediation commands, and unsupported process/PID/socket claims.
