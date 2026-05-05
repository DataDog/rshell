---
name: datadog/remote-host-diagnostics
description: Safe, fast, transcript-grounded host diagnostics through ./rshell with target help discovery, literal allowed paths, rshell-compatible bounded probes, exact evidence, and concise final answers.
---

# Remote Host Diagnostics

## Hard Rules

- Diagnose only from completed `./rshell --allow-all-commands ...` output. Do not answer from planned commands, prompt facts, repository knowledge, local time, or static capability notes.
- Include `help` in the first rshell call. Use `help <command>` before uncertain flags, after an unsupported-flag failure, and for capability-sensitive commands such as sockets. Production rshell deployments may restrict, omit, or extend features; target `help` is authoritative.
- If diagnostics need files, every rshell call that touches those roots must put literal `--allowed-paths=<root>` or `--allowed-paths=<root1>,<root2>` before `-c`. If primary and fallback roots are supplied, include both from the first file-reading call.
- Keep all work read-only and bounded. Do not write files, install packages, mutate services, restart, kill, delete, use recursive grep, use `find ... -exec grep`, or repeat broad scans over the same files.
- Final answers may describe only recorded calls and output. Never claim real remote/customer-host access unless proven. Never list planned, interrupted, placeholder, reconstructed, shortened-path, `same prefix`, `same allowed path`, `...`, or prose-inside-quote commands.

## Rshell-Safe Scripts

- Use one rshell script per call: `./rshell --allow-all-commands --allowed-paths=<literal-root> -c 'help; R="<literal-root>"; ... "$R/file.log" ...; true'`.
- Define path variables only inside the rshell script. Do not splice host-shell variables, host `$(...)`, or partially quoted host paths into the command.
- Avoid unsupported or often-absent shell habits unless `help` proves support: `while`, `read`, `xargs`, arrays, functions, process substitution, background jobs, and external utilities not listed by `help`.
- Prefer explicit selected files and simple globs over generated command lines. Good patterns: `find "$R" -maxdepth 3 -type f | sort | head -n 80`, `wc -l "$R"/*.log "$R"/*/*.log 2>/dev/null || true`, `for f in "$R"/*.log "$R"/*/*.log; do grep -H -n -m 20 -E "tokens" "$f" || true; done`.
- If a call fails because syntax or a flag is unsupported, record that fact, rerun once with a simpler supported script, and do not count the failed call as evidence.

## Fast Workflow

Most investigations should finish in two successful rshell calls; socket-only tasks usually finish in one. A third file/log call is only for one named missing proof that can change the conclusion.

1. **Discover and triage once.** First root-touching call: `help`, inventory each supplied root, then cheap evidence over prompt-named or obvious current/rotated files. Use `wc -l`, minimal `head`/`tail`, one or two `grep -H -n -m 25 -E` probes, and counts. For fallback layouts, inventory primary and fallback roots together and record empty roots.
2. **One decisive proof pass.** Select the likely current log, prompt-named rotated/noise logs, and at most two corroborating layers. In one labeled script collect exact-token samples, scoped counts for cause/impact/alternatives/lookalikes/recovery/zero claims, and one correlation: cause-to-impact, cross-layer proof, or source aggregation using `grep | sed | sort | uniq -c | sort`.
3. **Stop or fill one gap.** Before any third call, name the single missing field: driver/source, same-source success absence, downstream impact, dependency/system corroboration, fallback-root proof, certificate time comparison, or supported socket collection. Do not run another broad search.

Stop once the ledger supports file/root, window, affected object, raw cause and impact tokens, driver/source, recovery or absence, alternatives, historical/rotated lookalikes, and selected zero-match layers. Prefer stated uncertainty over another exploratory pass.

## Evidence Discipline

- Use `probe || true` or final `true` so non-matching greps do not erase useful output.
- Keep outputs small: counts plus decisive lines. Avoid large `sed` windows, timestamp-only greps, and repeated `head`/`tail` over every file.
- Query every prompt-named theory or red herring before rejecting it. Label rotated, previous-window, recovered, fallback, empty-root, or different-object matches instead of calling them zero.
- Negative claims need exact scoped zero counts or help/runtime evidence tied to the queried files and window.
- Preserve observed file basenames/paths, line numbers, timestamps, raw tokens, IDs, routes, check names, sources, users, counts, and key fields. Do not rename values, correct odd paths, infer defaults, or paraphrase away decisive wording. If output says `config validation failed`, `no metrics flushed since`, `Accepted`, `x509:`, or a numeric status, repeat that raw wording in the final.

## Domain Checks

- **Agent/telemetry:** Separate configuration, credential/auth, intake/APM, queue/aggregator/collector/flush, sibling health, and recovery. Pair cause with delivery impact, raw status/reason, transaction/config/credential fields, exact `since` markers, and current-vs-rotated lookalikes. Final must include the raw validation/auth/status line fragment, not only a paraphrase.
- **HTTP/service:** Correlate route and numeric status with app, proxy/access/error, and dependency/system evidence. Name the actual file basename for each layer. For exhaustion, find owner/client/fanout plus active/max or native dependency wording, then repeat those raw fields. Reject prompt-named gateway, flag, cache, DNS, and historical 5xx theories with scoped counts or dated recovered lines.
- **Auth/security:** Aggregate failures by source early, then prove the suspicious source's current accepted-success count. Report failed-password/invalid-user pattern, approximate scale, different-source accepted logins, and historical same-source success separately. If zero, write `no current successful/accepted login from <source>` and include the `0` accepted count scope. Avoid compromise wording without same-source success evidence.
- **Certificates/container fallback:** Inspect primary and fallback roots together. State which root was empty and which produced evidence. Quote the exact certificate/x509 clause, compare observed current time with `NotBefore`/`NotAfter` when present, and cite timing, renewal, kubelet/syslog, or equivalent system evidence before choosing timing versus certificate material. When an error contains a combined phrase, use the specific comparison to decide which clause matters.
- **Sockets:** Usually one call is enough: `help; help ss; ss -tlnH || ss -tln || true`. Do not try process/PID flags unless `help ss` lists them. If process flags are absent or socket reads fail, say local listening TCP addresses/ports are the supported target when collection succeeds and process/PID attribution is unavailable.

## Final Answer

Use concise sections:

- `Commands run`: one bullet per recorded rshell invocation. Repeat a standalone exact `./rshell --allow-all-commands` prefix in every bullet, with literal full `--allowed-paths=...` when used. For long scripts, quote only the real prefix through `-c` and summarize selected files/socket query in prose; never use ellipses or shortened paths.
- `Finding`: one sentence naming likely cause, affected service/check/route/source, supported actor/driver, raw cause/status token, and full incident window. Include raw line/status/reason/validity/source/count fields when present.
- `Evidence`: cite actual file basenames/paths, line numbers, timestamps, decisive fragments, counts/statuses, IDs/fields, actor/source, downstream impact, exact recovery, success, `since`, and zero-count markers.
- `Not supported`: prompt theories, historical/rotated matches, recovered noise, different-source successes, missing same-source successes, unavailable capabilities, and selected zero-match layers. Label file/window.
- `Uncertainty / next checks`: state what is not proven and suggest only safe read-only validation, audit, config/source review, rollback planning, owner follow-up, metric review, or capability follow-up. Do not propose remediation commands.

Final check: recorded calls only; literal allowed paths in every command bullet; transcript values only; raw tokens in the finding and evidence; actual file names for each layer; scoped zero-counts; current, historical, recovered, fallback, and different-source evidence labeled; no unsupported socket/PID, real-host, compromise, placeholder-command, invented-token, or remediation claims.
