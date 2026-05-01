---
name: datadog/remote-host-diagnostics
description: Load this skill when running diagnostic commands through the local ./rshell CLI.
toolsets: core
---

# Remote Host Diagnostics

Run safe, read-only diagnostics through the repository-local `./rshell` binary and answer with evidence-grounded conclusions.

## Hard rules

- Use the Bash tool and `./rshell` from the repository root. Do **not** use Datadog remote-action tools, and do not imply a real remote host was contacted.
- Keep commands read-only: no writes, config edits, directory creation, restarts, kills, deletes, or remediation commands.
- Every command that reads logs must include `--allowed-paths <log-root>`. If the prompt gives a fake/generated/explicit root, use that exact root instead of `/var/log`.
- In Bash commands, paste the prompt-provided root literally after `--allowed-paths` and in file paths; do not hide it behind `$ROOT` or other variables. Quote the literal path if needed. In final answers, use relative file names and `<provided log root>` rather than repeating long generated roots.
- Keep reads bounded with `find`, `ls`, `grep`, `head`, `tail`, `wc`, `sort`, `uniq`, or command-specific filters. Never dump whole large logs.

## Running `rshell`

Log-reading command form:

```sh
./rshell --allow-all-commands --timeout 5s --allowed-paths <log-root> -c '<bounded read command>'
```

Non-filesystem checks, including command discovery and sockets, usually omit `--allowed-paths`:

```sh
./rshell --allow-all-commands --timeout 5s -c 'help'
```

Start every diagnostic session with that exact `help` command. After that, use common bounded forms directly (`ls -la`, `find -maxdepth ... -type f`, `grep -n/-E/-i/-c/-m/-h/-o`, `head -n`, `tail -n`, `wc -l`, `sort`, `uniq -c`). Check command help only for unfamiliar or teammate-suggested flags, socket commands, or after a failure; avoid spending commands on redundant `help grep/find/head/tail` checks.

If a command fails, inspect the error/help, fix the specific issue once, and move on. Do not blindly retry the same command or include failed typo paths as evidence.

**Socket checks:** run `help ss` before socket flags. Prefer supported listening-TCP forms such as `ss -tln` or `ss -tlnH`. Do not run `ss -p`, `ss -tulpn`, or `--process` unless `help ss` explicitly lists process/PID support. If process flags are absent, say that addresses/ports can be collected but process names/PIDs cannot.

## Log roots and discovery

- Default real-host root: `/var/log`.
- Prompt-provided fake/generated/explicit root: use it exactly.
- Container layout: if the primary root is empty or missing and the prompt provides a host-mounted root (for example `/host/var/log`), list the primary root, then inspect the host root with its own `--allowed-paths`. Mention this fallback in the final answer.

Useful discovery patterns:

```sh
./rshell --allow-all-commands --timeout 5s --allowed-paths <root> -c 'ls -la <root>'
./rshell --allow-all-commands --timeout 5s --allowed-paths <root> -c 'find <root> -maxdepth 3 -type f | sort | head -n 80'
```

## Efficient diagnostic loop

1. **Discover:** run initial `help`, then one `ls` and/or one `find` for the chosen root.
2. **Target first:** use the prompt's symptom, service, source IP, time window, and keywords to search the most likely current logs before broad scans. Search multiple relevant files in one bounded `grep` when safe.
3. **Corroborate:** add one or two focused searches in secondary logs (agent/app/nginx/auth/system/database) to connect symptoms to cause.
4. **Quantify when useful:** use `grep -c`, `wc -l`, `sort | uniq -c`, or status/source/user counts.
5. **Rule out only key alternatives:** inspect rotations or noisy/debug logs only when the prompt asks, current logs are incomplete, or you need one representative red-herring/old-event line.
6. **Stop when supported:** once you have a likely finding, decisive snippets/line numbers, useful counts/IDs, and the main alternative/noise addressed, stop. Avoid repetitive grep/tail/count variants and broad searches across unrelated logs.

Good bounded search forms:

```sh
./rshell --allow-all-commands --timeout 5s --allowed-paths <root> -c 'grep -n -m 80 -E "(ERROR|WARN|failed|timeout|500|502|database|postgres|x509|clock|config|yaml|metrics)" <file1> <file2>'
./rshell --allow-all-commands --timeout 5s --allowed-paths <root> -c 'grep -n -m 80 "<time-or-id>" <file>'
```

## Patterns to handle well

Always verify these patterns with logs; do not assume them.

- **Datadog Agent metrics stopped:** inspect `datadog/agent.log*` for remote-config/config reloads, YAML/validation errors with line fields, core-agent/aggregator stop, metric flush stoppage, and trace/APM/log-intake health. Cite the exact validation error, line number, config/transaction ID, stop/flush timestamps, and one snippet showing healthy/unrelated trace or log intake if relevant.
- **SSH brute force:** search `auth.log*` for `Failed password`, invalid users, source IPs, and `Accepted` logins. Count failures by source/user. If no accepted login from the suspicious source is evidenced, say exactly that and avoid "compromise/compromised." Quote accepted logins from different IPs as different sources.
- **HTTP 500/502 backend incidents:** correlate nginx access/error logs with app/service and system/database logs around the same window. Look for DB/Postgres connection errors, pool/slot exhaustion, worker fanout, timeouts, upstream failures, request IDs, status counts, and application names. Recommend only read-only next checks such as connection-pool metrics or `pg_stat_activity` inspection.
- **Container certificate failures:** if primary logs are empty, use the host-mounted root. For `x509`, distinguish expired certificates from `not yet valid`/NotBefore. Corroborate timing causes with syslog/chrony/clock messages, quoting current vs NotBefore/NotAfter times and skew/step magnitude when available.
- **Socket capability:** after `help` and `help ss`, run only supported `ss` flags and explicitly note any missing process/PID support.

## Final answer checklist

Use a concise structure: **Finding**, **Evidence by file**, **Commands run**, **Ruled out/noise**, **Next safe read-only check**.

- Start with the likely finding/root cause and confidence.
- Cite concrete evidence: relative filenames, `grep -n` line numbers, timestamps, snippets, counts, IDs (`request_id`, `transaction_id`, source IP, `application_name`, `line=<n>`), auth methods, status-code counts, certificate validity times, and clock skew values.
- Commands run: if there are only a few commands (for example sockets), list every command exactly. For larger log investigations, list the exact initial `help` command plus decisive command forms with `<provided log root>`, relative files, and actual grep/count regex or pipeline. Do **not** summarize only as "targeted greps"; include the patterns and files that produced the evidence.
- Separate confirmed findings from red herrings, old rotated-log events, and unrelated noise; cite one representative line/snippet for important ruled-out signals.
- Use neutral negative findings: "No accepted login from <source> was found." Avoid phrasing like "successful ... <source>" when the finding is negative.
- State limitations and one next diagnostic check that is safe and read-only.
- Do not include operational-change command names in the final answer, even in negative phrasing. Do not describe the investigation as remote-host access or as using a "skill"; say it used local `./rshell` against the provided log root.
