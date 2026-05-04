---
name: datadog/remote-host-diagnostics
description: Safe read-only host diagnostics through ./rshell with target help discovery, literal allowed paths, compact evidence collection, and transcript-grounded final answers.
---

# Remote Host Diagnostics

## Hard Rules

- Diagnose only from completed `./rshell --allow-all-commands ...` output. Do not answer from planned commands, prompt facts, repository knowledge, local time, or static capability notes.
- Include `help` in the first rshell call. Use `help <command>` before uncertain flags or after an unsupported-flag failure. Production rshell builds may restrict, omit, or extend capabilities; target `help` is authoritative.
- If the prompt gives file/log roots, every rshell call that mentions them must include literal `--allowed-paths=<root>` or `--allowed-paths=<root1>,<root2>` before `-c`. Do not hide allowed paths in host-shell variables. If primary and fallback roots are supplied, include both from the first file-reading call.
- Keep diagnostics read-only and bounded. Do not write files, install packages, mutate services, restart, kill, delete, use recursive grep, use `find ... -exec grep`, or repeat broad scans over the same files.
- Prefer a single-quoted script: `-c 'help; printf "\n== label ==\n"; ...; true'`. Use double quotes inside the script. Keep paths literal, or define variables only inside that script. If quoting or flags fail, run one corrected bounded call and ignore the failed output as evidence.
- Final answers may describe only recorded calls. Never claim real remote/customer-host access unless proven, and never list planned, interrupted, placeholder, reconstructed, shortened-path, `same prefix`, or `...` commands.

## Fast Workflow

Most file/log investigations should finish in **two successful rshell calls**. A third successful file/log call is only for one named proof that can change the conclusion.

1. **Discover and inventory.** First root-touching call: `help`, then for each root `find <root> -maxdepth 3 -type f | sort | head -n 80`. If a root has no diagnostic logs, record that explicitly.
2. **One evidence pass.** Select the likely current log, any prompt-named rotated/noise log, and at most two corroborating layers such as proxy, dependency, system, audit, health, or host-mounted logs. In one labeled script over only those files, collect optional scope (`wc -l`, `head`, or `tail`), one bounded `grep -H -n -m <limit> -E` sample, exact `grep -H -c -E` counts for alternatives/negative claims/lookalikes/recovery, and one cause-to-impact or cross-layer correlation.
3. **Stop or fill one gap.** Do not run broad, exact, and polishing passes over the same files. Use a third call only for a single missing field such as driver/source attribution, same-source success absence, downstream impact, dependency/system corroboration, fallback-root proof, certificate time comparison, or supported socket collection. Name the field before running it.

Stop once cause, window, affected object, consequence, supported driver/source, and prompt-suggested alternatives are covered. Prefer saying a field is not proven over running another broad search.

## Probe Discipline

- Use `probe || true` or final `true` so non-matching greps do not erase useful output.
- Start with prompt terms plus incident time, affected objects, raw status/error tokens, cause words, actor/source fields, IDs, and recovery words. After a likely cause appears, pivot to exact cause/impact/source/recovery counts.
- Query every prompt-named red herring before rejecting it. Label historical/rotated lookalikes as historical or recovered rather than reporting them as zero.
- Keep output small: no root-wide recursive grep, long dumps, wide `sed` windows, repeated unchanged searches, unnecessary help pages, unsupported shell features, or complex nested host-shell quoting. If a grep line has enough context, do not add `sed`.
- For source concentration, aggregate early with selected-file filters plus `sort | uniq -c | sort` when supported, then print only the dominant source's first/last lines and accepted-success lines.

## Evidence Requirements

Every finding needs transcript evidence for cause, timestamp/window, affected object, consequence, driver/source when present, and each negative claim.

