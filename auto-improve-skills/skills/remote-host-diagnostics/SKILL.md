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

## Fast Run Plan

Prefer two or three rshell invocations. Add another only when the first command proves an empty primary root, a supported command fails, or an independent layer is still missing.

1. **Discover and map**: run `help` plus only command topics you expect to use, then inventory each prompt-provided root once with bounded `find`/`ls`. Include all primary and fallback roots literally in `--allowed-paths`. If one root is empty, prove that and inspect the fallback in the same pass when possible.
2. **Triage once, across layers**: after inventory, choose explicit candidate files: the current component log, any rotated/noise file needed for the user's hypothesis, and one independent layer such as proxy, system, dependency, or agent logs. In one script collect `wc -l`, targeted `grep -Hc` counts, and a few `grep -Hn -m` or `head`/`tail` samples for symptom, time, status/source, causal, recovery, and teammate-hypothesis tokens.
3. **Confirm only decisive tokens**: query identifiers, status codes, source actors, check names, certificate fields, request/transaction/session IDs, and recovery/success markers already found. Use small `sed -n` windows around known line numbers only. Stop after you have cause, consequence, alternative-hypothesis disposition, current/recovery state, and remaining uncertainty.

## Command Discipline

- Keep output small and transcript-clean. Use `echo` labels or `printf '%s\n' "label"`; do not let a label or quoting error hide diagnostic output.
- Aggregate before sampling high-volume logs: `grep -Hc`, `wc -l`, or `grep ... | sort | uniq -c` first, then representative lines with `-m`, `head`, or `tail`.
- Prefer anchored prompt/evidence patterns over broad `error|warn` sweeps: absolute time windows, route/check names, status classes, auth phrases, certificate terms, failure verbs, source/client fields, and recovery/success words.
- After inventory, do not rescan every file. Query the explicit candidates. Never use `grep -R` or `find ... -exec grep`.
- If a command or flag fails, run `help <command>`, then one supported subset. Record the exact limitation, and mention only commands actually run and outputs actually observed.
- Shell variables are fine inside scripts, but final answers must still name the literal allowed roots and files. If expansion or quoting breaks a query, rerun a corrected bounded query before reasoning from it.

## Evidence Ledger

For every important claim, preserve the raw fields that make it auditable: file, line number if available, absolute date/time window, component/check/route/source, status/code, count, request/transaction/session/key ID, config/certificate field, actor/client field, and embedded "since", "recovered", "resumed", "accepted", or success state. Quote short decisive lines exactly; for long lines, copy the exact distinguishing tokens.

## Branch Guidance

- Authentication anomalies: aggregate failures by source early, include the numeric count line, quote failed-password/invalid-user samples, check accepted/success events for the same source in the current window, and cite successes from different sources separately. Say "no current successful/accepted login from that source" when supported; do not infer compromise from failures alone.
- HTTP/service outages: correlate affected route/status samples from proxy or access logs with service/backend evidence and one independent layer. Identify the limiting resource and actor/client if exposed. In the same triage pass, dispose of named older, recovered, feature-flag, cache, DNS, external-service, or rotated-log alternatives by timestamp/source/status.
- Agent/check failures: distinguish configuration, credential/auth, certificate/timing, network, and dependency causes. Pair the causal line with downstream impact such as stopped components, paused or dropped payloads, rejected intake, no-flush/since markers, or failing checks. Separately cite healthy sibling paths so they do not become false causes.
- Certificates and container layouts: if the primary container log root is empty and host logs are mounted elsewhere, inventory both roots and say which root produced evidence. Pair the exact x509/check line with certificate bounds, clock/time-sync, kubelet/syslog/system, or rotation evidence before choosing timing/environment versus certificate material.
- Socket checks: run `help ss`, then actually run a help-advertised canonical listening TCP query such as `ss -tln` or `ss -tlnH` before optional IPv4/IPv6/summary variants. Do not run teammate-proposed process/PID flags unless `help ss` advertises them. If the supported query fails, quote the exact error and still explain that listening TCP addresses/ports are the supported target while process names/PIDs are unavailable when absent from help.

## Final Answer Contract

Use this concise structure. Keep it short, but keep the fields that prove the diagnosis.

- `Commands run`: faithful transcript summary with exact `./rshell` shape, literal `--allowed-paths` roots, key files queried, supported/unsupported command evidence, and no probes you only considered.
- `Finding`: one sentence naming the likely cause and affected service/check/traffic/source.
- `Evidence`: cite concrete files, date and time together when available, exact message fragments, identifiers, line/config/certificate fields, counts/statuses, and downstream symptoms. Preserve embedded "since" or recovery timestamps when they prove duration or recovery.
- `Not supported`: explicitly dispose of misleading hypotheses named by the user, historical rotated matches, recovered noise, different-source successes, missing same-source successes, or unavailable command capabilities.
- `Uncertainty / next checks`: state what is not proven and suggest only safe read-only validation, audit, config/source review, rollback planning, owner follow-up, or capability follow-up. Do not propose remediation commands.

Before finalizing, verify that cause, consequence, and counter-hypothesis each have source output; exact numeric counts appear when counts matter; historical/recovered/different-source evidence is labeled; `Commands run` matches the transcript exactly; and there are no real-host access claims, remediation commands, or unsupported process/PID/socket claims.
