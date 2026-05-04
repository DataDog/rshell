---
name: datadog/remote-host-diagnostics
description: Use when diagnosing remote host, service, network, or system issues through this repository's rshell. Guides safe, bounded, read-only diagnostics with ./rshell, explicit allowed paths, help-based capability discovery, and evidence-grounded final answers.
---

# Remote Host Diagnostics

Use this skill for host, service, log, socket, certificate, authentication, or system diagnostics through this repository's `./rshell`.

## Non-Negotiables

- Run diagnostic commands only through `./rshell --allow-all-commands -c '<script>'`.
- If reading files, put every literal root in `--allowed-paths=<root>` on every file-reading invocation. For primary plus fallback roots, use `--allowed-paths=<primary>,<fallback>`.
- Keep the investigation read-only and bounded. Do not write files, install packages, mutate services, restart processes, or run broad repeated scans.
- Use `help` inside rshell before relying on a command, feature, or flag. Production rshell deployments may restrict, omit, or extend features, so target-environment `help` is the source of truth.
- Actually run rshell and answer from transcript evidence. Do not answer from the prompt, repository knowledge, or a static capability snapshot.
- Do not claim you connected to, SSHed into, or accessed a real remote or customer host unless the user provided that evidence. Usually you are inspecting local fixtures or mounted logs.

## Fast Investigation Pattern

Aim for two rshell invocations; use a third only when a decisive claim is still unevidenced.

1. **Discover and inventory.** Put `help` on the same command line as `-c`, with no leading newline, so the transcript clearly shows capability discovery. Inventory prompt-provided roots once with `find -maxdepth` and `head`; prove an empty primary root and inspect any fallback root in the same pass.
2. **Triage selected files once.** Pick a small candidate set: the current component log, a rotated or named-noise log for the user's theory, and one independent layer such as proxy, system, dependency, audit, or sibling-agent logs. In one script collect counts and samples for symptom, cause, impact, source/actor, recovery, and counter-hypothesis terms.
3. **Confirm only decisive tokens.** If needed, run short `sed -n` windows around known line numbers or focused greps for IDs, status codes, source actors, certificate fields, socket flags, or recovery markers already found.

Stop when cause, consequence, counter-hypothesis disposition, current or recovery state, and remaining uncertainty are evidenced. Do not keep broadening the search after that.

## Transcript-Friendly Command Shape

Prefer compact scripts that make boundedness obvious. Use separate grep flags instead of combined short flags because they are easier to audit:

```sh
./rshell --allow-all-commands --allowed-paths=<root> -c 'help; help find; help grep; help sed; find <root> -maxdepth 3 -type f | sort | head -n 80'
./rshell --allow-all-commands --allowed-paths=<root> -c 'grep -H -c "<pattern>" <file>; grep -H -n -m 20 "<pattern>" <file>; sed -n "<start>,<end>p" <file>'
```

For socket-only diagnostics, no file allowlist is needed. Discover flags first, then run a help-advertised listening TCP query:

```sh
./rshell --allow-all-commands -c 'help ss; ss -tlnH'
```

If a command or flag fails, run `help <command>` and then one supported subset. Quote the limitation only if it appears in the transcript.

## Command Discipline

- Keep output small. Use `printf '%s\n' '<label>'` labels and bounded commands: `grep -H -c`, `grep -H -n -m`, `head -n`, `tail -n`, `wc -l`, and short `sed -n` windows.
- Aggregate before sampling high-volume logs. Count status/source/cause terms first; then sample representative lines.
- Prefer prompt-grounded and evidence-grounded patterns over generic `error|warn`: absolute time windows, route/check names, status classes, auth phrases, certificate terms, failure verbs, source/client fields, IDs, and recovery/success words.
- After inventory, do not rescan every file. Query explicit candidates. Avoid recursive greps and `find ... -exec grep`.
- Shell variables inside rshell scripts are fine, but the command line must still show literal `--allowed-paths` roots, and the final answer must name the files actually queried.
- If exact time or filename assumptions produce zero matches, say so and search nearby discovered dates/files rather than forcing the prompt wording into the conclusion.

## Evidence Ledger

For every important claim, preserve the raw fields that make it auditable:

- Location: file and line when available.
- Time: date plus time together when available, and whether evidence is current, old, rotated, recovered, or from a different source.
- Cause: exact error/status/check/route/source/actor/config/certificate/socket field.
- Impact: stopped components, failed checks, dropped or rejected payloads, no-flush or "since" markers, affected routes/statuses, unavailable fields, or current success/recovery markers.
- Counter-hypothesis: the user's proposed cause, historical lookalikes, healthy sibling paths, different-source successes, and unsupported command capabilities.

Short decisive quotes are better than paraphrases. For long lines, copy only distinguishing tokens.

## Diagnostic Cues

- **Agent or telemetry degradation:** Search current logs and rotated/noise logs for config, credential/auth, intake, trace/APM, forwarder, aggregator, and no-flush terms. Pair the causal line with downstream impact from the same incident. Separately cite healthy or recovered sibling paths so they do not become false causes.
- **Authentication anomalies:** Aggregate failures by source early, cite the count line, sample failed-password or invalid-user lines, then check accepted/success events for that same source. Cite successful logins from other sources separately and state "no current successful/accepted login from that source" only when supported.
- **HTTP/service errors:** Correlate access/proxy status with service/backend evidence and one independent dependency or system layer. After finding a resource limit, search nearby lines for the exposed actor/client/check/application name or fanout driver. Explicitly dispose of named older, recovered, feature-flag, cache, DNS, gateway, or external-service alternatives.
- **Certificates and container layouts:** If the primary container log root is empty and host logs are mounted elsewhere, inspect both roots in one command with both roots in `--allowed-paths` and say which root produced evidence. Distinguish timing/environment failures from expired or wrong certificate material by pairing x509/check lines with clock-sync, NotBefore/NotAfter, kubelet/syslog, rotation, or renewal evidence.
- **Sockets:** Run `help ss` before socket probes. Use a supported listening TCP address/port query such as `ss -tlnH` or `ss -tln`. Do not run or claim process/PID flags unless `help ss` advertises them. If process details are absent from help or unsupported, say listening local TCP addresses/ports are available but process/PID attribution is unavailable.

## Final Answer Contract

Use these sections and keep them concise:

- `Commands run`: exact rshell shape, actual number of invocations, literal `--allowed-paths` roots, key files queried, help or unsupported-capability evidence, and no probes you only considered.
- `Finding`: one sentence naming the likely cause and affected service/check/traffic/source.
- `Evidence`: concrete files, dates/times, message fragments, counts/statuses, IDs, fields, actor/source, and downstream impact. Include "since", recovery, or success markers when they prove duration or state.
- `Not supported`: dispose of misleading user hypotheses, historical or rotated matches, recovered noise, different-source successes, missing same-source successes, or unavailable command capabilities.
- `Uncertainty / next checks`: state what is not proven and suggest only safe read-only validation, audit, config/source review, rollback planning, owner follow-up, or capability follow-up. Do not propose remediation commands.

Before finalizing, check that every cause, consequence, and counter-hypothesis claim has transcript evidence; counts appear when scale matters; historical/recovered/different-source evidence is labeled; `Commands run` matches the actual transcript; and there are no real-host access claims, remediation commands, or unsupported process/PID/socket claims. If no rshell transcript exists, say diagnostics could not be completed.