- Preserve observed filenames, line numbers, dates/times, raw tokens, IDs, routes, check names, sources, users, counts, and key fields. Do not rename values, correct odd paths, or infer defaults.
- Put supported drivers in the finding when fields such as source, user, owner, app, job, worker, fanout, pool, active/max, credential ID, transaction ID, or route are present.
- Include cause time and user-visible impact time when they differ. For access/proxy logs, copy raw timestamps and also state the supported incident window in prose.
- Quote exact impact markers when observed: `since` clauses, no metrics/logs flushed, payload dropped, queue paused, request failed, check failing, or raw status codes.
- Dispose of every prompt-named theory or noise source in `Not supported` with scoped counts, dates/windows, source, status, and recovery state when available.
- Negative claims require exact zero counts or help/runtime evidence with the queried files/window. For auth, write explicitly: no successful/accepted login from the suspicious source in the current file/window; accepted successes were from different sources, with method, account, and source copied from the transcript.
- Fallback layouts: state the empty/primary root, evidence-producing fallback root, and that the conclusion uses host-mounted/fallback evidence in `Commands run`, `Finding`, `Evidence`, and `Not supported`.
- Certificate failures: quote the exact certificate/x509 clause, compare observed current time with validity bounds when present, and cite timing, rotation, kubelet/syslog, renewal, or equivalent system evidence before choosing timing versus certificate material.
- Multi-layer incidents: cite separate component/app, user-visible/proxy, and dependency/system/audit evidence when available. Include native dependency wording, not only the app summary; note selected layers with no matches.

Before finalizing, check this ledger: file/root, window, object, raw cause token, impact token, driver/source, recovery state, prompt alternatives, historical/rotated lookalikes, and selected layers with no matches.

## Domain Focus

- **Agent/telemetry:** Separate configuration, credential/auth, intake/APM, queue/aggregator/collector/flush, sibling health, and recovery. Pair the cause with same-incident delivery impact, raw status/reason, transaction/config/credential fields, and exact `since` markers. Enumerate same-object historical lookalikes separately when IDs, lines, dates, or recovery states differ.
- **HTTP/service:** Name route, numeric status, incident window, backend/dependency failure, and supported driver. Correlate service with proxy and dependency/system/audit evidence when available. For resource exhaustion, look for owner/client/fanout, active/max or equivalent saturation, and dependency-native error clauses. Reject gateway, feature-flag, cache, DNS, and historical 5xx theories with scoped counts or dates.
- **Auth/security:** Aggregate failures by source early, report failed-password/invalid-user patterns and approximate scale, then prove same-source success absence and different-source accepted logins. Avoid compromise wording without same-source success evidence.
- **Certificates/container fallback:** Inspect primary and fallback roots in the same sandboxed call. Name the affected check/service, exact certificate class, evidence-producing root type, and whether evidence points to environment timing or certificate material.
- **Sockets:** One successful call is usually enough: run `help; help ss; ss -tlnH || ss -tln || true` unless help shows a better supported query. Do not try suggested process/PID flags unless `help ss` lists them. If process flags are absent or socket reads fail, say local listening TCP addresses/ports are the supported target when collection succeeds and process/PID attribution is unavailable; cite help/runtime output.

## Final Answer

Use concise sections:

- `Commands run`: one bullet per recorded rshell invocation. Repeat the exact `./rshell --allow-all-commands` prefix, literal `--allowed-paths=...`, selected files or socket query, and bounded operation labels/types. For long scripts, summarize labels and regex themes; never use ellipses, placeholders, omitted filenames, shortened paths, or `same prefix`.
- `Finding`: one sentence naming the likely cause, affected service/check/route/source, supported actor/driver, raw cause/status token, and full incident window. Include user-visible impact time when different.
- `Evidence`: files, line numbers, dates/times, decisive fragments, counts/statuses, IDs/fields, actor/source, downstream impact, and exact recovery, success, `since`, or zero-count markers.
- `Not supported`: prompt theories, historical/rotated matches, recovered noise, different-source successes, missing same-source successes, unavailable capabilities, and selected layers with no matches.
- `Uncertainty / next checks`: state what is not proven and suggest only safe read-only validation, audit, config/source review, rollback planning, owner follow-up, metric review, or capability follow-up. Do not propose remediation commands.

Final check: recorded calls only; literal allowed paths; transcript values only; scoped zero-counts; current, historical, recovered, fallback, and different-source evidence labeled; no unsupported socket/PID, real-host, compromise, placeholder-command, shortened-path, invented-token, or remediation claims.
