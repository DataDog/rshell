---
name: datadog/remote-host-diagnostics
description: Safe, fast, transcript-grounded host diagnostics through ./rshell with target help discovery, literal allowed paths, two-pass evidence proof, and concise answers.
---

# Remote Host Diagnostics

## Contract

- Base every conclusion on completed `./rshell --allow-all-commands ...` output, not prompt hints, repository knowledge, local host state, static capability notes, or planned commands.
- Put `help` in the first rshell call; use `help <command>` before uncertain flags or after unsupported-flag failures. Production deployments may restrict, omit, or extend features, so target `help` is authoritative.
- If files are needed, every rshell call must include the exact literal prompt root in `--allowed-paths=<root>` before `-c`. For primary/fallback layouts, include both literal roots from the first file-reading call. No parent roots, variables, shorthands, ellipses, or "same path" wording.
- Keep diagnostics read-only and bounded. Do not write files, install packages, mutate services, restart, kill, delete, use recursive grep, use `find ... -exec grep`, or repeat broad scans over the same files.
- Failed syntax/flag probes are not evidence about the incident. Record the failure, simplify once with supported syntax, and ground findings only in productive output.
- Final answers may describe only recorded calls and output. Never claim real remote/customer-host access unless the transcript proves it. Never list placeholder, shortened-path, reconstructed, prose-inside-quote, or variable-expanded commands.

## Fast Investigation

Most file/log tasks should finish in two productive rshell calls; socket-only tasks usually finish in one. Use a third file/log call only for one named missing proof: source/driver, same-source success absence, recovery absence, fallback-root proof, certificate timing/material, downstream impact, or supported socket detail.

1. **Inventory once.** First root-touching call: `help`, shallow `find` for each literal root, and tiny `head`/`tail`/`grep -m` probes only for prompt-named literal files. Do not use `grep "$R"/*`, `find | while`, `read`, `xargs`, functions, or host-shell helpers; they waste time, miss nested files, or may be unsupported. Label empty/fallback roots immediately.
2. **Prove once.** Before call 2, choose primary, historical/noise, and fallback files from inventory. In one labeled script collect current cause and impact lines, source/driver fields, prompt-theory counts, same-source success/absence counts, recovery/absence counts, historical/rotated lookalikes, and one correlation or aggregation. Cover relevant layers, but do not sweep every file by default.
3. **Stop or fill one gap.** Before any third call, name the single missing ledger field and query only that field. Prefer a clear uncertainty statement over another discovery pass.

Stop when the ledger has: productive root/file, incident window, affected object, raw cause and impact/status tokens, driver/source if present, recovery/absence, rejected alternatives, historical/rotated matches, selected zero-count layers, and counts needed for the final answer.

## Rshell Mechanics

- Run from the repository root when possible:
  `./rshell --allow-all-commands --allowed-paths=<literal-root>[,<literal-root>] -c 'help; R="<literal-root>"; ...; true'`
- Define path variables only inside the rshell script. Keep `-c` as one quoted string; do not splice host-shell variables, host `$(...)`, or partially quoted host paths.
- The recorded shell command should contain the literal root inside both `--allowed-paths=` and the rshell script. Avoid nested host-shell quoting patterns that leave `'$R'` or host-expanded command substitutions in the transcript.
- Stay within target `help`: avoid `while`, `read`, `xargs`, arrays, functions, process substitution, background jobs, `[[...]]`, and external utilities not listed by `help`.
- Inventory first, then query literal files:
  `find "$R" -maxdepth 3 -type f | sort | head -n 80`
  `AG="$R/path/from/inventory.log"; grep -H -n -m 40 -E "specific|tokens" "$AG" || true`
- Make proof output self-labeling and count-based where useful:
  `printf "same_source_success_current="; grep -c -E "accepted|success" "$CUR" || true`
  `printf "alt_theory_current="; grep -c -E "prompt|named|tokens" "$CUR" "$ROT" || true`
- Every sample `grep` needs `-m` unless it is `grep -c` or a tight aggregation. Avoid broad globs, large `sed` windows, unbounded `grep | tail`, repeated all-file token sweeps, and full-date regexes until the date prefix is observed. Use `probe || true` or final `true` so non-matches do not discard output.

