---
name: datadog/remote-host-diagnostics
description: Safe read-only host diagnostics through ./rshell with target help discovery, literal allowed paths, compact evidence collection, and transcript-grounded final answers.
---

# Remote Host Diagnostics

## Hard Rules

- Diagnose only from completed `./rshell --allow-all-commands ...` output. Do not answer from planned commands, prompt facts, repository knowledge, local time, or static capability notes.
- Start discovery inside the target with `help`. Run `help <command>` before uncertain flags and after unsupported-flag failures. Production rshell builds may restrict, omit, or extend capabilities; target `help` is authoritative.
- When the prompt gives file/log roots, every rshell call that mentions or reads them must include the literal roots in `--allowed-paths=<root>` or `--allowed-paths=<root1>,<root2>` before `-c`. Do not hide allowed paths behind host-shell variables. If primary and fallback roots are supplied, include and inspect both from the first file-reading call, then state which root produced evidence.
- Keep diagnostics read-only and bounded. Do not write files, install packages, mutate services, restart, kill, delete, use recursive grep, use `find ... -exec grep`, or repeat broad scans over the same files.
- Do not claim you connected to, SSHed into, or accessed a real remote/customer host unless the transcript proves that exact fact.
- Prefer a single-quoted rshell script: `-c 'help; printf "\n== label ==\n"; ...; true'`. Use double quotes inside the script for labels and regexes. Keep paths literal, or define variables only inside that single-quoted script. If quoting or a flag fails, run one corrected bounded call and ignore the failed output as evidence.
- Final answers may describe only recorded calls. Do not list planned, interrupted, placeholder, reconstructed, `same prefix`, shortened-path, or `...` commands.

## Fast Workflow

Most file/log investigations should finish in two successful rshell calls, and should almost never need more than three successful file/log calls.

1. **Discover and inventory.** If roots are provided, make the first root-touching call sandboxed and include `help` plus a labeled inventory for each root: `find <root> -maxdepth 3 -type f | sort | head -n 80`. A help-only call without allowed paths is acceptable only when no root is touched. If a root has no diagnostic logs, say so explicitly.
2. **Select evidence files.** Pick the likely current component/security/app log, any prompt-named rotated or noise log, and at most two corroborating layers such as proxy, dependency, system, audit, health, or host-mounted logs. Do not inventory again unless the first inventory was incomplete.
3. **Fuse proof in one call.** Run one labeled script over selected files that captures:
   - line counts or short heads/tails only when useful for scope;
   - one bounded `grep -H -n -m 80 -E`-style sample combining prompt terms, incident clock tokens, affected objects, status/error tokens, cause words, actor/source fields, IDs, and recovery words;
   - `grep -H -c -E` counts for exact alternatives, prompt theories, negative claims, and historical/recovered lookalikes;
   - one focused correlation or recovery check.

Use a third call only when one missing proof can change the conclusion: source/driver attribution, same-source success absence, downstream impact, dependency/system corroboration, fallback-root proof, certificate time comparison, or supported socket collection. Before running it, name the single missing field it will answer. Do not run a fourth file/log call unless a previous command failed before producing usable evidence.

Stop when cause, time window, affected object, consequence, supported driver/source, and prompt-suggested alternatives are covered. Do not run another broad pass to look exhaustive.

## Probe Discipline

- Use `probe || true` or final `true` so a non-matching grep does not erase useful output.
- Search first with prompt terms and observed tokens. Once a likely cause appears, pivot to exact cause/impact/source/recovery counts instead of more generic sweeps.
- Prefer `grep -H -n -m`, `grep -H -c`, `wc -l`, `head`, `tail`, and short `sed -n` after line numbers are known. Do not set `-m` so low that decisive current, impact, or recovery lines can be skipped; narrow the regex instead of repeating broad searches.
- For source concentration, use selected-file filters plus `sort | uniq -c | sort` when target help supports the flags.
- Avoid output blowups: no root-wide recursive grep, long dumps, wide `sed` windows before line numbers, repeated unchanged searches, unsupported shell features, or complex nested host-shell quoting.

## Evidence Requirements

Every finding needs transcript evidence for cause, timestamp/window, affected object, consequence, driver/source when present, and each negative claim.

