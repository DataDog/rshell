---
name: datadog/remote-host-diagnostics
description: Safe read-only host diagnostics through ./rshell with target help discovery, literal allowed paths, compact evidence collection, and transcript-grounded final answers.
---

# Remote Host Diagnostics

## Non-Negotiables

- Diagnose only through recorded `./rshell --allow-all-commands -c '<script>'` calls. Do not answer from planned commands, prompt facts, repository knowledge, or static capability snapshots.
- For file reads, pass every prompt-provided root literally on every invocation with `--allowed-paths=<root>` or comma-separated roots. If primary and fallback roots are provided, include both from the first inventory onward and say which root produced evidence.
- Start with `help` inside rshell. Add `help <command>` for commands whose flags or behavior matter to the investigation, and after any unsupported-command failure. Production rshell deployments may restrict, omit, or extend features; target-environment `help` is authoritative.
- Keep all diagnostics read-only and bounded. Do not write files, install packages, mutate services, restart, kill, delete, run recursive grep, use `find ... -exec grep`, or run broad repeated scans.
- Do not claim you connected to, SSHed into, or accessed a real remote/customer host unless the user provided that evidence.
- In the final answer, describe only recorded tool calls; never list planned, interrupted, placeholder, or reconstructed commands.

## Fast Path

Use one rshell call for socket-only tasks. For log/file investigations, aim for two calls:

1. **Help + inventory.** Run `help` plus needed command help, then bounded inventory of the literal prompt roots with shallow `find`, `sort`, and `head`. State when a root is empty. A capped inventory is not exhaustive.
2. **Fused evidence pass.** Pick candidate files before grepping: current component log, prompt-named rotated/noise log, and at most two independent layers such as proxy, dependency, system, audit, security, or sibling health. In one labeled script, collect cause, impact/current state, actor/source, recovery or same-source success absence, prompt-theory checks, rotated/recovered lookalikes, and one independent-layer corroboration when available.

Use a third call only for one named missing proof that would change the answer: actor/driver attribution, exact impact/duration, same-source success absence, short context around already-known lines, certificate time comparison, or socket fallback after unsupported help/runtime output.

## Evidence Pass Pattern

Keep scripts small, labeled, and auditable with `printf '%s\n' '<label>'`. Prefer count-plus-sample blocks over separate broad sweeps:

- selected file line counts
- incident-window samples from current logs
- decisive cause counts and capped samples
- downstream impact/current-state/recovery counts and samples
- actor/source/owner fields that could explain who or what drove the event
- same-source success or recovery absence, plus different-source successes when relevant
- each prompt-suggested theory and rotated/recovered lookalike, with current vs historical labels
- one independent layer, if present, that confirms or contradicts the primary finding

Use `grep -H -c`, `grep -H -n -m 20`, `wc -l`, `head -n`, `tail -n`, and short `sed -n` only after line numbers are known. End exploratory scripts with `true` or `probe || true` so useful partial output survives a nonzero probe. Avoid `while`, `case`, functions, process substitution, background jobs, recursive grep, `find ... -exec grep`, and complex nested quoting.

Search with incident time tokens, affected object names, IDs, sources, status codes, and cause words found in the prompt/output. Once a likely cause exists, stop running generic `ERROR`, `WARN`, `status`, or `recovered` sweeps; switch to targeted proof and alternatives.

## What To Prove

Every cause, timestamp/window, affected object, consequence, actor/source, and negative finding needs transcript evidence. Preserve filenames, line numbers when available, full date/time, exact values, counts, IDs, status codes, and decisive message fragments. If a raw token matters, repeat it in `Evidence`.

Before finalizing, explicitly handle:

- **Main finding:** cause, affected service/check/route/source, incident window, impact, and current or recovery state.
- **Actor/driver:** if fields such as client/source, user, owner, application name, job, worker, fanout, pool, active/max, or credential/source ID are present, put the supported driver in the finding rather than only in uncertainty.
- **Prompt theories and lookalikes:** each teammate theory, noise file, rotated match, recovered match, and previous-window match gets a `Not supported` bullet with the count/status/date/source that separates it from the current incident.
- **Negative claims:** cite the exact zero count or runtime/help output and the queried files/window. Say "not found in the queried files/window" for bounded searches.
- **Current vs other-source evidence:** accepted logins, successes, recoveries, or healthy checks from other sources are not proof for the suspicious source. Label auth method, source, and current/historical scope when available.
- **Fallback layouts:** if one root is empty and another root produces logs, make the empty primary and evidence-producing fallback explicit in both `Commands run` and `Evidence`.
- **Certificates/time:** quote the exact x509/certificate clause, compare current time with validity fields when present, and cite clock-sync, rotation, renewal, kubelet/syslog, or equivalent system evidence before choosing timing versus certificate material.

## Domain Hints

- **Agent/telemetry:** Separate config validation, credential/API/auth failures, intake/APM noise, queue/aggregator/collector/flush impact, sibling health, and recovery. Pair the cause with same-incident delivery impact and raw status/reason.
- **Authentication:** Aggregate failures by source early. Report concentrated source, approximate count, failed-password/invalid-user pattern, same-source accepted/success count in the current window, and accepted successes from different sources or historical logs separately.
- **HTTP/service:** Put affected route, HTTP status, and incident time in the finding. Correlate proxy/access evidence with service/backend evidence and one dependency/system layer. Search actor/driver fields in the same evidence pass.
- **Sockets:** Usually run `help; help ss; ss -tlnH || ss -tln || true` in one invocation. Report local address, port, and state rows when returned. If process/PID flags are absent from help or runtime output, say process/PID attribution is unavailable; still summarize that supported listening TCP queries normally provide local addresses/ports/state when permissions allow. If runtime blocks socket reads, say no rows were collected.

## Final Answer Contract

Use concise sections:

- `Commands run`: one bullet per recorded rshell invocation. Include literal `./rshell --allow-all-commands`, every literal `--allowed-paths=...`, and the concrete files or socket probes. Summarize long scripts by their labels and selected files, but do not use `...`, `<root>`, "same prefix", "same allowed paths", or commands not actually run.
- `Finding`: one sentence naming the likely cause plus affected service/check/route/source, actor/driver when supported, exact raw cause token or status when available, and full incident date/time window when available.
- `Evidence`: concrete files, line numbers, full dates/times, exact decisive message fragments, counts/statuses, IDs/fields, actor/source, downstream impact, and exact recovery, success, "since", or zero-count markers.
- `Not supported`: dispose of misleading hypotheses, historical/rotated matches, recovered noise, different-source successes, missing same-source successes, unavailable command capabilities, and prompt-suggested theories not supported by the queried files.
- `Uncertainty / next checks`: state what is not proven and suggest only safe read-only validation, audit, config/source review, rollback planning, owner follow-up, or capability follow-up. Do not propose remediation commands.

Final check: command bullets map to recorded tool calls; prompt roots appear literally in every file-reading invocation; the finding has exact time/object when known; decisive raw tokens remain visible in `Evidence`; every negative claim has count/help evidence and queried scope; current, historical, recovered, fallback, and different-source evidence are labeled; no placeholders, unsupported socket/PID claims, real-host access claims, exhaustive-inventory overclaims, or remediation commands remain.
