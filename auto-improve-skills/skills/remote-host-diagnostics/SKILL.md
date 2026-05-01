---
name: datadog/remote-host-diagnostics
description: Load this skill when running diagnostic commands through the local ./rshell CLI.
toolsets: core
---

# Remote Host Diagnostics

Use only the repository-local `./rshell` via Bash for safe, read-only diagnostics, then answer with cited evidence.

## Hard rules

- Run `./rshell` from the repository root through the Bash tool. Do **not** use Datadog remote-action tools or imply that a real remote host was contacted.
- Read-only only: no writes, config edits, directory creation, restarts, kills, deletes, or remediation commands.
- All file/log operations (`ls`, `find`, reads) must go through `./rshell` with `--allowed-paths <log-root>`.
- If the prompt gives a fake/generated/explicit root, paste that exact root literally after `--allowed-paths` and in paths; do not hide it in `$ROOT` or variables. Default to `/var/log` only if no root is given. In final answers, replace long roots with `<provided log root>` and use relative filenames.
- Keep output bounded with `find`, `grep`, `head`, `tail`, `wc`, `sort`, `uniq`, `sed`, or command-specific filters. Never dump whole large logs.

## Running `rshell`

Start every diagnostic session with this exact command:

```sh
./rshell --allow-all-commands --timeout 5s -c 'help'
```

Log/file reads use:

```sh
./rshell --allow-all-commands --timeout 5s --allowed-paths <log-root> -c '<bounded read command>'
```

Non-filesystem checks such as command discovery and sockets usually omit `--allowed-paths`. Use common bounded commands directly; check help only for unfamiliar/teammate-suggested flags, socket commands, or after a failure.

If a command fails, inspect the error/help, fix that issue once, and move on. Do not blindly retry the same command or cite failed typo paths as evidence.

## Fast diagnostic workflow

- Root selection: use an explicit/fake root exactly. In containers, `ls -la` the primary root; if empty/missing and a host root is provided, switch to that root with its own `--allowed-paths` and mention the fallback.
- Discovery: skip `find` when target paths are obvious; otherwise run one `find <root> -maxdepth 3 -type f | sort | head -n 80`. If a guessed path fails, run `find` once instead of retyping variants.
- Target first: use the prompt's service, symptom, source IP, time window, IDs, and likely keywords. Prefer one bounded multi-file `grep -n -m <N> -E 'time|symptom|cause|noise' ... | head -n <N>` over many narrow retries.
- Budget/stop: after help, aim for 3-5 diagnostic commands: optional discovery, one decisive grep, one count/corroboration, and one negative/noise check only if needed. Stop when output supports symptom/impact, likely cause, key ID/count/source, and the main red herring; do not run more broad greps just to fill sections. Refine at most once.
- Counts/zero checks: combine counts in one labeled pipeline. For expected-zero matches, avoid bare `grep -c` or a final `grep` that exits 1; use `grep 'pattern' file | wc -l`. Do not retry useful zero-count commands just to clear status.
- For nginx/common-log status counts, match status robustly (for example `" (500|502) `) or structured `status=...`; compute once.

## Patterns to handle well

Always verify with logs; do not assume.

- **Datadog Agent metrics stopped:** search current `datadog/agent.log` first for remote-config/config reloads, YAML/validation errors with line fields, core-agent/aggregator stop, metric flush stoppage, and trace/APM/log-intake health/noise. If that grep shows cause, impact, and one healthy/unrelated trace/APM/intake signal, stop after at most one targeted rotated/system noise check; do not keep hunting for every intake variant. Cite validation text, line number, transaction/config ID, stop/flush timestamps, and one healthy/noise snippet if present.
- **SSH brute force:** search `auth.log*` for `Failed password`, invalid users, source IPs, and `Accepted` logins. Count by source/user; check `Accepted` lines for the suspicious source and other sources. If none match the suspicious source, write exactly: `No accepted login from <source> was found.` Avoid `successful` near the source IP, and avoid `compromise`/`compromised` from auth logs alone.
- **HTTP 500/502 backend incidents:** correlate nginx access/error logs with app/service and system/database logs around the same window. Look for DB/Postgres connection errors, pool/slot exhaustion, worker/job fanout, timeouts, upstream failures, request IDs, status counts, and application names. If evidence names a workload (`application_name`, `suspected_client`, job/worker/fanout), cite it as the likely driver in Finding and Evidence. After one cross-log window grep, run one focused count/driver/noise command and stop unless a key alternative remains unresolved. Recommend only read-only next checks such as connection-pool metrics or `pg_stat_activity` inspection.
- **Container certificate failures:** if primary logs are empty, use the host-mounted root. For `x509`, distinguish expired certificates from `not yet valid`/NotBefore; use `grep ... | wc -l` for expected-zero expired/not-yet-valid counts. Corroborate timing causes with syslog/chrony/clock messages; quote current vs NotBefore/NotAfter times, skew/step magnitude, and the time-sync process name when available. Do not search for recovery unless asked.
- **Socket capability:** after `help` and `help ss`, run one supported listening-TCP command (`ss -tln` or `ss -tlnH`). Do not run `ss -p`, `ss -tulpn`, or `--process` unless help lists process/PID support. If absent, say addresses/ports are available but process names/PIDs are not. No `wc -l` unless a count was requested.

## Final answer checklist

Use concise sections: **Finding**, **Evidence by file**, **Commands run**, **Ruled out/noise**, **Next safe read-only check**.

- Begin with the direct likely cause/finding and confidence; say high confidence when evidence is direct.
- Cite relative filenames, `grep -n` lines, timestamps/snippets, counts, IDs (`request_id`, `transaction_id`, source IP, `application_name`, `line=<n>`), auth methods, status counts, cert times, and clock skew values as relevant.
- In **Commands run**, list exact initial help and decisive `rshell` commands with `<provided log root>` for long roots. Keep regexes/pipelines; no ellipses, vague summaries, or "targeted greps". Omit only non-decisive repeats.
- Separate findings from red herrings, old rotated events, and unrelated noise; cite one ruled-out signal. For auth negatives, use `No accepted login from <source> was found.` rather than wording about successful logins or compromise.
- State limitations and one safe read-only next check. Do not include operational-change command names, describe remote-host access, or mention a "skill"; say local `./rshell` against the provided log root.
