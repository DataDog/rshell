---
name: datadog/remote-host-diagnostics
description: Safe, fast, transcript-grounded host diagnostics through ./rshell with target help discovery, exact allowed paths, inventory-driven proof, and concise final answers.
---

# Remote Host Diagnostics

## Contract

- Base conclusions only on completed `./rshell --allow-all-commands ...` output. Prompt hints, repository knowledge, static capability notes, local host state, and planned commands are not evidence.
- Put `help` in the first rshell call. Use `help <command>` before uncertain flags or after unsupported-flag failures. Production deployments may restrict, omit, or extend features; target `help` is authoritative.
- If files are needed, every rshell call must include the exact literal prompt root in `--allowed-paths=<root>` before `-c`. For primary/fallback layouts, include both literal roots in `--allowed-paths` and in the rshell script from the first file-reading call onward.
- Keep diagnostics read-only and bounded. Do not write files, install packages, mutate services, restart, kill, delete, use recursive grep, use `find ... -exec grep`, or repeat broad scans over the same files.
- Final answers may describe only recorded calls and output. Do not claim real remote/customer-host access unless the transcript proves it. Do not list shortened, placeholder, reconstructed, variable-expanded, or prose-inside-quote commands.

## Fast Workflow

Most file/log investigations should finish in two productive rshell calls; socket-only tasks usually need one. Use a third file/log call only for one named missing proof, such as source/driver, current same-source success absence, recovery absence, fallback-root proof, certificate timing/material, or downstream impact.

1. **Inventory once.** First root-touching call: `help`, shallow `find` for each literal root, small file counts, and tiny `head`/`tail`/`grep -m` probes only for prompt-named files that exist or for discovering the date prefix. Treat inventory as truth: if a generic prompt file is absent and a renamed file exists, switch to the discovered file names and stop probing the absent default.
2. **Prove once.** Before call 2, choose a small set of current, rotated/history, noise, fallback, and cross-layer files from inventory. In one labeled script collect raw cause/status lines, impact lines, driver/source fields, prompt-theory evidence, same-source success/absence counts, recovery/absence counts, historical lookalikes, and one correlation or aggregation. Prefer `grep -c` for scale and `grep -H -n -m 12..40` for decisive samples; avoid 80+ line dumps unless the file is tiny.
3. **Stop or fill one gap.** Before any third call, name the missing ledger field and query only that field. Prefer a clear uncertainty statement over another discovery pass.

Stop when the ledger has: productive root and file names, observed incident window, affected object, raw cause and impact/status tokens, driver/source if present, recovery/absence, rejected alternatives, historical/rotated matches, selected zero-count layers, and counts needed for the final answer.

## Rshell Mechanics

- Run rshell directly from the repository root when possible:
  `./rshell --allow-all-commands --allowed-paths=/literal/root -c 'help; R="/literal/root"; printf "inventory\n"; find "$R" -maxdepth 3 -type f | sort | head -n 80; true'`
- For two roots:
  `./rshell --allow-all-commands --allowed-paths=/primary/root,/fallback/root -c 'help; P="/primary/root"; H="/fallback/root"; find "$P" -maxdepth 3 -type f | sort | head -n 40; find "$H" -maxdepth 3 -type f | sort | head -n 80; true'`
- Define path variables only inside the rshell script. Keep `-c` as one quoted string. Do not use host-shell variables, host `$(...)`, nested quote splicing such as `'"$R"'`, or wrappers like `/bin/sh -lc` to construct the rshell command.
- Stay within target `help`: avoid `while`, `read`, `xargs`, arrays, functions, process substitution, background jobs, `[[...]]`, and external utilities not listed by `help`.
- Query literal files selected from inventory:
  `CUR="$R/path/from/inventory.log"; ROT="$R/path/from/inventory.log.1"; grep -H -n -m 30 -E "specific|tokens" "$CUR" "$ROT" || true`
- Make proof output self-labeling and count-based:
  `printf "current_same_source_success="; grep -c -E "Accepted|success" "$CUR" || true`
  `printf "current_prompt_theory="; grep -c -E "prompt|theory|tokens" "$CUR" "$SYS" || true`
