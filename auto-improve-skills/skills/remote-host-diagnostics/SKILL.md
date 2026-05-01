---
name: datadog/remote-host-diagnostics
description: Load this skill when running diagnostic commands through the local ./rshell CLI.
toolsets: core
---

# Remote Host Diagnostics

Use the repository-local `./rshell` binary for safe, read-only diagnostics and give evidence-grounded conclusions.

## Hard rules

- Use the Bash tool from the repository root. Do **not** use Datadog remote-action tools, and do not imply that a real remote host was contacted.
- Read-only only: no writes, config edits, directory creation, restarts, kills, deletes, or remediation commands.
- Every file/log read must include `--allowed-paths <log-root>`. If the prompt gives a fake/generated/explicit root, use that exact root instead of `/var/log`.
- In Bash commands, paste the prompt-provided root literally after `--allowed-paths` and in paths; do not hide it behind `$ROOT` or variables. Quote the literal path if needed. In final answers, use relative file names and `<provided log root>` rather than long generated roots.
- Keep output bounded with `ls`, `find`, `grep`, `head`, `tail`, `wc`, `sort`, `uniq`, `sed`, or command-specific filters. Never dump whole large logs.

## Running `rshell`

Log-reading form:

```sh
./rshell --allow-all-commands --timeout 5s --allowed-paths <log-root> -c '<bounded read command>'
```

Non-filesystem checks, including command discovery and sockets, usually omit `--allowed-paths`:

```sh
./rshell --allow-all-commands --timeout 5s -c 'help'
```

Start every diagnostic session with that exact `help` command. Then use common bounded forms directly (`ls -la`, `find -maxdepth ... -type f`, `grep -n/-E/-i/-c/-m/-h/-o`, `head -n`, `tail -n`, `wc -l`, `sort`, `uniq -c`, simple `sed`). Check command help only for unfamiliar or teammate-suggested flags, socket commands, or after a failure; avoid redundant `help grep/find/head/tail` checks.

If a command fails, inspect the error/help, fix that issue once, and move on. Do not blindly retry the same command or cite failed typo paths as evidence.

**Socket checks:** run `help ss` before socket flags. Prefer one supported listening-TCP command such as `ss -tln` or `ss -tlnH`; add `wc -l` only if a count matters. Do not run `ss -p`, `ss -tulpn`, or `--process` unless `help ss` explicitly lists process/PID support. If process flags are absent, say addresses/ports are available but process names/PIDs are not.

## Roots, discovery, and speed

- Default real-host root: `/var/log`.
- Prompt-provided fake/generated/explicit root: use it exactly.
- Container layout: if the primary root is empty/missing and a host-mounted root is provided (for example `/host/var/log`), list the primary root, then inspect the host root with its own `--allowed-paths`. Mention the fallback in the final answer.

Useful discovery patterns:

```sh
./rshell --allow-all-commands --timeout 5s --allowed-paths <root> -c 'ls -la <root>'
./rshell --allow-all-commands --timeout 5s --allowed-paths <root> -c 'find <root> -maxdepth 3 -type f | sort | head -n 80'
```

Aim for a few commands: initial `help`, one discovery command, one or two composite `grep -n -m <N> -E` searches over relevant current files, then only needed counts/corroboration/noise checks. Prefer one well-chosen multi-file grep to many narrow retries; if it already returned the needed line numbers/snippets, cite it and do not rerun narrower variants. Combine related counts in one pipeline/command when practical.

## Diagnostic loop

1. **Target first:** use the prompt's symptom, service, source IP, time window, IDs, and keywords before broad scans.
2. **Corroborate:** add one or two focused searches in secondary logs (agent/app/nginx/auth/system/database) to connect symptom to cause.
3. **Quantify when useful:** count source IPs, users, status codes, repeated errors, or positive-vs-negative matches only when scale or distinction matters.
4. **Rule out key alternatives:** inspect rotations/noisy logs only when asked, current logs are incomplete, or one representative red-herring/old-event line is needed.
5. **Stop when supported:** once you have the likely finding, decisive snippets/line numbers, useful counts/IDs, and main alternative/noise addressed, stop.

## Patterns to handle well

Always verify with logs; do not assume.

- **Datadog Agent metrics stopped:** search current `datadog/agent.log` first for remote-config/config reloads, YAML/validation errors with line fields, core-agent/aggregator stop, metric flush stoppage, and trace/APM/log-intake health/noise. Cite exact validation text, line number, config/transaction ID, stop/flush timestamps, and one healthy/unrelated trace/APM/intake snippet if present. Check rotated/noisy logs only for a requested red herring or missing current evidence.
- **SSH brute force:** search `auth.log*` for `Failed password`, invalid users, source IPs, and `Accepted` logins. Count failures by source/user. For a suspicious source with no matching `Accepted` line, write `No accepted login from <source> was found.` Avoid `successful ... <source>` and `compromise/compromised` unless an accepted login from that same source is evidenced.
- **HTTP 500/502 backend incidents:** correlate nginx access/error logs with app/service and system/database logs around the same window. Look for DB/Postgres connection errors, pool/slot exhaustion, worker fanout, timeouts, upstream failures, request IDs, status counts, and application names. One broad time-window grep plus one status/count or request-ID follow-up is often enough. Recommend only read-only next checks such as connection-pool metrics or `pg_stat_activity` inspection.
- **Container certificate failures:** if primary logs are empty, use the host-mounted root. For `x509`, distinguish expired certificates from `not yet valid`/NotBefore. Corroborate timing causes with syslog/chrony/clock messages; quote current vs NotBefore/NotAfter times, skew/step magnitude, and the time-sync process name when available.
- **Socket capability:** after `help` and `help ss`, run only supported `ss` flags and explicitly note any missing process/PID support.

## Final answer checklist

Use concise sections: **Finding**, **Evidence by file**, **Commands run**, **Ruled out/noise**, **Next safe read-only check**.

- Start with the likely finding/root cause and confidence.
- Cite concrete evidence: relative filenames, `grep -n` line numbers, timestamps, snippets, counts, IDs (`request_id`, `transaction_id`, source IP, `application_name`, `line=<n>`), auth methods, status-code counts, certificate validity times, and clock skew values.
- Commands run: for short investigations, list every command exactly. For larger log cases, list the exact initial `help` command plus 3-6 decisive `rshell` command forms with `<provided log root>`, relative files, and actual grep/count regex or pipeline. Never replace them with vague text like "targeted greps".
- Separate confirmed findings from red herrings, old rotated-log events, and unrelated noise; cite one representative line/snippet for important ruled-out signals.
- State negative findings neutrally, especially `No accepted login from <source> was found.`
- State limitations and one next diagnostic check that is safe and read-only.
- Do not include operational-change command names in the final answer, even in negative phrasing. Do not describe the investigation as remote-host access or as using a "skill"; say it used local `./rshell` against the provided log root.
