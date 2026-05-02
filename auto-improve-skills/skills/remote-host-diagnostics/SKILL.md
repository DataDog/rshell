---
name: datadog/remote-host-diagnostics
description: Use local ./rshell for remote-host diagnostics.
toolsets: core
---

# Remote Host Diagnostics

Use Bash only for local `./rshell`; no Datadog remote-action tools, direct host reads/writes, or remediation.

## Commands

- Start exactly: `./rshell --allow-all-commands --timeout 5s -c 'help'`.
- Logs: supplied roots only, never `/var/log`:
  `./rshell --allow-all-commands --allowed-paths <ROOT>[,<HOST_ROOT>] --timeout 10s -c '<read-only command>'`
  Long flag; comma roots; inventory each root/fallback, note empty primary.
- Read-only: `help/find/ls/grep/tail/head/wc/sort/uniq/cut/sed/strings/ss/ps/ip route/true`. No whole-log `cat`, redirects, write/delete/edit, service/process/config changes.

## Workflow

1. Inventory once/root: `find <ROOT> -maxdepth 3 -type f | head -n 60`; use exact paths. Current logs first; rotated only for history/red-herring.
2. Budget help+inventory+1 broad grep+1-2 targets (max 6). Broad grep files:
   `grep -HnEi '(<time>.*(ERROR|WARN|FATAL|\[error\]|\[warn\]|fail|stopped|refused|x509|50[0-9]|<symptom>)|<distinctive symptom>)' <files> | grep -viE 'heartbeat|DEBUG' | head -n 40`
3. Stop after requested cause/correlation, cross-source evidence, negatives/red herrings, scale. Count only if asked/needed; no repeat context.
4. Resource exhaustion (DB/pool/slots/files/CPU/mem): one driver grep for victim + `application_name|user=|host=|source=|pool=|suspected|worker|job|fanout`; cite driver only with evidence.
5. Auth: count failed sources/users (`grep|sort|uniq -c`), check same-source `Accepted`, list other `Accepted`. If none, final exact phrase: `No accepted login from that source`; avoid `successful` near suspect actor/IP.
6. Flags/`ss`: `help <command>` first. Sockets: one `ss -tlnH`/`ss -tln`; no `-p` unless listed; state PID/process unavailable.
7. No-match: `... | head ...; true`. Failed command: inspect error/help, correct once.

## Final answer

Bullets: scope (`local fixture logs only` for fakes; no real-host claim); finding/confidence; evidence (`<ROOT>/file:line` snippets/counts/times/IDs/actors); red herrings/negatives; commands run (roots as `<ROOT>`/`<HOST_ROOT>`, no repo paths); next checks (`inspect/verify/count/list`).

Avoid in final incl. paths/negatives: `restart`, `kill`, `delete`, `edit`, `apply`; say `service lifecycle`, `process termination`, `config-change`.
