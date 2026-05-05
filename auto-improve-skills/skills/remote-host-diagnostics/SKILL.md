---
name: datadog/remote-host-diagnostics
description: Safe, fast, transcript-grounded host diagnostics through ./rshell with target help discovery, exact allowed paths, inventory-driven proof, and concise final answers.
---

# Remote Host Diagnostics

## Contract

- Evidence comes only from completed `./rshell --allow-all-commands ...` output. Prompt hints, static capability notes, repository knowledge, local host state, and planned commands are not evidence.
- Run rshell from the repository root. Do not run separate host-shell preflights such as `pwd`, `ls ./rshell`, or wrapper probes unless rshell itself fails.
- Put `help` in the first rshell call, and run `help <command>` before uncertain flags or after unsupported-flag failures. Production deployments may restrict, omit, or extend features; target `help` is authoritative.
- If files are needed, every rshell call includes exact literal prompt roots in `--allowed-paths=<root>` before `-c`. For primary/fallback layouts, include both roots in `--allowed-paths` and in the first file-reading script.
- Copy prompt roots exactly in commands and final answers. Do not shorten, normalize, replace with a remembered default, drop variant/subdirectory segments, or use `Same`, `...`, `<root>`, or other placeholders.
- Keep diagnostics read-only and bounded. Do not write files, install packages, mutate services, restart, kill, delete, use recursive grep, use `find ... -exec grep`, or repeat broad searches over the same files.
- Final answers may describe only recorded calls and output. Do not claim real remote/customer-host access unless the transcript proves it.

## Fast Workflow

Default budget: one inventory call plus one proof call. Use a third call only for a named missing field that changes the conclusion, such as driver/source, same-source success absence, recovery absence, fallback-root proof, certificate timing/material, or downstream impact.

1. **Inventory once.** First root-touching call includes `help`, shallow `find` for each literal root, counts, and tiny `head`/`tail`/`grep -m` probes for prompt-named files/tokens. Inventory is truth: use discovered renamed files and stop probing absent default names.
2. **Choose the ledger before call 2.** From inventory select current, rotated/history, noise/red-herring, fallback, and cross-layer files. Write down the fields needed for the answer: productive root, file names, incident window, affected object, raw cause/status tokens, driver/source, impact, recovery or absence, rejected alternatives, historical matches, selected zero counts, and counts for scale.
3. **Prove in one labeled script.** Collect decisive raw samples with `grep -H -n -m 12..40` and scale/absence with `grep -c`. Label every block and count (`current_cause`, `count_same_source_success`, `rotated_recovered`, `dependency_driver`, etc.). Query each prompt-named theory or red herring once. Use counts for scale instead of large sample dumps.
4. **Stop or fill one evidence gap.** Stop only after the transcript contains the raw cause line, source/driver if claimed, impact/status line, and scoped zero counts for negative claims. If one of those is missing, run one narrow follow-up for that field. If they are already present, do not run a confirmation call just to restate tokens.

## Rshell Mechanics

Use one plain rshell command; define variables only inside the `-c` script:

```sh
./rshell --allow-all-commands --allowed-paths=/literal/root -c 'help; R="/literal/root"; printf "inventory_files\n"; find "$R" -maxdepth 3 -type f | sort | head -n 100; printf "file_count="; find "$R" -maxdepth 3 -type f | wc -l; true'
```

For two roots:

```sh
./rshell --allow-all-commands --allowed-paths=/primary/root,/fallback/root -c 'help; P="/primary/root"; H="/fallback/root"; printf "primary_files\n"; find "$P" -maxdepth 3 -type f | sort | head -n 60; printf "fallback_files\n"; find "$H" -maxdepth 3 -type f | sort | head -n 100; true'
```

- Keep `-c` as one quoted string. Do not use host-shell variables, host `$(...)`, nested quote splicing, or `/bin/sh -lc` to construct an rshell script.
- Stay within target `help`: avoid `while`, `read`, `xargs`, arrays, functions, process substitution, background jobs, `[[...]]`, and external utilities not listed by `help`.
- Query literal files selected from inventory:
  `CUR="$R/path/from/inventory.log"; ROT="$R/path/from/inventory.log.1"; grep -H -n -m 30 -E "specific|tokens" "$CUR" "$ROT" || true`