- Every sample `grep` needs `-m` unless it is `grep -c` or a tight aggregation. Avoid broad globs, large `sed` windows, unbounded `grep | tail`, repeated all-file token sweeps, and full-date regexes until the actual date prefix is observed. Use `probe || true` or a final `true` so non-matches do not discard useful output.

## Evidence Discipline

- Query every prompt-named theory or red herring before rejecting it. Label current, rotated, previous-window, recovered, fallback, empty-root, different-object, and different-source evidence.
- Preserve observed basenames/paths, lines, timestamps, raw tokens, IDs, routes, checks, sources, users, counts, and key fields. Do not rename values, correct odd paths, infer defaults, or paraphrase away decisive wording.
- Every raw error/status/validity/check/source token named in the final answer must appear in recorded output. Counts support scale; they do not substitute for the exact decisive message.
- Negative claims require exact scoped zero counts or help/runtime evidence tied to queried files and window. In the final, cite the count label and also write the plain sentence, e.g. `No current successful/accepted login from <source> was found in <file/window>.`
- Use prompt times only as search hints. Verify actual date/time prefix, file role, source, and object from output before narrowing queries or writing the finding.
- Make `Finding` stand alone: affected object, observed incident window, raw cause token, source/driver if present, raw impact/status token or count, and decisive line/status/validity/route/key/check/source fields.

## Domain Playbooks

- **Agent/telemetry:** Separate config, credential/auth, intake/APM, queue/aggregator/collector/forwarder, sibling health, and recovery. Include current plus rotated/noise matches for validation, auth/status, flush/drop/queue/no-flush, source/transaction/config, and sibling-health terms. If metrics stopped, preserve the raw `no metrics flushed`/flush line and its `since` or last-success field.
- **HTTP/service:** Correlate affected route plus numeric 500/502/status evidence across access/proxy, service, and dependency/system layers. For dependency exhaustion, search dependency/system logs for owner/client/job/fanout/application fields, active/max values, reserved-slot wording, and native errors.
- **Auth/security:** Aggregate current failures by source early, choose the suspicious current source from the observed concentration, then prove its current accepted/success count. Report failed-password/invalid-user pattern, approximate scale, different-source accepted logins, and historical same-source success separately. Do not imply compromise when current same-source success is zero.
- **Certificates/container fallback:** Inspect primary and fallback roots together in the first file-reading call. State which root was empty and which produced evidence. Preserve at least one raw `x509`/certificate line exactly, including `NotBefore`/`NotAfter` or current-time wording when present; then compare timing evidence with certificate material, rotation, kubelet/syslog, chrony/NTP, or service evidence before choosing a cause.
- **Sockets:** Usually one call is enough: `help; help ss; ss -tlnH || ss -tln || true`. Do not try process/PID flags unless `help ss` lists them. If runtime socket reads fail, say syntax was supported but collection failed in this run. If process flags are absent, state that PID/program attribution is unavailable.

## Final Answer

Use concise sections:

- `Commands run`: one bullet per recorded rshell invocation. Each bullet repeats exact `./rshell --allow-all-commands`, the literal full `--allowed-paths=...`, and the purpose. Include exact `-c` only when readable; otherwise do not quote a fake command.
- `Finding`: one sentence naming likely cause, affected service/check/route/source, actor/driver, raw cause/status token, full observed incident window, and impact.
- `Evidence`: compact bullets with actual basenames/paths, lines, timestamps, decisive fragments, counts/statuses, IDs/fields, actor/source, impact, recovery, success, `since`, and zero-count markers.
- `Not supported`: prompt theories, historical/rotated matches, recovered noise, different-source successes, missing same-source successes, unavailable capabilities, and selected zero-match layers. Label file/window.
- `Uncertainty / next checks`: state what is not proven and suggest only safe read-only validation, audit, config/source review, rollback planning, owner follow-up, metric review, or capability follow-up. Do not propose remediation commands.

Final audit: exact prompt roots in command bullets, inventory-derived file names, raw tokens in finding/evidence, count labels repeated, scoped zero-counts plus plain absence sentences, labeled current/historical/recovered/fallback/different-source evidence, no unsupported socket/PID, real-host, compromise, placeholder-command, invented-token, or remediation claims.
