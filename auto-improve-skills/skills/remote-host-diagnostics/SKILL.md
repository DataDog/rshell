---
name: datadog/remote-host-diagnostics
description: Safe read-only host diagnostics through ./rshell with help discovery, explicit allowed paths, bounded evidence, and transcript-grounded final answers.
---

# Remote Host Diagnostics

## Hard Rules

- Run diagnostics only as actual `./rshell --allow-all-commands -c '<script>'` tool calls. Do not answer from planned commands, prompt facts, repository knowledge, or static capability snapshots.
- Start with `help` plus `help <command>` for commands/flags you rely on. Production rshell deployments may restrict, omit, or extend features; target-environment `help` is authoritative. Do not repeat help unless using a new command/flag or recovering from an unsupported command.
- For file reads, pass every prompt-provided root literally on every invocation with `--allowed-paths=<root>` or comma-separated `--allowed-paths=<primary>,<fallback>`. Inventory primary plus fallback roots together and state which root produced evidence.
- Keep work read-only and bounded. Do not write files, install packages, mutate services, restart, kill, delete, run recursive grep, use `find ... -exec grep`, or run broad repeated scans.
- Do not claim you connected to, SSHed into, or accessed a real remote/customer host unless the user provided that evidence.
- In the final answer, describe only recorded tool calls; never list planned, interrupted, placeholder, or reconstructed commands.

## Fast Workflow

Default to two rshell invocations for log/file investigations:

1. **Help + inventory.** Run `help`/needed command help, then inventory only prompt-provided roots with bounded `find ... -maxdepth ... -type f | sort | head -n 60`. Say when a root is empty. A capped inventory is not exhaustive.
2. **One fused evidence pass.** Pick exact candidate files before grepping: current component log, prompt-named rotated/noise log, and at most one or two proxy/system/dependency/audit/security/sibling layers. Combine cause, impact, actor/source, recovery/current-state, and counter-hypothesis checks instead of later synonym sweeps.

Use a third invocation only for one named missing proof: exact impact or "since" marker, actor/driver attribution, same-source success absence, short `sed -n` context around known lines, certificate validity/time comparison, or supported socket confirmation. Stop once cause, impact, and main alternatives are evidenced.

For sockets, usually use one invocation: `help; help ss; ss -tlnH || ss -tln || true`. Do not run or claim process/PID flags unless `help ss` advertises them and output supports the claim; if absent, unsupported, or runtime-blocked, say process/PID data is unavailable from this rshell run.

## Evidence Pass Shape

Keep scripts auditably small and labeled with `printf '%s\n' '<label>'`. A good pass has 5-8 blocks:

- line counts for exact candidate files
- incident-window counts and capped samples
- decisive cause counts and capped samples
- impact, current-state, recovery, or no-success counts/samples
- actor/owner/source fields when they could explain who or what drove the event
- prompt-named counter-hypothesis and rotated/recovered lookalike checks
- one independent-layer sample when available

Count first, then sample with caps. Prefer `grep -H -c`, `grep -H -n -m 20`, `wc -l`, `head -n`, and short `sed -n` windows. Raise caps only for a specific missing answer. End exploratory scripts with `true` or `probe || true` so useful partial output survives nonzero probes.

Use rshell-supported shell only. Avoid `while`, `case`, functions, process substitution, background jobs, recursive grep, `find ... -exec grep`, and complex nested quoting. Use incident time tokens, affected object names, IDs, sources, statuses, and likely cause words from the prompt/output rather than giant generic regexes.

## Evidence Discipline

- Every cause, timestamp/window, affected object, consequence, and negative finding needs transcript evidence.
- Preserve file name, line number when available, full date/time, exact values, counts, IDs, status codes, and decisive message fragments. Copy raw tokens from output.
- Label evidence as current, historical, rotated, recovered, different-source, fallback-root, sampled, or unavailable. Do not let old recovered lookalikes become the current cause.
- For zero/negative claims, cite the exact zero count or runtime/help output and the queried files/window. Say "not found in the queried files/window" for bounded searches; do not say an event never existed unless the transcript proves that scope. If a prompt-suggested red herring is not found, report the bounded miss instead of forcing it into the conclusion.
- Explicitly dispose of each prompt-suggested theory and each obvious rotated/recovered lookalike. Name the alternative, cite the count/status/date/source that separates it from the current incident, and avoid making the alternative sound causal.

## Diagnostic Priorities

- **Agent/telemetry:** Separate config validation, credential/API/auth failures, intake/APM noise, queue/aggregator/collector/flush impact, sibling health, and recovery. Pair the cause with same-incident delivery impact, raw status/reason, and no-flush/stopped/recovery wording.
- **Authentication:** Aggregate failures by source early. State the concentrated source, approximate count, failed-password/invalid-user pattern, and same-source accepted/successful count in the current window. If zero, write it plainly as "No accepted/successful login from `<source>` in the current window (count=0)." Accepted lines from other sources are different-source evidence; old rotated successes are historical.
- **HTTP/service:** Put affected route, HTTP status, and incident time in the finding. Correlate proxy/access evidence with service/backend evidence and one dependency/system layer. Search once for actor/driver fields such as client/source, application name, job, worker, fanout, user, pool, active/max, or owner; if evidenced, put the driver in the finding. Dispose of recovered or older gateway, feature, cache, DNS, dependency, and rotated alternatives.
- **Certificates/container layouts:** If a primary log root is empty and a host-mounted root is provided, inspect both roots in one inventory and say which root produced evidence. Quote the exact x509/certificate clause, compare current time with NotBefore/NotAfter-style validity fields, and cite clock-sync, rotation, renewal, kubelet/syslog, or equivalent system evidence before choosing timing versus certificate material.
- **Sockets:** Use supported local TCP listening probes after `help ss`. Report local address, port, and state rows when collected; explicitly say process names/PIDs are unsupported or unavailable when process flags are absent, rejected, or not run.

## Final Answer Contract

Use concise sections:

- `Commands run`: one bullet per recorded rshell invocation. Include `./rshell --allow-all-commands`, each literal `--allowed-paths=...`, and concrete files or socket probes. Summarize long scripts by labels, but never use `<root>`, `...`, "bounded pass", or commands not actually run.
- `Finding`: one sentence naming the likely cause plus affected service/check/route/source, exact raw cause token or status when available, and the full incident date/time window when available.
- `Evidence`: concrete files, line numbers, full dates/times, exact decisive message fragments, counts/statuses, IDs/fields, actor/source, downstream impact, and exact "since", recovery, success, or zero-count markers when they prove duration or state.
- `Not supported`: dispose of misleading hypotheses, historical/rotated matches, recovered noise, different-source successes, missing same-source successes, unavailable command capabilities, and prompt-suggested theories not supported by the queried files.
- `Uncertainty / next checks`: state what is not proven and suggest only safe read-only validation, audit, config/source review, rollback planning, owner follow-up, or capability follow-up. Do not propose remediation commands.

Before finalizing, verify command bullets map to recorded tool calls; prompt roots appear literally; no placeholders remain; the finding has exact time/object when known; decisive raw tokens remain visible; each negative claim has count/help evidence and queried scope; current vs historical/recovered/different-source evidence is labeled; and there are no unsupported process/PID/socket claims, real-host access claims, exhaustive-inventory overclaims, or remediation commands.
