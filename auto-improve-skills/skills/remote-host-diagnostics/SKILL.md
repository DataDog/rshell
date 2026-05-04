---
name: datadog/remote-host-diagnostics
description: Use when diagnosing host, service, network, socket, log, certificate, authentication, or system issues through this repository's rshell. Guides safe, bounded, read-only diagnostics with ./rshell, explicit allowed paths, help-based capability discovery, and evidence-grounded final answers.
---

# Remote Host Diagnostics

Use this skill for host/service diagnostics through this repository's `./rshell`.

## Hard Rules

- Run diagnostics only as actual tool calls to `./rshell --allow-all-commands -c '<script>'`. Do not answer from planned commands, prompt facts, repository knowledge, or a static capability snapshot.
- For file reads, put every literal root in `--allowed-paths=<root>` on every invocation. For primary plus fallback roots, use one comma-separated `--allowed-paths=<primary>,<fallback>` and inspect both roots in the same inventory pass.
- Keep work read-only and bounded. Do not write files, install packages, mutate services, restart, kill, delete, or run broad repeated scans.
- Run `help` inside rshell before relying on a command, feature, or flag. Production deployments may restrict, omit, or extend features; target-environment `help` is the source of truth.
- Do not claim you connected to, SSHed into, or accessed a real remote/customer host unless the user provided that evidence.
- In the final answer, list only commands that actually ran. If a command was only planned, interrupted, or not present in the transcript, do not include it.

## Fast Investigation Pattern

Aim for two rshell invocations. Use a third only when a decisive claim is still unevidenced.

1. **Capability and inventory:** put `help` first in the same `-c` command, then `help <command>` for the builtins and flags you plan to use. Inventory only prompt-provided roots with bounded `find ... -maxdepth ... | sort | head -n N`.
2. **One evidence pass:** from inventory, choose a small file set: the current component log, current/rotated or named alternative logs for the user's theory, and one independent layer such as proxy, system, dependency, audit, or sibling-agent evidence. In one script collect counts plus capped samples for symptom, cause, impact, source/actor, current state, recovery, and counter-hypothesis terms.
3. **Focused confirmation:** if needed, run short `sed -n` windows or narrow greps for the exact IDs, statuses, source actors, certificate fields, socket flags, recovery markers, and same-source success/failure probes that the evidence pass surfaced.

Stop when cause, consequence, counter-hypothesis disposition, current/recovery state, and uncertainty are evidenced. Combine prompt-named alternatives into the same evidence pass instead of spending separate invocations on each red herring.

## Command Shape

Keep scripts compact and transcript-friendly:

```sh
./rshell --allow-all-commands --allowed-paths=<root> -c 'help; help find; help grep; help sed; help head; help wc; find <root> -maxdepth 3 -type f | sort | head -n 80'
./rshell --allow-all-commands --allowed-paths=<root> -c 'grep -H -c "<pattern>" <file1> <file2>; grep -H -n -m 20 "<pattern>" <file1> <file2>; sed -n "<start>,<end>p" <file1>'
```

Use rshell-supported shell only. Avoid `while`, `case`, functions, process substitution, background jobs, recursive grep, and `find ... -exec grep`. Prefer clear grep flags (`grep -H -n -m 20`) and simple labels with `printf '%s\n' '<label>'`.

For socket-only diagnostics, no file allowlist is needed. Run a help-informed supported query in the actual transcript:

```sh
./rshell --allow-all-commands -c 'help; help ss; ss -tlnH'
```

Do not run or claim process/PID socket flags unless `help ss` advertises them. If `ss -tlnH` or another help-supported query is runtime-blocked, say the supported target is local listening TCP address/port collection, process/PID attribution is unavailable when unsupported, and this run could not collect rows because of the transcript error.

## Evidence Discipline

- Keep output small: `grep -H -c`, `grep -H -n -m`, `wc -l`, `head -n`, `tail -n`, and short `sed -n` windows. Keep most samples to 5-20 lines per file.
- Aggregate before sampling high-volume logs. Prefer exact route/check names, status classes, auth phrases, certificate terms, failure verbs, source/client fields, IDs, time windows, and recovery/success words over generic `error|warn`.
- After inventory, query explicit candidate files. Do not rescan every file unless inventory failed to identify candidates.
- If filename or time assumptions produce zero matches, say so and search nearby discovered dates/files rather than forcing prompt wording into the conclusion.
- Quote the decisive phrase exactly enough for a reviewer to recognize it: error/status text, check or route, source/actor, ID, count, validity field, "since" marker, recovery marker, or unsupported-capability text. Do not replace decisive wording with only a paraphrase.
- Negative claims require evidence: same-source accepted/success greps or counts for "no current success"; rotated/historical plus recovery markers for "old noise"; help or runtime error output for unsupported capabilities.
- For every important claim, preserve file/line, full date plus time when available, exact error/status/check/route/source/actor/config/certificate/socket field, downstream impact, and whether evidence is current, old, rotated, recovered, different-source, or unavailable.

## Diagnostic Cues

- **Agent/telemetry:** Search current and rotated/noise logs for config, credential/auth, intake, trace/APM, forwarder/queue, aggregator/collector, flush, and recovery terms. Pair the causal line with downstream impact from the same incident, then cite healthy or recovered sibling paths separately.
- **Authentication:** Aggregate failures by source early, cite the count, sample failed-password or invalid-user lines, then probe accepted/success events for that same source in current and historical logs. Cite successful logins from other sources separately and avoid claiming compromise without same-source success evidence.
- **HTTP/service:** Correlate access/proxy status with service/backend evidence and one independent dependency/system layer. After finding a resource limit, do not finalize until you search nearby lines for the actor/client/check/application/worker/fanout driver and cite it if present. Dispose of prompt-named older, recovered, feature-flag, cache, DNS, gateway, or external-service alternatives.
- **Certificates/container layouts:** If a primary log root is empty and host logs are mounted elsewhere, inspect both roots in one command and say which root produced evidence. For x509 messages that combine multiple possibilities, disambiguate by comparing current time with validity fields and by citing clock-sync, rotation, renewal, kubelet/syslog, or equivalent system evidence.
- **Sockets:** Run `help ss` before socket probes and then run a compact supported listening-TCP query such as `ss -tlnH` when help allows it. If process details are absent or unsupported, say listening local TCP addresses/ports are the safe supported socket data while process/PID attribution is unavailable.

## Final Answer Contract

Use concise sections:

- `Commands run`: actual number of rshell invocations, exact command shape, literal `--allowed-paths` roots with no ellipses, key files queried, help/unsupported-capability evidence, and no probes you only considered.
- `Finding`: one sentence naming the likely cause and affected service/check/traffic/source.
- `Evidence`: concrete files, full dates/times, exact decisive message fragments, counts/statuses, IDs, fields, actor/source, downstream impact, and exact "since", recovery, or success markers when they prove duration or state.
- `Not supported`: dispose of misleading hypotheses, historical/rotated matches, recovered noise, different-source successes, missing same-source successes, or unavailable command capabilities.
- `Uncertainty / next checks`: state what is not proven and suggest only safe read-only validation, audit, config/source review, rollback planning, owner follow-up, or capability follow-up. Do not propose remediation commands.

Before finalizing, verify that every cause, consequence, timestamp/window, and counter-hypothesis has transcript evidence; counts appear when scale matters; historical/recovered/different-source evidence is labeled; decisive wording is quoted rather than only paraphrased; `Commands run` matches actual tool calls; and there are no real-host access claims, remediation commands, or unsupported process/PID/socket claims. If no rshell tool call exists, say diagnostics could not be completed and do not list reconstructed commands or quote results.
