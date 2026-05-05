---
name: datadog/remote-host-diagnostics
description: Safe, fast, transcript-grounded host diagnostics through ./rshell with target help discovery, literal allowed paths, bounded evidence collection, exact command reporting, and accurate final answers.
---

# Remote Host Diagnostics

## Non-Negotiables

- Diagnose only from completed `./rshell --allow-all-commands ...` output. Do not answer from planned commands, prompt facts, repository knowledge, local time, or static capability notes.
- Include `help` in the first rshell call. Use `help <command>` only before uncertain flags, after an unsupported-flag failure, or for capability-sensitive commands such as sockets. Production rshell deployments may restrict, omit, or extend features; target `help` is authoritative.
- If diagnostics need files, every rshell call that mentions those roots must put literal `--allowed-paths=<root>` or `--allowed-paths=<root1>,<root2>` before `-c`. Do not hide allowed paths in host-shell variables. If primary and fallback roots are supplied, include both from the first file-reading call.
- Keep all work read-only and bounded. Do not write files, install packages, mutate services, restart, kill, delete, use recursive grep, use `find ... -exec grep`, or repeat broad scans over the same files.
- Use one simple rshell script: `--allowed-paths=<root> -c 'help; R="<root>"; ... "$R/file.log" ...; true'`. Define path variables only inside that script; never splice host-shell variables into quoted paths.
- Final answers may describe only recorded calls and output. Never claim real remote/customer-host access unless proven. Never list planned, interrupted, placeholder, reconstructed, shortened-path, `same prefix`, `same allowed path`, `...`, or prose-inside-quote commands.

## Fast Path

Most investigations should finish in two successful rshell calls; socket-only tasks usually finish in one. A third file/log call is only for one named missing proof that can change the conclusion.

1. **Discover and triage once.** First root-touching call: `help`, `find <root> -maxdepth 3 -type f | sort | head -n 80` for each supplied root, then cheap evidence over prompt-named or obvious current/rotated files. Use `wc -l`, minimal coverage `head`/`tail`, one or two `grep -H -n -m 25 -E` probes, and counts. For fallback layouts, inventory primary and fallback roots together and record empty roots.
2. **One decisive proof pass.** Choose the likely current log, prompt-named rotated/noise logs, and at most two corroborating layers. In one labeled script over only those files, collect:
   - samples: exact-token `grep -H -n -m 60 -E` only for likely cause, impact, actor/source, recovery, and prompt alternatives;
   - counts: `grep -H -c -E` for the likely cause, impact, prompt alternatives, lookalikes, success/recovery, and negative claims;
   - correlation: one cause-to-impact or cross-layer proof, or one source aggregation using selected-file filters with `sort | uniq -c | sort`.
3. **Stop or fill one gap.** Before any third call, name the single missing field: driver/source, same-source success absence, downstream impact, dependency/system corroboration, fallback-root proof, certificate time comparison, or supported socket collection. Do not run another broad search.

Stop once the ledger supports file/root, window, affected object, raw cause/impact tokens, driver/source, recovery, alternatives, historical/rotated lookalikes, and selected zero-match layers. Prefer stated uncertainty over another exploratory pass; do not spend a call polishing ready evidence.

## Probe Discipline

- Use `probe || true` or final `true` so non-matching greps do not erase useful output.
- Avoid low-signal broad patterns over noisy logs. Do not grep debug/noise files for generic `error|warn|healthy` unless paired with incident objects, sources, or exact status tokens.
- Keep outputs small: counts plus a few decisive lines. Avoid large `sed` windows, timestamp-only greps, and repeated `head`/`tail` over every file.
- Do not repeat `help`, `wc`, broad greps, unchanged counts, or surrounding context for lines that already have enough detail.
- Query every prompt-named theory or red herring before rejecting it. Label rotated, previous-window, recovered, or different-object matches as such instead of calling them zero.
- Negative claims need exact scoped zero counts or help/runtime evidence tied to the queried files and window.
- Preserve observed filenames, line numbers, timestamps, raw tokens, IDs, routes, check names, sources, users, counts, and key fields. Do not rename values, correct odd paths, or infer defaults.

## Domain Checks

- **Agent/telemetry:** Separate configuration, credential/auth, intake/APM, queue/aggregator/collector/flush, sibling health, and recovery. Pair cause with delivery impact, raw status/reason, transaction/config/credential fields, exact `since` markers, and current-vs-rotated lookalikes. Final must include raw validation/auth/status tokens and exact line/status/ID fields.
- **HTTP/service:** Correlate route and numeric status with app, proxy, and dependency/system evidence. For exhaustion, find owner/client/fanout plus active/max or native dependency wording, then repeat those raw fields. Reject prompt-named gateway, flag, cache, DNS, and historical 5xx theories with scoped counts or dated recovered lines.
- **Auth/security:** Aggregate failures by source early, then prove the suspicious source's current accepted-success count. Report failed-password/invalid-user pattern, approximate scale, different-source accepted logins, and historical same-source success separately. If zero, write `no current successful/accepted login from <source>`. Avoid compromise wording without same-source success evidence.
- **Certificates/container fallback:** Inspect primary and fallback roots together. State which root was empty and which produced evidence. Quote the exact certificate/x509 clause, compare observed current time with `NotBefore`/`NotAfter` when present, and cite timing, renewal, kubelet/syslog, or equivalent system evidence before choosing timing versus certificate material.
- **Sockets:** Usually one call is enough: `help; help ss; ss -tlnH || ss -tln || true`. Do not try process/PID flags unless `help ss` lists them. If process flags are absent or socket reads fail, say local listening TCP addresses/ports are the supported target when collection succeeds and process/PID attribution is unavailable.

## Final Answer

Use concise sections:

- `Commands run`: one bullet per recorded rshell invocation. Repeat a standalone exact `./rshell --allow-all-commands` prefix in every bullet, with literal `--allowed-paths=...` when used. For long scripts, quote only the real prefix through `-c` and summarize selected files/socket query and labels in prose.
- `Finding`: one sentence naming likely cause, affected service/check/route/source, supported actor/driver, raw cause/status token, and full incident window. Keep it literal with raw line/status/reason/validity/source/count fields when present.
- `Evidence`: cite decisive files, line numbers, timestamps, fragments, counts/statuses, IDs/fields, actor/source, downstream impact, and exact recovery, success, `since`, or zero-count markers. If a negative claim matters, include `0` count and scope.
- `Not supported`: prompt theories, historical/rotated matches, recovered noise, different-source successes, missing same-source successes, unavailable capabilities, and selected zero-match layers. Label file/window.
- `Uncertainty / next checks`: state what is not proven and suggest only safe read-only validation, audit, config/source review, rollback planning, owner follow-up, metric review, or capability follow-up. Do not propose remediation commands.

Final check: recorded calls only; literal allowed paths in each command bullet; transcript values only; raw tokens in the finding; scoped zero-counts; current, historical, recovered, fallback, and different-source evidence labeled; no unsupported socket/PID, real-host, compromise, placeholder-command, shortened-path, `same prefix`, invented-token, or remediation claims.
