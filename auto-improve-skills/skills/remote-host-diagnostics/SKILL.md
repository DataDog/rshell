---
name: datadog/remote-host-diagnostics
description: Safe read-only host diagnostics through ./rshell with target help discovery, literal allowed paths, compact evidence collection, and transcript-grounded final answers.
---

# Remote Host Diagnostics

## Rules

- Diagnose only from recorded `./rshell --allow-all-commands ...` output. Do not answer from planned commands, prompt facts, repository knowledge, or static capability snapshots.
- If the prompt gives file/log roots, every rshell invocation that mentions or reads those roots must include every root literally in `--allowed-paths=<root>` or `--allowed-paths=<root1>,<root2>` before `-c`. Make the first root-touching call sandboxed too: combine `help` with inventory. A help-only call without allowed paths is acceptable only when no path/root is touched.
- If primary and fallback roots are supplied, include both from the first file-reading call, inspect both, and state which root produced evidence.
- Start with `help` inside rshell. Use `help <command>` before uncertain flags and after unsupported-command failures. Production builds may restrict, omit, or extend commands; target `help` is authoritative.
- Keep diagnostics read-only and bounded. Do not write files, install packages, mutate services, restart, kill, delete, use recursive grep, use `find ... -exec grep`, or repeat broad scans.
- Do not claim you connected to, SSHed into, or accessed a real remote/customer host unless the user supplied that evidence.
- Keep host-shell quoting simple: prefer one double-quoted `-c` semicolon script with `help`, labeled `printf` sections, bounded probes, and a final `true`. Use literal paths in the rshell script; if variables help, define and expand them inside rshell only. If quoting fails locally, rerun one corrected bounded call and ignore the failed output.
- Final answers may describe only recorded, completed tool calls and transcript evidence. Never list planned, interrupted, placeholder, "same prefix", or reconstructed commands.

## Fast Workflow

Most log/file investigations should finish in two successful rshell calls.

1. **Discover.** If roots are provided, run a sandboxed first call that includes `help` and inventories each literal root with labeled `find <root> -maxdepth 3 -type f | sort | head -n 80`. Do not run a separate unsandboxed inventory. If a root has only marker files or no logs, say it produced no diagnostic log evidence. Do not infer dates from local time; if only a clock is known, search that clock token to discover the log date.
2. **Select files.** Choose the likely current component/security/app log, any prompt-named rotated or noise log, and at most two corroborating layers such as proxy, dependency, system, audit, or health logs.
3. **Fuse evidence.** Use one labeled script over selected files. Aim for four to five labels: selected-file line counts when useful; one incident-window or prompt-term sample; exact cause/impact/driver samples; compact counts for prompt theories, historical/recovered lookalikes, scale, and negative claims; and one corroborating or recovery check. Combine cause, impact, actor/source/driver, status, and prompt terms in one sample grep before adding exact counts.

Use a third call only when one missing proof could change the conclusion: driver/source attribution, dependency/system corroboration, impact duration, same-source success absence, certificate time comparison, fallback-root proof, or socket recovery. The third call should answer only that proof.
Stop after cause, impact, window, affected object, driver/source when present, and misleading theories are supported. Do not run another broad pass just to look exhaustive.

## Efficient Probes

Use small labeled scripts with `probe || true` or final `true` so partial output survives nonzero grep.

- `wc -l file1 file2`
- `grep -H -n -m 20 -E 'terms' file1 file2` or `-m 40` when several files share one pattern
- `grep -H -c -E 'exact|alternative|negative' file1 file2`
- `head -n`, `tail -n`, or short `sed -n` only after line numbers are known
- scoped `grep ... | sort | uniq -c | sort` for source counts when target help supports the needed flags

Search first with prompt terms and observed tokens: clocks, affected objects, sources, status codes, IDs, cause words, route/check names, and actor fields. Do not run both broad count and broad sample probes unless the count supports a negative claim; sample broad terms first, then count exact alternatives. Once a likely cause appears, stop generic sweeps.

Avoid output blowups: no root-wide recursive grep, repeated searches over unchanged files, long dumps, wide `sed` windows before line numbers are known, unsupported shell features, or complex nested quoting. Prefer decisive `grep -m` line evidence plus exact `grep -c` negatives over dumping large time ranges.

## Evidence Rules