## Evidence Discipline

- Query every prompt-named theory or red herring before rejecting it. Label current, rotated, previous-window, recovered, fallback, empty-root, different-object, and different-source evidence.
- Negative claims require exact scoped zero counts or help/runtime evidence tied to queried files and window.
- Preserve observed basenames/paths, lines, timestamps, raw tokens, IDs, routes, checks, sources, users, counts, and key fields. Do not rename values, correct odd paths, infer defaults, or paraphrase away decisive wording.
- Every raw error/status/validity/check/source token named in the final answer must appear as an actual recorded output line; counts alone support scale, not the exact message text.
- If scale matters, produce a count label with `grep -c`, `wc -l`, or supported `sort`/`uniq` aggregation, then repeat the observed count or approximation in the final answer. If absence matters, cite the zero count and also write the plain sentence, e.g. no matching current success/recovery was found in that scope.
- Use prompt times as search hints, not facts. Verify the actual date/time prefix, file role, source, and object from output before narrowing queries or writing the finding.
- Make `Finding` stand alone: affected object, incident window, raw cause token, source/driver if present, raw impact/status token or count, and decisive line/status/validity/route/key/check/source fields.

## Domain Playbooks

- **Agent/telemetry:** Separate config, credential/auth, intake/APM, queue/aggregator/collector/forwarder, sibling health, and recovery. Include current plus rotated/noise matches for validation, auth/status, flush/drop/queue/no-flush, source/transaction/config, and sibling-health terms.
- **HTTP/service:** Correlate route plus numeric status across access/proxy, service, and dependency/system layers. For dependency exhaustion, search dependency/system logs for owner/client/job/fanout/application fields, active/max values, reserved-slot wording, and native errors.
- **Auth/security:** Aggregate failures by source early, then prove the suspicious source's current accepted-success count. Report failed-password/invalid-user pattern, approximate scale, different-source accepted logins, and historical same-source success separately. If zero, write plainly that there was no current accepted/successful login from that source and cite the `0` scope.
- **Certificates/container fallback:** Inspect primary and fallback roots together in the first file-reading call with both literal roots in `--allowed-paths` and in the script. State which root was empty, which produced evidence, and preserve at least one raw certificate/x509 line. Compare current time with validity fields when present, and cite timing, renewal/rotation, system, or service evidence before choosing timing versus certificate material.
- **Sockets:** Usually one call is enough: `help; help ss; ss -tlnH || ss -tln || true`. Do not try process/PID flags unless `help ss` lists them. If flags are supported but runtime socket reads fail, say syntax is supported but collection failed in this run. If process flags are absent, state that PID/program attribution is unavailable.

## Final Answer

Use concise sections:

- `Commands run`: one bullet per recorded rshell invocation. Each bullet independently repeats exact `./rshell --allow-all-commands`, literal full `--allowed-paths=...`, and a short purpose. Include exact `-c` only when readable; if omitted, do not quote a fake command. Never write "same command", "same path", shortened roots, or `...`.
- `Finding`: one sentence naming likely cause, affected service/check/route/source, actor/driver, raw cause/status token, full incident window, and impact. Include raw line/status/reason/validity/source/count fields when present.
- `Evidence`: cite actual basenames/paths, lines, timestamps, decisive fragments, counts/statuses, IDs/fields, actor/source, impact, recovery, success, `since`, and zero-count markers. Use compact label/value bullets for counts.
- `Not supported`: prompt theories, historical/rotated matches, recovered noise, different-source successes, missing same-source successes, unavailable capabilities, and selected zero-match layers. Label file/window.
- `Uncertainty / next checks`: state what is not proven and suggest only safe read-only validation, audit, config/source review, rollback planning, owner follow-up, metric review, or capability follow-up. Do not propose remediation commands.

Final check: concise commands, recorded output only, literal allowed paths in every command bullet, raw tokens in finding/evidence, actual file names per layer, count labels repeated, scoped zero-counts, labeled current/historical/recovered/fallback/different-source evidence, no unsupported socket/PID, real-host, compromise, placeholder-command, invented-token, or remediation claims.