- Use `probe || true` or a final `true` so non-matches do not discard useful output. Avoid broad globs, large `sed` windows, unbounded `grep | tail`, proof-pass catch-all `tail`, `grep -m` above 60, and full-date regexes until the actual date prefix is observed.

## Evidence Discipline

- Preserve observed basenames/paths, lines, timestamps, raw tokens, IDs, routes, checks, sources, users, counts, and key fields. Do not rename values, correct odd paths, infer defaults, or paraphrase away decisive wording.
- Every raw error/status/validity/check/source token in the final answer must appear in recorded output. Counts show scale; they do not replace the exact decisive message.
- Negative claims require exact scoped zero counts or help/runtime evidence. In the final, cite the count label and write the plain sentence, for example: `No current successful/accepted login from <source> was found in <file/window>.`
- Use prompt times only as search hints. Verify actual date/time prefix, file role, source, and affected object from output before narrowing or writing the finding.
- If the same-window evidence names a driver/source, state it as supported evidence, not merely as possible. If it does not, say what is unproven.

## Domain Playbooks

- **Agent/telemetry:** Test config/reload, credential/API auth, intake/APM, queue/aggregator/collector/forwarder, sibling health, rotated history, and recovery. Preserve raw validation/auth/status tokens, transaction/source/config fields, `no metrics flushed` or drop/reject lines, core/aggregator stop lines, and trace/APM healthy lines when they reject the teammate theory.
- **HTTP/service:** Correlate affected route plus 500/502/status evidence across access/proxy, service, and dependency/system layers. For dependency exhaustion, pull native DB/system errors, active/max or limit values, owner/client/job/application fields, fanout wording, and then reject payment/cache/feature/DNS/rotated noise if prompted.
- **Auth/security:** Aggregate current failures by source early, then sample invalid-user/failed-password lines for the concentrated source. Prove current same-source accepted/success count, separate different-source successful logins, separate rotated/historical matches, and avoid compromise language when same-source success is zero.
- **Certificates/container fallback:** Inspect primary and fallback roots together in the first file-reading call. State which root was empty and which produced evidence. Preserve one raw `x509`/certificate line and timing/material fields such as validity windows, current-time wording, rotation/kubelet/syslog, or chrony/NTP before choosing clock skew versus certificate material.
- **Sockets:** Usually one call is enough: `help; help ss; ss -tlnH || ss -tln || true`. Do not try process/PID flags unless `help ss` lists them. If syntax is supported but runtime socket reads fail, say collection failed in this run. If process flags are absent, state that PID/program attribution is unavailable.

## Final Answer

Use concise sections:

- `Commands run`: one bullet per recorded rshell invocation. Repeat the exact `./rshell --allow-all-commands` and literal full `--allowed-paths=...` every time; include exact `-c` only if it is readable. Never write `Same`, `.../logs`, `<root>`, shortened roots, reconstructed commands, variable-expanded commands, or prose-inside-quote commands.
- `Finding`: one stand-alone sentence with likely cause, affected service/check/route/source, driver/actor if present, raw cause token, raw impact/status token or count, and the full observed incident date/time window when logs provide one.
- `Evidence`: compact bullets with actual files, lines, timestamps, decisive fragments, count labels, IDs/fields, actor/source, impact, recovery, success, and zero-count markers.
- `Not supported`: prompt theories, historical/rotated matches, recovered noise, different-source successes, missing same-source successes, unavailable capabilities, and selected zero-match layers. Label file/window.
- `Uncertainty / next checks`: state what is not proven and suggest only safe read-only validation, audit, config/source review, rollback planning, owner follow-up, metric review, or capability follow-up. Do not propose remediation commands.

Final audit: exact prompt roots in command bullets, inventory-derived file names, raw tokens in finding/evidence, count labels repeated, scoped zero counts plus plain absence sentences, labeled current/historical/recovered/fallback/different-source evidence, no unsupported socket/PID, real-host, compromise, placeholder-command, invented-token, or remediation claims.