Every finding needs transcript evidence for cause, timestamp/window, affected object, consequence, actor/source when present, and negative claims.

- Preserve observed filenames, line numbers, full dates/times, raw error/status tokens, IDs, sources, counts, routes, check/service names, and key fields. Do not normalize renamed values to familiar defaults.
- Put supported drivers in the finding when fields such as source, user, owner, app, job, worker, fanout, pool, active/max, credential ID, or transaction ID are present.
- If cause and user-visible impact start at different times, include both full date/time windows.
- Dispose of every prompt-named theory or noise source in `Not supported`, using scoped counts, status, date/window, source, and recovery state when available.
- Negative claims need exact zero counts or help/runtime output with queried files/window. For auth, say `No successful/Accepted login from <source> was found in <file/window> (Accepted count=0 ...)` and list accepted successes from other sources separately.
- For fallback layouts, make the primary root and evidence-producing fallback root explicit in `Commands run`, `Finding`, `Evidence`, and `Not supported`.
- For certificates, quote the exact x509/certificate clause in the finding or evidence, compare current time with `NotBefore`/`NotAfter`, and cite clock-sync, rotation, renewal, kubelet/syslog, or equivalent system evidence before choosing timing versus certificate material.
- For multi-layer incidents, cite separate app/component, user-visible/proxy, and dependency/system/audit evidence when those layers exist. If a selected layer has no matches, say so in `Not supported`.

## Domain Hints

- **Agent/telemetry:** Separate config, credential/API/auth, intake/APM, queue/aggregator/collector/flush, sibling health, and recovery. Pair cause with same-incident delivery impact, raw status/reason, transaction/config/credential fields, and any exact `since` marker.
- **HTTP/service:** Name the affected route, HTTP status, incident window, backend/dependency failure, and driver. Put the route and numeric status near each other in the finding, then correlate service evidence with proxy evidence and one dependency/system line or count. When resource exhaustion appears, look for owner/client/fanout and saturation fields before finalizing. Reject gateway, feature-flag, cache, DNS, and historical 5xx theories with scoped counts or dates.
- **Auth/security:** Aggregate failures by observed source early, report failed-password/invalid-user patterns and approximate scale, then prove same-source accepted/session-opened count and different-source accepted logins. Avoid compromise wording without same-source success.
- **Certificates/container fallback:** Inspect primary and fallback roots in the same sandboxed call. In the finding, name the affected check/service, exact certificate error class, evidence-producing root type, and whether evidence points to environment timing or certificate material.
- **Sockets:** Run `help; help ss; ss -tlnH || ss -tln || true` unless help shows a better query. Do not use process/PID flags unless `help ss` lists them. If process flags are absent or socket reads fail, say local listening TCP addresses/ports are the supported target when collection succeeds and process/PID attribution is unavailable; cite help/runtime output.

## Final Answer

Use concise sections:

- `Commands run`: one bullet per recorded rshell invocation. Include the exact `./rshell --allow-all-commands` prefix, every literal `--allowed-paths=...`, actual selected files or socket command, and bounded operation labels/types. Keep long scripts readable by summarizing regex themes, but do not use ellipses, placeholders, "same prefix", fake roots, or omitted filenames.
- `Finding`: one sentence naming the likely cause plus affected service/check/route/source, actor/driver when supported, raw cause token/status, and full incident date/time window. Include user-visible impact time when it differs from first backend cause evidence.
- `Evidence`: concrete files, line numbers, full dates/times, decisive fragments, counts/statuses, IDs/fields, actor/source, downstream impact, and exact recovery, success, `since`, or zero-count markers.
- `Not supported`: misleading hypotheses, historical/rotated matches, recovered noise, different-source successes, missing same-source successes, unavailable command capabilities, and prompt-suggested theories not supported by the queried files/window.
- `Uncertainty / next checks`: state what is not proven and suggest only safe read-only validation, audit, config/source review, rollback planning, owner follow-up, metric review, or capability follow-up. Do not propose remediation commands.

Final check: command bullets map to recorded calls; every file-reading bullet includes literal prompt roots; observed values are preserved; finding has exact time/object/cause/impact/driver when known; negative claims use zero counts and scope; current, historical, recovered, fallback, and different-source evidence are labeled; no unsupported socket/PID, real-host, exhaustive-inventory, compromise, placeholder-command, or remediation claims remain.
