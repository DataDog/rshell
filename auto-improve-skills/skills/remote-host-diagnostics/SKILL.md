---
name: datadog/remote-host-diagnostics
description: Safe, fast, transcript-grounded host diagnostics through ./rshell with target help discovery, literal allowed paths, capped probes, exact evidence, and concise answers.
---

# Remote Host Diagnostics

## Hard Rules

- Diagnose only from completed `./rshell --allow-all-commands ...` output. Do not answer from planned commands, prompt facts, repository knowledge, local time, or static capability notes.
- Include `help` in the first rshell call. Use `help <command>` before uncertain flags, after an unsupported-flag failure, and for capability-sensitive commands such as sockets. Production rshell deployments may restrict, omit, or extend features; target `help` is authoritative.
- If diagnostics need files, every rshell call must put the exact literal prompt root in `--allowed-paths=<root>` or `--allowed-paths=<root1>,<root2>` before `-c`; do not substitute parents, shorthands, variables, or old roots. Include primary and fallback roots together from the first file-reading call.
- Keep all work read-only and bounded. Do not write files, install packages, mutate services, restart, kill, delete, use recursive grep, use `find ... -exec grep`, or repeat broad scans over the same files.
- Final answers may describe only recorded calls and output. Never claim real remote/customer-host access unless proven. Never list planned, interrupted, placeholder, reconstructed, shortened-path, `same prefix`, `same allowed path`, `...`, or prose-inside-quote commands.

## Rshell-Safe Scripts

- Run rshell directly from the repository root when possible: `./rshell --allow-all-commands --allowed-paths=<literal-root> -c 'help; R="<literal-root>"; ... "$R/file.log" ...; true'`.
- Define path variables only inside the rshell script. Keep the `-c` script as one quoted string; do not splice host-shell variables, host `$(...)`, or partially quoted host paths.
- Avoid unsupported habits unless `help` proves support: `while`, `read`, `xargs`, arrays, functions, process substitution, background jobs, and external utilities not listed by `help`.
- Prefer explicit selected files and simple globs: `find "$R" -maxdepth 3 -type f | sort | head -n 80`, `wc -l "$R"/*.log "$R"/*/*.log 2>/dev/null || true`, `for f in "$R"/*.log "$R"/*/*.log; do grep -H -n -m 20 -E "tokens" "$f" || true; done`.
- If syntax or flags fail, record that, rerun once with a simpler supported script, and do not treat the failed call as evidence.

## Fast Workflow

Most file/log investigations should finish in two successful calls; socket-only tasks usually finish in one. A third file/log call is only for one named missing proof.

1. **Discover and triage once.** First root-touching call: `help`, inventory each root, cheap line counts, and one bounded theme probe over prompt-named or obvious current/rotated files. For fallback layouts, inventory both roots and record the empty one.
2. **One decisive proof pass.** Select the likely current file, prompt-named rotated/noise files, and at most two corroborating layers. In one labeled script collect exact cause, impact, actor/source, scoped counts for alternatives/lookalikes/recovery/zero claims, and one correlation or aggregation.
3. **Stop or fill one gap.** Before any third call, name the single missing field: driver/source, same-source success absence, downstream impact, dependency/system corroboration, fallback-root proof, certificate time comparison, or supported socket collection. The third call must query only that field, not rediscover files.

Stop once the ledger supports file/root, incident window, affected object, raw cause and impact tokens, driver/source, recovery or absence, alternatives, historical/rotated lookalikes, and selected zero-match layers. Prefer stated uncertainty over another exploratory pass.

## Output Budget

- After inventory, every sample `grep` needs `-m` unless it is a `grep -c` count or tightly filtered aggregation. Use exact objects/statuses/sources, not timestamp-only or `error|warn` sweeps.
- Keep selected-file output to decisive lines plus counts. Avoid large `sed` windows, repeated tails, unbounded `grep | tail`, and whole-log aggregation.
- For aggregation, restrict first: `grep -E "specific tokens" "$file" | sed ... | sort | uniq -c | sort`. Do not spend a call polishing line numbers when prior output proves the finding.

## Evidence Discipline

- Use `probe || true` or final `true` so non-matching greps do not erase useful output.
- Query every prompt-named theory or red herring before rejecting it. Label rotated, previous-window, recovered, fallback, empty-root, or different-object matches instead of calling them zero.
- Negative claims need exact scoped zero counts or help/runtime evidence tied to the queried files and window.
- Preserve observed basenames/paths, lines, timestamps, raw tokens, IDs, routes, checks, sources, users, counts, and key fields. Do not rename values, correct odd paths, infer defaults, or paraphrase away decisive wording.
- Before answering, verify the ledger: command roots, productive file/root, dated incident window, affected object, raw cause, raw impact/status, driver/source, recovery or absence count, rejected alternatives, and safe next check.

## Domain Checks

- **Agent/telemetry:** Separate configuration, credential/auth, intake/APM, queue/aggregator/collector/flush, sibling health, and recovery. Pair cause with delivery impact, raw status/reason, transaction/config/credential fields, exact `since` markers, and current-vs-rotated lookalikes.
- **HTTP/service:** Correlate route and numeric status with app, proxy/access/error, and dependency/system evidence; put route and status in the same sentence and name each layer's basename. For exhaustion, repeat owner/client/fanout plus active/max or native dependency wording. Reject prompt theories with scoped counts or dated recovered lines.
- **Auth/security:** Aggregate failures by source early, then prove the suspicious source's current accepted-success count. Report failed-password/invalid-user pattern, approximate scale, different-source accepted logins, and historical same-source success separately. If zero, explicitly say no current successful or accepted login from that source and include the `0` accepted count scope.
- **Certificates/container fallback:** Inspect primary and fallback roots together. State which root was empty and which produced evidence. Quote the certificate/x509 clause, compare current time with `NotBefore`/`NotAfter` when present, and cite timing, renewal, kubelet/syslog, or equivalent system evidence before choosing timing versus certificate material.
- **Sockets:** Usually one call is enough: `help; help ss; ss -tlnH || ss -tln || true`. Do not try process/PID flags unless `help ss` lists them. If process flags are absent or socket reads fail, say local listening TCP addresses/ports are the supported target when collection succeeds and process/PID attribution is unavailable.

## Final Answer

Use concise sections:

- `Commands run`: one bullet per recorded rshell invocation. Repeat exact `./rshell --allow-all-commands`, literal full `--allowed-paths=...`, and `-c`; summarize long scripts in prose without ellipses or shortened paths.
- `Finding`: one sentence naming likely cause, affected service/check/route/source, actor/driver, raw cause/status token, and full incident window. Include date plus prompt/observed incident minute when supported, and raw line/status/reason/validity/source/count fields when present.
- `Evidence`: cite actual basenames/paths, lines, timestamps, decisive fragments, counts/statuses, IDs/fields, actor/source, impact, recovery, success, `since`, and zero-count markers.
- `Not supported`: prompt theories, historical/rotated matches, recovered noise, different-source successes, missing same-source successes, unavailable capabilities, and selected zero-match layers. Label file/window.
- `Uncertainty / next checks`: state what is not proven and suggest only safe read-only validation, audit, config/source review, rollback planning, owner follow-up, metric review, or capability follow-up. Do not propose remediation commands.

Final check: recorded calls only; literal allowed paths in every command bullet; transcript values only; raw tokens in the finding and evidence; actual file names for each layer; scoped zero-counts; current, historical, recovered, fallback, and different-source evidence labeled; no unsupported socket/PID, real-host, compromise, placeholder-command, invented-token, or remediation claims.
