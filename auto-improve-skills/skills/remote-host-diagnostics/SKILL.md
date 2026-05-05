---
name: datadog/remote-host-diagnostics
description: Safe, fast, transcript-grounded host diagnostics through ./rshell with target help discovery, literal allowed paths, bounded evidence collection, and accurate final answers.
---

# Remote Host Diagnostics

## Non-Negotiables

- Diagnose only from completed `./rshell --allow-all-commands ...` output. Do not answer from planned commands, prompt facts, repository knowledge, local time, or static capability notes.
- Include `help` in the first rshell call. Use `help <command>` only before uncertain flags, after an unsupported-flag failure, or for capability-sensitive commands such as sockets. Production rshell deployments may restrict, omit, or extend features; target `help` is authoritative.
- If diagnostics need files, every rshell call that mentions those roots must put literal `--allowed-paths=<root>` or `--allowed-paths=<root1>,<root2>` before `-c`. Do not hide allowed paths in host-shell variables. If primary and fallback roots are supplied, include both from the first file-reading call.
- Keep all work read-only and bounded. Do not write files, install packages, mutate services, restart, kill, delete, use recursive grep, use `find ... -exec grep`, or repeat broad scans over the same files.
- Prefer `-c 'help; printf "\n== label ==\n"; ...; true'`. Use double quotes inside the script, keep root paths literal, and define short variables only inside that script after the literal allowed paths are already present.
- Final answers may describe only recorded calls and output. Never claim real remote/customer-host access unless proven. Never list planned, interrupted, placeholder, reconstructed, shortened-path, `same prefix`, `...`, or prose-inside-quote commands.

## Fast Path

Most investigations should finish in two successful rshell calls. A third successful file/log call is only for one named missing proof that can change the conclusion.

1. **Discover and inventory.** First root-touching call: `help`, then `find <root> -maxdepth 3 -type f | sort | head -n 80` for each supplied root. For fallback layouts, inventory the primary and fallback roots in the same sandboxed call and explicitly record empty roots.
2. **One decisive evidence pass.** From the inventory, choose the likely current log, prompt-named rotated/noise logs, and at most two corroborating layers such as proxy, dependency, system, audit, health, or host-mounted logs. In one labeled script over only those files, collect:
   - scope: `wc -l` for selected files, plus `head`/`tail` only when log date coverage is unclear;
   - samples: `grep -H -n -m 40 -E` with prompt nouns, incident time tokens, affected objects, raw status/error tokens, cause words, actor/source fields, IDs, and recovery words;
   - counts: `grep -H -c -E` for the likely cause, impact, prompt alternatives, lookalikes, success/recovery, and negative claims;
   - correlation: one cause-to-impact or cross-layer proof, or one source aggregation using selected-file filters with `sort | uniq -c | sort`.
3. **Stop or fill one gap.** Before any third call, name the single missing field: driver/source, same-source success absence, downstream impact, dependency/system corroboration, fallback-root proof, certificate time comparison, or supported socket collection. Do not run another broad search.

Stop once the ledger has enough support for file/root, window, affected object, raw cause token, impact token, driver/source when present, recovery state, prompt alternatives, historical/rotated lookalikes, and selected layers with no matches. Prefer a stated uncertainty over another exploratory pass.

## Probe Discipline

- Use `probe || true` or final `true` so non-matching greps do not erase useful output.
- Avoid low-signal broad patterns over noisy logs. Do not grep debug/noise files for generic `error|warn|healthy` unless paired with incident objects, sources, or exact status tokens.
- Do not repeat `help` pages, `wc`, broad incident greps, or unchanged counts. If a grep line already has enough context, do not add `sed` windows.
- Query every prompt-named theory or red herring before rejecting it. Label rotated, previous-window, recovered, or different-object matches as such instead of calling them zero.
- Negative claims need exact scoped zero counts or help/runtime evidence tied to the queried files and window.
- Preserve observed filenames, line numbers, timestamps, raw tokens, IDs, routes, check names, sources, users, counts, and key fields. Do not rename values, correct odd paths, or infer defaults.

## Domain Checks

- **Agent/telemetry:** Separate configuration, credential/auth, intake/APM, queue/aggregator/collector/flush, sibling health, and recovery. Pair the cause with delivery impact, raw status/reason, transaction/config/credential fields, exact `since` markers, and current-vs-rotated lookalikes.
- **HTTP/service:** Correlate route and numeric status with app, proxy, and dependency/system evidence. For exhaustion, look for owner/client/fanout plus active/max or native dependency wording. Reject prompt-named gateway, flag, cache, DNS, and historical 5xx theories with scoped counts or dated recovered lines.
- **Auth/security:** Aggregate failures by source early, then prove the suspicious source's current accepted-success count. Report failed-password/invalid-user patterns, approximate scale, different-source accepted logins, and any historical same-source success separately. Avoid compromise wording without same-source success evidence.
- **Certificates/container fallback:** Inspect primary and fallback roots together. State which root was empty and which produced evidence. Quote the exact certificate/x509 clause, compare observed current time with validity bounds when present, and cite timing, renewal, kubelet/syslog, or equivalent system evidence before choosing timing versus certificate material.
- **Sockets:** Usually one call is enough: `help; help ss; ss -tlnH || ss -tln || true`. Do not try process/PID flags unless `help ss` lists them. If process flags are absent or socket reads fail, say local listening TCP addresses/ports are the supported target when collection succeeds and process/PID attribution is unavailable.

## Final Answer

Use concise sections:

- `Commands run`: one bullet per recorded rshell invocation. Include the exact `./rshell --allow-all-commands` prefix, literal `--allowed-paths=...` when used, selected files or socket query, and bounded operation labels/types. For long scripts, summarize outside code quotes; do not invent a quoted `-c 'bounded grep themes...'` script.
- `Finding`: one sentence naming the likely cause, affected service/check/route/source, supported actor/driver, raw cause/status token, and full incident window. Include user-visible impact time when different.
- `Evidence`: cite decisive files, line numbers, timestamps, fragments, counts/statuses, IDs/fields, actor/source, downstream impact, and exact recovery, success, `since`, or zero-count markers.
- `Not supported`: prompt theories, historical/rotated matches, recovered noise, different-source successes, missing same-source successes, unavailable capabilities, and selected layers with no matches.
- `Uncertainty / next checks`: state what is not proven and suggest only safe read-only validation, audit, config/source review, rollback planning, owner follow-up, metric review, or capability follow-up. Do not propose remediation commands.

Final check: recorded calls only; literal allowed paths; transcript values only; scoped zero-counts; current, historical, recovered, fallback, and different-source evidence labeled; no unsupported socket/PID, real-host, compromise, placeholder-command, shortened-path, invented-token, or remediation claims.
