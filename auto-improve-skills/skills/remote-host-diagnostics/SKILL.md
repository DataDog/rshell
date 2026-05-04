---
name: datadog/remote-host-diagnostics
description: Safe read-only host diagnostics through ./rshell with help discovery, explicit allowed paths, bounded evidence, and transcript-grounded final answers.
---

# Remote Host Diagnostics

## Hard Rules

- Run diagnostics only as actual tool calls to `./rshell --allow-all-commands -c '<script>'`. Do not answer from planned commands, prompt facts, repository knowledge, or a static capability snapshot.
- Run `help` inside rshell before relying on a command, feature, or flag. Production deployments may restrict, omit, or extend features; target-environment `help` is the source of truth.
- For file reads, pass every prompt-provided root literally with `--allowed-paths=<root>` on every invocation. If the prompt gives primary plus fallback roots, use one comma-separated allowlist and inspect both roots in the same inventory command.
- Keep work read-only and bounded. Do not write files, install packages, mutate services, restart, kill, delete, or run broad repeated scans.
- Do not claim you connected to, SSHed into, or accessed a real remote/customer host unless the user provided that evidence.
- In the final answer, describe only recorded tool calls. Never list planned, interrupted, placeholder, or reconstructed commands.

## Fast Pattern

Aim for two rshell invocations; use a third only for one missing proof. The fastest reliable path is: inventory, choose a small candidate set, collect decisive counts/samples, stop.

1. **Help and inventory.** Start the first script with `help` plus `help <command>` for commands you plan to use. Inventory only prompt-provided roots with bounded `find ... -maxdepth ... | sort | head -n 60`. If one root is empty, say so and continue to any prompt-provided fallback root. Do not call a capped `head` inventory exhaustive.
2. **Choose candidates before grepping.** From inventory and prompt clues, select only the current relevant component log, prompt-named rotated/noise log, and at most one or two independent layers such as proxy, system, dependency, audit, security, or sibling-agent evidence. Prefer literal candidate file paths over broad all-log searches.
3. **One compact evidence pass.** In one script collect counts first, then capped samples. Use incident time tokens, affected object names, and likely cause words from the prompt/output instead of giant generic regexes. Gather proof for:
   - symptom and incident time window
   - likely cause and exact decisive wording
   - impact/current state or recovery
   - actor/source/driver fields
   - same-source or same-ID success/recovery checks
   - prompt-named counter-hypotheses
4. **Focused confirmation.** If a cell is missing, run only the narrow proof needed: short `sed -n` windows around known line numbers, exact ID/status/source greps, certificate validity/time comparisons, same-source accepted/success counts, or a supported socket probe.

Stop when cause, consequence, current/recovery state, counter-hypothesis disposition, and uncertainty are evidenced. Prefer `grep -H -c`, `grep -H -n -m 20`, `wc -l`, `head -n`, and short `sed -n` windows. Avoid repeated huge multi-log greps, large `tail` dumps, or broad fallback scans after the cause is already proven.

## Command Discipline

Use rshell-supported shell only. Avoid `while`, `case`, functions, process substitution, background jobs, recursive grep, and `find ... -exec grep`. Prefer literal candidate file paths over host-shell variables or complex nested quoting. Label each question with `printf '%s\n' '<label>'`.

Exploratory greps, empty files, and permission-limited read-only probes may return nonzero while still producing useful output. Label them, leave stderr/stdout visible, and end the script with `true` or `probe || true` so the transcript records all results.

Keep scripts auditably small. A good evidence pass usually has 4-8 labeled blocks: line counts, incident-window samples, decisive cause counts/samples, impact/recovery counts, counter-hypothesis counts/samples, and one independent-layer sample. Default to `-m 20`; raise caps only when a count proves there is more evidence than the cap and the extra lines answer a specific missing question.

Example shape:

```sh
./rshell --allow-all-commands --allowed-paths=<root> -c 'help; help find; help grep; help sed; help head; help wc; find <root> -maxdepth 3 -type f | sort | head -n 60'
./rshell --allow-all-commands --allowed-paths=<root> -c 'printf "%s\n" "counts"; grep -H -c "<pattern>" <file1> <file2> || true; printf "%s\n" "samples"; grep -H -n -m 20 "<pattern>" <file1> <file2> || true; true'
```

