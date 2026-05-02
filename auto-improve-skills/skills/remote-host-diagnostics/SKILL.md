---
name: datadog/remote-host-diagnostics
description: Use ./rshell for safe, bounded host/log diagnostics
toolsets: core
---

# Remote Host Diagnostics

Use only `./rshell`; do not inspect prompt log paths with other tools. Keep actions read-only and bounded.

## Workflow
1. Start with builtin discovery:
   `./rshell --allow-all-commands --timeout 5s -c 'help'`
   Run `help <cmd>` before relying on suggested flags. For sockets, run `help ss`; do not use process/PID flags unless help shows them. Prefer `ss -tln` or `ss -tlnH` for local listening TCP sockets and state when process/PID details are unsupported.

2. For every prompt-provided log/root path, every `./rshell` command that reads it must include `--allowed-paths <ROOT>`; for multiple roots use a comma-separated list. First list files narrowly, e.g.:
   `./rshell --allow-all-commands --timeout 5s --allowed-paths <ROOT> -c 'find <ROOT> -type f | head -100'`
   If a primary root is empty and a host/container fallback root is provided, inspect both roots and say which supplied evidence.

3. Never dump whole logs with `cat`, `less`, or `more`. Use bounded filters: `grep -n -i -m N -E ...`, `head`, `tail`, `wc -l`, `sort | uniq -c`, and targeted current/rotated log files. Gather symptom lines, root-cause lines, and lines that disprove red herrings; stop once evidence is sufficient.

4. General checks:
   - Time-correlate around the reported window across relevant app/service, web/proxy, system/db, auth, and agent logs.
   - Auth: count failures by source/user and separately check accepted logins from the same source before judging compromise.
   - 5xx: connect proxy/app errors to backend dependency or resource errors; next steps should be read-only checks such as logs, metrics, or status queries.
   - Certificate/check failures: decide expiry vs clock/time-sync skew using service logs plus syslog/time-sync evidence.

## Final answer
Briefly list the commands or command patterns run, cite log filenames/snippets, state the likely cause, distinguish unrelated noise, note confidence/uncertainty, and give one safe read-only next diagnostic check. Do not claim a real remote host was contacted. In the final, avoid naming service-control or file-change remediation commands; simply say no changes were made.
