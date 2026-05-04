---
name: datadog/remote-host-diagnostics
description: Safe read-only host diagnostics through ./rshell with help discovery, literal allowed paths, compact evidence collection, and transcript-grounded final answers.
---

# Remote Host Diagnostics

## Hard Rules

- Diagnose only through recorded `./rshell --allow-all-commands -c '<script>'` tool calls. Do not answer from planned commands, prompt facts, repository knowledge, or static capability snapshots.
- Start the first script with `help; help <command>; ...` for every command/flag family you rely on, so help discovery is visible in the command transcript. Production rshell deployments may restrict, omit, or extend features; target-environment `help` is authoritative. Repeat help only for a new command/flag or after an unsupported command.
- For file reads, pass every prompt-provided root literally on every invocation with `--allowed-paths=<root>` or comma-separated roots. Do not replace the sandbox root with a discovered subdirectory; use subdirectories only in file operands. For primary/fallback layouts, include both roots from the first inventory onward and state which root produced evidence.
- Keep work read-only and bounded. Do not write files, install packages, mutate services, restart, kill, delete, run recursive grep, use `find ... -exec grep`, or run broad repeated scans.
- Do not claim you connected to, SSHed into, or accessed a real remote/customer host unless the user provided that evidence.
- In the final answer, describe only recorded tool calls; never list planned, interrupted, placeholder, or reconstructed commands.

## Fast Workflow

Aim for two rshell invocations for log/file investigations and one for socket inventory. Use a third invocation only for one named missing proof.

1. **Help + inventory.** Run `help` and needed command help in the same first `-c` script, then inventory only prompt-provided roots with bounded `find ... -maxdepth ... -type f | sort | head -n 60`. Say when a root is empty. A capped inventory is not exhaustive.
2. **One fused evidence pass.** Pick exact candidate files before grepping: current component log, any prompt-named rotated/noise log, and at most one or two proxy/system/dependency/audit/security/sibling layers. In one script, gather cause, impact/current state, actor/source, recovery/success absence, prompt-theory checks, rotated/recovered lookalikes, and one independent layer when available.

Use a third invocation only for a specific missing answer: exact impact or "since" marker, actor/driver attribution, same-source success absence, short `sed -n` context around known lines, certificate validity/time comparison, or supported socket confirmation. Stop once cause, impact, and main alternatives are evidenced.

## Efficient Evidence Scripts

Keep scripts small, labeled, and auditable with `printf '%s\n' '<label>'`. A good evidence pass has 5-8 blocks, not dozens:

- selected candidate file line counts
- incident-window counts and capped samples
- decisive cause counts and capped samples
- impact, current-state, recovery, success, or no-success counts/samples
- actor/owner/source fields when they could explain who or what drove the event
- prompt-named counter-hypothesis and rotated/recovered lookalike checks
- one independent-layer sample when available

Count first for decisions and negative claims, then sample with caps. Prefer `grep -H -c`, `grep -H -n -m 20`, `wc -l`, `head -n`, `tail -n`, and short `sed -n` windows. Avoid separate full-file sweeps for generic `ERROR`, `WARN`, `status`, and `recovered` after you already have a likely cause; search terms should be tied to the incident time, affected object, source, status, or prompt theory. Raise caps only for one missing proof. End exploratory scripts with `true` or `probe || true` so useful partial output survives nonzero probes.

Use rshell-supported shell only. Avoid `while`, `case`, functions, process substitution, background jobs, recursive grep, `find ... -exec grep`, and complex nested quoting. Use incident time tokens, affected object names, IDs, sources, statuses, and likely cause words from the prompt/output rather than giant generic regexes.

## Evidence Discipline

- Every cause, timestamp/window, affected object, consequence, and negative finding needs transcript evidence.
- Preserve filename, line number when available, full date/time, exact values, counts, IDs, status codes, and decisive message fragments. If a raw token matters in the finding, keep it visible again in the Evidence section; do not paraphrase away line/column fields, "since" markers, raw x509/status/error clauses, route/status pairs, source addresses, or zero counts.
- Label evidence as current, historical, rotated, recovered, different-source, fallback-root, sampled, or unavailable. Do not let old recovered lookalikes become the current cause.
- For zero/negative claims, cite the exact zero count or runtime/help output and the queried files/window. Say "not found in the queried files/window" for bounded searches; do not say an event never existed unless the transcript proves that scope.
- Explicitly dispose of each prompt-suggested theory and each obvious rotated/recovered lookalike. Name the alternative, cite the count/status/date/source that separates it from the current incident, and avoid making the alternative sound causal.

## Diagnostic Priorities

- **Agent/telemetry:** Separate config validation, credential/API/auth failures, intake/APM noise, queue/aggregator/collector/flush impact, sibling health, and recovery. Pair the cause with same-incident delivery impact, raw status/reason, stopped/no-flush wording, and recovery or recovery-absence evidence.
- **Authentication:** Aggregate failures by source early with bounded `grep`, `sed`, `sort`, and `uniq -c`. State the concentrated source, approximate count, failed-password/invalid-user pattern, and same-source accepted/successful count in the current window. Accepted lines from other sources are different-source evidence; old rotated successes are historical.
- **HTTP/service:** Put affected route, HTTP status, and incident time in the finding. Correlate proxy/access evidence with service/backend evidence and one dependency/system layer. Search once for actor/driver fields such as client/source, application name, job, worker, fanout, user, pool, active/max, or owner; if evidenced, put the driver in the finding.
- **Certificates/container layouts:** If a primary log root is empty and a host-mounted root is provided, inspect both roots in one inventory and say which root produced evidence. Quote the exact certificate/x509 clause, compare current time with NotBefore/NotAfter-style validity fields when present, and cite clock-sync, rotation, renewal, kubelet/syslog, or equivalent system evidence before choosing timing versus certificate material.
- **Sockets:** Usually use one invocation: `help; help ss; ss -tlnH || ss -tln || true`. Report local address, port, and state rows when collected. Do not run or claim process/PID flags unless `help ss` advertises them and output supports the claim; if absent, unsupported, or runtime-blocked, say process/PID data is unavailable from this rshell run.

## Final Answer Contract

Use concise sections:

- `Commands run`: one bullet per recorded rshell invocation. Include literal `./rshell --allow-all-commands`, every literal `--allowed-paths=...`, and concrete files or socket probes. Summarize long scripts by labels, but do not write "same prefix", "same --allowed-paths", `<root>`, `...`, "bounded pass", or commands not actually run.
- `Finding`: one sentence naming the likely cause plus affected service/check/route/source, exact raw cause token or status when available, and the full incident date/time window when available.
- `Evidence`: concrete files, line numbers, full dates/times, exact decisive message fragments, counts/statuses, IDs/fields, actor/source, downstream impact, and exact "since", recovery, success, or zero-count markers when they prove duration or state.
- `Not supported`: dispose of misleading hypotheses, historical/rotated matches, recovered noise, different-source successes, missing same-source successes, unavailable command capabilities, and prompt-suggested theories not supported by the queried files.
- `Uncertainty / next checks`: state what is not proven and suggest only safe read-only validation, audit, config/source review, rollback planning, owner follow-up, or capability follow-up. Do not propose remediation commands.

Before finalizing, verify command bullets map to recorded tool calls; prompt roots appear literally in every file-reading invocation; no placeholders remain; the finding has exact time/object when known; decisive raw tokens remain visible in Evidence; each negative claim has count/help evidence and queried scope; current vs historical/recovered/different-source evidence is labeled; and there are no unsupported process/PID/socket claims, real-host access claims, exhaustive-inventory overclaims, or remediation commands.