- Preserve observed filenames, line numbers, dates/times, raw status/error tokens, IDs, routes, check names, sources, users, counts, and key fields. Do not rename values to familiar defaults, correct odd-looking paths, or infer missing values.
- Put supported drivers in the finding when fields such as source, user, owner, app, job, worker, fanout, pool, active/max, credential ID, transaction ID, or route are present.
- If cause and user-visible impact start at different times, include both full windows.
- Dispose of every prompt-named theory or noise source in `Not supported` with scoped counts, dates/windows, source, status, and recovery state when available.
- Negative claims need exact zero counts or help/runtime evidence with the queried files/window. For authentication, use explicit wording: no successful/accepted login from the suspicious source in the current file/window; accepted successes were from other sources, with those sources listed separately.
- For fallback layouts, make the empty/primary root and evidence-producing fallback root explicit in `Commands run`, `Finding`, `Evidence`, and `Not supported`.
- For certificate failures, quote the exact certificate/x509 clause, compare observed current time with validity bounds when present, and cite timing, rotation, kubelet/syslog, renewal, or equivalent system evidence before choosing environment timing versus certificate material.
- For multi-layer incidents, cite separate component/app, user-visible/proxy, and dependency/system/audit evidence when those layers exist. If a selected layer has no matches, say so in `Not supported`.

Before finalizing, check a small transcript ledger: evidence file/root, timestamp/window, object, raw cause token, impact token, driver/source, recovery state, and rejected theories. If a field is absent, say it is not proven instead of filling it from defaults or the prompt.

## Domain Focus

- **Agent/telemetry:** Separate configuration, credential/auth, intake/APM, queue/aggregator/collector/flush, sibling health, and recovery. Pair the cause with same-incident delivery impact, raw status/reason, transaction/config/credential fields, and any exact `since` marker.
- **HTTP/service:** Name route, numeric status, incident window, backend/dependency failure, and supported driver. Correlate service with proxy and one dependency/system line or count. For resource exhaustion, look for owner/client/fanout and saturation. Reject gateway, feature-flag, cache, DNS, and historical 5xx theories with scoped counts or dates; if a historical/rotated error exists and recovered, say so.
- **Auth/security:** Aggregate failures by source early, report failed-password/invalid-user patterns and approximate scale, then prove same-source success absence and different-source accepted logins. Avoid compromise wording without same-source success evidence.
- **Certificates/container fallback:** Inspect primary and fallback roots in the same sandboxed call. Name the affected check/service, exact certificate error class, evidence-producing root type, and whether evidence points to environment timing or certificate material.
- **Sockets:** One successful call is usually enough: run `help; help ss; ss -tlnH || ss -tln || true` unless help shows a better supported query. Do not try suggested process/PID flags unless `help ss` lists them. If process flags are absent or socket reads fail, say local listening TCP addresses/ports are the supported target when collection succeeds and process/PID attribution is unavailable; cite help/runtime output.

## Final Answer

Use concise sections:

- `Commands run`: one bullet per recorded rshell invocation. Each bullet stands alone with the exact `./rshell --allow-all-commands` prefix, every literal `--allowed-paths=...`, selected files or socket query, and bounded operation labels/types. For long scripts, summarize labels and regex themes rather than writing fake code; never use ellipses, placeholders, omitted filenames, shortened paths, or `same prefix`.
- `Finding`: one sentence naming the likely cause, affected service/check/route/source, supported actor/driver, raw cause/status token, and full incident window. Include user-visible impact time when different. Use only values copied from transcript evidence.
- `Evidence`: concrete files, line numbers, full dates/times, decisive fragments, counts/statuses, IDs/fields, actor/source, downstream impact, and exact recovery, success, `since`, or zero-count markers.
- `Not supported`: prompt theories, historical/rotated matches, recovered noise, different-source successes, missing same-source successes, unavailable capabilities, and selected layers with no matches.
- `Uncertainty / next checks`: state what is not proven and suggest only safe read-only validation, audit, config/source review, rollback planning, owner follow-up, metric review, or capability follow-up. Do not propose remediation commands.

Final check: recorded calls only; literal allowed paths; finding fields copied from transcript; scoped zero-counts for negative claims; current, historical, recovered, fallback, and different-source evidence labeled; no unsupported socket/PID, real-host, compromise, placeholder-command, shortened-path, invented-token, or remediation claims.
