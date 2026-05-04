---
name: datadog/remote-host-diagnostics
description: Safe read-only host diagnostics through ./rshell with target help discovery, literal allowed paths, compact evidence collection, and transcript-grounded final answers.
---

# Remote Host Diagnostics

## Operating Rules

- Diagnose only from recorded `./rshell --allow-all-commands -c '<script>'` calls. Do not answer from planned commands, prompt facts, repository knowledge, or static capability snapshots.
- For file reads, every invocation must include every prompt-provided root literally in `--allowed-paths=<root>` or one comma-separated `--allowed-paths=<root1>,<root2>`. When primary and fallback roots are supplied, include both from the first call, inspect both, and state which root produced evidence.
- Start with `help` inside rshell. Run `help <command>` before relying on uncertain flags and after any unsupported-command failure. Production rshell builds may restrict, omit, or extend commands; target `help` output is authoritative.
- Keep diagnostics read-only and bounded. Do not write files, install packages, mutate services, restart, kill, delete, run recursive grep, use `find ... -exec grep`, or repeat broad scans.
- Do not claim you connected to, SSHed into, or accessed a real remote/customer host unless the user supplied that evidence.
- In the final answer, describe only recorded tool calls and transcript evidence. Never list planned, interrupted, placeholder, shortened, or reconstructed commands.

## Fast Workflow

Most log/file investigations should finish in two rshell calls.

1. **Discovery call.** Run `help`; inventory each literal root with a labeled `find <root> -maxdepth 3 -type f | sort | head -n 80`. If a root has only marker files or no logs, say it produced no diagnostic log evidence instead of treating it as evidence. Do not infer dates from the local environment; if the prompt gives only a clock time, search that clock token to discover the log date.
2. **Plan selected files before searching.** Choose the likely current component/security/app log, any prompt-named rotated or noise log, and at most two corroborating layers such as proxy, dependency, system, audit, sibling service, or health logs. Keep this file list small enough that the next command can be quoted verbatim in the final answer.
3. **Fused evidence call.** Use one labeled script over the selected files to gather: line counts; incident-window samples; targeted cause and impact counts; actor/source/driver fields; prompt-theory and historical/recovered lookalike checks; and one corroborating layer or current-state/recovery check.

Use a third call only when one missing proof would change the conclusion, such as exact driver, impact duration, same-source success absence, short context around known lines, certificate time comparison, fallback-root proof, or socket recovery after unsupported flags/runtime failure. Do not add another call just to collect more examples of an already proven point.

## Efficient Probes

Keep scripts small, labeled with `printf '%s\n' '<label>'`, and resilient with `probe || true` or a final `true` so partial output survives a nonzero grep.

Preferred bounded probes:

- `wc -l file1 file2`
- `grep -H -c -E 'terms' file1 file2`
- `grep -H -n -m 20 -E 'terms' file1 file2` or `-m 40` when several files share one pattern
- `head -n`, `tail -n`, and short `sed -n` only after line numbers are known
- For auth summaries, `grep ... | sort | uniq -c | sort` is acceptable when scoped to selected files and terms

Search first with prompt terms and observed tokens: clock strings, affected objects, sources, status codes, IDs, cause words, and actor fields. Once a likely cause appears, stop generic `ERROR`, `WARN`, `status`, or `recovered` sweeps and switch to exact tokens plus alternatives.

Avoid output blowups: no root-wide recursive grep; no more than about six labeled probes in the fused call; no repeated searches over unchanged files; no long context dumps; no unsupported shell features such as `while`, `case`, functions, process substitution, background jobs, or complex nested quoting.

## Evidence Rules

Every finding needs transcript evidence for cause, timestamp/window, affected object, consequence, actor/source when present, and negative claims.

- Preserve observed filenames, line numbers, full dates/times, raw error/status tokens, IDs, sources, counts, and key fields. Do not normalize renamed logs, routes, services, or files to familiar defaults.
- Put supported drivers in the finding when fields such as source, user, owner, application name, job, worker, fanout, pool, active/max, credential ID, or transaction ID are present.
- If cause and user-visible impact start at different times, include both full date/time windows.
- Dispose of every prompt-named theory or noise source in `Not supported`, using scoped counts, status, date/window, source, and recovery state when available.
- For negative claims, cite exact zero counts or help/runtime output and the queried files/window. Phrase auth negatives plainly: `No successful/Accepted login from <source> was found in <file/window>`. List accepted successes from other sources separately.
- For fallback layouts, make the primary root and evidence-producing fallback root explicit in `Commands run`, `Evidence`, and `Finding` or `Not supported`.
- For certificates, quote the exact x509/certificate clause, compare observed current time with `NotBefore`/`NotAfter`, and cite clock-sync, rotation, renewal, kubelet/syslog, or equivalent system evidence before choosing timing versus certificate material.

## Domain Hints

- **Agent/telemetry:** Separate config validation, credential/API/auth failures, intake/APM noise, queue/aggregator/collector/flush impact, sibling health, and recovery. Pair the cause with same-incident delivery impact, raw status/reason, and any `since` marker.
- **HTTP/service:** Name the affected route, HTTP status, incident window, backend/dependency failure, and driver. Correlate service evidence with proxy evidence and one system/dependency layer. Reject gateway, feature-flag, cache, DNS, and historical 5xx theories with scoped counts or dates in one compact `Not supported` bullet when possible.
- **Auth/security:** Aggregate failures by source early, report failed-password/invalid-user patterns and approximate scale, then prove same-source accepted/session-opened count and different-source accepted logins. Avoid compromise wording unless success from the same source is recorded.
- **Sockets:** Run `help; help ss; ss -tlnH || ss -tln || true` unless help shows a better supported query. Do not use process/PID flags unless `help ss` lists them. If process flags are absent or runtime rejects socket reads, say local listening TCP addresses/ports are the supported target when collection succeeds, while process/PID attribution is unavailable; cite help/runtime output.

## Final Answer Contract

Use concise sections:

- `Commands run`: one bullet per recorded rshell invocation. Paste the complete literal `./rshell --allow-all-commands` command with every literal `--allowed-paths=...`, selected files or socket probe, and bounded operations. Do not use `same`, `...`, `<root>`, shell variables, "same prefix", or labels that hide real roots/files.
- `Finding`: one sentence naming the likely cause plus affected service/check/route/source, actor/driver when supported, raw cause token/status, and full incident date/time window. Include the prompt's user-visible impact time when it differs from first backend cause evidence.
- `Evidence`: concrete files, line numbers, full dates/times, decisive message fragments, counts/statuses, IDs/fields, actor/source, downstream impact, and exact recovery, success, `since`, or zero-count markers.
- `Not supported`: misleading hypotheses, historical/rotated matches, recovered noise, different-source successes, missing same-source successes, unavailable command capabilities, and prompt-suggested theories not supported by the queried files/window.
- `Uncertainty / next checks`: state what is not proven and suggest only safe read-only validation, audit, config/source review, rollback planning, owner follow-up, metric review, or capability follow-up. Do not propose remediation commands.

Final check: command bullets map to recorded tool calls; file-reading command bullets include literal prompt roots; no command bullet uses placeholders; observed names and values are preserved; the finding has exact time/object/cause/driver when known; negative claims use zero counts and scope; current, historical, recovered, fallback, and different-source evidence are labeled; no unsupported socket/PID claims, real-host access claims, exhaustive-inventory claims, compromise claims, or remediation commands remain.
