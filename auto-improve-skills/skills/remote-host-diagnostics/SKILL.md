---
name: datadog/remote-host-diagnostics
description: Safe, fast, transcript-grounded host diagnostics through ./rshell with target help discovery, literal allowed paths, capped evidence passes, and concise answers.
---

# Remote Host Diagnostics

## Non-Negotiables

- Base every conclusion on completed `./rshell --allow-all-commands ...` output, not prompt hints, repository knowledge, local host state, static capability notes, or planned commands.
- Put `help` in the first rshell call. Use `help <command>` before uncertain flags, after unsupported-flag failures, and for capability-sensitive commands. Production rshell deployments may restrict, omit, or extend features; target `help` is authoritative.
- If files are needed, every rshell call must include the exact literal prompt root in `--allowed-paths=<root>` before `-c`. For primary/fallback layouts, include both literal roots from the first file-reading call. Do not substitute parents, variables, shorthands, or previous roots.
- Keep diagnostics read-only and bounded. Do not write files, install packages, mutate services, restart, kill, delete, use recursive grep, use `find ... -exec grep`, or repeat broad scans over the same files.
- Failed syntax/flag probes are not evidence about the incident. Record the failure, simplify once with supported syntax, and ground findings only in productive output.
- Final answers may describe only recorded calls and output. Never claim real remote/customer-host access unless the transcript proves it. Never list placeholder, shortened-path, reconstructed, `same allowed path`, prose-inside-quote, or ellipsis commands.

## Fast Investigation Shape

Most file/log tasks should finish in two successful rshell calls; socket-only tasks usually finish in one. A third file/log call is only for one named missing proof: source/driver, same-source success absence, recovery absence, fallback-root proof, certificate timing, downstream impact, or a supported socket detail.

1. **Inventory once.** First root-touching call: `help`, shallow inventory for each root, line counts, and one bounded theme probe over prompt-named or obvious current/rotated files. Label empty and fallback roots immediately.
2. **Prove once.** Second call: assign literal file variables from inventory, then collect exact cause tokens, impact/status, driver/source fields, scoped alternative/recovery counts, and one correlation or aggregation. Include prompt-named theories and red herrings in this same call.
3. **Stop or fill one gap.** Before any third call, name the single missing ledger field and query only that field. Prefer a clear uncertainty statement over another discovery pass.

Stop when the ledger supports: productive root/file, incident window, affected object, raw cause and impact/status tokens, driver/source if present, recovery or absence, rejected alternatives, historical/rotated matches, and selected zero-count layers.

## Rshell Script Pattern

- Run from the repository root when possible:
  `./rshell --allow-all-commands --allowed-paths=<literal-root>[,<literal-root>] -c 'help; R="<literal-root>"; ...; true'`
- Define path variables only inside the rshell script. Keep `-c` as one quoted string; do not splice host-shell variables, host `$(...)`, or partially quoted host paths.
- The recorded shell command should contain the literal root inside both `--allowed-paths=` and the rshell script. Avoid nested host-shell quoting patterns that leave `'$R'` or host-expanded command substitutions in the transcript.
- Stay within target `help`: avoid `while`, `read`, `xargs`, arrays, functions, process substitution, background jobs, and external utilities not listed by `help`.
- Prefer selected files and simple globs:
  `find "$R" -maxdepth 3 -type f | sort | head -n 80`
  `wc -l "$R"/*.log "$R"/*/*.log "$R"/*/*/*.log 2>/dev/null || true`
  `for f in "$R"/*.log "$R"/*/*.log "$R"/*/*/*.log; do grep -H -n -m 25 -E "specific|tokens" "$f" || true; done`
- If a glob probe reports literal-glob open errors or misses obvious files, do not retry the same broad glob. Use the literal filenames printed by inventory in the proof call.
- After inventory, every sample `grep` needs `-m` unless it is a `grep -c` count or tight aggregation. Avoid large `sed` windows, unbounded `grep | tail`, and full-date regexes until the date prefix is observed in output.
- Use `probe || true` or final `true` so non-matching greps do not discard useful output.

