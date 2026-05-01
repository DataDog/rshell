---
name: datadog/remote-host-diagnostics
description: Load this skill when running diagnostic commands through the local ./rshell CLI.
toolsets: core
---

# Remote Host Diagnostics

Use only the repository-local `./rshell` binary for safe, read-only diagnostics, then answer with evidence-grounded conclusions.

## Hard rules

- Run `./rshell` through the Bash tool from the repository root. Do **not** use Datadog remote-action tools, and do not imply that a real remote host was contacted.
- Read-only only: no writes, config edits, directory creation, restarts, kills, deletes, or remediation commands.
- Access logs/files only through `./rshell`; every file/log read, including `ls`/`find`, must include `--allowed-paths <log-root>`.
- If the prompt gives a fake/generated/explicit root, use that exact root instead of `/var/log`. Paste it literally after `--allowed-paths` and in paths; do not hide it in `$ROOT` or variables. In final answers, replace long roots with `<provided log root>` and use relative file names.
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

Non-filesystem checks such as command discovery and sockets usually omit `--allowed-paths`.
Use common bounded forms directly (`ls -la`, `find`, `grep`, `head`/`tail`, `wc`, `sort`/`uniq`, simple `sed`). Check help only for unfamiliar/teammate-suggested flags, socket commands, or after a failure; avoid redundant help checks.

If a command fails, inspect the error/help, fix that issue once, and move on. Do not blindly retry the same command or cite failed typo paths as evidence.

## Fast diagnostic workflow

- Root selection: use an explicit/fake root exactly; default to `/var/log` only if none is given. In containers, `ls -la` the primary root; if empty/missing and a host root is provided, switch to that root with its own `--allowed-paths` and mention the fallback.
- File discovery: skip `find` when common target paths are obvious; otherwise run one `find <root> -maxdepth 3 -type f | sort | head -n 80`. If a guessed path fails, run `find` once instead of retyping variants.
- Target first: use the prompt's symptom, service, source IP, time window, IDs, and likely keywords. Prefer one bounded multi-file `grep -n -m <N> -E 'time|symptom|cause|noise' ... | head -n <N>` to many narrow retries.
- Budget: after help, aim for 3-5 diagnostic commands: optional discovery, one decisive grep, one count/corroboration, and one negative/noise check only if needed. Do not run overlapping broad window greps; refine at most once, then stop when evidence covers root cause, line/timestamp snippet, count/ID, key negative, and red herring.
- Counts/zero checks: combine counts in one labeled pipeline. If zero matches are expected, avoid bare `grep -c` or a final `grep` that returns exit 1; use `grep 'pattern' file | wc -l`. Do not retry a command that already produced a useful zero count just to clear status.
- For nginx/common-log status counts, match status robustly (for example `'" (500|502) '`) or structured `status=...`; compute once.

## Patterns to handle well

Always verify with logs; do not assume.

- **Datadog Agent metrics stopped:** search current `datadog/agent.log` first for remote-config/config reloads, YAML/validation errors with line fields, core-agent/aggregator stop, metric flush stoppage, and trace/APM/log-intake health/noise. Usually stop after that plus at most one targeted rotated/system noise check; avoid unrelated app/nginx scans unless symptoms point there. Cite validation text, line number, config/transaction ID, stop/flush timestamps, and one healthy/unrelated trace/APM/intake snippet if present.
- **SSH brute force:** search `auth.log*` for `Failed password`, invalid users, source IPs, and `Accepted` logins. Count by source/user; check accepted logins for suspicious and other sources. If none match, write exactly: `No accepted login from <source> was found.` Do not pair `successful` with that source in negatives or say `compromise/compromised` without same-source accepted evidence.
- **HTTP 500/502 backend incidents:** correlate nginx access/error logs with app/service and system/database logs around the same window. Look for DB/Postgres connection errors, pool/slot exhaustion, worker fanout, timeouts, upstream failures, request IDs, status counts, and application names. Use one cross-log window grep; do not repeat similar broad greps over the same files/time. Then run one status/count or focused request-ID/cause command and stop unless a key alternative remains unresolved. Recommend only read-only next checks such as connection-pool metrics or `pg_stat_activity` inspection.
- **Container certificate failures:** if primary logs are empty, use the host-mounted root. For `x509`, distinguish expired certificates from `not yet valid`/NotBefore; use `grep ... | wc -l` for expected-zero expired/not-yet-valid counts. Corroborate timing causes with syslog/chrony/clock messages; quote current vs NotBefore/NotAfter times, skew/step magnitude, and the time-sync process name when available. Do not search for recovery unless asked.
- **Socket capability:** after `help` and `help ss`, run one supported listening-TCP command (`ss -tln` or `ss -tlnH`). Do not run `ss -p`, `ss -tulpn`, or `--process` unless help lists process/PID support. If absent, say addresses/ports are available but process names/PIDs are not. No `wc -l` unless a count was requested.

## Final answer checklist

Use concise sections: **Finding**, **Evidence by file**, **Commands run**, **Ruled out/noise**, **Next safe read-only check**.

- Begin with the direct likely cause/finding and confidence; say high confidence when evidence is direct.
- Cite relative filenames, `grep -n` lines, timestamps/snippets, counts, IDs (`request_id`, `transaction_id`, source IP, `application_name`, `line=<n>`), auth methods, status counts, cert times, and clock skew values as relevant.
- In **Commands run**, list exact initial help and decisive `rshell` commands with `<provided log root>` for long roots. Keep regexes/pipelines; no ellipses or vague "targeted greps". Omit only non-decisive repeats.
- Separate findings from red herrings, old rotated events, and unrelated noise; cite one ruled-out signal. State negatives neutrally, especially `No accepted login from <source> was found.`
- State limitations and one safe read-only next check. Do not include operational-change command names, describe remote-host access, or mention a "skill"; say local `./rshell` against the provided log root.
