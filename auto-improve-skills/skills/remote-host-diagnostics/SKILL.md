---
name: datadog/remote-host-diagnostics
description: Safe read-only host diagnostics through ./rshell with help discovery, explicit allowed paths, bounded evidence, and transcript-grounded final answers.
---

# Remote Host Diagnostics

## Hard Rules

- Run diagnostics only as actual tool calls to `./rshell --allow-all-commands -c '<script>'`. Do not answer from planned commands, prompt facts, repository knowledge, or a static capability snapshot.
- For file reads, put every literal root used in `--allowed-paths=<root>` on every invocation. For primary plus fallback roots, use one comma-separated `--allowed-paths=<primary>,<fallback>` and inspect both roots in the same inventory pass.
- Keep work read-only and bounded. Do not write files, install packages, mutate services, restart, kill, delete, or run broad repeated scans.
- Run `help` inside rshell before relying on a command, feature, or flag. Production deployments may restrict, omit, or extend features; target-environment `help` is the source of truth.
- Do not claim you connected to, SSHed into, or accessed a real remote/customer host unless the user provided that evidence.
- In the final answer, list only recorded tool calls. If a command was only planned, interrupted, or absent from the transcript, do not include it.

## Fast Investigation Pattern

Aim for two rshell invocations; use a third only to fill one specific missing evidence gap.

1. **Capability and inventory:** put `help` first in the same `-c` command, then `help <command>` for the builtins and flags you plan to use. Inventory only prompt-provided roots with bounded `find ... -maxdepth ... | sort | head -n N`.
2. **Evidence matrix pass:** query a small file set: current component log, prompt-named rotated/alternative logs, and one independent layer such as proxy, system, dependency, audit, or sibling-agent evidence. In one script collect counts plus capped samples for symptom, cause, impact/current state, source/driver, same-source auth success, recovery, and prompt-named counter-hypotheses.
3. **Focused confirmation:** if needed, run only the narrow command needed for a missing cell: short `sed -n` windows, exact ID/status/source greps, certificate validity/time comparisons, same-source accepted/success counts, or a supported socket probe.

Stop when cause, consequence, counter-hypothesis disposition, current/recovery state, and uncertainty are evidenced. Fold prompt-named alternatives into the same evidence pass. If an actor/driver or same-source success is absent, cite the bounded search instead of inferring it.

## Command Shape

Keep scripts compact and transcript-friendly:

```sh
./rshell --allow-all-commands --allowed-paths=<root> -c 'help; help find; help grep; help sed; help head; help wc; find <root> -maxdepth 3 -type f | sort | head -n 80'
./rshell --allow-all-commands --allowed-paths=<root> -c 'grep -H -c "<pattern>" <file1> <file2> <file3>; grep -H -n -m 20 "<pattern>" <file1> <file2> <file3>; tail -n 40 <file1>; true'
```

Use rshell-supported shell only. Avoid `while`, `case`, functions, process substitution, background jobs, recursive grep, and `find ... -exec grep`. Prefer literal candidate file paths over host-shell variables or complex nested quoting. Use clear grep flags (`grep -H -n -m 20`) and labels with `printf '%s\n' '<label>'`.

Exploratory greps, empty files, and permission-limited read-only probes may return nonzero while still producing useful output. Label them, leave stderr/stdout visible, and end with `true` or `probe || true` so the transcript records the command. Report the error or zero result.

Socket-only diagnostics need no file allowlist. Record a help-informed baseline:

```sh
./rshell --allow-all-commands -c 'help; help ss; ss -tlnH || true'
```

Run the plain listening-TCP baseline before optional IPv4/IPv6 or summary probes. Do not run or claim process/PID socket flags unless `help ss` advertises them. If the supported query is runtime-blocked, distinguish advertised capability from collected rows and say process/PID attribution is unavailable when unsupported.

## Evidence Discipline

- Keep output small with `grep -H -c`, `grep -H -n -m`, `wc -l`, `head -n`, `tail -n`, and short `sed -n` windows. Aggregate before sampling high-volume logs.
- Prefer exact route/check names, status classes, auth phrases, certificate terms, failure verbs, source/client fields, IDs, time windows, and recovery/success words over generic `error|warn`.
- After inventory, query explicit candidate files. If filename or time assumptions produce zero matches, say so and search nearby discovered dates/files rather than forcing prompt wording into the conclusion.
- Quote decisive wording: error/status text, check/route, source/actor, ID, count, validity field, "since" marker, recovery marker, or unsupported-capability text.
- Negative and current-state claims require evidence: same-source success counts, rotated recovery markers, help/runtime errors, continued failure, no-output/no-flush markers, retry/success markers, tail samples, or explicit absence counts.
- Preserve file/line, full date plus time when available, exact field values, downstream impact, and whether evidence is current, old, rotated, recovered, different-source, or unavailable.

## Diagnostic Cues

- **Agent/telemetry:** Search current plus rotated/noise logs for config, credential/auth, intake, trace/APM, queue, aggregator/collector, flush, and recovery terms. Pair the causal line with same-incident impact and current-state markers; cite healthy or recovered sibling paths separately.
- **Authentication:** Aggregate failures by source early, cite the count, sample failed-password/invalid-user lines, then probe accepted/success events for that same source in current and historical logs. If zero, say no successful or accepted login from that source in the current window. Label other successes as different-source.
- **HTTP/service:** Correlate access/proxy status with service/backend evidence and one dependency/system layer. After a limit or dependency failure, search nearby lines for actor/driver fields such as client/source, application name, worker/job, fanout, user, pool, active/max, or owner; cite them or say the actor was not proven. Dispose of older, recovered, feature-flag, cache, DNS, gateway, or external-service alternatives.
- **Certificates/container layouts:** If a primary log root is empty and host logs are mounted elsewhere, inspect both roots in one command and say which root produced evidence. For x509 messages, quote the exact certificate clause, compare current time with validity fields, and cite clock-sync, rotation, renewal, kubelet/syslog, or equivalent system evidence before choosing clock/timing versus certificate material.
- **Sockets:** Run `help ss` before socket probes and then the plain supported listening-TCP baseline. If process details are absent or unsupported, say local TCP addresses/ports are the safe supported socket data while process/PID attribution is unavailable.

## Final Answer Contract

Use concise sections:

- `Commands run`: recorded rshell invocations, exact command shape, literal `--allowed-paths` roots with no ellipses, key files queried, help/unsupported-capability evidence, and no probes you only considered.
- `Finding`: one sentence naming the likely cause and affected service/check/traffic/source.
- `Evidence`: concrete files, full dates/times, exact decisive message fragments, counts/statuses, IDs, fields, actor/source, downstream impact, and exact "since", recovery, or success markers when they prove duration or state.
- `Not supported`: dispose of misleading hypotheses, historical/rotated matches, recovered noise, different-source successes, missing same-source successes, or unavailable command capabilities.
- `Uncertainty / next checks`: state what is not proven and suggest only safe read-only validation, audit, config/source review, rollback planning, owner follow-up, or capability follow-up. Do not propose remediation commands.

Before finalizing, verify that every cause, consequence, timestamp/window, and counter-hypothesis has transcript evidence; counts appear when scale matters; zero/negative findings are backed by counts or help/runtime output; historical/recovered/different-source evidence is labeled; decisive wording is quoted; `Commands run` matches recorded tool calls; and there are no real-host access claims, remediation commands, or unsupported process/PID/socket claims. If no rshell tool call exists, say diagnostics could not be completed and do not reconstruct commands or results.
