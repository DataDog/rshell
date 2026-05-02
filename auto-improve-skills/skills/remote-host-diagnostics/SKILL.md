---
name: datadog/remote-host-diagnostics
description: Use ./rshell diagnostics.
toolsets: core
---

# Remote Host Diagnostics

Run `./rshell` via Bash; no Datadog remote-action tools, host I/O, writes/remediation.

Start exactly: `./rshell --allow-all-commands --timeout 5s -c 'help'`.

Logs use supplied roots only, never `/var/log`:
`./rshell --allow-all-commands --allowed-paths <ROOT>[,<HOST_ROOT>] --timeout 10s -c '<read-only command>'`
Use literal comma-separated roots; quote `r=<ROOT>`/`$r` in `-c`.

Read-only filters only; no whole-log `cat`, redirects, service/process/config changes.

## Workflow

1. Inventory roots once/combine: `find <ROOT> -maxdepth 3 -type f | head -n 50`; if primary empty, say/use host. Current datadog/auth/app/nginx/system/syslog first; rotated/noisy max one bounded check.
2. Then one focused cross-log grep + <=2 follow-ups. Combine `echo` sections; no duplicate/synonym-only greps. Stop when cause+impact+key negative/red herring supported. Fail/no-match: inspect error/help, correct once.
3. Grep: `grep -HnEi '(<time>.*(error|warn|fatal|fail|stopped|refused|x509|50[0-9]|<symptom>)|<distinctive>)' <files> | grep -viE 'heartbeat|DEBUG' | head -n 30; true`. Preserve lines/IDs/counts, parser line/col, auth method, daemon names.
4. HTTP/resource: app/nginx/system for symptom + `postgres|database|pool|slots|refused|too many clients`; if exhausted, one driver grep `application_name|pool|worker|job|fanout|suspected`.
5. Auth: one command: fail source/user counts, suspect count/samples/`Accepted`, other-source `Accepted` method+IP. If none, final says `No accepted login from that source`; avoid `successful` near suspect IP.
6. Cert/time: x509/cert needs failure + clock/ntp/chrony/syslog/recovery evidence; if not expired, include bounded expired/NotAfter negative count.
7. `ss`: `help ss` first. Run one supported socket (`ss -tlnH`, else `ss -tln`); no `-p` unless listed; state PID/process unavailable.

Final: scope; finding/confidence with line/count/ID/source; evidence (`<ROOT>/file:line` snippets/counts/times/actors); red herrings/negatives; commands; next read-only checks. Fakes: `local fixture logs only`; no real-host claim; use `<ROOT>`/`<HOST_ROOT>`, no repo paths. Avoid restart/kill/delete/edit/apply; say service lifecycle/process termination/config-change.
