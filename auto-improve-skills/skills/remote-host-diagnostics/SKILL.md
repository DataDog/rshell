---
name: datadog/remote-host-diagnostics
description: Use local ./rshell for remote-host diagnostics.
toolsets: core
---

# Remote Host Diagnostics

Use Bash only for local `./rshell`; no remote-action tools, host I/O, or remediation.

## Commands

- Start exactly: `./rshell --allow-all-commands --timeout 5s -c 'help'`.
- Logs: supplied roots only, never `/var/log`:
  `./rshell --allow-all-commands --allowed-paths <ROOT>[,<HOST_ROOT>] --timeout 10s -c '<read-only command>'`
  Long flag/comma roots; `r=<ROOT>;h=<HOST_ROOT>` ok in `-c`; inventory roots, note empty primary.
- Read-only: `help/find/ls/grep/tail/head/wc/sort/uniq/cut/sed/strings/ss/ps/ip route/true`. No whole-log `cat`, redirects, writes, service/process/config changes.

## Workflow

1. Inventory once/root: `find <ROOT> -maxdepth 3 -type f | head -n 50`. Pick likely current files: metrics/Agent=datadog; auth=auth; HTTP=app/nginx/system; cert/time=agent/syslog. Rotated only if requested.
2. Budget 4-5 calls; 6 for counts/red herrings. Grep:
   `grep -HnEi '(<time>.*(ERROR|WARN|FATAL|\[error\]|\[warn\]|fail|stopped|refused|x509|50[0-9]|<symptom>)|<distinctive>)' <files> | grep -viE 'heartbeat|DEBUG' | head -n 30; true`
   Avoid generic names alone; pair with errors/statuses.
3. Stop after cause/finding, cross-source proof, requested negative/red herring/scale. Prefer `grep -Hn`; skip `sed`/repeat context unless needed.
4. Resource exhaustion: one driver grep for victim + `application_name|pool|suspected|worker|job|fanout`; cite driver only with evidence.
5. Auth: count failed sources/users (`grep|sort|uniq -c`); check same-source `Accepted` and list other `Accepted`. If none, final exact phrase: `No accepted login from that source`; avoid `successful` near suspect.
6. Flags/`ss`: `help <command>` first. Sockets: one `ss -tlnH`/`ss -tln`; no `-p` unless listed; state PID/process unavailable.
7. No-match: `... | head ...; true`. Failed command: inspect error/help, correct once.

## Final answer

Bullets: scope; finding/confidence; evidence (`<ROOT>/file:line` snippets/counts/times/IDs/actors); red herrings/negatives; command summary; next read-only checks. Fakes: say `local fixture logs only`; no real-host claim. Use `<ROOT>`/`<HOST_ROOT>`, no repo paths.

Avoid final incl. paths/negatives: `restart`, `kill`, `delete`, `edit`, `apply`; say `service lifecycle`, `process termination`, `config-change`
