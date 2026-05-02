---
name: datadog/remote-host-diagnostics
description: Use ./rshell diagnostics.
toolsets: core
---

# Remote Host Diagnostics

Run `./rshell` via Bash; no Datadog remote-action tools, host I/O, writes/remediation.

## Commands

- Start exactly: `./rshell --allow-all-commands --timeout 5s -c 'help'`.
- Logs: supplied roots; never `/var/log`:
  `./rshell --allow-all-commands --allowed-paths <ROOT>[,<HOST_ROOT>] --timeout 10s -c '<read-only command>'`
  Literal roots in `--allowed-paths` (not vars); comma-separate. In `-c`, `r=<ROOT>` ok; quote `$r`.
- Read-only: `help/find/ls/grep/tail/head/wc/sort/uniq/cut/sed/strings/ss/ps/ip route/true`. No whole-log `cat`/redirects/service/process/config changes.

## Workflow

1. Inventory once/root; if primary empty use host root: `find <ROOT> -maxdepth 3 -type f | head -n 50`. Pick current datadog/auth/app/nginx/system/syslog. Rotated/noisy only if needed/requested; one bounded check.
2. After help+inventory: focused cross-log grep, then proof/negative/driver follow-up (3 max for distinct logs). Stop when supported; no duplicates.
3. Grep:
   `grep -HnEi '(<time>.*(ERROR|WARN|FATAL|\[error\]|\[warn\]|fail|stopped|refused|x509|50[0-9]|<symptom>)|<distinctive>)' <files> | grep -viE 'heartbeat|DEBUG' | head -n 30; true`
   Keep statuses/errors plus lines/IDs/actors/counts.
4. HTTP/resource: grep app/nginx/system for symptom + `postgres|database|pool|slots|refused|too many clients`; if exhaustion, one driver grep: `application_name|pool|worker|job|fanout|suspected`.
5. Auth: one command for failed-source/user counts, suspect failure count/samples, suspect `Accepted`, and other-source `Accepted` lines (method+IP). If none, final phrase `No accepted login from that source`; avoid `successful` near suspect IP.
6. Flags/`ss`: `help ss` first. Sockets: `ss -tlnH`/`ss -tln`; no `-p` unless listed; state PID/process unavailable.
7. Fail/no-match: inspect error/help, correct once; use `... | head ...; true`.

## Final answer

Bullets: scope; finding/confidence with line/count/ID/source; evidence (`<ROOT>/file:line` snippets/counts/times/actors); red herrings/negatives; commands; next read-only checks. Fakes: `local fixture logs only`; no real-host claim. Use `<ROOT>`/`<HOST_ROOT>`, no repo paths.

Avoid: `restart`, `kill`, `delete`, `edit`, `apply`; say `service lifecycle`, `process termination`, `config-change`.
