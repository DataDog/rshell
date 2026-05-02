---
name: datadog/remote-host-diagnostics
description: Use local ./rshell for remote-host diagnostics.
toolsets: core
---

# Remote Host Diagnostics

Run local `./rshell` via Bash; no Datadog remote-action tools, host I/O, writes, or remediation.

## Commands

- Start once exactly: `./rshell --allow-all-commands --timeout 5s -c 'help'`.
- Logs: supplied roots only, never `/var/log`:
  `./rshell --allow-all-commands --allowed-paths <ROOT>[,<HOST_ROOT>] --timeout 10s -c '<read-only command>'`
  Long flag/comma; `r=<ROOT>;h=<HOST_ROOT>` ok; inventory roots; if primary empty use host root.
- Read-only: `help/find/ls/grep/tail/head/wc/sort/uniq/cut/sed/strings/ss/ps/ip route/true`. No whole-log `cat`/redirects/service/process/config changes.

## Workflow

1. Inventory once/root: `find <ROOT> -maxdepth 3 -type f | head -n 50`. Pick current: datadog/auth/app/nginx/system/syslog. Rotated/noisy only if requested/current insufficient; one bounded check.
2. Budget 2-4 diagnostics after help+inventory (counts/ss +1). Batch:
   `grep -HnEi '(<time>.*(ERROR|WARN|FATAL|\[error\]|\[warn\]|fail|stopped|refused|x509|50[0-9]|<symptom>)|<distinctive>)' <files> | grep -viE 'heartbeat|DEBUG' | head -n 30; true`
   Pair generic names with errors/statuses.
3. Stop after cause + proof/requested negative/scale. One line per negative/red herring; avoid repeat file/term greps.
4. HTTP/resource: one app/nginx/system grep for symptom + `postgres|database|pool|slots|refused|too many clients`; if exhaustion, one driver grep for `application_name|pool|suspected|worker|job|fanout`.
5. Auth: count failed sources/users, sample suspect, check suspect `Accepted`, list exact other accepted IPs/users. If none, final phrase `No accepted login from that source`; avoid `successful` near suspect.
6. Flags/`ss`: `help <command>` first. Sockets: one `ss -tlnH`/`ss -tln`; no `-p` unless listed; state PID/process unavailable.
7. Failed: inspect error/help, correct once. No-match: `... | head ...; true`.

## Final answer

Bullets: scope; finding/confidence; evidence (`<ROOT>/file:line` snippets/counts/times/IDs/actors); red herrings/negatives; commands; next read-only checks. Fakes: say `local fixture logs only`; no real-host claim. Use `<ROOT>`/`<HOST_ROOT>`, no repo paths.

Avoid final words: `restart`, `kill`, `delete`, `edit`, `apply`; say `service lifecycle`, `process termination`, `config-change`.
