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

Aim to answer most log investigations in three to six rshell invocations: capability check, root inventory, targeted triage, focused context, and one hypothesis check if needed. Stop once the finding, impact, competing-hypothesis status, and remaining uncertainty are supported.

1. Restate the symptom, target service or host, relevant time window, prompt-provided roots, and teammate hypothesis.
2. Run one capability check, such as `help` plus only the command topics you expect to use. Do not spend time probing external tools that top-level `help` does not list.
3. Include every prompt-provided root exactly in `--allowed-paths`; do not allowlist a broader parent just to save typing. Inventory each root once with bounded depth and sorted output. If a primary root is empty and a host-mounted or fallback root is provided, inspect both, use explicit allowlists for both, and say so in the final answer.
4. Choose candidate files from names and symptoms: current log for the affected component, one rotated or noise file for the competing hypothesis, and one independent layer when available. Avoid repeated all-file sweeps.
5. Triage candidates with bounded filters before opening context: `grep -Hn -m`, `grep -Hc`, `wc -l`, `head`, `tail`, and small `sed -n` or advertised context windows around already-found lines. Do not use `grep -R` or `find ... -exec grep`.
6. Preserve a field ledger from decisive lines: source file, event timestamp, component/check/route/source, status or code, key IDs, request/transaction/session IDs, config or certificate fields, line/offset fields, counts, and recovery or "since" timestamps. Quote the whole decisive line when it is short; otherwise copy the raw tokens that distinguish this event from similar decoys.
7. Correlate cause to consequence across time, component, and source. Capture the triggering line and at least one downstream symptom line rather than only one side of the chain.
8. Test the teammate hypothesis with one targeted query against the relevant current and historical files. Classify it as current cause, historical/recovered, different source, unrelated noise, or unsupported.

## Command Shape

- Prefer combining related read-only checks for the same allowlisted root in one rshell script. One command can inventory, count, and show bounded context if outputs stay small.
- Keep file lists explicit after inventory. If there are many files, narrow by filename first instead of grepping every file repeatedly.
- For high-volume logs, count first and then print only representative matches with `-m` or a narrow time/status filter. Avoid dumping every request in a window when a count plus a few samples proves the point.
- Use patterns derived from the prompt and evidence classes: timestamps, route or check names, status classes, failure verbs, authentication phrases, certificate words, transaction/request IDs, and recovery/success words.
- When a command or flag fails, recover quickly: read `help <command>`, run one supported subset, and record the limitation. In the final answer, mention only commands actually run and outputs actually observed.

## Domain Checks

- Authentication anomalies: count failures by source, quote the failure and success event phrases exactly from the log, check accepted/success events for the same source, and distinguish different-source successes from compromise.
- HTTP/service outages: tie user-visible status or route failures to backend/service evidence and one independent layer such as proxy, app, system, dependency, or agent logs. Reject older or recovered decoys by timestamp and source.
- Agent/check failures: connect configuration, credential, certificate, network, or dependency errors to the affected check/service behavior. Preserve decisive structured fields from the causal line and from the downstream effect line. Separate healthy sibling paths from the failing one.
- Containerized layouts: if the primary in-container log path is empty and host logs are mounted elsewhere, explicitly inventory both and base the finding on the host-mounted evidence.
- Socket checks: run `help ss` first. If process/PID flags are absent, do not rely on them or claim process names. Then run a help-advertised listening TCP query using the supported TCP, listening, numeric, and optional no-header flags. If the supported query fails because of platform or permission limits, report that limitation instead of trying unrelated tools.

## Final Answer Contract

Use this concise structure:

- `Commands run`: exact commands or a faithful compact summary, including literal `--allowed-paths` roots, key files queried, and any unsupported-command evidence. Do not list probes you considered but did not run.
- `Finding`: one sentence naming the likely cause and affected service/check/traffic/source.
- `Evidence`: cite concrete file names, absolute date/time windows, exact message fragments, identifiers, line/config/certificate fields, counts/statuses, and downstream symptoms. Include date and time together when available, and preserve embedded "since" or recovery timestamps when they prove duration or recovery.
- `Not supported`: explicitly dispose of misleading hypotheses named by the user, historical rotated-log matches, recovered noise, different-source successes, or unavailable command capabilities.
- `Uncertainty / next checks`: state what is not proven and suggest only safe read-only validation, audit, config/source review, rollback planning, owner follow-up, or capability follow-up. Do not propose remediation commands.

Before finalizing, check that the answer names the source file or command output for every important claim, includes the raw wording that proves the diagnosis, avoids real-host access claims, and does not keep investigating after enough evidence is collected.