Socket-only diagnostics need no file allowlist. Run `help ss` first, then a plain supported listening-TCP probe such as `ss -tlnH || true`. Do not run or claim process/PID socket flags unless `help ss` advertises them. If advertised support is runtime-blocked, distinguish supported syntax from collected rows and state that process/PID attribution is unavailable when unsupported.

## Evidence Discipline

- Every cause, timestamp/window, affected service/check/route/source, consequence, and negative finding needs transcript evidence.
- Preserve file name, line number when available, full date plus time when available, exact field values, counts, IDs, status codes, and decisive message fragments. Copy raw tokens from output; do not retype paths, IDs, or error strings from memory.
- For scale, count first and then sample. For zero/negative claims, cite the exact zero count or runtime/help output.
- Label evidence as current, historical, rotated, recovered, different-source, fallback-root, or unavailable. Do not let old recovered lookalikes become the current cause.
- If filename or time assumptions produce zero matches, report the zero and search nearby discovered files/dates once rather than forcing prompt wording into the conclusion.

## Diagnostic Cues

- **Agent/telemetry:** Separate config validation, credential/API/auth failures, intake/APM noise, queue/aggregator/collector/flush impact, sibling health, and recovery. Pair the causal line with the same-incident impact and any no-flush or recovery marker. In the final answer, keep the exact raw failure token, transaction/key/status field, and no-flush or stopped component wording together so the causal chain is machine-checkable.
- **Authentication:** Aggregate failures by source early. State the concentrated source, approximate count, failed-password/invalid-user pattern, and whether accepted/successful events for that same source exist in the current window. Successful accepted lines from other sources are different-source evidence; include the auth method field when present. Old rotated successes are historical.
- **HTTP/service:** Put affected route, HTTP failure status, and incident time in the finding. Correlate proxy/access evidence with service/backend evidence and one dependency/system layer. Search nearby for actor/driver fields such as client/source, application name, job, worker, fanout, user, pool, active/max, or owner. Explicitly dispose of recovered or older gateway, feature, cache, DNS, dependency, and rotated alternatives. Suggest read-only checks that name the data to inspect, such as connection activity, pool metrics, job/audit metadata, or owner records.
- **Certificates/container layouts:** If a primary log root is empty and a host-mounted root is provided, inspect both in one command and say which root produced evidence. Quote the exact x509/certificate clause, compare current time with validity fields, and cite clock-sync, rotation, renewal, kubelet/syslog, or equivalent system evidence before choosing timing versus certificate material.
- **Sockets:** Use `help ss`, then supported local TCP listening probes. Report local address/port rows when collected; if process flags are absent or unsupported, say process names/PIDs are not available from the supported rshell data.

## Final Answer Contract

Use concise sections:

- `Commands run`: one concise bullet per recorded rshell invocation. Include `./rshell --allow-all-commands`, each literal `--allowed-paths=...` allowlist, and the concrete files or socket probe queried. If a `-c` script is long, summarize its labeled blocks instead of pasting every argument, but do not use `<root>`, `...`, "bounded pass", or any file/command not actually run. Include help output or unsupported-capability evidence when relevant.
- `Finding`: one sentence naming the likely cause plus affected service/check/route/source, exact raw cause token or status when available, and the full incident date/time window when available.
- `Evidence`: concrete files, line numbers, full dates/times, exact decisive message fragments, counts/statuses, IDs/fields, actor/source, downstream impact, and exact "since", recovery, or success markers when they prove duration or state. Keep decisive raw tokens visible; do not paraphrase them away.
- `Not supported`: dispose of misleading hypotheses, historical/rotated matches, recovered noise, different-source successes, missing same-source successes, or unavailable command capabilities.
- `Uncertainty / next checks`: state what is not proven and suggest only safe read-only validation, audit, config/source review, rollback planning, owner follow-up, or capability follow-up. Do not propose remediation commands.

Before finalizing, verify: command bullets map to recorded tool calls; all prompt-provided roots used for reads appear literally; no placeholders remain; the finding includes exact time and affected object when known; decisive raw tokens from the transcript appear in Evidence; each negative claim has a count or runtime/help result; current versus historical/recovered/different-source evidence is labeled; and no unsupported process/PID/socket claims, real-host access claims, inventory-exhaustiveness overclaims, or remediation commands appear.
