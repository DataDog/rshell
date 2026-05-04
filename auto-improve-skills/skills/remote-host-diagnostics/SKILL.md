---
name: datadog/remote-host-diagnostics
description: Safe read-only host diagnostics through ./rshell with target help discovery, literal allowed paths, compact evidence collection, and transcript-grounded final answers.
---

# Remote Host Diagnostics

## Non-Negotiables

- Diagnose only through recorded `./rshell --allow-all-commands -c '<script>'` calls. Do not answer from planned commands, prompt facts, repository knowledge, or static capability snapshots.
- For any file read, pass every prompt-provided root literally on every invocation with `--allowed-paths=<root>` or comma-separated roots. If primary and fallback roots are provided, include both from the first inventory onward and state which one produced evidence.
- Start with `help` inside rshell. Add `help <command>` only when flags or behavior matter, and after any unsupported-command failure. Production rshell deployments may restrict, omit, or extend capabilities; target `help` output is authoritative.
- Keep diagnostics read-only and bounded. Do not write files, install packages, mutate services, restart, kill, delete, run recursive grep, use `find ... -exec grep`, or run broad repeated scans.
- Do not claim you connected to, SSHed into, or accessed a real remote/customer host unless the user provided that evidence.
- In the final answer, describe only recorded tool calls. Never list planned, interrupted, placeholder, or reconstructed commands.

## Default Transcript Shape

Use one call for socket-only tasks. For log/file investigations, default to two calls:

1. **Help + inventory.** Run `help`, minimal needed command help, and a shallow inventory of every literal root with `find ... -maxdepth 3 -type f | sort | head -n 80`. If a root is empty, prove that with the inventory. Do not run command help for every common filter unless you need that command's details.
2. **Fused evidence pass.** Select files before searching: the current component log, prompt-named rotated/noise log, and at most two independent layers such as proxy, dependency, system, audit, security, or sibling health. In one labeled script, collect line counts, cause samples/counts, impact/current state, actor/source fields, recovery or same-source success absence, prompt-theory checks, rotated/recovered lookalikes, and one corroborating layer when available.

Use a third call only for one named proof that would change the answer: exact actor/driver, impact duration, same-source success absence, short context around known lines, certificate time comparison, fallback-root proof, or socket recovery after an unsupported flag/runtime failure. Do not add a fourth call just to polish already sufficient evidence.

## Efficient Evidence Scripts

Keep scripts small, labeled with `printf '%s\n' '<label>'`, and resilient with `probe || true` or a final `true` so useful partial output survives a nonzero grep.

Prefer bounded multi-file probes:

- `wc -l file1 file2`
- `grep -H -c -E 'terms' file1 file2`
- `grep -H -n -m 20 -E 'terms' file1 file2`
- `head -n`, `tail -n`, and short `sed -n` only after line numbers are known

Search with incident time tokens, affected object names, source identifiers, status codes, IDs, and cause words from the prompt or earlier output. Once a likely cause appears, stop generic `ERROR`, `WARN`, `status`, or `recovered` sweeps and switch to targeted proof plus alternatives. Avoid `while`, `case`, functions, process substitution, background jobs, recursive grep, `find ... -exec grep`, and complex nested quoting.

## Proof Checklist

Every finding needs transcript evidence for cause, timestamp/window, affected object, consequence, actor/source when present, and negative claims.

- Preserve observed filenames, line numbers, full dates/times, raw error/status tokens, IDs, sources, counts, and key fields. Do not normalize renamed logs or variant service names to familiar defaults.
- Put the supported driver in the finding when fields such as source, user, owner, application name, job, worker, fanout, pool, active/max, credential ID, or transaction ID are present.
- If cause and user-visible impact begin at different times, include both full date/time windows.
- For prompt theories, noise files, rotated logs, recovered entries, and previous-window matches, give a `Not supported` bullet with the count/status/date/source that separates them from the current incident.
- For negative claims, cite exact zero counts or runtime/help output and the queried files/window. Say "not found in the queried files/window" rather than implying exhaustive host-wide absence.
- For auth/security, aggregate by source early, report failed-password/invalid-user patterns, same-source accepted/session-opened count, and accepted successes from different sources separately. Avoid colloquial compromise wording such as "got in"; use "no observed same-source Accepted/session-opened lines" instead.
- For fallback layouts, make the empty primary root and evidence-producing fallback root explicit in `Commands run`, `Evidence`, and `Finding` or `Not supported`.
- For certificates, quote the exact x509/certificate clause, compare observed current time with `NotBefore`/`NotAfter`, and cite clock-sync, rotation, renewal, kubelet/syslog, or equivalent system evidence before choosing timing versus certificate material.

## Domain Hints

- **Agent/telemetry:** Separate config validation, credential/API/auth failures, intake/APM noise, queue/aggregator/collector/flush impact, sibling health, and recovery. Pair the cause with same-incident delivery impact and raw status/reason.
- **HTTP/service:** Name the affected route, HTTP status, incident date/time, backend/dependency failure, and driver. Correlate service/backend evidence with proxy evidence and one system/dependency layer.
- **Sockets:** Run `help; help ss; ss -tlnH || ss -tln || true` unless help shows a better supported query. Do not use process/PID flags unless `help ss` lists them. If process flags are absent or runtime rejects socket reads, say process/PID attribution or socket rows are unavailable and cite the help/runtime output.

## Final Answer Contract

Use concise sections:

- `Commands run`: one bullet per recorded rshell invocation. Include literal `./rshell --allow-all-commands`, every literal `--allowed-paths=...`, concrete selected files or socket probes, and bounded operations such as `wc -l` or `grep -H -n -m N`. Do not use `...`, `<root>`, "same prefix", "same allowed paths", labels that hide the real files, or commands not actually run.
- `Finding`: one sentence naming the likely cause plus affected service/check/route/source, actor/driver when supported, raw cause token/status, and full incident date/time window. Include the prompt's user-visible impact time when it differs from first backend cause evidence.
- `Evidence`: concrete files, line numbers, full dates/times, decisive message fragments, counts/statuses, IDs/fields, actor/source, downstream impact, and exact recovery, success, "since", or zero-count markers.
- `Not supported`: dispose of misleading hypotheses, historical/rotated matches, recovered noise, different-source successes, missing same-source successes, unavailable command capabilities, and prompt-suggested theories not supported by the queried files.
- `Uncertainty / next checks`: state what is not proven and suggest only safe read-only validation, audit, config/source review, rollback planning, owner follow-up, or capability follow-up. Do not propose remediation commands.

Final check: command bullets map to recorded tool calls; literal prompt roots appear in every file-reading invocation; observed names and values are not normalized; the finding has exact time/object/cause/driver when known; negative claims have count/help evidence and scope; current, historical, recovered, fallback, and different-source evidence are labeled; no placeholders, unsupported socket/PID claims, real-host access claims, exhaustive-inventory overclaims, compromise overclaims, or remediation commands remain.
