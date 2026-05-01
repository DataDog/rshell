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
Use common bounded forms directly (`ls -la`, `find -maxdepth ... -type f`, `grep -n/-E/-i/-c/-m/-h/-o`, `head -n`, `tail -n`, `wc -l`, `sort`, `uniq -c`, simple `sed`). Check command help only for unfamiliar/teammate-suggested flags, socket commands, or after a failure; avoid redundant help checks.

If a command fails, inspect the error/help, fix that issue once, and move on. Do not blindly retry the same command or cite failed typo paths as evidence.

## Fast diagnostic workflow

- Root selection: default to `/var/log` only when no root is provided. For container layouts, if the primary root is empty/missing and a host-mounted root is provided, list the primary root, then inspect the host root with its own `--allowed-paths` and mention the fallback.
- Discover once: normally run one `find <root> -maxdepth 3 -type f | sort | head -n 80` (or `ls -la` when checking whether a root is empty).
- Target first: use the prompt's symptom, service, source IP, time window, IDs, and likely keywords before broad scans.
- Aim for 4-7 total commands: initial help, one discovery command, 2-4 decisive grep/count/corroboration commands. Run an extra command only if it fills a missing final-answer gap: root cause, line/timestamp snippet, count/ID, key negative, or important red herring.
- Prefer one high-yield multi-file `grep -n -m <N> -E 'time|symptom|cause|noise' ... | head -n <N>` to many narrow retries. If it already returned decisive line numbers/snippets, cite it and do not rerun a narrower variant.
- Combine related counts in one labeled pipeline when practical, then stop. Avoid repeated counts, per-minute enumeration, request-ID fanout, rotation/noise scans, or broad OOM/panic searches unless the prompt or missing evidence requires them.
- For nginx/common-log status counts, match status robustly (for example `'" (500|502) '`) or use structured `status=...` fields; compute the count once.

## Patterns to handle well

Always verify with logs; do not assume.

- **Datadog Agent metrics stopped:** search current `datadog/agent.log` first for remote-config/config reloads, YAML/validation errors with line fields, core-agent/aggregator stop, metric flush stoppage, and trace/APM/log-intake health/noise. Usually this is enough plus at most one rotated/system noise check. Cite validation text, line number, config/transaction ID, stop/flush timestamps, and one healthy/unrelated trace/APM/intake snippet if present.
- **SSH brute force:** search `auth.log*` for `Failed password`, invalid users, source IPs, and `Accepted` logins. Count failures by source/user and check accepted logins for the suspicious source and for other sources. If none match the suspicious source, write exactly: `No accepted login from <source> was found.` To avoid ambiguity, do not pair the word `successful` with that source in negative statements, and do not say `compromise/compromised` unless an accepted login from the same source is evidenced.
- **HTTP 500/502 backend incidents:** correlate nginx access/error logs with app/service and system/database logs around the same window. Look for DB/Postgres connection errors, pool/slot exhaustion, worker fanout, timeouts, upstream failures, request IDs, status counts, and application names. After one cross-log window grep identifies a cause and one status/count or request-ID command corroborates it, stop unless a key alternative remains unresolved. Recommend only read-only next checks such as connection-pool metrics or `pg_stat_activity` inspection.
- **Container certificate failures:** if primary logs are empty, use the host-mounted root. For `x509`, distinguish expired certificates from `not yet valid`/NotBefore. Corroborate timing causes with syslog/chrony/clock messages; quote current vs NotBefore/NotAfter times, skew/step magnitude, and the time-sync process name when available.
- **Socket capability:** after `help` and `help ss`, run one supported listening-TCP command such as `ss -tln` or `ss -tlnH`. Do not run `ss -p`, `ss -tulpn`, or `--process` unless `help ss` explicitly lists process/PID support. If process flags are absent, say addresses/ports are available but process names/PIDs are not. Do not add `wc -l` unless a count was requested.

## Final answer checklist

Use concise sections: **Finding**, **Evidence by file**, **Commands run**, **Ruled out/noise**, **Next safe read-only check**.

- Start with the likely finding/root cause and confidence.
- Cite concrete evidence: relative filenames, `grep -n` line numbers, timestamps, snippets, counts, IDs (`request_id`, `transaction_id`, source IP, `application_name`, `line=<n>`), auth methods, status counts, certificate validity times, and clock skew values.
- In **Commands run**, list the exact initial help command and the decisive `rshell` commands with `<provided log root>` substituted for long roots. Keep actual regexes/pipelines; do not use ellipses or vague phrases like "targeted greps". For large investigations, omit only non-decisive exploratory repeats.
- Separate confirmed findings from red herrings, old rotated-log events, and unrelated noise; cite one representative snippet for important ruled-out signals.
- State negative findings neutrally, especially `No accepted login from <source> was found.`
- State limitations and one next diagnostic check that is safe and read-only.
- Do not include operational-change command names in the final answer, even in negative phrasing. Do not describe the investigation as remote-host access or as using a "skill"; say it used local `./rshell` against the provided log root.