## Evidence Discipline

- Query every prompt-named theory or red herring before rejecting it. Label current, rotated, previous-window, recovered, fallback, empty-root, different-object, and different-source evidence.
- Negative claims require exact scoped zero counts or help/runtime evidence tied to the queried files and window.
- Preserve observed basenames/paths, lines, timestamps, raw tokens, IDs, routes, checks, sources, users, counts, and key fields. Do not rename values, correct odd paths, infer defaults, or paraphrase away decisive wording.
- Use prompt times as search hints, not facts. Verify the actual date/time prefix, file role, source, and object from output before narrowing queries or writing the finding.
- Make `Finding` stand alone: include affected object, incident window, raw cause token, source/driver if present, raw impact/status token or count, and any decisive line/status/validity/route/key/check/source field.

## Domain Playbooks

- **Agent/telemetry:** Separate config, credential/auth, intake/APM, queue/aggregator/collector/forwarder, sibling health, and recovery. In the proof pass include current and rotated/noise matches for config-validation, auth/status, flush/drop/queue, source/transaction/config, and APM/trace or other sibling-health terms. Pair the cause with delivery impact and reject historical recovered lookalikes by matching their distinct source/time/key fields.
- **HTTP/service:** Correlate route plus numeric status across access/proxy, service, and dependency/system layers. For dependency exhaustion, the proof pass should search dependency/system logs for owner/client/job/fanout fields, active/max values, reserved-slot wording, and native dependency errors before declaring the driver unknown. Reject prompt theories, cache/flag/payment/DNS-style red herrings, and rotated recovered bursts with scoped evidence.
- **Auth/security:** Aggregate failures by source early, then prove the suspicious source's current accepted-success count with a separate count command. Report failed-password/invalid-user pattern, approximate scale, different-source accepted logins, and historical same-source success separately. If zero, state no current accepted/successful login from that source and cite the `0` accepted count scope.
- **Certificates/container fallback:** Inspect primary and fallback roots together in the first file-reading call with both literal roots in `--allowed-paths`. State which root was empty and which produced evidence. Quote the certificate/x509 clause, compare observed current time with validity fields when present, and cite timing, renewal, kubelet/syslog, or equivalent system evidence before choosing timing versus certificate material.
- **Sockets:** Usually one call is enough: `help; help ss; ss -tlnH || ss -tln || true`. Do not try process/PID flags unless `help ss` lists them. If process flags are absent or socket reads fail, say that listening TCP addresses/ports are the supported target when collection succeeds and process/PID attribution is unavailable.

## Final Answer

Use concise sections:

- `Commands run`: one bullet per recorded rshell invocation with exact `./rshell --allow-all-commands`, literal full `--allowed-paths=...`, and a short purpose. Include the exact `-c` script only when it stays readable. If you omit script details, do not quote a fake command or put prose inside quotes.
- `Finding`: one sentence naming likely cause, affected service/check/route/source, actor/driver, raw cause/status token, and full incident window. Include raw line/status/reason/validity/source/count fields when present.
- `Evidence`: cite actual basenames/paths, lines, timestamps, decisive fragments, counts/statuses, IDs/fields, actor/source, impact, recovery, success, `since`, and zero-count markers. Put decisive counts in compact label/value bullets.
- `Not supported`: prompt theories, historical/rotated matches, recovered noise, different-source successes, missing same-source successes, unavailable capabilities, and selected zero-match layers. Label file/window.
- `Uncertainty / next checks`: state what is not proven and suggest only safe read-only validation, audit, config/source review, rollback planning, owner follow-up, metric review, or capability follow-up. Do not propose remediation commands.

Final check: concise commands, recorded output only, literal allowed paths, raw tokens in finding/evidence, actual file names per layer, scoped zero-counts, labeled current/historical/recovered/fallback/different-source evidence, no unsupported socket/PID, real-host, compromise, placeholder-command, invented-token, or remediation claims.
