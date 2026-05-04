---
name: datadog/remote-host-diagnostics
description: Safe read-only host diagnostics through ./rshell with help discovery, explicit allowed paths, bounded evidence, and transcript-grounded final answers.
---

# Remote Host Diagnostics

## Hard Rules

- Run diagnostics only as actual tool calls to `./rshell --allow-all-commands -c '<script>'`. Do not answer from planned commands, prompt facts, repository knowledge, or static capability snapshots.
- Start each diagnostic path with `help` inside rshell, plus `help <command>` for commands or flags you will rely on. Production rshell deployments may restrict, omit, or extend features; target-environment `help` is authoritative.
- For file reads, pass every prompt-provided root literally on every invocation with `--allowed-paths=<root>` or one comma-separated `--allowed-paths=<primary>,<fallback>`. If primary and fallback roots are provided, inspect both in the same inventory command.
- Keep work read-only and bounded. Do not write files, install packages, mutate services, restart, kill, delete, or run broad repeated scans.
- Do not claim you connected to, SSHed into, or accessed a real remote/customer host unless the user provided that evidence.
- In the final answer, describe only recorded tool calls. Never list planned, interrupted, placeholder, or reconstructed commands.

## Fast Workflow

Default to two rshell invocations for log/file investigations:

1. **Help + inventory.** Run `help` and the needed `help <command>` calls, then inventory only prompt-provided roots with bounded `find ... -maxdepth ... -type f | sort | head -n 60`. Say when a root is empty and continue to any prompt-provided fallback root. A capped inventory is not exhaustive.
2. **One evidence pass.** Pick a small candidate set before grepping: the current relevant component log, prompt-named rotated/noise log, and at most one or two independent layers such as proxy, system, dependency, audit, security, or sibling-agent evidence. Prefer literal candidate file paths over broad all-log searches.

Use a third invocation only for one missing proof, such as a short `sed -n` context window around already-known line numbers, an exact same-source success check, a certificate validity/time comparison, or a supported socket confirmation. Do not run broad fallback scans after the cause is already evidenced.

For sockets, usually use one invocation: `help; help ss; ss -tlnH || ss -tln || true`. Do not run or claim process/PID flags unless `help ss` advertises them and the command output supports the claim.

## Evidence Pass Shape

Keep scripts auditably small and labeled with `printf '%s\n' '<label>'`. A good pass has 4-7 blocks:

- line counts for candidate files
- incident-window counts and capped samples
- decisive cause counts and capped samples
- impact, current-state, recovery, or no-success counts/samples
- prompt-named counter-hypothesis counts/samples
- one independent-layer sample when available

Count first, then sample with caps. Prefer `grep -H -c`, `grep -H -n -m 20`, `wc -l`, `head -n`, and short `sed -n` windows. Raise caps only when a count proves more lines exist and the extra lines answer a specific missing question. End exploratory scripts with `true` or `probe || true` so useful partial output survives nonzero grep/probe results.

Use rshell-supported shell only. Avoid `while`, `case`, functions, process substitution, background jobs, recursive grep, `find ... -exec grep`, and complex nested quoting. Use incident time tokens, affected object names, IDs, sources, statuses, and likely cause words from the prompt/output rather than giant generic regexes.

Example shapes:

```sh
./rshell --allow-all-commands --allowed-paths=<root> -c 'help; help find; help grep; help sed; help head; help wc; find <root> -maxdepth 3 -type f | sort | head -n 60'
./rshell --allow-all-commands --allowed-paths=<root> -c 'printf "%s\n" "counts"; grep -H -c "<pattern>" <file1> <file2> || true; printf "%s\n" "samples"; grep -H -n -m 20 "<pattern>" <file1> <file2> || true; true'
```

## Evidence Discipline

- Every cause, timestamp/window, affected service/check/route/source, consequence, and negative finding needs transcript evidence.
- Preserve file name, line number when available, full date plus time when available, exact field values, counts, IDs, status codes, and decisive message fragments. Copy raw tokens from output; do not retype paths, IDs, or error strings from memory.
- Label evidence as current, historical, rotated, recovered, different-source, fallback-root, or unavailable. Do not let old recovered lookalikes become the current cause.
- For zero/negative claims, cite the exact zero count or runtime/help output. If filename or time assumptions produce zero matches, report the zero and search nearby discovered files/dates once rather than forcing prompt wording into the conclusion.
- Explicitly dispose of each prompt-suggested theory and each obvious rotated/recovered lookalike. Name the alternative, cite the count/status/date/source that separates it from the current incident, and avoid making the alternative sound causal.

## Diagnostic Priorities

- **Agent/telemetry:** Separate config validation, credential/API/auth failures, intake/APM noise, queue/aggregator/collector/flush impact, sibling health, and recovery. Pair the causal line with same-incident impact and no-flush/stopped/recovery wording.
- **Authentication:** Aggregate failures by source early. State the concentrated source, approximate count, failed-password/invalid-user pattern, and whether accepted/successful events for that same source exist in the current window. Accepted lines from other sources are different-source evidence; old rotated successes are historical.
- **HTTP/service:** Put affected route, HTTP status, and incident time in the finding. Correlate proxy/access evidence with service/backend evidence and one dependency/system layer. Search for actor/driver fields such as client/source, application name, job, worker, fanout, user, pool, active/max, or owner. Dispose of recovered or older gateway, feature, cache, DNS, dependency, and rotated alternatives.
- **Certificates/container layouts:** If a primary log root is empty and a host-mounted root is provided, inspect both roots in one inventory and say which root produced evidence. Quote the exact x509/certificate clause, compare current time with validity fields, and cite clock-sync, rotation, renewal, kubelet/syslog, or equivalent system evidence before choosing timing versus certificate material.
- **Sockets:** Use supported local TCP listening probes after `help ss`. Report local address/port rows when collected; if process flags are absent or runtime-blocked, state that process names/PIDs are unavailable from the supported rshell data.

## Final Answer Contract

Use concise sections:

- `Commands run`: one bullet per recorded rshell invocation. Include `./rshell --allow-all-commands`, each literal `--allowed-paths=...` allowlist, and the concrete files or socket probe queried. For long `-c` scripts, summarize labeled blocks instead of pasting every argument, but do not use `<root>`, `...`, "bounded pass", or any file/command not actually run.
- `Finding`: one sentence naming the likely cause plus affected service/check/route/source, exact raw cause token or status when available, and the full incident date/time window when available.
- `Evidence`: concrete files, line numbers, full dates/times, exact decisive message fragments, counts/statuses, IDs/fields, actor/source, downstream impact, and exact "since", recovery, or success markers when they prove duration or state.
- `Not supported`: dispose of misleading hypotheses, historical/rotated matches, recovered noise, different-source successes, missing same-source successes, or unavailable command capabilities.
- `Uncertainty / next checks`: state what is not proven and suggest only safe read-only validation, audit, config/source review, rollback planning, owner follow-up, or capability follow-up. Do not propose remediation commands.

Before finalizing, verify that command bullets map to recorded tool calls; all prompt-provided roots used for reads appear literally; no placeholders remain; the finding includes exact time and affected object when known; decisive raw tokens remain visible; each negative claim has a count or runtime/help result; current versus historical/recovered/different-source evidence is labeled; and no unsupported process/PID/socket claims, real-host access claims, inventory-exhaustiveness overclaims, or remediation commands appear.
